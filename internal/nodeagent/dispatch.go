// Package-level: dispatch.go 把 server 通过 nodeagent 通道下发的 method
// 路由到 nodeapi.API 的 Do* 业务方法以及 ipsentinel.NodeHandler.Dispatch。
//
// 流式 method（LogsStream / TracerouteStream）通过 SetSender 注入的 Sender
// 主动 PushEvent 推帧；Handle 同步阻塞直到流自然结束或 ctx 取消，然后由
// session.go 自动回一条普通 Ok 响应作为终态。
package nodeagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"pulse/internal/coremanager"
	"pulse/internal/nodeapi"
	"pulse/internal/nodes"
	"pulse/internal/nodes/confighash"
	"pulse/internal/sniproxy"
)

// IPSentinelHandler 由 *ipsentinel.NodeHandler 实现，注入避免 nodeagent 直接
// import ipsentinel（解耦，便于测试）。
type IPSentinelHandler interface {
	Dispatch(ctx context.Context, method string, body []byte) (any, error)
}

// APIDispatcher 把 method/body 路由到 nodeapi 内部 do* 方法。
type APIDispatcher struct {
	api        *nodeapi.API
	ipsentinel IPSentinelHandler

	sender Sender // 由 session 在建立连接后通过 SetSender 注入；流式 method 需要它。
}

// NewAPIDispatcher 构造一个 dispatcher。ipsentinel 可为 nil，
// 此时所有 IPSentinel* method 会返回错误。
func NewAPIDispatcher(api *nodeapi.API, ipsentinel IPSentinelHandler) *APIDispatcher {
	return &APIDispatcher{api: api, ipsentinel: ipsentinel}
}

// SetSender 由 nodeagent.session 在连接建立后调用，把 Sender 注入给 dispatcher
// 用于流式 method 主动推帧。
func (d *APIDispatcher) SetSender(s Sender) { d.sender = s }

// Handle 实现 Dispatcher。
func (d *APIDispatcher) Handle(ctx context.Context, method string, body json.RawMessage) (json.RawMessage, error) {
	switch method {
	// ── 流式 method ──────────────────────────────────────────────
	case "LogsStream":
		return nil, d.handleLogsStream(ctx)
	case "TracerouteStream":
		var req nodes.TracerouteRequest
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, fmt.Errorf("decode TracerouteStream body: %w", err)
			}
		}
		return nil, d.handleTracerouteStream(ctx, req)

	// ── runtime / 状态 ──
	case "Runtime":
		return marshal(d.api.DoRuntime(ctx))
	case "Status":
		return marshal(d.api.DoStatus())
	case "Config":
		return marshal(d.api.DoConfig())
	case "Logs":
		return marshal(d.api.DoLogs())
	case "AccessLogs":
		return marshal(d.api.DoAccessLogs())

	case "Usage":
		var req struct {
			Reset bool `json:"reset"`
		}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, fmt.Errorf("decode Usage body: %w", err)
			}
		}
		return marshal(d.api.DoUsage(req.Reset))

	// ── 进程控制 ──
	case "Start":
		var req nodes.ConfigRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("decode Start body: %w", err)
		}
		st, err := d.api.DoStart(req.Config, "")
		if err != nil {
			return nil, err
		}
		return marshal(st)
	case "Stop":
		st, err := d.api.DoStop()
		if err != nil {
			return nil, err
		}
		return marshal(st)
	case "Restart":
		var req nodes.ConfigRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("decode Restart body: %w", err)
		}
		st, err := d.api.DoRestart(req.Config, "")
		if err != nil {
			return nil, err
		}
		return marshal(st)

	// ── 用户增删 ──
	case "AddUser":
		var req nodes.UserChangeRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("decode AddUser body: %w", err)
		}
		cfg := coremanager.UserConfig{
			InboundTag: req.InboundTag,
			Protocol:   req.Protocol,
			Email:      req.Email,
			UUID:       req.UUID,
			Password:   req.Password,
			Flow:       req.Flow,
		}
		if err := d.api.DoAddUser(ctx, cfg); err != nil {
			return nil, err
		}
		return marshal(map[string]any{"ok": true})
	case "RemoveUser":
		var req struct {
			InboundTag string `json:"inbound_tag"`
			Email      string `json:"email"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("decode RemoveUser body: %w", err)
		}
		if err := d.api.DoRemoveUser(ctx, req.InboundTag, req.Email); err != nil {
			return nil, err
		}
		return marshal(map[string]any{"ok": true})

	case "KickUser":
		var req struct {
			Emails []string `json:"emails"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("decode KickUser body: %w", err)
		}
		n, err := d.api.DoKickUsers(ctx, req.Emails)
		if err != nil {
			return nil, err
		}
		return marshal(map[string]any{"kicked": n})

	case "Update":
		return marshal(d.api.DoUpdate())

	case "EnsureCert":
		var req struct {
			Domain  string `json:"domain"`
			CFToken string `json:"cf_token"`
		}
		if len(body) > 0 {
			_ = json.Unmarshal(body, &req)
		}
		return marshal(d.api.DoEnsureCert(req.Domain, req.CFToken))

	// ── 检测/测速/延迟 ──
	case "SpeedTest":
		res, err := d.api.DoSpeedTest(ctx)
		if err != nil {
			return nil, err
		}
		return marshal(res)
	case "CheckUnlock":
		return marshal(d.api.DoCheckUnlock(ctx))
	case "ProbeLatency":
		return marshal(d.api.DoProbeLatency(ctx))

	// ── SNI 代理 ──
	case "SyncSNIProxy":
		var req sniproxy.ManagerConfig
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("decode SyncSNIProxy body: %w", err)
		}
		resp, err := d.api.DoSyncSNIProxy(req)
		if err != nil {
			return nil, err
		}
		return marshal(resp)
	case "SNIProxyStatus":
		return marshal(d.api.DoSNIProxyStatus())
	case "SNIProxyCertReady":
		return marshal(d.api.DoSNIProxyCertReady())

	// ── IPSentinel ──
	case "IPSentinelDetect", "IPSentinelDetectGoogle", "IPSentinelRun",
		"IPSentinelStatus", "IPSentinelGetConfig", "IPSentinelSetConfig":
		if d.ipsentinel == nil {
			return nil, fmt.Errorf("ipsentinel handler not configured")
		}
		out, err := d.ipsentinel.Dispatch(ctx, method, body)
		if err != nil {
			return nil, err
		}
		return marshal(out)
	}

	return nil, fmt.Errorf("unknown method: %s", method)
}

