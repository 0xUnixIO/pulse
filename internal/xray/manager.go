// Package xray 管理 xray-core 的 in-process 生命周期，并通过内部 stats API 采集流量数据。
// xray 以 in-process 嵌入方式运行，无需系统安装 xray 二进制。
package xray

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/0xUnixIO/Xray-core/app/stats/command"
	"github.com/0xUnixIO/Xray-core/common/conntrack"
	xrayCommonLog "github.com/0xUnixIO/Xray-core/common/log"
	"github.com/0xUnixIO/Xray-core/common/protocol"
	xrayCore "github.com/0xUnixIO/Xray-core/core"
	"github.com/0xUnixIO/Xray-core/features/inbound"
	"github.com/0xUnixIO/Xray-core/features/stats"
	"github.com/0xUnixIO/Xray-core/infra/conf/serial"
	"github.com/0xUnixIO/Xray-core/proxy"
	anytlsProxy "github.com/0xUnixIO/Xray-core/proxy/anytls"
	ss2022Proxy "github.com/0xUnixIO/Xray-core/proxy/shadowsocks_2022"
	trojanProxy "github.com/0xUnixIO/Xray-core/proxy/trojan"
	vlessProxy "github.com/0xUnixIO/Xray-core/proxy/vless"

	// 注册所有必要的 xray 协议、transport 及 app（必须 side-effect import）
	_ "github.com/0xUnixIO/Xray-core/app/dispatcher"
	_ "github.com/0xUnixIO/Xray-core/app/log"
	_ "github.com/0xUnixIO/Xray-core/app/policy"
	_ "github.com/0xUnixIO/Xray-core/app/proxyman/inbound"
	_ "github.com/0xUnixIO/Xray-core/app/proxyman/outbound"
	_ "github.com/0xUnixIO/Xray-core/app/router"
	_ "github.com/0xUnixIO/Xray-core/app/stats"
	_ "github.com/0xUnixIO/Xray-core/app/stats/command"
	_ "github.com/0xUnixIO/Xray-core/main/json"
	_ "github.com/0xUnixIO/Xray-core/proxy/anytls"
	_ "github.com/0xUnixIO/Xray-core/proxy/blackhole"
	_ "github.com/0xUnixIO/Xray-core/proxy/dokodemo"
	_ "github.com/0xUnixIO/Xray-core/proxy/freedom"
	_ "github.com/0xUnixIO/Xray-core/proxy/shadowsocks"
	_ "github.com/0xUnixIO/Xray-core/proxy/trojan"
	_ "github.com/0xUnixIO/Xray-core/proxy/vless/inbound"
	_ "github.com/0xUnixIO/Xray-core/proxy/vless/outbound"
	_ "github.com/0xUnixIO/Xray-core/transport/internet/reality"
	_ "github.com/0xUnixIO/Xray-core/transport/internet/tcp"
	_ "github.com/0xUnixIO/Xray-core/transport/internet/tls"
	_ "github.com/0xUnixIO/Xray-core/transport/internet/udp"
	_ "github.com/0xUnixIO/Xray-core/transport/internet/websocket"

	"pulse/internal/coremanager"
)

// 编译期检查：确保 *Manager 满足 coremanager.Manager 接口。
var _ coremanager.Manager = (*Manager)(nil)

const maxLogs = 2000

// maxSourceIPsPerUser 限制每用户每个采集周期内追踪的最大源 IP 数量。
// 防止 IPv6 轮换等场景下内存无限膨胀；超过此值后新 IP 不再记录。
const maxSourceIPsPerUser = 256

// maxAccessLogBuf access log ring buffer 上限，超出后丢弃最旧的条目。
const maxAccessLogBuf = 5000

// ErrNotRunning xray 未运行时的错误，满足 errors.Is(err, coremanager.ErrNotRunning)。
var ErrNotRunning = fmt.Errorf("xray is not running: %w", coremanager.ErrNotRunning)

