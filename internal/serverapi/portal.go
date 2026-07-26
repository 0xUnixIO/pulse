package serverapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"pulse/internal/announcements"
	"pulse/internal/idgen"
	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/outbounds"
	"pulse/internal/plans"
	"pulse/internal/tickets"
	"pulse/internal/users"
)

// SettingsGetter reads settings from the store.
type SettingsGetter interface {
	GetSetting(key string) (string, bool)
}

// PortalSessionStore 管理用户门户 session。
type PortalSessionStore interface {
	Create(token, userID string, expiresAt time.Time) error
	GetUserID(token string) (string, bool)
	Delete(token string) error
	DeleteByUserID(userID string) error
}

type portalAPI struct {
	users         users.Store
	nodes         nodes.Store
	inbounds      inbounds.InboundStore
	outbounds     outbounds.Store     // may be nil
	settings      SettingsGetter
	plans         plans.Store         // may be nil
	announcements announcements.Store // may be nil
	tickets       tickets.Store       // may be nil
	sessions      PortalSessionStore
	uploadsDir    string
	dial          jobs.NodeDialer
	applyOpts     jobs.ApplyOptions
}

// RegisterPortalAPI registers public user-portal endpoints (no admin auth).
func RegisterPortalAPI(mux *http.ServeMux, us users.Store, ns nodes.Store, ibs inbounds.InboundStore, obs outbounds.Store, settings SettingsGetter, planStore plans.Store, annStore announcements.Store, ticketStore tickets.Store, sesStore PortalSessionStore, uploadsDir string, dial jobs.NodeDialer, applyOpts jobs.ApplyOptions) {
	a := &portalAPI{users: us, nodes: ns, inbounds: ibs, outbounds: obs, settings: settings, plans: planStore, announcements: annStore, tickets: ticketStore, sessions: sesStore, uploadsDir: uploadsDir, dial: dial, applyOpts: applyOpts}
	mux.HandleFunc("GET /v1/portal/", a.handlePortal)
	mux.HandleFunc("POST /v1/portal/", a.handlePortalPost)
	// 账号密码登录端点（独立于 sub_token 路由）：返回 sub_token，前端跳转到 /user/:token。
	mux.HandleFunc("POST /v1/user/login", a.handleUserLogin)
}

// handleUserLogin 处理 /v1/user/login：账号密码登录，成功返回 sub_token。
// 前端拿到后跳转到 /user/:sub_token。无 cookie / session，纯 stateless。
func (a *portalAPI) handleUserLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "username and password are required"})
		return
	}
	if len(req.Password) > 72 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "password too long"})
		return
	}

	_, hash, subToken, err := a.users.GetPasswordByUsername(req.Username)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials"})
		return
	}
	if hash == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "password not set, please contact admin"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials"})
		return
	}
	if subToken == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "user has no sub_token"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sub_token": subToken})
}

// portalAuth 通过 sub_token 解析用户 ID。
// 新模型：sub_token 即凭证，不再使用密码 cookie session。
// 账号密码登录走独立端点 /v1/user/login，登录成功后跳转到 /user/:sub_token。
func (a *portalAPI) portalAuth(r *http.Request, subToken string) (userID string, ok bool) {
	_ = r
	user, err := a.users.GetUserBySubToken(subToken)
	if err != nil {
		return "", false
	}
	return user.ID, true
}

func (a *portalAPI) handlePortal(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/portal/")
	parts := strings.SplitN(path, "/", 2)
	subToken := parts[0]
	if subToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "sub_token is required"})
		return
	}

	subPath := ""
	if len(parts) == 2 {
		subPath = parts[1]
	}

	userID, authed := a.portalAuth(r, subToken)
	if !authed {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "invalid token"})
		return
	}

	user, err := a.users.GetUser(userID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "user not found"})
		return
	}

	if subPath == "tickets" || strings.HasPrefix(subPath, "tickets/") {
		a.handlePortalTicketsGET(w, r, user, strings.TrimPrefix(subPath, "tickets"))
		return
	}

	switch subPath {
	case "", "info":
		a.handlePortalInfo(w, r, user)
	case "daily-usage":
		a.handlePortalDailyUsage(w, r, user)
	case "node-usage":
		a.handlePortalNodeUsage(w, r, user)
	case "traceroute":
		a.handlePortalTraceroute(w, r, user)
	case "hosts":
		a.handlePortalHosts(w, r, user)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
	}
}

