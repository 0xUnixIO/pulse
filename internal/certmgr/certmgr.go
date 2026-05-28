// Package certmgr 封装 certmagic，为 sniproxy 提供按域名自动申请和续期的 TLS 证书。
//
// 仅支持 ACME + Cloudflare DNS-01 challenge：DNS-01 不需要开放 80/443 端口，
// 通过 Cloudflare API 写 TXT 记录完成验证。证书持久化到磁盘，进程重启后复用。
package certmgr

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"
)

// Config 构造 Manager 所需的配置。
type Config struct {
	// StoragePath 证书和账户密钥的本地存储目录。
	// 建议：/var/lib/pulse-node/certs。
	StoragePath string

	// Email ACME 账户邮箱，用于接收续期警告等通知。
	Email string

	// CloudflareAPIToken 用于 DNS-01 的 Cloudflare API 令牌，需要 Zone.DNS:Write 权限。
	CloudflareAPIToken string

	// Staging 为 true 时使用 Let's Encrypt staging 环境，证书不受信任但没有频率限制，
	// 适合测试。生产环境留 false。
	Staging bool
}

// Manager 管理一组域名的 TLS 证书。并发安全。
type Manager struct {
	cfg         *certmagic.Config
	cache       *certmagic.Cache
	storagePath string // 本地证书存储根目录，CertInfos 按此路径读证书

	mu      sync.Mutex
	managed map[string]struct{} // 当前已申请证书的域名集合
	issuer  *certmagic.ACMEIssuer

	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
}

// New 按 Config 创建 Manager，返回后即可用 Manage 开始申请证书。
func New(cfg Config) (*Manager, error) {
	if cfg.StoragePath == "" {
		return nil, fmt.Errorf("certmgr: StoragePath is required")
	}
	if cfg.CloudflareAPIToken == "" {
		return nil, fmt.Errorf("certmgr: CloudflareAPIToken is required")
	}

	storage := &certmagic.FileStorage{Path: cfg.StoragePath}

	m := &Manager{
		managed:     make(map[string]struct{}),
		storagePath: cfg.StoragePath,
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())

	// 先建 Cache，GetConfigForCert 延迟引用 m.cfg（它在下面赋值）。
	m.cache = certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) {
			return m.cfg, nil
		},
	})

	// Config 关联 Cache 和 Storage，先不设 Issuer（Issuer 需要反向引用 Config）。
	m.cfg = certmagic.New(m.cache, certmagic.Config{
		Storage: storage,
	})

	// Issuer 持有 Config 的反向引用，用于内部状态。
	m.issuer = certmagic.NewACMEIssuer(m.cfg, certmagic.ACMEIssuer{
		Email:  cfg.Email,
		Agreed: true,
		DNS01Solver: &certmagic.DNS01Solver{
			DNSManager: certmagic.DNSManager{
				DNSProvider: &cloudflare.Provider{APIToken: cfg.CloudflareAPIToken},
				Resolvers:   dnsResolvers(),
			},
		},
	})
	if cfg.Staging {
		m.issuer.CA = certmagic.LetsEncryptStagingCA
	} else {
		m.issuer.CA = certmagic.LetsEncryptProductionCA
	}
	m.cfg.Issuers = []certmagic.Issuer{m.issuer}

	return m, nil
}

// Replace 把管理的域名集合原子切换到 domains：新增的开始管理、不再出现的停止管理。
func (m *Manager) Replace(domains []string) error {
	want := make(map[string]struct{}, len(domains))
	for _, d := range domains {
		if d != "" {
			want[d] = struct{}{}
		}
	}

	m.mu.Lock()
	var toAdd []string
	var toRemove []certmagic.SubjectIssuer
	for d := range want {
		if _, ok := m.managed[d]; !ok {
			toAdd = append(toAdd, d)
			m.managed[d] = struct{}{}
		}
	}
	for d := range m.managed {
		if _, ok := want[d]; !ok {
			toRemove = append(toRemove, certmagic.SubjectIssuer{Subject: d})
			delete(m.managed, d)
		}
	}
	m.mu.Unlock()

	if len(toRemove) > 0 {
		m.cache.RemoveManaged(toRemove)
	}
	if len(toAdd) > 0 {
		return m.cfg.ManageAsync(m.ctx, toAdd)
	}
	return nil
}