// Manager 管理 xray-core in-process 实例的启停及流量采集。
type Manager struct {
	mu      sync.Mutex
	resetMu sync.Mutex // 序列化 Usage(reset=true) 调用，防止并发竞争
	// xray-core in-process 实例（替代原来的 *exec.Cmd）
	instance  *xrayCore.Instance
	startedAt time.Time
	// instanceCancel 取消实例根 ctx。xray 每个连接的 ctx 都派生自它
	// （app/proxyman/inbound/worker.go 的 context.WithCancel(w.ctx)），
	// 因此取消它才能断开存量连接——instance.Close() 只关 listener，
	// 已建立的连接会继续收发。
	instanceCancel context.CancelFunc
	lastConfig     string
	configFile     string // 持久化路径（用于进程重启恢复）

	logs        []string
	subscribers map[int64]chan string
	nextSubID   int64

	// activeSessions 通过解析日志行维护活跃连接状态：sessionID → 复合用户名（user@@@tag）。
	// 仅在 mu 下访问（appendLogLocked 和 Usage 均持有 mu）。
	activeSessions map[string]string

	// sessionSourceIPs 通过解析 xray access log 追踪每用户的源 IP 集合：
	// compositeUser（user@@@tag） → IP 集合。仅在 mu 下访问。
	// Usage(reset=true) 时快照并清空，相当于"最近一个采集周期内见到的 IP"。
	sessionSourceIPs map[string]map[string]struct{}

	// sessionProtocol 记录每个会话的入站协议类型（vless/trojan/ss2022/anytls）。
	sessionProtocol map[string]string
	// sessionRemoteIP 记录每个会话出站后的真实目标 IP（从 proxy/freedom: connection opened 解析）。
	sessionRemoteIP map[string]string
	// lastDispatcherSID 记录最近一次 app/dispatcher 行对应的 session ID，
	// 用于将 access log 行（无 session ID 前缀）与会话信息关联。
	lastDispatcherSID string

	// accessLogBuf 缓冲最近解析出的 access log 条目，供控制面定期拉取。
	// DrainAccessLogs() 调用后清空。仅在 mu 下访问。
	accessLogBuf []coremanager.AccessLogEntry

	// 实时网速缓存（后台协程每 2s 采样更新）
	speedMu     sync.RWMutex
	nodeUpBps   int64
	nodeDownBps int64
	samplerStop chan struct{} // 关闭此 channel 停止采样协程

	netStatsMu    sync.Mutex
	netRxBaseline int64
	netTxBaseline int64
	netStatsReady bool
}

// NewManager 创建 Manager。
// configFile 为保存最近一次配置的文件路径；启动成功后写入，显式 Stop 后删除。
// 传入空字符串则不持久化（适用于测试）。
func NewManager(configFile string) *Manager {
	return &Manager{
		logs:             make([]string, 0, maxLogs),
		configFile:       configFile,
		subscribers:      make(map[int64]chan string),
		activeSessions:   make(map[string]string),
		sessionSourceIPs: make(map[string]map[string]struct{}),
		sessionProtocol:  make(map[string]string),
		sessionRemoteIP:  make(map[string]string),
	}
}