// handlePortalPost 处理 POST 请求。
func (a *portalAPI) handlePortalPost(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/portal/")
	parts := strings.SplitN(path, "/", 2)
	subToken := parts[0]
	if subToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "sub_token is required"})
		return
	}

	subPath := ""
	if len(parts) == 2 {
		subPath = parts[1]
	}

	userID, authed := a.portalAuth(r, subToken)
	if !authed {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "invalid token"})
		return
	}

	user, err := a.users.GetUser(userID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "user not found"})
		return
	}

	if subPath == "tickets" || strings.HasPrefix(subPath, "tickets/") {
		a.handlePortalTicketsPOST(w, r, user, strings.TrimPrefix(subPath, "tickets"))
		return
	}
	switch subPath {
	case "hosts/exclude":
		a.handlePortalHostExclude(w, r, user)
	case "reset-traffic":
		a.handlePortalResetTraffic(w, r, user)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
	}
}

// handlePortalResetTraffic 允许用户通过自己的门户凭据重置流量。
func (a *portalAPI) handlePortalResetTraffic(w http.ResponseWriter, r *http.Request, user users.User) {
	now := time.Now().UTC()
	if user.Status != users.StatusActive && user.Status != users.StatusLimited {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "当前账户状态无法自助重置流量"})
		return
	}
	if user.UploadBytes+user.DownloadBytes <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "当前没有可重置的流量"})
		return
	}
	if user.ExpireAt == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "永久有效账户无法自助重置流量"})
		return
	}
	if !user.ExpireAt.After(now.AddDate(0, 0, 30)) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "剩余有效期不足 30 天，无法自助重置流量"})
		return
	}
	if _, err := a.users.ResetTrafficForValidity(user.ID, now, 30); err != nil {
		if errors.Is(err, users.ErrTrafficResetNotAllowed) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "账户状态已变化，请刷新后重试"})
			return
		}
		internalError(w, r, err)
		return
	}

	accesses, err := a.users.ListUserInboundsByUser(user.ID)
	if err != nil {
		log.Printf("handlePortalResetTraffic: list user inbounds %s: %v", user.ID, err)
	}
	nodeIDs := make(map[string]struct{})
	for _, access := range accesses {
		nodeIDs[access.NodeID] = struct{}{}
	}
	for nodeID := range nodeIDs {
		go func(id string) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := jobs.ApplyNode(ctx, id, a.nodes, a.users, a.inbounds, a.outbounds, a.dial, a.applyOpts); err != nil {
				log.Printf("handlePortalResetTraffic: apply node %s: %v", id, err)
			}
		}(nodeID)
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
func (a *portalAPI) handlePortalInfo(w http.ResponseWriter, r *http.Request, user users.User) {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http"
	}
	host := r.Host
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		host = fwdHost
	}
	subURL := scheme + "://" + host + "/sub/" + user.SubToken

	// Node info — 仅对活跃用户返回节点信息，过期/禁用等状态返回空列表
	type nodeInfo struct {
		Name      string   `json:"name"`
		Protocols []string `json:"protocols"`
	}
	var nodeInfos []nodeInfo
	if user.EffectiveEnabled() {
		accesses, _ := a.users.ListUserInboundsByUser(user.ID)
		nodeIDSet := make(map[string]bool)
		for _, acc := range accesses {
			nodeIDSet[acc.NodeID] = true
		}
		for nid := range nodeIDSet {
			node, err := a.nodes.Get(nid)
			if err != nil {
				continue
			}
			nodeInbounds, err := a.inbounds.ListInboundsByNode(nid)
			if err != nil {
				continue
			}
			seen := make(map[string]bool)
			var protocols []string
			for _, ib := range nodeInbounds {
				label := strings.ToUpper(ib.Protocol)
				if !seen[label] {
					seen[label] = true
					protocols = append(protocols, label)
				}
			}
			nodeInfos = append(nodeInfos, nodeInfo{Name: node.Name, Protocols: protocols})
		}
	}

	result := map[string]any{
		"username":               user.Username,
		"status":                 user.EffectiveStatus(),
		"sub_url":                subURL,
		"upload_bytes":           user.UploadBytes,
		"download_bytes":         user.DownloadBytes,
		"total_bytes":            user.UploadBytes + user.DownloadBytes,
		"data_limit":             user.TrafficLimit,
		"expire_at":              user.ExpireAt,
		"nodes":                  nodeInfos,
		"next_traffic_reset_at": nextTrafficResetAt(user.DataLimitResetStrategy, user.CreatedAt, user.LastTrafficResetAt),
	}

	// 公告列表：激活的排在最前面
	if a.announcements != nil {
		type annItem struct {
			ID        string    `json:"id"`
			Title     string    `json:"title"`
			Content   string    `json:"content"`
			Enabled   bool      `json:"enabled"`
			CreatedAt time.Time `json:"created_at"`
		}
		if list, err := a.announcements.List(); err == nil && len(list) > 0 {
			var active, rest []annItem
			for _, ann := range list {
				item := annItem{ID: ann.ID, Title: ann.Title, Content: ann.Content, Enabled: ann.Enabled, CreatedAt: ann.CreatedAt}
				if ann.Enabled {
					active = append(active, item)
				} else {
					rest = append(rest, item)
				}
			}
			result["announcements"] = append(active, rest...)
		}
	}

	// Plan name
	if a.plans != nil && user.CurrentPlanID != "" {
		if plan, err := a.plans.GetPlan(user.CurrentPlanID); err == nil {
			result["plan_name"] = plan.Name
		}
	}

	writeJSON(w, http.StatusOK, result)
}

