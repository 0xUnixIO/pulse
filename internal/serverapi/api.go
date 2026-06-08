package serverapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"pulse/internal/config"
	"pulse/internal/geoip"
	"pulse/internal/idgen"
	"pulse/internal/inbounds"
	"pulse/internal/accesslogs"
	"pulse/internal/nodehub"
	"pulse/internal/auditrules"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/outbounds"
	"pulse/internal/users"
)

type API struct {
	store         nodes.Store
	usersStore    users.Store
	inboundStore  inbounds.InboundStore
	outboundStore outbounds.Store
	clientFactory func(node nodes.Node) *nodes.Client
	applyOpts     jobs.ApplyOptions
	settings      UpdateSettingsStore // 用于节点更新时获取 GitHub Token
	// clientCache 缓存每个节点的 RPC 客户端，避免重复构造。
	// 节点更新/删除以及 SetNodeHub 时自动失效。
	clientCache    sync.Map // nodeID → *nodes.Client
	accessLogStore accesslogs.Store
	auditRuleStore auditrules.Store
	hub            *nodehub.Hub // 用于在线状态查询；可能为 nil（早期或测试）
	mHub           *metricsHub  // 节点指标广播器，StartMetricsHub 后可用
}

type upsertNodeRequest struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	BaseURL      string  `json:"base_url"`
	ExpireAt     *string `json:"expire_at"`
	PanelURL     string  `json:"panel_url"`
	Remark       string  `json:"remark"`
	IPOverride   string  `json:"ip_override"`
	Disabled     bool    `json:"disabled"`
	ACMEEmail    string  `json:"acme_email"`
	PanelDomain  string  `json:"panel_domain"`
	ExtraProxies string  `json:"extra_proxies"`
	HTTPSPort    int     `json:"https_port"`
	IsLanding    bool    `json:"is_landing"`
}

type configRequest struct {
	Config string `json:"config"`
}

func New(store nodes.Store) *API {
	return &API{
		store: store,
		clientFactory: func(node nodes.Node) *nodes.Client {
			// 默认 factory：未注入 hub 时返回一个 hub == nil 的 Client；
			// 所有方法会返回 nodes.ErrHubNotConfigured。生产环境会立即被
			// SetNodeHub 替换，仅在单测或冷启动早期短暂出现。
			return nodes.NewClientWithHub(node.ID, nil)
		},
	}
}

func NewWithUsers(nodesStore nodes.Store, usersStore users.Store, inboundStore inbounds.InboundStore, outboundStore outbounds.Store, applyOpts jobs.ApplyOptions, settings UpdateSettingsStore) *API {
	api := New(nodesStore)
	api.usersStore = usersStore
	api.inboundStore = inboundStore
	api.outboundStore = outboundStore
	api.applyOpts = applyOpts
	api.settings = settings
	return api
}

func (a *API) SetAccessLogStore(s accesslogs.Store) {
	a.accessLogStore = s
}

func (a *API) SetAuditRuleStore(s auditrules.Store) {
	a.auditRuleStore = s
}

// StartMetricsHub 启动节点指标广播器。应在 SetNodeHub 之后调用。
func (a *API) StartMetricsHub(ctx context.Context) {
	a.mHub = newMetricsHub()
	go a.mHub.Run(ctx, a.store, a.clientFor)
}

// SetNodeHub 注入 gRPC 长连接 hub，所有节点 RPC 调用都通过此 hub 走 mTLS gRPC。
// 同时清空缓存：旧的（hub == nil）默认 Client 不应再被复用。
func (a *API) SetNodeHub(hub *nodehub.Hub) {
	if hub == nil {
		return
	}
	a.hub = hub
	a.clientFactory = func(node nodes.Node) *nodes.Client {
		return nodes.NewClientWithHub(node.ID, hub)
	}
	a.clientCache = sync.Map{}
}

// nodeWithOnline 返回包含 online 字段的节点 JSON map（嵌入原 Node + online）。
func (a *API) nodeWithOnline(node nodes.Node) map[string]any {
	online := false
	if a.hub != nil {
		online = a.hub.IsOnline(node.ID)
	}
	b, _ := json.Marshal(node)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m == nil {
		m = map[string]any{}
	}
	m["online"] = online
	return m
}