// SavedConfig 读取磁盘上持久化的配置（进程重启后自动恢复用）。
// 文件不存在时返回空字符串，不报错。
func (m *Manager) SavedConfig() string {
	if m.configFile == "" {
		return ""
	}
	data, err := os.ReadFile(m.configFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// Config 返回最近一次成功启动时使用的配置（JSON 字符串）。
func (m *Manager) Config() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastConfig
}

// Start 以 in-process 方式启动 xray-core 实例。
// config 为 xray JSON 配置字符串；in-process 不需要外部 gRPC 端口，api.listen 可忽略。
func (m *Manager) Start(config string) error {
	m.mu.Lock()
	if m.instance != nil {
		m.mu.Unlock()
		return errors.New("xray is already running")
	}
	m.mu.Unlock()

	// 移除配置中的 api.listen（in-process 不需要 gRPC 监听端口）
	cleanedConfig := stripAPIListen(config)

	// 解析 JSON 配置为 protobuf Config
	pbConfig, err := serial.LoadJSONConfig(bytes.NewReader([]byte(cleanedConfig)))
	if err != nil {
		return fmt.Errorf("parse xray config: %w", err)
	}

	// 创建 xray-core 实例。用可取消的根 ctx，使 Stop 能强制断开存量连接。
	instanceCtx, instanceCancel := context.WithCancel(context.Background())
	instance, err := xrayCore.NewWithContext(instanceCtx, pbConfig)
	if err != nil {
		instanceCancel()
		return fmt.Errorf("create xray instance: %w", err)
	}

	// 启动实例
	if err := instance.Start(); err != nil {
		instanceCancel()
		return fmt.Errorf("start xray instance: %w", err)
	}

	stopCh := make(chan struct{})

	m.mu.Lock()
	m.instance = instance
	m.instanceCancel = instanceCancel
	m.startedAt = time.Now().UTC()
	m.lastConfig = config
	m.samplerStop = stopCh
	m.appendLogLocked("xray started (in-process, version " + xrayCore.Version() + ")")
	configFile := m.configFile
	m.mu.Unlock()

	m.resetNetworkBaseline()

	// 启动网速采样 goroutine
	go m.runSpeedSampler(stopCh)

	// 接管 xray-core 的全局 log handler：同时写 stdout（保留 journald）和面板缓冲区。
	// 必须在 mu.Unlock() 之后注册，避免 handler 回调时与 mu 产生死锁。
	logCh := make(chan string, 256)
	stdoutHandler := xrayCommonLog.NewLogger(xrayCommonLog.CreateStdoutLogWriter())
	xrayCommonLog.RegisterHandler(&xrayLogHandler{stdout: stdoutHandler, ch: logCh})
	go m.runLogCapture(logCh, stopCh)

	// 持久化配置，进程重启后可自动恢复
	if configFile != "" {
		_ = os.WriteFile(configFile, []byte(config), 0600)
	}

	return nil
}

// Stop 停止 xray-core in-process 实例及 AnyTLS 入站服务，并断开所有存量连接。
func (m *Manager) Stop() error {
	return m.stop(true)
}

// stop 停止实例。dropConns 决定是否强制断开存量连接：
//
//   - 显式停止（Stop）传 true：服务要停了，连接不该继续存活。
//   - 配置重载（Restart）传 false：旧实例交出监听端口，存量连接留在旧实例上
//     自然结束，新连接由新实例接管——即优雅重载，不误伤在线用户。
//     超限用户的止血由 KickUser 精确处理，不依赖重启。
func (m *Manager) stop(dropConns bool) error {
	m.mu.Lock()
	instance := m.instance
	if instance == nil {
		m.mu.Unlock()
		return ErrNotRunning
	}
	// 立即清除引用，防止并发操作
	m.instance = nil
	instanceCancel := m.instanceCancel
	m.instanceCancel = nil
	m.startedAt = time.Time{}
	stopCh := m.samplerStop
	m.samplerStop = nil
	configFile := m.configFile
	m.activeSessions = make(map[string]string)
	m.sessionSourceIPs = make(map[string]map[string]struct{})
	m.sessionProtocol = make(map[string]string)
	m.sessionRemoteIP = make(map[string]string)
	m.lastDispatcherSID = ""
	m.accessLogBuf = m.accessLogBuf[:0]
	m.appendLogLocked("xray stopping")
	m.mu.Unlock()

	if stopCh != nil {
		close(stopCh)
	}

	// 卸载自定义 log handler，恢复默认 stdout 输出，避免 logCapture goroutine 退出后悬空引用
	xrayCommonLog.RegisterHandler(xrayCommonLog.NewLogger(xrayCommonLog.CreateStdoutLogWriter()))

	closeErr := instance.Close()

	// instance.Close() 只关闭 listener 与各 feature，已建立的连接仍在各自
	// goroutine 里收发；取消实例根 ctx 才能真正断链。
	if dropConns && instanceCancel != nil {
		instanceCancel()
	}

	if closeErr != nil {
		return fmt.Errorf("close xray instance: %w", closeErr)
	}

	// 显式停止时清除持久化配置，避免下次进程启动时自动恢复
	if configFile != "" {
		_ = os.Remove(configFile)
	}

	return nil
}

// Restart 重启 xray。若配置未变化则跳过。
//
// 走优雅重载：存量连接留在旧实例上自然结束，不因一次配置下发把在线用户全断掉。
// 需要立即切断某个用户的流量时用 KickUser，它精确到用户、不波及他人。
func (m *Manager) Restart(config string) error {
	m.mu.Lock()
	unchanged := m.instance != nil && config == m.lastConfig
	m.mu.Unlock()

	if unchanged {
		return nil
	}

	if err := m.stop(false); err != nil && !errors.Is(err, ErrNotRunning) {
		return err
	}
	return m.Start(config)
}

// Status 返回 xray 运行状态。
func (m *Manager) Status() coremanager.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return coremanager.Status{
		Running:   m.instance != nil,
		StartedAt: m.startedAt,
	}
}

// Usage 通过 xray-core stats.Manager 查询用户和入站流量（in-process，无 gRPC 网络开销）。
func (m *Manager) Usage(reset bool) coremanager.UsageStats {
	m.mu.Lock()
	instance := m.instance
	startedAt := m.startedAt
	m.mu.Unlock()

	running := instance != nil
	result := coremanager.UsageStats{
		Available: running,
		Running:   running,
		StartedAt: startedAt,
		Users:     make([]coremanager.UserUsage, 0),
	}
	if !running {
		return result
	}

	m.resetMu.Lock()
	defer m.resetMu.Unlock()

	// 从 in-process 实例中获取 stats.Manager
	sm, ok := instance.GetFeature(stats.ManagerType()).(stats.Manager)
	if sm == nil || !ok {
		return result
	}

	// 使用 command.NewStatsServer 直接调用 QueryStats（无 gRPC 网络，纯函数调用）
	handler := command.NewStatsServer(sm)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := handler.QueryStats(ctx, &command.QueryStatsRequest{
		Pattern: ">>>traffic>>>",
		Reset_:  reset,
	})
	if err != nil {
		m.applyNetworkTraffic(&result, reset)
		return result
	}

	applyTrafficStats(&result, resp.GetStat())
	m.applyNetworkTraffic(&result, reset)

	// 填入实时速度
	m.speedMu.RLock()
	result.UploadSpeed = m.nodeUpBps
	result.DownloadSpeed = m.nodeDownBps
	m.speedMu.RUnlock()

	// 从活跃会话 map 聚合每用户连接数（基于日志解析，xray 无 Clash API）
	// 按 compositeUser 统计，避免多 inbound 用户被 jobs.SyncUsage 重复累加。
	m.mu.Lock()
	cfg := m.lastConfig
	userConnCount := make(map[string]int, len(m.activeSessions))
	for _, compositeUser := range m.activeSessions {
		userConnCount[compositeUser]++
	}
	totalActiveSessions := len(m.activeSessions)

	// 快照源 IP 并按需清空（reset=true 时清空，模拟滑动窗口语义）
	userSourceIPs := make(map[string]map[string]struct{}, len(m.sessionSourceIPs))
	for user, ips := range m.sessionSourceIPs {
		cp := make(map[string]struct{}, len(ips))
		for ip := range ips {
			cp[ip] = struct{}{}
		}
		userSourceIPs[user] = cp
	}
	if reset {
		m.sessionSourceIPs = make(map[string]map[string]struct{})
	}
	m.mu.Unlock()

	// 将连接数、源 IP、设备数写入对应用户
	for i := range result.Users {
		compositeUser := result.Users[i].User
		if n, ok := userConnCount[compositeUser]; ok {
			result.Users[i].Connections = n
		}
		if ips, ok := userSourceIPs[compositeUser]; ok && len(ips) > 0 {
			ipList := make([]string, 0, len(ips))
			for ip := range ips {
				ipList = append(ipList, ip)
			}
			sort.Strings(ipList)
			result.Users[i].SourceIPs = ipList
			result.Users[i].Devices = len(ipList)
		}
	}

	// 节点总连接数：优先使用日志解析值，回退到 TCP 端口计数
	if totalActiveSessions > 0 {
		result.Connections = totalActiveSessions
	} else if cfg != "" {
		result.Connections = countActiveConnections(extractInboundPorts(cfg))
	}

	sort.Slice(result.Users, func(i, j int) bool {
		left := result.Users[i].UploadTotal + result.Users[i].DownloadTotal
		right := result.Users[j].UploadTotal + result.Users[j].DownloadTotal
		if left == right {
			return result.Users[i].User < result.Users[j].User
		}
		return left > right
	})

	return result
}

