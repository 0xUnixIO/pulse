package serverapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"pulse/internal/cfdomain"
	"pulse/internal/cloudflarex"
	"pulse/internal/idgen"
	"pulse/internal/nodedomain"
	"pulse/internal/nodes"
)

type cfDomainAPI struct {
	store      cfdomain.Store
	ndStore    nodedomain.Store
	nodesStore nodes.Store
}

// RegisterCFDomainAPI 注册 CF 域名管理相关的 API 路由。
// ndStore/nodesStore 用于在面板里增改 DNS 记录后同步写入节点域名表，
// 使入站的域名选择器无需手动“从 CF 同步”即可看到新记录。
func RegisterCFDomainAPI(mux *http.ServeMux, store cfdomain.Store, ndStore nodedomain.Store, nodesStore nodes.Store) {
	a := &cfDomainAPI{store: store, ndStore: ndStore, nodesStore: nodesStore}
	mux.HandleFunc("/v1/cf-domains", a.handleCFDomains)
	mux.HandleFunc("/v1/cf-domains/", a.handleCFDomainRoutes)
}

// handleCFDomains 处理 /v1/cf-domains 的 GET 和 POST 请求。
func (a *cfDomainAPI) handleCFDomains(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		domains, err := a.store.ListCFDomains()
		if err != nil {
			internalError(w, r, err)
			return
		}
		// 脱敏 token
		for i := range domains {
			domains[i].CFToken = domains[i].MaskToken()
		}
		writeJSON(w, http.StatusOK, map[string]any{"domains": domains})

	case http.MethodPost:
		var req cfdomain.CFDomain
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.CFToken == "" || req.ZoneID == "" || req.ZoneName == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cf_token, zone_id and zone_name are required"})
			return
		}
		if req.ID == "" {
			req.ID = idgen.NextString()
		}
		domain, err := a.store.UpsertCFDomain(req)
		if err != nil {
			internalError(w, r, err)
			return
		}
		// 返回脱敏后的域名
		domain.CFToken = domain.MaskToken()
		writeJSON(w, http.StatusCreated, domain)

	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

// handleCFDomainRoutes 处理 /v1/cf-domains/ 下的子路由。
func (a *cfDomainAPI) handleCFDomainRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/cf-domains/")
	if path == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cf domain id is required"})
		return
	}

	// 处理 verify-token 特殊路由
	if path == "verify-token" {
		a.handleVerifyToken(w, r)
		return
	}

	// 解析路径: {id} 或 {id}/records 或 {id}/records/{recordId}
	parts := strings.SplitN(path, "/", 3)
	domainID := parts[0]

	if len(parts) == 1 {
		// /v1/cf-domains/{id}
		a.handleCFDomainByID(w, r, domainID)
		return
	}

	if parts[1] != "records" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}

	if len(parts) == 2 || parts[2] == "" {
		// /v1/cf-domains/{id}/records
		a.handleDNSRecords(w, r, domainID)
		return
	}

	// /v1/cf-domains/{id}/records/{recordId}
	recordID := parts[2]
	a.handleDNSRecordByID(w, r, domainID, recordID)
}

// handleVerifyToken 验证 CF token 并返回 zones 列表。
func (a *cfDomainAPI) handleVerifyToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req struct {
		CFToken string `json:"cf_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}
	if req.CFToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cf_token is required"})
		return
	}

	client := cloudflarex.NewClient(req.CFToken)
	ctx := r.Context()

	if err := client.VerifyToken(ctx); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "token verification failed: " + err.Error()})
		return
	}

	zones, err := client.ListZones(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list zones: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"zones": zones})
}

// handleCFDomainByID 处理单个 CF 域名的删除操作。
func (a *cfDomainAPI) handleCFDomainByID(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodDelete:
		if err := a.store.DeleteCFDomain(id); err != nil {
			writeCFDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeMethodNotAllowed(w, http.MethodDelete)
	}
}

// handleDNSRecords 处理 DNS 记录的列表和创建。
func (a *cfDomainAPI) handleDNSRecords(w http.ResponseWriter, r *http.Request, domainID string) {
	domain, err := a.store.GetCFDomain(domainID)
	if err != nil {
		writeCFDomainError(w, err)
		return
	}
	client := cloudflarex.NewClient(domain.CFToken)
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		recordType := r.URL.Query().Get("type")
		records, err := client.ListDNSRecords(ctx, domain.ZoneID, recordType)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"records": records})

	case http.MethodPost:
		var record cloudflarex.DNSRecord
		if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		created, err := client.CreateDNSRecord(ctx, domain.ZoneID, record)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		a.mirrorNodeDomain(r, domainID, created)
		writeJSON(w, http.StatusCreated, created)

	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

// handleDNSRecordByID 处理单条 DNS 记录的更新和删除。
func (a *cfDomainAPI) handleDNSRecordByID(w http.ResponseWriter, r *http.Request, domainID string, recordID string) {
	domain, err := a.store.GetCFDomain(domainID)
	if err != nil {
		writeCFDomainError(w, err)
		return
	}
	client := cloudflarex.NewClient(domain.CFToken)
	ctx := r.Context()

	switch r.Method {
	case http.MethodPut:
		var record cloudflarex.DNSRecord
		if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		updated, err := client.UpdateDNSRecord(ctx, domain.ZoneID, recordID, record)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		a.mirrorNodeDomain(r, domainID, updated)
		writeJSON(w, http.StatusOK, updated)

	case http.MethodDelete:
		if err := client.DeleteDNSRecord(ctx, domain.ZoneID, recordID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})

	default:
		writeMethodNotAllowed(w, http.MethodPut+", "+http.MethodDelete)
	}
}

// mirrorNodeDomain 把面板里新建/修改的 DNS 记录写入节点域名表。
// A/AAAA 按 IP 自动匹配节点；匹配不到时保留该 FQDN 原有的节点分配。
// 失败只记日志，不影响 CF 操作本身的结果（之后仍可手动“从 CF 同步”）。
func (a *cfDomainAPI) mirrorNodeDomain(r *http.Request, cfDomainID string, rec cloudflarex.DNSRecord) {
	if a.ndStore == nil || rec.Name == "" {
		return
	}
	switch rec.Type {
	case "A", "AAAA", "CNAME":
	default:
		return
	}

	nodeID := ""
	if a.nodesStore != nil && (rec.Type == "A" || rec.Type == "AAAA") {
		if nodeList, err := a.nodesStore.List(); err == nil {
			nodeID = buildNodeIPMap(nodeList)[rec.Content]
		}
	}
	if nodeID == "" {
		if existing, err := a.ndStore.ListByCFDomain(cfDomainID); err == nil {
			for _, nd := range existing {
				if nd.FQDN == rec.Name {
					nodeID = nd.NodeID
					break
				}
			}
		}
	}

	if _, err := a.ndStore.Upsert(nodedomain.NodeDomain{
		ID:         idgen.NextString(),
		NodeID:     nodeID,
		CFDomainID: cfDomainID,
		FQDN:       rec.Name,
		RecordType: rec.Type,
		Content:    rec.Content,
		Proxied:    rec.Proxied,
	}); err != nil {
		slog.WarnContext(r.Context(), "sync node domain after dns record change failed", "fqdn", rec.Name, "err", err)
	}
}

// writeCFDomainError 根据错误类型返回对应的 HTTP 状态码。
func writeCFDomainError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, cfdomain.ErrCFDomainNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}