func (a *portalAPI) handlePortalDailyUsage(w http.ResponseWriter, r *http.Request, user users.User) {
	daily, err := a.users.ListUserDailyUsage(user.ID, 7)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"daily": daily})
}

func (a *portalAPI) handlePortalNodeUsage(w http.ResponseWriter, r *http.Request, user users.User) {
	rawUsage, err := a.users.ListUserNodeUsage(user.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	nodeList, _ := a.nodes.List()
	nodeNames := make(map[string]string, len(nodeList))
	for _, n := range nodeList {
		nodeNames[n.ID] = n.Name
	}
	type nodeUsageItem struct {
		NodeID        string `json:"node_id"`
		NodeName      string `json:"node_name"`
		UploadBytes   int64  `json:"upload_bytes"`
		DownloadBytes int64  `json:"download_bytes"`
		TotalBytes    int64  `json:"total_bytes"`
	}
	items := make([]nodeUsageItem, 0, len(rawUsage))
	for _, u := range rawUsage {
		t := u.UploadBytes + u.DownloadBytes
		if t == 0 {
			continue
		}
		name := nodeNames[u.NodeID]
		if name == "" {
			name = u.NodeID
		}
		items = append(items, nodeUsageItem{
			NodeID:        u.NodeID,
			NodeName:      name,
			UploadBytes:   u.UploadBytes,
			DownloadBytes: u.DownloadBytes,
			TotalBytes:    t,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": items})
}

// ── 工单 Portal API ────────────────────────────────────────────────

// handlePortalTicketsGET 处理 GET /v1/portal/{token}/tickets[/...]
func (a *portalAPI) handlePortalTicketsGET(w http.ResponseWriter, r *http.Request, user users.User, rest string) {
	if a.tickets == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "tickets not available"})
		return
	}
	rest = strings.TrimPrefix(rest, "/")

	// GET tickets — 列表
	if rest == "" {
		list, err := a.tickets.ListTicketsByUser(user.ID)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if list == nil {
			list = []tickets.Ticket{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"tickets": list})
		return
	}

	// GET tickets/{id} — 详情
	ticketID := rest
	if idx := strings.Index(rest, "/"); idx >= 0 {
		ticketID = rest[:idx]
	}
	t, err := a.tickets.GetTicket(ticketID)
	if err != nil || t.UserID != user.ID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "ticket not found"})
		return
	}
	msgs, _ := a.tickets.ListMessages(ticketID)
	if msgs == nil {
		msgs = []tickets.Message{}
	}
	imgs, _ := a.tickets.ListImages(ticketID)
	if imgs == nil {
		imgs = []tickets.Image{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": t, "messages": msgs, "images": imgs})
}

// handlePortalTicketsPOST 处理 POST /v1/portal/{token}/tickets[/...]
func (a *portalAPI) handlePortalTicketsPOST(w http.ResponseWriter, r *http.Request, user users.User, rest string) {
	if a.tickets == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "tickets not available"})
		return
	}
	rest = strings.TrimPrefix(rest, "/")

	// POST tickets — 创建工单
	if rest == "" {
		var body struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		if body.Title == "" || body.Content == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "title and content are required"})
			return
		}
		t := &tickets.Ticket{
			ID: idgen.NextString(), UserID: user.ID, Username: user.Username,
			Title: body.Title, Status: tickets.StatusOpen,
		}
		if err := a.tickets.CreateTicket(t); err != nil {
			internalError(w, r, err)
			return
		}
		// 第一条消息作为工单内容
		msg := &tickets.Message{ID: idgen.NextString(), TicketID: t.ID, Content: body.Content, IsAdmin: false}
		if err := a.tickets.AddMessage(msg); err != nil {
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
		return
	}

	// POST tickets/{id}/reply 或 tickets/{id}/images
	parts := strings.SplitN(rest, "/", 2)
	ticketID := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	t, err := a.tickets.GetTicket(ticketID)
	if err != nil || t.UserID != user.ID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "ticket not found"})
		return
	}

	switch action {
	case "reply":
		if t.Status == tickets.StatusClosed {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ticket is closed"})
			return
		}
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		msg := &tickets.Message{ID: idgen.NextString(), TicketID: ticketID, Content: body.Content, IsAdmin: false}
		if err := a.tickets.AddMessage(msg); err != nil {
			internalError(w, r, err)
			return
		}
		a.tickets.UpdateTicketStatus(ticketID, tickets.StatusOpen)
		writeJSON(w, http.StatusOK, msg)

	case "images":
		if a.uploadsDir == "" {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "uploads not configured"})
			return
		}
		if err := r.ParseMultipartForm(5 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "file too large (max 5MB)"})
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing file"})
			return
		}
		defer file.Close()

		ext := strings.ToLower(filepath.Ext(header.Filename))
		allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true}
		if !allowed[ext] {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unsupported file type"})
			return
		}

		storedName := idgen.NextString() + ext
		os.MkdirAll(a.uploadsDir, 0o755)
		dst, err := os.Create(filepath.Join(a.uploadsDir, storedName))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "save file failed"})
			return
		}
		defer dst.Close()
		written, err := io.Copy(dst, file)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "save file failed"})
			return
		}

		img := &tickets.Image{
			ID: idgen.NextString(), TicketID: ticketID,
			Filename: header.Filename, StoredName: storedName, Size: written,
		}
		if err := a.tickets.AddImage(img); err != nil {
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, img)

	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
	}
}