func applyTrafficStats(result *coremanager.UsageStats, statsList []*command.Stat) {
	userTraffic := make(map[string]*coremanager.UserUsage)
	var inboundUpload, inboundDownload int64
	hasInboundStats := false

	for _, stat := range statsList {
		if stat == nil {
			continue
		}
		parts := strings.SplitN(stat.GetName(), ">>>", 4)
		if len(parts) != 4 || parts[2] != "traffic" {
			continue
		}
		direction := parts[3]

		switch parts[0] {
		case "user":
			username := parts[1]
			uu, exists := userTraffic[username]
			if !exists {
				uu = &coremanager.UserUsage{User: username}
				userTraffic[username] = uu
			}
			switch direction {
			case "uplink":
				uu.UploadTotal = stat.GetValue()
			case "downlink":
				uu.DownloadTotal = stat.GetValue()
			}
		case "inbound":
			hasInboundStats = true
			switch direction {
			case "uplink":
				inboundUpload += stat.GetValue()
			case "downlink":
				inboundDownload += stat.GetValue()
			}
		}
	}

	var userUpload, userDownload int64
	for _, uu := range userTraffic {
		result.Users = append(result.Users, *uu)
		userUpload += uu.UploadTotal
		userDownload += uu.DownloadTotal
	}

	if hasInboundStats {
		result.UploadTotal = inboundUpload
		result.DownloadTotal = inboundDownload
		return
	}
	result.UploadTotal = userUpload
	result.DownloadTotal = userDownload
}

func (m *Manager) resetNetworkBaseline() {
	rx, tx, err := readNetStats()
	if err != nil {
		return
	}
	m.netStatsMu.Lock()
	m.netRxBaseline = rx
	m.netTxBaseline = tx
	m.netStatsReady = true
	m.netStatsMu.Unlock()
}

func (m *Manager) applyNetworkTraffic(result *coremanager.UsageStats, reset bool) {
	rx, tx, err := readNetStats()
	if err != nil {
		m.netStatsMu.Lock()
		networkSourceActive := m.netStatsReady
		m.netStatsMu.Unlock()
		if networkSourceActive {
			// 保留 baseline，待下次读取成功时一次性结算，避免回退到 Xray
			// 统计后又被网卡累计差值重复记账。
			result.UploadTotal = 0
			result.DownloadTotal = 0
		}
		return
	}

	m.netStatsMu.Lock()
	defer m.netStatsMu.Unlock()
	if !m.netStatsReady || rx < m.netRxBaseline || tx < m.netTxBaseline {
		m.netRxBaseline = rx
		m.netTxBaseline = tx
		m.netStatsReady = true
		return
	}

	result.UploadTotal = tx - m.netTxBaseline
	result.DownloadTotal = rx - m.netRxBaseline
	if reset {
		m.netRxBaseline = rx
		m.netTxBaseline = tx
	}
}

// Version 返回内嵌的 xray-core 版本字符串。
func (m *Manager) Version(context.Context) (string, error) {
	return "Xray " + xrayCore.Version(), nil
}

// RuntimeInfo 返回 xray 运行时信息（in-process 模式下始终 Available）。
func (m *Manager) RuntimeInfo(context.Context) coremanager.RuntimeInfo {
	return buildInfo()
}