func RegisterUsersAPI(mux *http.ServeMux, usersStore users.Store, nodesStore nodes.Store, inboundStore inbounds.InboundStore, outboundStore outbounds.Store, dial jobs.NodeDialer, applyOpts jobs.ApplyOptions, geoDB *geoip.DB, sesStore PortalSessionStore) {
	base := New(nodesStore)
	base.inboundStore = inboundStore
	base.outboundStore = outboundStore
	a := newUserAPI(usersStore, nodesStore, inboundStore, outboundStore, base, dial, applyOpts, geoDB)
	a.sessions = sesStore
	a.Register(mux)
}

func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/nodes", a.handleNodes)
	// 精确路由优先于 /v1/nodes/ 通配
	mux.HandleFunc("GET /v1/nodes/metrics/stream", a.handleAllNodeMetricsStream)
	mux.HandleFunc("GET /v1/nodes/traceroute/latest", a.handleAllTracerouteLatest)
	mux.HandleFunc("GET /v1/nodes/latency", a.handleLatency)
	mux.HandleFunc("/v1/nodes/", a.handleNodeRoutes)
	mux.HandleFunc("GET /v1/audit/logs", a.handleAuditLogs)
	mux.HandleFunc("GET /v1/audit/users", a.handleAuditUsers)
	mux.HandleFunc("GET /v1/audit/count", a.handleAuditCount)
	mux.HandleFunc("GET /v1/audit/analysis", a.handleAuditAnalysis)
	mux.HandleFunc("/v1/audit/rules", a.handleAuditRules)
	mux.HandleFunc("/v1/audit/rules/", a.handleAuditRuleByID)
}

func (a *API) handleNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := a.store.List()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		out := make([]map[string]any, 0, len(items))
		for _, n := range items {
			out = append(out, a.nodeWithOnline(n))
		}
		writeJSON(w, http.StatusOK, map[string]any{"nodes": out})
	case http.MethodPost:
		var req upsertNodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
			return
		}
		if strings.TrimSpace(req.ID) == "" {
			req.ID = idgen.NextString()
		}
		node, err := a.store.Upsert(nodes.Node{
			ID:           req.ID,
			Name:         req.Name,
			BaseURL:      strings.TrimRight(req.BaseURL, "/"),
			ExpireAt:     parseExpireAt(req.ExpireAt),
			PanelURL:     req.PanelURL,
			Remark:       req.Remark,
			IPOverride:   strings.TrimSpace(req.IPOverride),
			Disabled:     req.Disabled,
			ACMEEmail:    req.ACMEEmail,
			PanelDomain:  req.PanelDomain,
			ExtraProxies: req.ExtraProxies,
			HTTPSPort:    req.HTTPSPort,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, node)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (a *API) handleNodeRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/nodes/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node id is required"})
		return
	}

	nodeID := parts[0]
	if len(parts) == 1 {
		a.handleNode(w, r, nodeID)
		return
	}

	// DELETE /v1/nodes/{nodeID}/traceroute/results/{snapshotID}
	if len(parts) == 4 && parts[1] == "traceroute" && parts[2] == "results" {
		a.handleTracerouteResultDelete(w, r, parts[3])
		return
	}

	switch strings.Join(parts[1:], "/") {
	case "runtime":
		a.handleNodeRuntime(w, r, nodeID)
	case "runtime/status":
		a.handleNodeStatus(w, r, nodeID)
	case "runtime/usage":
		a.handleNodeUsage(w, r, nodeID)
	case "runtime/config":
		a.handleNodeConfig(w, r, nodeID)
	case "runtime/logs":
		a.handleNodeLogs(w, r, nodeID)
	case "runtime/logs/stream":
		a.handleNodeLogsStream(w, r, nodeID)
	case "runtime/start":
		a.handleNodeStart(w, r, nodeID)
	case "runtime/stop":
		a.handleNodeStop(w, r, nodeID)
	case "runtime/restart":
		a.handleNodeRestart(w, r, nodeID)
	case "runtime/apply":
		a.handleNodeApply(w, r, nodeID)
	case "runtime/metrics":
		a.handleNodeMetrics(w, r, nodeID)
	case "runtime/speedtest":
		a.handleNodeSpeedTest(w, r, nodeID)
	case "runtime/check":
		a.handleNodeCheck(w, r, nodeID)
	case "runtime/traceroute":
		a.handleNodeTraceroute(w, r, nodeID)
	case "traceroute/results":
		a.handleNodeTracerouteResults(w, r, nodeID)
	case "update":
		a.handleNodeUpdate(w, r, nodeID)
	case "sniproxy/sync":
		a.handleNodeSNIProxySync(w, r, nodeID)
	case "sniproxy/status":
		a.handleNodeSNIProxyStatus(w, r, nodeID)
	case "traffic":
		a.handleNodeTraffic(w, r, nodeID)
	case "checks":
		a.handleNodeChecksGet(w, r, nodeID)
	case "speedtest":
		a.handleNodeSpeedTestGet(w, r, nodeID)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
	}
}