// nextTrafficResetAt 根据重置策略和上次重置时间计算下次重置时间，无重置策略时返回 nil。
func nextTrafficResetAt(strategy string, createdAt time.Time, lastResetAt *time.Time) *time.Time {
	ref := createdAt
	if lastResetAt != nil && !lastResetAt.IsZero() {
		ref = *lastResetAt
	}
	var next time.Time
	switch strategy {
	case "day":
		next = ref.Add(24 * time.Hour)
	case "week":
		next = ref.AddDate(0, 0, 7)
	case "month":
		next = ref.AddDate(0, 1, 0)
	case "year":
		next = ref.AddDate(1, 0, 0)
	default:
		return nil
	}
	return &next
}

// handlePortalTraceroute 返回所有节点的最新 traceroute 快照（只读，供用户门户使用）。
func (a *portalAPI) handlePortalTraceroute(w http.ResponseWriter, r *http.Request, user users.User) {
	snapshots, err := a.nodes.ListLatestTracerouteSnapshots()
	if err != nil {
		internalError(w, r, err)
		return
	}
	if snapshots == nil {
		snapshots = map[string][]nodes.TracerouteSnapshot{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snapshots})
}

// handlePortalHosts 返回用户可用 host 列表及排除状态。
// GET /v1/portal/{token}/hosts
func (a *portalAPI) handlePortalHosts(w http.ResponseWriter, r *http.Request, user users.User) {
	// 查询用户已排除的 host 集合
	excludedIDs, err := a.users.ListHostExclusionsByUser(user.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	excludedSet := make(map[string]bool, len(excludedIDs))
	for _, id := range excludedIDs {
		excludedSet[id] = true
	}

	// 查询该用户的所有 user_inbounds（跳过已禁用节点）
	accesses, err := a.users.ListActiveUserInboundsByUser(user.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}

	type hostItem struct {
		HostID       string `json:"host_id"`
		Protocol     string `json:"protocol"`
		Country      string `json:"country"`
		Region       string `json:"region"`
		Network      string `json:"network"`
		Entry        string `json:"entry"`
		Remark       string `json:"remark"`
		NodeName     string `json:"node_name"`
		OutboundName string `json:"outbound_name"`
		Excluded     bool   `json:"excluded"`
	}

	// 缓存节点名，避免重复查询
	nodeNames := make(map[string]string)
	nodeName := func(nodeID string) string {
		if n, ok := nodeNames[nodeID]; ok {
			return n
		}
		if node, err := a.nodes.Get(nodeID); err == nil {
			nodeNames[nodeID] = node.Name
			return node.Name
		}
		return ""
	}

	// 缓存出口名，避免重复查询
	outboundNames := make(map[string]string)
	outboundName := func(outboundID string) string {
		if outboundID == "" || a.outbounds == nil {
			return ""
		}
		if n, ok := outboundNames[outboundID]; ok {
			return n
		}
		if ob, err := a.outbounds.Get(outboundID); err == nil {
			outboundNames[outboundID] = ob.Name
			return ob.Name
		}
		return ""
	}

	seen := make(map[string]bool)
	var items []hostItem

	for _, acc := range accesses {
		ib, err := a.inbounds.GetInbound(acc.InboundID)
		if err != nil {
			continue
		}
		hosts, err := a.inbounds.ListHostsByInbound(ib.ID)
		if err != nil {
			continue
		}
		for _, h := range hosts {
			if seen[h.ID] {
				continue
			}
			seen[h.ID] = true
			items = append(items, hostItem{
				HostID:       h.ID,
				Protocol:     ib.Protocol,
				Country:      h.Country,
				Region:       h.Region,
				Network:      h.Network,
				Entry:        h.Entry,
				Remark:       h.Remark,
				NodeName:     nodeName(acc.NodeID),
				OutboundName: outboundName(ib.OutboundID),
				Excluded:     excludedSet[h.ID],
			})
		}
	}

	if items == nil {
		items = []hostItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": items})
}

// handlePortalHostExclude 切换某 host 的排除状态。
// POST /v1/portal/{token}/hosts/exclude
// body: {"host_id": "...", "excluded": true/false}
func (a *portalAPI) handlePortalHostExclude(w http.ResponseWriter, r *http.Request, user users.User) {
	var body struct {
		HostID   string `json:"host_id"`
		Excluded bool   `json:"excluded"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if body.HostID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "host_id is required"})
		return
	}

	var opErr error
	if body.Excluded {
		opErr = a.users.SetHostExclusion(user.ID, body.HostID)
	} else {
		opErr = a.users.ClearHostExclusion(user.ID, body.HostID)
	}
	if opErr != nil {
		internalError(w, r, opErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