// Logs 返回最近的内部状态日志行。
func (m *Manager) Logs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.logs))
	copy(out, m.logs)
	return out
}

// Subscribe 注册日志订阅者，返回订阅 ID 和只读 channel。
func (m *Manager) Subscribe() (id int64, ch <-chan string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextSubID++
	id = m.nextSubID
	c := make(chan string, 64)
	m.subscribers[id] = c
	return id, c
}

// Unsubscribe 注销订阅者并关闭其 channel。
func (m *Manager) Unsubscribe(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.subscribers[id]; ok {
		close(c)
		delete(m.subscribers, id)
	}
}

// ─── 内部辅助 ─────────────────────────────────────────────────────────────────

func (m *Manager) appendLogLocked(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if len(m.logs) == maxLogs {
		copy(m.logs, m.logs[1:])
		m.logs = m.logs[:maxLogs-1]
	}
	m.logs = append(m.logs, line)
	for _, c := range m.subscribers {
		select {
		case c <- line:
		default:
		}
	}
	// 解析日志行，实时维护活跃会话 map
	m.parseSessionLog(line)
}

// parseSessionLog 从单条日志行更新活跃会话状态。
// 必须在 m.mu 已持有时调用（由 appendLogLocked 保证）。
//
// xray 日志格式：[Level] [SESSION_ID] module: message
//
// 开启会话：
//
//	[Info] [SESSION_ID] proxy/anytls: anytls: USER@@@TAG tunnelling to tcp:HOST:PORT
//
// 关闭会话：
//
//	[Info] [SESSION_ID] app/proxyman/outbound: ... failed to process outbound traffic
//
// Access log（由 dispatcher 通过 log.Record 发出，无 [Level]/[SESSION_ID] 前缀）：
//
//	from IP:PORT accepted tcp:HOST:PORT [detour] email: USER@@@TAG
func (m *Manager) parseSessionLog(line string) {
	// Access log 解析：提取源 IP 用于设备追踪。
	// 格式：from <addr> accepted <dest> ... email: <compositeUser>
	if strings.HasPrefix(line, "from ") {
		m.parseAccessLog(line)
		return
	}

	// 跳过日志级别 [Info]/[Debug] 等，找第二对方括号中的 session ID
	// 格式：[Level] [SESSION_ID] ...
	firstClose := strings.Index(line, "]")
	if firstClose < 0 {
		return
	}
	after := line[firstClose+1:]
	after = strings.TrimLeft(after, " ")
	if len(after) == 0 || after[0] != '[' {
		return
	}
	secondClose := strings.Index(after, "]")
	if secondClose <= 1 {
		return
	}
	sessionID := after[1:secondClose]
	// session ID 只含数字
	for _, c := range sessionID {
		if c < '0' || c > '9' {
			return
		}
	}

	rest := after[secondClose+1:]

	// 协议类型：从 inbound 首行推断
	switch {
	case strings.Contains(rest, " proxy/vless/inbound:"):
		m.sessionProtocol[sessionID] = "vless"
	case strings.Contains(rest, " proxy/trojan:") && strings.Contains(rest, "received request"):
		m.sessionProtocol[sessionID] = "trojan"
	case strings.Contains(rest, " proxy/shadowsocks_2022:"):
		m.sessionProtocol[sessionID] = "ss2022"
	case strings.Contains(rest, " proxy/shadowsocks:"):
		m.sessionProtocol[sessionID] = "shadowsocks"
	case strings.Contains(rest, " proxy/hysteria:") || strings.Contains(rest, " proxy/hysteria/"):
		m.sessionProtocol[sessionID] = "hysteria"
	}

	// dispatcher 行：记录最近的 session ID，供 access log 行关联
	if strings.Contains(rest, " app/dispatcher:") {
		m.lastDispatcherSID = sessionID
	}

	// proxy/freedom: connection opened → 提取远端真实 IP
	if strings.Contains(rest, "proxy/freedom: connection opened") {
		const remotePrefix = "remote endpoint "
		if ri := strings.LastIndex(rest, remotePrefix); ri >= 0 {
			remoteAddr := strings.TrimSpace(rest[ri+len(remotePrefix):])
			remoteHost, _, err := net.SplitHostPort(remoteAddr)
			if err != nil {
				remoteHost = remoteAddr
			}
			if remoteHost != "" {
				m.sessionRemoteIP[sessionID] = remoteHost
			}
		}
	}

	// AnyTLS：tunnelling 日志包含复合用户名
	if idx := strings.Index(rest, " tunnelling to "); idx >= 0 {
		prefix := "anytls: "
		if pi := strings.LastIndex(rest[:idx], prefix); pi >= 0 {
			compositeUser := strings.TrimSpace(rest[pi+len(prefix) : idx])
			if compositeUser != "" {
				m.activeSessions[sessionID] = compositeUser
			}
		}
		m.sessionProtocol[sessionID] = "anytls"
		return
	}

	// 连接结束（正常或异常），清理 session 相关 map
	// xray 在连接结束时输出 "connection ends" 或 "failed to process outbound traffic"
	if strings.Contains(rest, "connection ends") || strings.Contains(rest, "failed to process outbound traffic") {
		delete(m.activeSessions, sessionID)
		delete(m.sessionProtocol, sessionID)
		delete(m.sessionRemoteIP, sessionID)
	}
}

