// Package selfupdate 实现 Go 原生自更新：从 GitHub Releases 下载 tarball，
// 解包新二进制，原子替换当前可执行文件，最后通过 systemd/openrc 重启服务。
//
// 不依赖 curl / sh，下载中断可重试，os.Rename 保证替换原子性。
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// maxBinarySize 限制单个 binary entry 的最大写入量，防止恶意镜像耗尽磁盘。
const maxBinarySize int64 = 512 << 20 // 512 MiB

const (
	PulseRepo       = "0xUnixIO/pulse"
	cacheTTL        = 30 * time.Minute
	downloadTimeout = 5 * time.Minute
)

// Release 对应 GitHub Releases API 的关键字段。
type Release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
}

var (
	cache   *Release
	cacheAt time.Time
	cacheMu sync.Mutex
)

// FetchLatestReleaseCached 带 30min 内存缓存的版本查询，供 check 接口调用。
func FetchLatestReleaseCached() (*Release, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cache != nil && time.Since(cacheAt) < cacheTTL {
		return cache, nil
	}
	r, err := fetchLatestRelease(context.Background())
	if err != nil {
		return nil, err
	}
	cache = r
	cacheAt = time.Now()
	return r, nil
}

// Apply 下载最新版本的 component（"server" 或 "node"）二进制，
// 原子替换当前可执行文件，然后通过 systemd/openrc 重启服务。
// 在后台 goroutine 中调用时传入 context.Background()。
func Apply(ctx context.Context, component string) error {
	if component != "server" && component != "node" {
		return fmt.Errorf("非法 component %q，只允许 server 或 node", component)
	}
	release, err := fetchLatestRelease(ctx)
	if err != nil {
		return fmt.Errorf("获取最新版本失败: %w", err)
	}
	return applyRelease(ctx, component, release.TagName)
}

func applyRelease(ctx context.Context, component, tag string) error {
	arch := goarch()
	asset := fmt.Sprintf("pulse-%s-linux-%s.tar.gz", component, arch)
	downloadURL := buildDownloadURL(tag, asset)

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取可执行路径失败: %w", err)
	}

	// 在目标目录创建临时文件，保证与目标在同一文件系统，os.Rename 才是原子的。
	tmp, err := os.CreateTemp(filepath.Dir(exePath), ".pulse-update-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	binEntry := fmt.Sprintf("pulse-%s-linux-%s/bin/pulse-%s", component, arch, component)
	if err := downloadAndExtract(ctx, downloadURL, binEntry, tmpPath); err != nil {
		return err
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("chmod 失败: %w", err)
	}

	// 原子替换：Linux 上 Rename 是 rename(2)，保证不出现半替换状态。
	if err := os.Rename(tmpPath, exePath); err != nil {
		return fmt.Errorf("替换二进制失败: %w", err)
	}

	return restartService(component)
}

// downloadAndExtract 下载 tarball 并从中提取指定路径的 entry 写入 dest。
func downloadAndExtract(ctx context.Context, url, entryPath, dest string) error {
	dlCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载返回 HTTP %d: %s", resp.StatusCode, url)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("解压 gzip 失败: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读取 tar 失败: %w", err)
		}
		if hdr.Name != entryPath {
			continue
		}
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
		if err != nil {
			return fmt.Errorf("写入临时文件失败: %w", err)
		}
		n, copyErr := io.Copy(f, io.LimitReader(tr, maxBinarySize+1))
		closeErr := f.Close()
		if copyErr != nil {
			return fmt.Errorf("写入二进制数据失败: %w", copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("关闭临时文件失败: %w", closeErr)
		}
		if n > maxBinarySize {
			return fmt.Errorf("binary 超过 %d MiB 限制，中止更新", maxBinarySize>>20)
		}
		return nil
	}
	return fmt.Errorf("tarball 中未找到 %s", entryPath)
}

// restartService 依次尝试 systemd → openrc 重启服务。
func restartService(component string) error {
	svc := "pulse-" + component
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := exec.LookPath("systemctl"); err == nil {
		out, err := exec.CommandContext(ctx, "systemctl", "restart", svc).CombinedOutput()
		if err != nil {
			return fmt.Errorf("systemctl restart %s 失败: %w\n%s", svc, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if rcService, err := exec.LookPath("rc-service"); err == nil {
		return restartOpenRC(rcService, svc)
	}
	return fmt.Errorf("未找到 systemctl 或 rc-service，请手动重启 %s", svc)
}

// restartOpenRC 必须脱离当前服务进程执行。同步等待 rc-service restart 会让
// OpenRC 等待当前 pulse 进程退出，而当前进程又在等待 rc-service 返回，导致
// 自更新后服务停在离线状态。新 session 确保 OpenRC 停止主进程时不会连带终止
// 负责完成 restart 的子进程。
func restartOpenRC(rcService, svc string) error {
	cmd := exec.Command(rcService, svc, "restart")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 rc-service %s restart 失败: %w", svc, err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("释放 rc-service %s restart 进程失败: %w", svc, err)
	}
	return nil
}

func fetchLatestRelease(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", PulseRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub API 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API 返回 %d", resp.StatusCode)
	}
	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	return &r, nil
}

func buildDownloadURL(tag, asset string) string {
	if mirror := strings.TrimRight(os.Getenv("PULSE_DOWNLOAD_MIRROR"), "/"); mirror != "" {
		// 镜像站格式：{mirror}/{owner}/{repo}/releases/download/{tag}/{asset}
		// 与 install.sh 的 PULSE_DOWNLOAD_MIRROR 约定一致（如 ghfast.top）。
		return fmt.Sprintf("%s/%s/releases/download/%s/%s", mirror, PulseRepo, tag, asset)
	}
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", PulseRepo, tag, asset)
}

func goarch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}