func marshal(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal response: %w", err)
	}
	return json.RawMessage(b), nil
}

// handleLogsStream 同步阻塞，把 nodeapi.LogsChannel 的每行包成
// {"line":"..."} 通过 sender 推回 server，event="log"。
// ctx 取消（caller Close 或 server cancel_id）或日志通道关闭时返回 nil；
// session 会在 Handle 返回后回一条普通 Ok 响应，作为流终态。
func (d *APIDispatcher) handleLogsStream(ctx context.Context) error {
	if d.sender == nil {
		return errors.New("nodeagent: sender not initialized for streaming")
	}
	reqID := ReqIDFromContext(ctx)
	if reqID == "" {
		return errors.New("nodeagent: LogsStream missing reqID in ctx")
	}
	ch := d.api.LogsChannel(ctx)
	for line := range ch {
		body, err := json.Marshal(map[string]string{"line": line})
		if err != nil {
			return fmt.Errorf("marshal log frame: %w", err)
		}
		if err := d.sender.PushEvent(reqID, "log", body, 0); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
	}
	return nil
}

// handleTracerouteStream 同步阻塞，把 nodeapi.TracerouteHops 的每跳推回 server，
// event="traceroute_hop"。错误事件会作为终态错误返回（session 会回一条 Ok=false 响应）。
func (d *APIDispatcher) handleTracerouteStream(ctx context.Context, req nodes.TracerouteRequest) error {
	if d.sender == nil {
		return errors.New("nodeagent: sender not initialized for streaming")
	}
	reqID := ReqIDFromContext(ctx)
	if reqID == "" {
		return errors.New("nodeagent: TracerouteStream missing reqID in ctx")
	}
	apiReq := nodeapi.TracerouteRequest{
		Host:   req.Host,
		Method: req.Method,
		Port:   req.Port,
	}
	ch := d.api.TracerouteHops(ctx, apiReq)
	for ev := range ch {
		if ev.Err != "" {
			// 把错误事件也作为帧推回（与 HTTP SSE 行为对齐：客户端能看到错误内容），
			// 然后返回一个非 nil error 让 session 回 Ok=false 终态。
			body, _ := json.Marshal(map[string]string{"error": ev.Err})
			_ = d.sender.PushEvent(reqID, "traceroute_hop", body, 0)
			return errors.New(ev.Err)
		}
		body, err := json.Marshal(ev.Hop)
		if err != nil {
			return fmt.Errorf("marshal hop frame: %w", err)
		}
		if err := d.sender.PushEvent(reqID, "traceroute_hop", body, 0); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
	}
	return nil
}

// ── ConfigHasher ─────────────────────────────────────────────────

// ConfigHasher 返回一个函数，给 DefaultHelloProvider 使用。
// hash 由当前 xray 配置中的"用户列表 + inbound 倍率"规范化后 SHA256 得到，
// 仅在影响下发的关键字段变化时改变（用户增删、UUID/secret 变化、启用状态变化、
// inbound 倍率变化），server 端可据此判断是否需要重下发配置。
//
// 算法实现位于 internal/nodes/confighash 共享包，与 server 侧保持字节一致。
func ConfigHasher(api *nodeapi.API) func() string {
	return func() string {
		if api == nil {
			return ""
		}
		raw := api.DoConfig()
		cfgStr, _ := raw["config"].(string)
		if cfgStr == "" {
			return ""
		}
		return confighash.HashFromXrayJSON(cfgStr)
	}
}