// parseAccessLog 从 xray access log 行中提取源 IP、目标地址、路由标签和复合用户名。
// 必须在 m.mu 已持有时调用。
//
// 格式：from <addr> accepted <dest> [<detour>] email: <compositeUser>
func (m *Manager) parseAccessLog(line string) {
	accIdx := strings.Index(line, " accepted ")
	if accIdx < 0 {
		return
	}

	const emailPrefix = " email: "
	emailIdx := strings.LastIndex(line, emailPrefix)
	if emailIdx < 0 {
		return
	}
	compositeUser := strings.TrimSpace(line[emailIdx+len(emailPrefix):])
	if compositeUser == "" {
		return
	}

	// 提取源 IP 和源端口
	addrStr := line[len("from "):accIdx]
	host, srcPort, err := net.SplitHostPort(addrStr)
	if err != nil {
		host = addrStr
		srcPort = ""
	}
	if net.ParseIP(host) == nil {
		return
	}

	if m.sessionSourceIPs[compositeUser] == nil {
		m.sessionSourceIPs[compositeUser] = make(map[string]struct{})
	}
	if len(m.sessionSourceIPs[compositeUser]) < maxSourceIPsPerUser {
		m.sessionSourceIPs[compositeUser][host] = struct{}{}
	}

	// 提取目标地址和路由标签
	// middle 形如："example.com:443 [HK SH >> direct]"
	// IPv6 目标地址形如："tcp:[2606:4700::1]:443 [HK >> direct]"
	// 关键：路由标签的 ] 总在 middle 末尾，IPv6 的 ] 后面还有 :port，据此区分
	middle := strings.TrimSpace(line[accIdx+len(" accepted ") : emailIdx])
	var dest, routeTag string
	if bracketEnd := strings.LastIndex(middle, "]"); bracketEnd >= 0 {
		if strings.TrimSpace(middle[bracketEnd+1:]) == "" {
			// ] 在末尾 → 最后一个 [...] 是路由标签
			if bracketStart := strings.LastIndex(middle[:bracketEnd], "["); bracketStart >= 0 {
				dest = strings.TrimSpace(middle[:bracketStart])
				routeTag = middle[bracketStart+1 : bracketEnd]
			} else {
				dest = middle
			}
		} else {
			// ] 后面还有内容（如 IPv6 地址 :port），整体当作目标地址
			dest = middle
		}
	} else {
		dest = middle
	}
	if dest == "" {
		return
	}

	// 解析复合用户名 username@inbound_tag（分隔符由 proxycfg.UserInboundSep 定义）
	// 用 LastIndex 避免用户名本身含 @ 时误切
	user, inboundTag := compositeUser, ""
	if idx := strings.LastIndex(compositeUser, "@"); idx >= 0 {
		user = compositeUser[:idx]
		inboundTag = compositeUser[idx+1:]
	}

	// Protocol/SessionID 通过 lastDispatcherSID 关联：access log 行自身无 session ID，
	// 依赖"access log 行总紧跟在 app/dispatcher 行之后"的日志顺序。
	// 高并发时两条连接的 dispatcher 行可能交替出现，导致协议字段偶发性串扰，
	// 作为参考信息使用，不应依赖其 100% 准确。
	entry := coremanager.AccessLogEntry{
		SourceIP:    host,
		SourcePort:  srcPort,
		Destination: dest,
		RouteTag:    routeTag,
		Protocol:    m.sessionProtocol[m.lastDispatcherSID],
		User:        user,
		InboundTag:  inboundTag,
		SessionID:   m.lastDispatcherSID,
		Time:        time.Now(),
	}
	if len(m.accessLogBuf) >= maxAccessLogBuf {
		copy(m.accessLogBuf, m.accessLogBuf[1:])
		m.accessLogBuf = m.accessLogBuf[:len(m.accessLogBuf)-1]
	}
	m.accessLogBuf = append(m.accessLogBuf, entry)
}

// DrainAccessLogs 取走并清空 access log 缓冲区，同时填充 RemoteIP（从 sessionRemoteIP 关联）。
func (m *Manager) DrainAccessLogs() []coremanager.AccessLogEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.accessLogBuf) == 0 {
		return nil
	}
	out := make([]coremanager.AccessLogEntry, len(m.accessLogBuf))
	for i, e := range m.accessLogBuf {
		if e.SessionID != "" {
			if remoteIP, ok := m.sessionRemoteIP[e.SessionID]; ok {
				e.RemoteIP = remoteIP
			}
		}
		out[i] = e
	}
	m.accessLogBuf = m.accessLogBuf[:0]
	return out
}

