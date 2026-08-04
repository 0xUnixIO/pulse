package serverapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"pulse/internal/idgen"
	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/outbounds"
	"pulse/internal/users"
)

type inboundAPI struct {
	store         inbounds.InboundStore
	userStore     users.Store
	nodeStore     nodes.Store
	outboundStore outbounds.Store
	dial          jobs.NodeDialer
	applyOpts     jobs.ApplyOptions
	syncNode     func(nodeID string) // 异步触发节点 NodeGate sync
}

func RegisterInboundsAPI(mux *http.ServeMux, store inbounds.InboundStore, userStore users.Store, nodeStore nodes.Store, outboundStore outbounds.Store, dial jobs.NodeDialer, applyOpts jobs.ApplyOptions, syncNode func(nodeID string)) {
	a := &inboundAPI{store: store, userStore: userStore, nodeStore: nodeStore, outboundStore: outboundStore, dial: dial, applyOpts: applyOpts, syncNode: syncNode}
	mux.HandleFunc("/v1/inbounds", a.handleInbounds)
	mux.HandleFunc("/v1/inbounds/ss-outbound-options", a.handleSSOutboundOptions)
	mux.HandleFunc("/v1/inbounds/", a.handleInboundRoutes)
	mux.HandleFunc("/v1/hosts", a.handleHosts)
	mux.HandleFunc("/v1/hosts/", a.handleHostRoutes)
}

// ─── Inbound handlers ────────────────────────────────────────────────────────

