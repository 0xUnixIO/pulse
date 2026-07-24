package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"pulse/internal/ipsentinel"
)

// ErrNodeOffline 表示目标节点当前没有活跃 gRPC 长连接（hub 不知道该节点）。
// 与 nodehub.ErrNodeOffline 等价但属于 nodes 包，调用方（jobs 等）只依赖 nodes。
var ErrNodeOffline = errors.New("nodes: node offline")

// ErrHubNotConfigured 表示 Client 未注入 hub。生产环境不应触发——
// server.Run 启动时会通过 SetNodeHub 注入。仅用于偏执检查与单测。
var ErrHubNotConfigured = errors.New("nodes: hub not configured")

// HubCaller 抽象 nodehub.Hub.Call，便于测试时注入 mock。
// 生产环境由 *nodehub.Hub 实现。
type HubCaller interface {
	Call(ctx context.Context, nodeID, method string, body any) (json.RawMessage, error)
}

// HubStream 是 hub 流式调用返回的最小接口。生产实现由 nodehub 注册，
// 测试可以注入 mock。
type HubStream interface {
	Frames() <-chan HubStreamFrame
	Done() <-chan struct{}
	Err() error
	Close()
}

// HubStreamFrame 与 nodehub.StreamFrame 字段对齐。
type HubStreamFrame struct {
	Event string
	Body  json.RawMessage
}

// HubStreamFunc 是 nodehub.Hub.CallStream 的适配函数签名。
// 由 nodehub 包通过 RegisterHubStreamCaller 注入，避免 nodes 包反向 import nodehub。
type HubStreamFunc func(ctx context.Context, hub any, nodeID, method string, body any) (HubStream, error)

var hubStreamFn HubStreamFunc

// RegisterHubStreamCaller 由 nodehub 包在 init 中调用，注册"如何用任意 hub 对象
// 发起一次流式调用"的适配函数。未注册时，LogsStream/TracerouteStream 会返回错误。
func RegisterHubStreamCaller(fn HubStreamFunc) { hubStreamFn = fn }

// hubOfflineErrSentinels 由各包注册的"节点离线"sentinel 错误集合。
// callHub 用 errors.Is 探测它（生产中由 nodehub.ErrNodeOffline 提供）。
// 单独引出一个变量，让 nodes 包不直接 import nodehub。
var hubOfflineErrSentinels []error

// RegisterHubOfflineError 由 nodehub 包在 init 中调用注册其 ErrNodeOffline，
// 让 nodes.callHub 能识别 hub 层的离线错误。
// 不调用也不会报错——直接对比错误字符串作为兜底（见 mapHubErr）。
func RegisterHubOfflineError(err error) {
	if err != nil {
		hubOfflineErrSentinels = append(hubOfflineErrSentinels, err)
	}
}

// Client 是控制面到节点的 RPC 客户端，所有调用经由 hub gRPC 长连接路由。
// 通过 NewClientWithHub 构造；hub == nil 的实例所有方法都会返回 ErrHubNotConfigured。
type Client struct {
	nodeID string
	hub    HubCaller
}