// buildInfo 从 Go 模块信息中读取 xray-core 版本。
func buildInfo() coremanager.RuntimeInfo {
	info := coremanager.RuntimeInfo{
		Available: true,
		Module:    "github.com/0xUnixIO/Xray-core",
		Version:   xrayCore.Version(),
	}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	for _, dep := range bi.Deps {
		if dep.Path == info.Module {
			info.Version = dep.Version
			return info
		}
	}
	return info
}

// stripAPIListen 从 xray JSON 配置中移除 api.listen 字段。
// in-process 模式下不需要 gRPC 监听端口；Stats 功能通过 in-process API 直接访问。
// 如果解析失败则返回原始配置（安全降级）。
func stripAPIListen(config string) string {
	// 仅在含有 "listen" 时才尝试处理，减少不必要的 JSON 解析
	if !strings.Contains(config, `"listen"`) {
		return config
	}
	// 逐行扫描，找到 api 对象中的 listen 字段并替换为空字符串端口
	// 简单方案：直接返回原始配置，xray-core 会忽略 listen 若已有内部注册
	// 实际上 xray-core in-process 的 dokodemo-door api inbound 监听 port:0 会绑定随机端口，
	// 这对 in-process 调用没有影响，故直接使用原始配置即可。
	return config
}

// defaultRouteInterface 返回 IPv4 默认路由对应的网卡名。
func defaultRouteInterface() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()

	bestIface := ""
	bestMetric := int64(1<<63 - 1)
	scanner := bufio.NewScanner(f)
	scanner.Scan() // 跳过表头
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 || fields[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseInt(fields[3], 16, 64)
		if err != nil || flags&1 == 0 {
			continue
		}
		metric, err := strconv.ParseInt(fields[6], 10, 64)
		if err != nil {
			continue
		}
		if metric < bestMetric {
			bestIface = fields[0]
			bestMetric = metric
		}
	}
	return bestIface
}

// readNetStats 读取 /proc/net/dev，优先返回默认路由网卡的累计 rx/tx 字节数。
func readNetStats() (rx, tx int64, err error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	defaultIface := defaultRouteInterface()
	scanner := bufio.NewScanner(f)
	scanner.Scan() // 跳过表头第一行
	scanner.Scan() // 跳过表头第二行
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:colon])
		if iface == "lo" || (defaultIface != "" && iface != defaultIface) {
			continue
		}
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 9 {
			continue
		}
		r, e1 := strconv.ParseInt(fields[0], 10, 64) // rx_bytes
		t, e2 := strconv.ParseInt(fields[8], 10, 64) // tx_bytes
		if e1 != nil || e2 != nil {
			continue
		}
		rx += r
		tx += t
	}
	return rx, tx, scanner.Err()
}

// runSpeedSampler 每 2 秒读取 /proc/net/dev 差值，更新节点实时网速缓存。
func (m *Manager) runSpeedSampler(stop <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	prevRx, prevTx, _ := readNetStats()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			rx, tx, err := readNetStats()
			if err != nil {
				continue
			}
			var up, down int64
			if rx >= prevRx && tx >= prevTx {
				down = (rx - prevRx) / 2
				up = (tx - prevTx) / 2
			}
			prevRx, prevTx = rx, tx

			m.speedMu.Lock()
			m.nodeUpBps = up
			m.nodeDownBps = down
			m.speedMu.Unlock()
		}
	}
}

// xrayLogHandler 实现 xrayCommonLog.Handler 接口，
// 接管 xray-core 全局 log handler 后同时写 stdout 和 manager 日志缓冲区。
type xrayLogHandler struct {
	stdout xrayCommonLog.Handler
	ch     chan<- string
}

func (h *xrayLogHandler) Handle(msg xrayCommonLog.Message) {
	if gm, ok := msg.(*xrayCommonLog.GeneralMessage); ok && gm.Severity == xrayCommonLog.Severity_Debug {
		return
	}
	h.stdout.Handle(msg) // 保留 journald 输出
	line := msg.String()
	select {
	case h.ch <- line:
	default:
		// 缓冲区满则丢弃，不阻塞 xray 内部 goroutine
	}
}