func (a *API) handleNode(w http.ResponseWriter, r *http.Request, nodeID string) {
	switch r.Method {
	case http.MethodGet:
		node, err := a.store.Get(nodeID)
		if err != nil {
			writeNodeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, a.nodeWithOnline(node))
	case http.MethodPut:
		var req upsertNodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.Name == "" || req.BaseURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name and base_url are required"})
			return
		}
		node, err := a.store.Upsert(nodes.Node{
			ID:           nodeID,
			Name:         req.Name,
			BaseURL:      strings.TrimRight(req.BaseURL, "/"),
			ExpireAt:     parseExpireAt(req.ExpireAt),
			PanelURL:     req.PanelURL,
			Remark:       req.Remark,
			IPOverride:   strings.TrimSpace(req.IPOverride),
			Disabled:     req.Disabled,
			ACMEEmail:    req.ACMEEmail,
			PanelDomain:  req.PanelDomain,
			ExtraProxies: req.ExtraProxies,
			HTTPSPort:    req.HTTPSPort,
			IsLanding:    req.IsLanding,
		})
		if err != nil {
			writeNodeError(w, err)
			return
		}
		// BaseURL 可能已变更，旧的缓存客户端不再有效
		a.evictClient(nodeID)
		writeJSON(w, http.StatusOK, node)
	case http.MethodDelete:
		// 先级联删除该节点下的所有入站（及其关联的 user_inbounds、分组成员）
		if a.inboundStore != nil {
			ibs, err := a.inboundStore.ListInboundsByNode(nodeID)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "list inbounds: " + err.Error()})
				return
			}
			for _, ib := range ibs {
				if err := a.inboundStore.DeleteInbound(ib.ID); err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "delete inbound: " + err.Error()})
					return
				}
			}
		}
		if err := a.store.Delete(nodeID); err != nil {
			writeNodeError(w, err)
			return
		}
		a.evictClient(nodeID)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