func (a *inboundAPI) handleInbounds(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		var items []inbounds.Inbound
		var err error
		if nodeID := r.URL.Query().Get("node_id"); nodeID != "" {
			items, err = a.store.ListInboundsByNode(nodeID)
		} else {
			items, err = a.store.ListInbounds()
		}
		if err != nil {
			internalError(w, r, err)
			return
		}
		userCounts, err := a.userStore.CountUsersByInbound()
		if err != nil {
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"inbounds": items, "user_counts": userCounts})
	case http.MethodPost:
		var req inbounds.Inbound
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.NodeID == "" || req.Protocol == "" || req.Port == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "node_id, protocol and port are required"})
			return
		}
		if !supportedProtocol(req.Protocol) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unsupported protocol"})
			return
		}
		if req.ID == "" {
			req.ID = idgen.NextString()
		}
		if req.Tag == "" {
			req.Tag = fmt.Sprintf("%s-%d", req.Protocol, req.Port)
		}
		trimInbound(&req)
		item, err := a.store.UpsertInbound(req)
		if err != nil {
			internalError(w, r, err)
			return
		}
		a.applyInboundNode(item.NodeID)
		writeJSON(w, http.StatusOK, item)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (a *inboundAPI) handleInboundRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/inbounds/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	if id == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "inbound id is required"})
		return
	}

	// Sub-routes: /v1/inbounds/{id}/users
	if len(parts) == 2 && parts[1] == "users" {
		a.handleInboundUsers(w, r, id)
		return
	}

	switch r.Method {
	case http.MethodGet:
		item, err := a.store.GetInbound(id)
		if err != nil {
			writeInboundError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		// 以 map 形式解码，区分「字段被省略」与「显式传空清空」：
		// 省略的字段保留现有值（部分更新），显式传空字符串则清空。
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		req := inbounds.Inbound{ID: id}
		for key, dest := range map[string]*string{
			"node_id":                &req.NodeID,
			"protocol":               &req.Protocol,
			"tag":                    &req.Tag,
			"method":                 &req.Method,
			"password":               &req.Password,
			"security":               &req.Security,
			"reality_private_key":    &req.RealityPrivateKey,
			"reality_public_key":     &req.RealityPublicKey,
			"reality_handshake_addr": &req.RealityHandshakeAddr,
			"reality_short_id":       &req.RealityShortID,
			"outbound_id":            &req.OutboundID,
			"extra":                  &req.Extra,
		} {
			if raw, ok := body[key]; ok {
				_ = json.Unmarshal(raw, dest)
			}
		}
		if raw, ok := body["port"]; ok {
			_ = json.Unmarshal(raw, &req.Port)
		}
		if raw, ok := body["traffic_rate"]; ok {
			_ = json.Unmarshal(raw, &req.TrafficRate)
		}
		if req.Protocol != "" && !supportedProtocol(req.Protocol) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unsupported protocol"})
			return
		}
		// 合并现有字段（请求体未出现的字段保留原值）
		existing, err := a.store.GetInbound(id)
		if err != nil {
			writeInboundError(w, err)
			return
		}
		reqFields := map[string]*string{
			"node_id":                &req.NodeID,
			"protocol":               &req.Protocol,
			"tag":                    &req.Tag,
			"method":                 &req.Method,
			"password":               &req.Password,
			"security":               &req.Security,
			"reality_private_key":    &req.RealityPrivateKey,
			"reality_public_key":     &req.RealityPublicKey,
			"reality_handshake_addr": &req.RealityHandshakeAddr,
			"reality_short_id":       &req.RealityShortID,
			"outbound_id":            &req.OutboundID,
			"extra":                  &req.Extra,
		}
		existingValues := map[string]string{
			"node_id":                existing.NodeID,
			"protocol":               existing.Protocol,
			"tag":                    existing.Tag,
			"method":                 existing.Method,
			"password":               existing.Password,
			"security":               existing.Security,
			"reality_private_key":    existing.RealityPrivateKey,
			"reality_public_key":     existing.RealityPublicKey,
			"reality_handshake_addr": existing.RealityHandshakeAddr,
			"reality_short_id":       existing.RealityShortID,
			"outbound_id":            existing.OutboundID,
			"extra":                  existing.Extra,
		}
		for key, dest := range reqFields {
			if _, ok := body[key]; !ok {
				*dest = existingValues[key]
			}
		}
		if _, ok := body["port"]; !ok {
			req.Port = existing.Port
		}
		if _, ok := body["traffic_rate"]; !ok || req.TrafficRate <= 0 {
			req.TrafficRate = existing.TrafficRate
		}
		trimInbound(&req)
		nodeChanged := req.NodeID != existing.NodeID
		item, err := a.store.UpsertInbound(req)
		if err != nil {
			internalError(w, r, err)
			return
		}
		// inbound 迁移节点时，同步更新 user_inbounds.node_id
		if nodeChanged {
			if err := a.userStore.UpdateUserInboundsNode(item.ID, item.NodeID); err != nil {
				internalError(w, r, fmt.Errorf("update user_inbounds node: %w", err))
				return
			}
			a.applyInboundNode(existing.NodeID) // 旧节点重下发（移除该 inbound）
		}
		a.applyInboundNode(item.NodeID)
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		existing, err := a.store.GetInbound(id)
		if err != nil {
			writeInboundError(w, err)
			return
		}
		if err := a.store.DeleteInbound(id); err != nil {
			writeInboundError(w, err)
			return
		}
		a.applyInboundNode(existing.NodeID)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

// ─── Host handlers ────────────────────────────────────────────────────────────

func (a *inboundAPI) handleHosts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		inboundID := r.URL.Query().Get("inbound_id")
		var items []inbounds.Host
		var err error
		if inboundID != "" {
			items, err = a.store.ListHostsByInbound(inboundID)
		} else {
			items, err = a.store.ListHosts()
		}
		if err != nil {
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"hosts": items})
	case http.MethodPost:
		var req inbounds.Host
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.InboundID == "" || req.Address == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "inbound_id and address are required"})
			return
		}
		if _, err := a.store.GetInbound(req.InboundID); err != nil {
			writeInboundError(w, err)
			return
		}
		if req.ID == "" {
			req.ID = idgen.NextString()
		}
		if req.Security == "" {
			req.Security = "none"
		}
		trimHost(&req)
		item, err := a.store.UpsertHost(req)
		if err != nil {
			internalError(w, r, err)
			return
		}
		a.triggerHostNodeSync(item)
		writeJSON(w, http.StatusOK, item)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (a *inboundAPI) handleHostRoutes(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/hosts/")
	if id == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "host id is required"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := a.store.GetHost(id)
		if err != nil {
			writeHostError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		existing, err := a.store.GetHost(id)
		if err != nil {
			writeHostError(w, err)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&existing); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		existing.ID = id
		trimHost(&existing)
		item, err := a.store.UpsertHost(existing)
		if err != nil {
			internalError(w, r, err)
			return
		}
		a.triggerHostNodeSync(item)
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if err := a.store.DeleteHost(id); err != nil {
			writeHostError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

// applyInboundNode 在后台异步将指定节点的最新配置下发到节点（inbound 变更后调用）。
func (a *inboundAPI) applyInboundNode(nodeID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := jobs.ApplyNode(ctx, nodeID, a.nodeStore, a.userStore, a.store, a.outboundStore, a.dial, a.applyOpts); err != nil {
			log.Printf("warn: apply node %s after inbound change: %v", nodeID, err)
		}
	}()
}

func writeInboundError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, inbounds.ErrInboundNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func writeHostError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, inbounds.ErrHostNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

// handleInboundUsers manages user assignments for an inbound.
// GET  → list user IDs assigned to this inbound
// PUT  → bulk-set user IDs (reconciles adds/removes)
func (a *inboundAPI) handleInboundUsers(w http.ResponseWriter, r *http.Request, inboundID string) {
	ib, err := a.store.GetInbound(inboundID)
	if err != nil {
		writeInboundError(w, err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		existing, err := a.userStore.ListUserInboundsByInbound(inboundID)
		if err != nil {
			internalError(w, r, err)
			return
		}
		userIDs := make([]string, 0, len(existing))
		for _, acc := range existing {
			userIDs = append(userIDs, acc.UserID)
		}
		writeJSON(w, http.StatusOK, map[string]any{"user_ids": userIDs})

	case http.MethodPut:
		var req struct {
			UserIDs []string `json:"user_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}

		wanted := make(map[string]struct{}, len(req.UserIDs))
		for _, uid := range req.UserIDs {
			wanted[uid] = struct{}{}
		}

		existing, err := a.userStore.ListUserInboundsByInbound(inboundID)
		if err != nil {
			internalError(w, r, err)
			return
		}
		existingByUser := make(map[string]users.UserInbound, len(existing))
		for _, acc := range existing {
			existingByUser[acc.UserID] = acc
		}

		// Add new user assignments
		for _, uid := range req.UserIDs {
			if _, ok := existingByUser[uid]; !ok {
				secret := randomToken(12)
				if ib.Protocol == "shadowsocks" && strings.HasPrefix(ib.Method, "2022-") {
					secret = ssPassword(ib.Method)
				}
				acc := users.UserInbound{
					ID:        idgen.NextString(),
					UserID:    uid,
					InboundID: inboundID,
					NodeID:    ib.NodeID,
					UUID:      randomUUID(),
					Secret:    secret,
				}
				if _, err := a.userStore.UpsertUserInbound(acc); err != nil {
					internalError(w, r, err)
					return
				}
			}
		}

		// Remove deselected users
		for uid, acc := range existingByUser {
			if _, ok := wanted[uid]; !ok {
				if err := a.userStore.DeleteUserInbound(acc.ID); err != nil {
					internalError(w, r, err)
					return
				}
			}
		}

		a.applyInboundNode(ib.NodeID)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
	}
}

// handleSSOutboundOptions 返回所有 shadowsocks inbound 的用户选项，
// 可用作其他 inbound/路由规则的出口选择。
func (a *inboundAPI) handleSSOutboundOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	allIbs, err := a.store.ListInbounds()
	if err != nil {
		internalError(w, r, err)
		return
	}
	nodeList, _ := a.nodeStore.List()
	nodeMap := make(map[string]nodes.Node, len(nodeList))
	for _, n := range nodeList {
		nodeMap[n.ID] = n
	}
	allUsers, _ := a.userStore.ListUsers()
	userMap := make(map[string]string, len(allUsers))
	for _, u := range allUsers {
		userMap[u.ID] = u.Username
	}

	type ssOption struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	var opts []ssOption
	for _, ib := range allIbs {
		if ib.Protocol != "shadowsocks" {
			continue
		}
		nodeName := ib.NodeID
		if n, ok := nodeMap[ib.NodeID]; ok {
			nodeName = n.Name
		}
		accs, _ := a.userStore.ListUserInboundsByInbound(ib.ID)
		for _, acc := range accs {
			username := acc.UserID
			if name, ok := userMap[acc.UserID]; ok {
				username = name
			}
			opts = append(opts, ssOption{
				ID:    fmt.Sprintf("nodeib:%s:%s", ib.ID, acc.ID),
				Label: fmt.Sprintf("%s - SS:%d (%s)", nodeName, ib.Port, username),
			})
		}
	}
	if opts == nil {
		opts = []ssOption{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"options": opts})
}

// trimHost 对 Host 的所有字符串字段做 TrimSpace，防止用户输入带入多余空格。
func trimHost(h *inbounds.Host) {
	h.Address  = strings.TrimSpace(h.Address)
	h.Remark   = strings.TrimSpace(h.Remark)
	h.SNI      = strings.TrimSpace(h.SNI)
	h.Host     = strings.TrimSpace(h.Host)
	h.Path     = strings.TrimSpace(h.Path)
	h.ALPN     = strings.TrimSpace(h.ALPN)
	h.Fingerprint        = strings.TrimSpace(h.Fingerprint)
	h.RealityPublicKey   = strings.TrimSpace(h.RealityPublicKey)
	h.RealityShortID     = strings.TrimSpace(h.RealityShortID)
	h.RealitySpiderX     = strings.TrimSpace(h.RealitySpiderX)
	h.Country    = strings.TrimSpace(h.Country)
	h.Region     = strings.TrimSpace(h.Region)
	h.Network    = strings.TrimSpace(h.Network)
	h.Entry      = strings.TrimSpace(h.Entry)
	h.Tags       = strings.TrimSpace(h.Tags)
}

// triggerHostNodeSync 在 Host 保存后自动触发相关节点的 NodeGate sync：
// 落地节点（https_port 影响 servers 端口）+ 前置节点（portforward 目标端口）。
func (a *inboundAPI) triggerHostNodeSync(h inbounds.Host) {
	if a.syncNode == nil {
		return
	}
	triggered := make(map[string]struct{})
	// 落地节点
	if ib, err := a.store.GetInbound(h.InboundID); err == nil && ib.NodeID != "" {
		triggered[ib.NodeID] = struct{}{}
		a.syncNode(ib.NodeID)
	}
	// 前置节点
	if h.RelayNodeID != "" {
		if _, done := triggered[h.RelayNodeID]; !done {
			a.syncNode(h.RelayNodeID)
		}
	}
}

// trimInbound 对 Inbound 的所有字符串字段做 TrimSpace。
func trimInbound(ib *inbounds.Inbound) {
	ib.Tag                  = strings.TrimSpace(ib.Tag)
	ib.Method               = strings.TrimSpace(ib.Method)
	ib.Password             = strings.TrimSpace(ib.Password)
	ib.Security             = strings.TrimSpace(ib.Security)
	ib.RealityPrivateKey    = strings.TrimSpace(ib.RealityPrivateKey)
	ib.RealityPublicKey     = strings.TrimSpace(ib.RealityPublicKey)
	ib.RealityHandshakeAddr = strings.TrimSpace(ib.RealityHandshakeAddr)
	ib.RealityShortID       = strings.TrimSpace(ib.RealityShortID)
	ib.OutboundID           = strings.TrimSpace(ib.OutboundID)
}