// runLogCapture 从 ch 读取 xray 日志行并写入 manager 日志缓冲区，直到 stop 关闭。
func (m *Manager) runLogCapture(ch <-chan string, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			m.mu.Lock()
			m.appendLogLocked(line)
			m.mu.Unlock()
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 动态用户管理（热增删，不重启 Xray）

// AddUser 向运行中的 Xray inbound 热增用户，无需重启、不断现有连接。
func (m *Manager) AddUser(ctx context.Context, cfg coremanager.UserConfig) error {
	m.mu.Lock()
	instance := m.instance
	m.mu.Unlock()
	if instance == nil {
		return ErrNotRunning
	}

	// 构建协议对应的 MemoryAccount
	account, err := buildAccount(cfg)
	if err != nil {
		return err
	}

	memUser := &protocol.MemoryUser{
		Email:   cfg.Email,
		Level:   0,
		Account: account,
	}

	return xrayAddUser(ctx, instance, cfg.InboundTag, memUser)
}

// KickUser 强制断开该用户在本节点的全部存量连接，返回被断开的连接数。
//
// RemoveUser 只更新 inbound validator，拦截的是后续新连接；鉴权仅在建链时
// 做一次，存量连接不会被回查，会一直跑到自己结束（100 GB 额度曾因此跑到
// 128 GB）。此方法按用户精确断连，同节点其他用户不受影响。
func (m *Manager) KickUser(_ context.Context, email string) (int, error) {
	m.mu.Lock()
	running := m.instance != nil
	m.mu.Unlock()
	if !running {
		return 0, ErrNotRunning
	}
	return conntrack.Kick(email), nil
}

// RemoveUser 从运行中的 Xray inbound 热删用户。
func (m *Manager) RemoveUser(ctx context.Context, inboundTag, email string) error {
	m.mu.Lock()
	instance := m.instance
	m.mu.Unlock()
	if instance == nil {
		return ErrNotRunning
	}

	return xrayRemoveUser(ctx, instance, inboundTag, email)
}

// buildAccount 根据协议类型将 UserConfig 转成 protocol.Account。
func buildAccount(cfg coremanager.UserConfig) (protocol.Account, error) {
	switch cfg.Protocol {
	case "vless":
		return (&vlessProxy.Account{
			Id:   cfg.UUID,
			Flow: cfg.Flow,
		}).AsAccount()
	case "trojan":
		return (&trojanProxy.Account{
			Password: cfg.Password,
		}).AsAccount()
	case "shadowsocks":
		return (&ss2022Proxy.Account{
			Key: cfg.Password, // per-user PSK
		}).AsAccount()
	case "anytls":
		return &anytlsProxy.MemoryAccount{Password: cfg.Password}, nil
	default:
		return nil, fmt.Errorf("AddUser: unsupported protocol %q", cfg.Protocol)
	}
}

// xrayAddUser 通过 in-process proxy.UserManager 接口热增用户。
func xrayAddUser(ctx context.Context, instance *xrayCore.Instance, tag string, user *protocol.MemoryUser) error {
	ihm, ok := instance.GetFeature(inbound.ManagerType()).(inbound.Manager)
	if !ok {
		return fmt.Errorf("AddUser: inbound manager not available")
	}
	handler, err := ihm.GetHandler(ctx, tag)
	if err != nil {
		return fmt.Errorf("AddUser: get handler %q: %w", tag, err)
	}
	gi, ok := handler.(proxy.GetInbound)
	if !ok {
		return fmt.Errorf("AddUser: handler %q does not support GetInbound", tag)
	}
	um, ok := gi.GetInbound().(proxy.UserManager)
	if !ok {
		return fmt.Errorf("AddUser: inbound %q does not support UserManager", tag)
	}
	return um.AddUser(ctx, user)
}

// xrayRemoveUser 通过 in-process proxy.UserManager 接口热删用户。
func xrayRemoveUser(ctx context.Context, instance *xrayCore.Instance, tag, email string) error {
	ihm, ok := instance.GetFeature(inbound.ManagerType()).(inbound.Manager)
	if !ok {
		return fmt.Errorf("RemoveUser: inbound manager not available")
	}
	handler, err := ihm.GetHandler(ctx, tag)
	if err != nil {
		return fmt.Errorf("RemoveUser: get handler %q: %w", tag, err)
	}
	gi, ok := handler.(proxy.GetInbound)
	if !ok {
		return fmt.Errorf("RemoveUser: handler %q does not support GetInbound", tag)
	}
	um, ok := gi.GetInbound().(proxy.UserManager)
	if !ok {
		return fmt.Errorf("RemoveUser: inbound %q does not support UserManager", tag)
	}
	return um.RemoveUser(ctx, email)
}

// extractInboundPorts 从 xray JSON 配置中提取所有 inbound 的监听端口。
func extractInboundPorts(config string) map[int]struct{} {
	ports := make(map[int]struct{})
	var raw struct {
		Inbounds []struct {
			Port int `json:"port"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(config), &raw); err != nil {
		return ports
	}
	for _, ib := range raw.Inbounds {
		if ib.Port > 0 {
			ports[ib.Port] = struct{}{}
		}
	}
	return ports
}

// countActiveConnections 统计与给定端口集合上 ESTABLISHED 状态的 TCP 连接数，
// 通过读取 /proc/net/tcp 和 /proc/net/tcp6 实现（Linux 专用）。
func countActiveConnections(ports map[int]struct{}) int {
	if len(ports) == 0 {
		return 0
	}
	total := 0
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Scan() // 跳过标题行
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 {
				continue
			}
			// fields[3] 是状态：01 = ESTABLISHED
			if fields[3] != "01" {
				continue
			}
			// fields[1] 是 local_address：XXXXXXXX:PPPP（小端 hex）
			parts := strings.SplitN(fields[1], ":", 2)
			if len(parts) != 2 {
				continue
			}
			port64, err := strconv.ParseInt(parts[1], 16, 32)
			if err != nil {
				continue
			}
			if _, ok := ports[int(port64)]; ok {
				total++
			}
		}
		f.Close()
	}
	return total
}