func (a *API) handleNodeRuntime(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := client.Runtime(ctx)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleNodeMetrics(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	out, err := client.Usage(ctx, false)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAllNodeMetricsStream 推送所有节点的实时指标（SSE）。
// 所有 SSE 连接共享同一个 metricsHub 广播，避免每个连接独立轮询 DB 导致连接池耗尽。
func (a *API) handleAllNodeMetricsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	if a.mHub == nil {
		http.Error(w, "metrics hub not ready", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := a.mHub.subscribe()
	defer a.mHub.unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (a *API) handleNodeStatus(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := client.Status(ctx)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleNodeConfig(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := client.Config(ctx)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleNodeLogs(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := client.Logs(ctx)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleNodeLogsStream(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "streaming not supported"})
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	body, err := client.LogsStream(r.Context())
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	bridgeSSE(w, r, body)
}

func (a *API) handleNodeUsage(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := client.Usage(ctx, false)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleNodeStart(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	req, ok := decodeConfigRequest(w, r)
	if !ok {
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out, err := client.Start(ctx, nodes.ConfigRequest{Config: req.Config})
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleNodeStop(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	out, err := client.Stop(ctx)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleNodeRestart(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if a.usersStore == nil || a.inboundStore == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "stores not configured"})
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	nodeInbounds, err := a.inboundStore.ListInboundsByNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	userAccesses, err := a.usersStore.ListUserInboundsByNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	userIDs := collectNodeUserIDs(userAccesses)
	userMap, err := a.usersStore.GetUsersByIDs(userIDs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	node, err := a.store.Get(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	status, _, err := jobs.ApplyNodeUsers(ctx, client, nodeInbounds, userAccesses, userMap, a.inboundStore, a.outboundStore, a.applyOpts, node)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *API) handleNodeApply(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if a.usersStore == nil || a.inboundStore == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "stores not configured"})
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	nodeInbounds, err := a.inboundStore.ListInboundsByNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	userAccesses, err := a.usersStore.ListUserInboundsByNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	userIDs := collectNodeUserIDs(userAccesses)
	userMap, err := a.usersStore.GetUsersByIDs(userIDs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	node, err := a.store.Get(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	status, _, err := jobs.ApplyNodeUsers(ctx, client, nodeInbounds, userAccesses, userMap, a.inboundStore, a.outboundStore, a.applyOpts, node)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// TriggerNodeSync 异步触发指定节点的 NodeGate sync。

func (a *API) TriggerNodeSync(nodeID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := a.doSyncNodeSNIProxy(ctx, nodeID); err != nil {
			log.Printf("auto sniproxy sync node %s: %v", nodeID, err)
		}
	}()
}

func (a *API) handleNodeSpeedTest(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 70*time.Second)
	defer cancel()
	resp, err := client.SpeedTest(ctx)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	result := nodes.SpeedTestResult{
		DownBps:  resp.DownBps,
		UpBps:    resp.UpBps,
		TestedAt: time.Now().UTC(),
	}
	_ = a.store.UpsertNodeSpeedTest(nodeID, result)
	writeJSON(w, http.StatusOK, result)
}

func (a *API) handleNodeCheck(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	resp, err := client.CheckUnlock(ctx)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	now := time.Now().UTC()
	var results []nodes.CheckResult
	for _, r := range resp.Direct {
		results = append(results, nodes.CheckResult{
			Service: r.Service, CheckType: "direct",
			Unlocked: r.Unlocked, Region: r.Region, Note: r.Note, CheckedAt: now,
		})
	}
	for _, r := range resp.Proxied {
		results = append(results, nodes.CheckResult{
			Service: r.Service, CheckType: "proxied",
			Unlocked: r.Unlocked, Region: r.Region, Note: r.Note, CheckedAt: now,
		})
	}
	if len(results) > 0 {
		_ = a.store.UpsertNodeCheckResults(nodeID, results)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) handleNodeTraceroute(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "streaming not supported"})
		return
	}
	host := r.URL.Query().Get("host")
	if host == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "host 参数不能为空"})
		return
	}
	method := r.URL.Query().Get("method")
	if method != "tcp" {
		method = "icmp"
	}
	port := 80
	if p := r.URL.Query().Get("port"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 && v <= 65535 {
			port = v
		}
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	body, err := client.TracerouteStream(r.Context(), nodes.TracerouteRequest{
		Host: host, Method: method, Port: port,
	})
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	bridgeSSE(w, r, body)
}

func (a *API) handleNodeUpdate(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := client.Update(ctx)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// collectNodeUserIDs 提取用户凭据列表中去重后的 UserID（api.go 内部使用）。
func collectNodeUserIDs(accesses []users.UserInbound) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, acc := range accesses {
		if _, ok := seen[acc.UserID]; !ok {
			seen[acc.UserID] = struct{}{}
			out = append(out, acc.UserID)
		}
	}
	return out
}

func (a *API) clientFor(nodeID string) (*nodes.Client, error) {
	// 命中缓存直接返回（避免重复构造）
	if v, ok := a.clientCache.Load(nodeID); ok {
		return v.(*nodes.Client), nil
	}
	node, err := a.store.Get(nodeID)
	if err != nil {
		return nil, err
	}
	if node.Disabled {
		return nil, fmt.Errorf("node is disabled")
	}
	client := a.clientFactory(node)
	a.clientCache.Store(nodeID, client)
	return client, nil
}

func (a *API) evictClient(nodeID string) {
	a.clientCache.Delete(nodeID)
}

// EvictClient 导出给外部调用（如 node-register 接口更新 BaseURL 后使缓存失效）。
func (a *API) EvictClient(nodeID string) {
	a.clientCache.Delete(nodeID)
}

// Dial 根据节点 ID 返回 RPC 客户端，可用于 jobs.NodeDialer。
func (a *API) Dial(nodeID string) (*nodes.Client, error) {
	return a.clientFor(nodeID)
}

func decodeConfigRequest(w http.ResponseWriter, r *http.Request) (configRequest, bool) {
	var req configRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return configRequest{}, false
	}
	if req.Config == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "config is required"})
		return configRequest{}, false
	}
	return req, true
}

func writeNodeError(w http.ResponseWriter, err error) {
	status := http.StatusUnprocessableEntity // 422，不被 Cloudflare 拦截
	if errors.Is(err, nodes.ErrNodeNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func writeMethodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
}

// ServerPort 从 PULSE_SERVER_ADDR 解析监听端口，默认返回 8080。
func ServerPort() int {
	addr := config.Load().ServerAddr
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 8080
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return 8080
	}
	return port
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// internalError 记录完整错误到日志，并向客户端返回通用 500 响应，避免内部细节泄露。
func internalError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("internal error: %s %s: %v", r.Method, r.URL.Path, err)
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal server error"})
}

func parseExpireAt(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, *s); err == nil {
			u := t.UTC()
			return &u
		}
	}
	return nil
}