// CertInfo 是单个证书的只读元数据视图。
type CertInfo struct {
	Domain    string    `json:"domain"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	Issuer    string    `json:"issuer"`
	// Ready 为 true 表示磁盘上有可用证书；false 表示正在申请或申请失败。
	Ready bool `json:"ready"`
}

// CertInfos 返回所有受管域名的证书快照（读磁盘；顺序与 Managed 一致）。
// 申请失败或 ACME 还在跑的域名会出现在列表里但 Ready=false。
func (m *Manager) CertInfos() []CertInfo {
	domains := m.Managed()
	out := make([]CertInfo, 0, len(domains))
	for _, d := range domains {
		info := CertInfo{Domain: d}
		certPath := filepath.Join(m.storagePath, "certificates",
			"acme-v02.api.letsencrypt.org-directory", d, d+".crt")
		data, err := os.ReadFile(certPath)
		if err != nil {
			out = append(out, info)
			continue
		}
		block, _ := pem.Decode(data)
		if block == nil {
			out = append(out, info)
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			out = append(out, info)
			continue
		}
		info.NotBefore = c.NotBefore
		info.NotAfter = c.NotAfter
		info.Issuer = c.Issuer.CommonName
		info.Ready = true
		out = append(out, info)
	}
	return out
}

// GetCertPath 返回指定域名 cert / key 文件的绝对路径，要求两文件都已存在。
// 适合需要直接持有 PEM 文件路径的场景（例如 hy2 inbound：UDP/QUIC，由 Xray 自己加载证书）。
func (m *Manager) GetCertPath(domain string) (certFile, keyFile string, err error) {
	if domain == "" {
		return "", "", fmt.Errorf("certmgr: domain is required")
	}
	if !isValidDomain(domain) {
		return "", "", fmt.Errorf("certmgr: invalid domain %q", domain)
	}
	dir := filepath.Join(m.storagePath, "certificates",
		"acme-v02.api.letsencrypt.org-directory", domain)
	certFile = filepath.Join(dir, domain+".crt")
	keyFile = filepath.Join(dir, domain+".key")
	if _, err := os.Stat(certFile); err != nil {
		return "", "", fmt.Errorf("certmgr: cert not ready for %s: %w", domain, err)
	}
	if _, err := os.Stat(keyFile); err != nil {
		return "", "", fmt.Errorf("certmgr: key not ready for %s: %w", domain, err)
	}
	return certFile, keyFile, nil
}

// dnsResolvers 探测当前网络是否支持 IPv6，返回合适的 DNS resolver 列表。
// IPv4 resolver 始终包含；IPv6 resolver 仅在能连通时加入。
func dnsResolvers() []string {
	resolvers := []string{"1.1.1.1:53", "8.8.8.8:53"}
	conn, err := net.DialTimeout("udp6", "[2606:4700:4700::1111]:53", 2*time.Second)
	if err == nil {
		conn.Close()
		resolvers = append(resolvers, "[2606:4700:4700::1111]:53")
	}
	return resolvers
}

func isValidDomain(d string) bool {
	if d == "" || len(d) > 253 || strings.HasPrefix(d, ".") || strings.HasPrefix(d, "-") {
		return false
	}
	for _, r := range d {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '-') {
			return false
		}
	}
	return !strings.Contains(d, "..")
}

// Managed 返回当前正在管理的域名快照（无序）。
func (m *Manager) Managed() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.managed))
	for d := range m.managed {
		out = append(out, d)
	}
	return out
}

// TLSConfig 返回配好 GetCertificate 的 *tls.Config，sniproxy 模式 B 直接用。
func (m *Manager) TLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: m.cfg.GetCertificate,
		NextProtos:     []string{"h2", "http/1.1"},
	}
}

// Close 停止所有后台 renewal goroutine 并清理 Cache。可重复调用。
func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		m.cancel()
		m.cache.Stop()
	})
}