type RuntimeInfo struct {
	Available   bool   `json:"available"`
	Module      string `json:"module"`
	Version     string `json:"version,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	NodeVersion string `json:"node_version,omitempty"`
}

type Status struct {
	Running   bool      `json:"running"`
	StartedAt time.Time `json:"started_at,omitempty"`
}

type LogsResponse struct {
	Logs []string `json:"logs"`
}

type AccessLogEntry struct {
	SourceIP    string    `json:"source_ip"`
	SourcePort  string    `json:"source_port"`
	Destination string    `json:"destination"`
	RemoteIP    string    `json:"remote_ip"`
	RouteTag    string    `json:"route_tag"`
	Protocol    string    `json:"protocol"`
	User        string    `json:"user"`
	InboundTag  string    `json:"inbound_tag"`
	Time        time.Time `json:"time"`
}

type AccessLogsResponse struct {
	Entries []AccessLogEntry `json:"entries"`
}

type UsageStats struct {
	Available     bool        `json:"available"`
	Running       bool        `json:"running"`
	StartedAt     time.Time   `json:"started_at,omitempty"`
	UploadTotal   int64       `json:"upload_total"`
	DownloadTotal int64       `json:"download_total"`
	UploadSpeed   int64       `json:"upload_speed"`   // bytes/s
	DownloadSpeed int64       `json:"download_speed"` // bytes/s
	Connections   int         `json:"connections"`
	Users         []UserUsage `json:"users"`
}

type UserUsage struct {
	User          string   `json:"user"`
	UploadTotal   int64    `json:"upload_total"`
	DownloadTotal int64    `json:"download_total"`
	Connections   int      `json:"connections"`
	Devices       int      `json:"devices"`
	SourceIPs     []string `json:"source_ips,omitempty"` // 原始源 IP 列表，用于跨节点精确去重
}

type ConfigRequest struct {
	Config string `json:"config"`
}

// UserChangeRequest 描述向节点热增或热删单个用户所需的凭证信息。
type UserChangeRequest struct {
	InboundTag string `json:"inbound_tag"`
	Protocol   string `json:"protocol"`           // "vless" | "trojan" | "shadowsocks" | "anytls"
	Email      string `json:"email"`              // username@@@tag
	UUID       string `json:"uuid,omitempty"`     // vless
	Password   string `json:"password,omitempty"` // trojan / anytls
	Flow       string `json:"flow,omitempty"`     // vless+reality: "xtls-rprx-vision"
}

// NewClientWithHub 构造一个绑定到 gRPC long-connection hub 的 Client。
// nodeID 是节点逻辑 ID（与 hub 注册时一致）。hub 必须非 nil；否则后续方法
// 都会返回 ErrHubNotConfigured。
func NewClientWithHub(nodeID string, hub HubCaller) *Client {
	return &Client{nodeID: nodeID, hub: hub}
}

func (c *Client) Runtime(ctx context.Context) (RuntimeInfo, error) {
	var out RuntimeInfo
	err := c.callHub(ctx, "Runtime", nil, &out)
	return out, err
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var out Status
	err := c.callHub(ctx, "Status", nil, &out)
	return out, err
}

func (c *Client) Logs(ctx context.Context) (LogsResponse, error) {
	var out LogsResponse
	err := c.callHub(ctx, "Logs", nil, &out)
	return out, err
}

func (c *Client) AccessLogs(ctx context.Context) (AccessLogsResponse, error) {
	var out AccessLogsResponse
	err := c.callHub(ctx, "AccessLogs", nil, &out)
	return out, err
}

// LogsStream 打开节点日志流，返回 SSE 字节流（调用方负责 Close）。
// 由 hub.CallStream + sseAdapter 提供。
func (c *Client) LogsStream(ctx context.Context) (io.ReadCloser, error) {
	return c.openStream(ctx, "LogsStream", nil)
}

type ConfigResponse struct {
	Config string `json:"config"`
}

func (c *Client) Config(ctx context.Context) (ConfigResponse, error) {
	var out ConfigResponse
	err := c.callHub(ctx, "Config", nil, &out)
	return out, err
}

// Usage 拉取节点流量快照。生产环境推荐通过 UsageBuffer.Drain 消费 node 主动
// push 的 delta（参见 nodeagent.UsagePusher 与 nodes.UsageBuffer），此方法
// 仍可作为按需查询使用（panel /v1/nodes/{id}/runtime/usage 等）。
// reset 转发给节点端 DoUsage(reset)。
func (c *Client) Usage(ctx context.Context, reset bool) (UsageStats, error) {
	var out UsageStats
	body := map[string]bool{"reset": reset}
	err := c.callHub(ctx, "Usage", body, &out)
	return out, err
}

func (c *Client) Start(ctx context.Context, req ConfigRequest) (Status, error) {
	var out Status
	err := c.callHub(ctx, "Start", req, &out)
	return out, err
}

func (c *Client) Stop(ctx context.Context) (Status, error) {
	var out Status
	err := c.callHub(ctx, "Stop", nil, &out)
	return out, err
}

func (c *Client) Restart(ctx context.Context, req ConfigRequest) (Status, error) {
	var out Status
	err := c.callHub(ctx, "Restart", req, &out)
	return out, err
}

// AddUser 向节点正在运行的 inbound 热增单个用户，无需重启核心。
func (c *Client) AddUser(ctx context.Context, req UserChangeRequest) error {
	return c.callHub(ctx, "AddUser", req, nil)
}

// RemoveUser 从节点正在运行的 inbound 热删单个用户。
func (c *Client) RemoveUser(ctx context.Context, inboundTag, email string) error {
	body := map[string]string{"inbound_tag": inboundTag, "email": email}
	return c.callHub(ctx, "RemoveUser", body, nil)
}

// KickUsers 断开这些用户在节点上的全部存量连接，返回被断开的连接总数。
//
// RemoveUser 只拦得住新连接：鉴权仅在建链时做一次，存量连接不会被回查，
// 会一直跑到自己结束。超限用户需要 RemoveUser + KickUsers 两步才能真正止血。
// 断连精确到用户，同节点其他用户不受影响。
//
// 批量形式是为对账兜底服务的：那里需要周期性扫过全部应禁用用户，
// 逐个发 RPC 的量会随用户数与 inbound 数相乘放大。
func (c *Client) KickUsers(ctx context.Context, emails []string) (int, error) {
	if len(emails) == 0 {
		return 0, nil
	}
	var out struct {
		Kicked int `json:"kicked"`
	}
	body := map[string][]string{"emails": emails}
	if err := c.callHub(ctx, "KickUser", body, &out); err != nil {
		return 0, err
	}
	return out.Kicked, nil
}

func (c *Client) Update(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.callHub(ctx, "Update", nil, &out)
	return out, err
}

type CertPaths struct {
	Domain   string `json:"domain"`
	CertPath string `json:"cert_path"`
	KeyPath  string `json:"key_path"`
}

func (c *Client) EnsureCert(ctx context.Context, domain, cfToken string) (CertPaths, error) {
	var out CertPaths
	body := map[string]string{"domain": domain, "cf_token": cfToken}
	err := c.callHub(ctx, "EnsureCert", body, &out)
	return out, err
}

// CheckUnlockResult 节点检测单个服务的结果。
type CheckUnlockResult struct {
	Service  string `json:"service"`
	Unlocked bool   `json:"unlocked"`
	Region   string `json:"region,omitempty"`
	Note     string `json:"note,omitempty"`
}

// CheckUnlockResponse 节点解锁检测接口响应。
type CheckUnlockResponse struct {
	Direct         []CheckUnlockResult `json:"direct"`
	Proxied        []CheckUnlockResult `json:"proxied,omitempty"`
	ProxyAvailable bool                `json:"proxy_available"`
}

// SpeedTestResponse 节点测速接口响应。
type SpeedTestResponse struct {
	DownBps int64 `json:"down_bps"`
	UpBps   int64 `json:"up_bps"`
}

// SpeedTest 向节点发起测速请求（由 ctx 控制超时）。
func (c *Client) SpeedTest(ctx context.Context) (SpeedTestResponse, error) {
	var out SpeedTestResponse
	err := c.callHub(ctx, "SpeedTest", nil, &out)
	return out, err
}

// CheckUnlock 向节点发起解锁检测请求。
func (c *Client) CheckUnlock(ctx context.Context) (CheckUnlockResponse, error) {
	var out CheckUnlockResponse
	err := c.callHub(ctx, "CheckUnlock", nil, &out)
	return out, err
}

// TracerouteHop 单跳追踪结果。
type TracerouteHop struct {
	Hop     int       `json:"hop"`
	IP      string    `json:"ip,omitempty"`
	RttMs   []float64 `json:"rtt_ms,omitempty"`
	Timeout bool      `json:"timeout,omitempty"`
}

// TracerouteResult 路由追踪结果。
type TracerouteResult struct {
	Host   string          `json:"host"`
	Method string          `json:"method"`
	Hops   []TracerouteHop `json:"hops"`
	Error  string          `json:"error,omitempty"`
}

// TracerouteRequest 路由追踪请求参数。
type TracerouteRequest struct {
	Host   string `json:"host"`   // 目标地址
	Method string `json:"method"` // "icmp" 或 "tcp"
	Port   int    `json:"port"`   // TCP 模式下的目标端口（默认 80）
}

// LatencyProbeResult 节点三网延迟探测结果（ms），nil 表示超时/不可达。
type LatencyProbeResult struct {
	CT *int `json:"ct"`
	CU *int `json:"cu"`
	CM *int `json:"cm"`
}

// ProbeLatency 触发节点对上海三网目标的 TCP 延迟探测。
func (c *Client) ProbeLatency(ctx context.Context) (LatencyProbeResult, error) {
	var out LatencyProbeResult
	err := c.callHub(ctx, "ProbeLatency", nil, &out)
	return out, err
}

// TracerouteStream 向节点发起路由追踪 SSE 请求。
func (c *Client) TracerouteStream(ctx context.Context, req TracerouteRequest) (io.ReadCloser, error) {
	return c.openStream(ctx, "TracerouteStream", req)
}

// SNIProxyRoute 对应节点 sniproxy.Route（这里用独立类型避免 nodes 依赖 sniproxy 包）。
type SNIProxyRoute struct {
	SNI     string `json:"sni"`
	Backend string `json:"backend"`
	// Mode 取值："transparent" 或 "terminating"。
	Mode string `json:"mode"`
}

// SNIProxySyncRequest 推送给节点的完整 sniproxy 配置。
type SNIProxySyncRequest struct {
	Listen          string          `json:"listen"`
	CertStoragePath string          `json:"cert_storage_path,omitempty"`
	ACMEEmail       string          `json:"acme_email,omitempty"`
	CloudflareToken string          `json:"cloudflare_token,omitempty"`
	ACMEStaging     bool            `json:"acme_staging,omitempty"`
	Routes          []SNIProxyRoute `json:"routes"`
	// CertDomains 仅用于触发 certmgr 申请证书但不参与 NodeGate 路由表
	// 的额外域名（如 hy2 inbound 的 SNI：UDP/QUIC 由 Xray 直接持证，
	// 但证书仍由节点 certmgr 通过 ACME DNS-01 申请并落盘）。
	CertDomains []string `json:"cert_domains,omitempty"`
}

// SyncSNIProxy 把内置 SNI 代理的完整配置推送给节点，节点端热更新。
func (c *Client) SyncSNIProxy(ctx context.Context, req SNIProxySyncRequest) error {
	return c.callHub(ctx, "SyncSNIProxy", req, nil)
}

// SNIProxyStatus 查询节点当前 SNI 代理运行状态。
func (c *Client) SNIProxyStatus(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.callHub(ctx, "SNIProxyStatus", nil, &out)
	return out, err
}

// SNIProxyCertReady 返回节点上 certmgr 已经在磁盘上落地的域名列表。
// 用于 panel 在下发 hy2 inbound 前判断证书是否就绪。
func (c *Client) SNIProxyCertReady(ctx context.Context) ([]string, error) {
	var out struct {
		Domains []string `json:"domains"`
	}
	err := c.callHub(ctx, "SNIProxyCertReady", nil, &out)
	return out.Domains, err
}

// IPSentinelDetect 触发节点同步 IP 检测。
func (c *Client) IPSentinelDetect(ctx context.Context) (ipsentinel.DetectResult, error) {
	var out ipsentinel.DetectResult
	err := c.callHub(ctx, "IPSentinelDetect", nil, &out)
	return out, err
}

// IPSentinelDetectGoogle 从节点实际访问 Google，检测 Google 对该 IP 的地区判断。
func (c *Client) IPSentinelDetectGoogle(ctx context.Context) (ipsentinel.GoogleDetectResult, error) {
	var out ipsentinel.GoogleDetectResult
	err := c.callHub(ctx, "IPSentinelDetectGoogle", nil, &out)
	return out, err
}

// IPSentinelRun 触发节点运行 IP Sentinel 任务（google/trust/auto），异步执行。
func (c *Client) IPSentinelRun(ctx context.Context, taskType string) error {
	body := map[string]string{"type": taskType}
	return c.callHub(ctx, "IPSentinelRun", body, nil)
}

// IPSentinelStatus 获取节点 IP Sentinel 任务状态。
func (c *Client) IPSentinelStatus(ctx context.Context) (ipsentinel.NodeStatus, error) {
	var out ipsentinel.NodeStatus
	err := c.callHub(ctx, "IPSentinelStatus", nil, &out)
	return out, err
}

// IPSentinelGetConfig 获取节点当前 IP Sentinel 配置。
func (c *Client) IPSentinelGetConfig(ctx context.Context) (ipsentinel.Config, error) {
	var out ipsentinel.Config
	err := c.callHub(ctx, "IPSentinelGetConfig", nil, &out)
	return out, err
}

// IPSentinelSetConfig 设置节点 IP Sentinel 配置。
func (c *Client) IPSentinelSetConfig(ctx context.Context, cfg ipsentinel.Config) error {
	return c.callHub(ctx, "IPSentinelSetConfig", cfg, nil)
}

func (c *Client) callHub(ctx context.Context, method string, body any, out any) error {
	if c.hub == nil {
		return ErrHubNotConfigured
	}
	raw, err := c.hub.Call(ctx, c.nodeID, method, body)
	if err != nil {
		return mapHubErr(err)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Client) openStream(ctx context.Context, method string, body any) (io.ReadCloser, error) {
	if c.hub == nil {
		return nil, ErrHubNotConfigured
	}
	if hubStreamFn == nil {
		return nil, fmt.Errorf("nodes.Client.%s: hub stream caller not registered", method)
	}
	stream, err := hubStreamFn(ctx, c.hub, c.nodeID, method, body)
	if err != nil {
		return nil, mapHubErr(err)
	}
	return newSSEAdapter(stream), nil
}

// mapHubErr 把 nodehub 层的错误转换成 nodes 包的错误（特别是离线 sentinel）。
func mapHubErr(err error) error {
	if err == nil {
		return nil
	}
	for _, sent := range hubOfflineErrSentinels {
		if errors.Is(err, sent) {
			return ErrNodeOffline
		}
	}
	if strings.Contains(err.Error(), "node offline") {
		return ErrNodeOffline
	}
	return err
}
