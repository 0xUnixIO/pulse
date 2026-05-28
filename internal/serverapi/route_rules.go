package serverapi

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"pulse/internal/idgen"
	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/outbounds"
	"pulse/internal/routerules"
	"pulse/internal/users"
)

type routeRuleAPI struct {
	store         routerules.Store
	nodeStore     nodes.Store
	userStore     users.Store
	ibStore       inbounds.InboundStore
	outboundStore outbounds.Store
	dial          jobs.NodeDialer
	applyOpts     jobs.ApplyOptions
}

func RegisterRouteRulesAPI(mux *http.ServeMux, store routerules.Store, nodeStore nodes.Store, userStore users.Store, ibStore inbounds.InboundStore, outboundStore outbounds.Store, dial jobs.NodeDialer, applyOpts jobs.ApplyOptions) {
	a := &routeRuleAPI{
		store:         store,
		nodeStore:     nodeStore,
		userStore:     userStore,
		ibStore:       ibStore,
		outboundStore: outboundStore,
		dial:          dial,
		applyOpts:     applyOpts,
	}
	mux.HandleFunc("/v1/routerules/batch", a.handleBatchImport)
	mux.HandleFunc("/v1/routerules", a.handleRouteRules)
	mux.HandleFunc("/v1/routerules/", a.handleRouteRuleRoutes)
}

// applyAllNodes 在路由规则变更后异步重新下发配置到所有节点。
func (a *routeRuleAPI) applyAllNodes() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		nodesList, err := a.nodeStore.List()
		if err != nil {
			log.Printf("warn: route rule change: list nodes: %v", err)
			return
		}
		for _, n := range nodesList {
			if n.Disabled {
				continue
			}
			if err := jobs.ApplyNode(ctx, n.ID, a.nodeStore, a.userStore, a.ibStore, a.outboundStore, a.dial, a.applyOpts); err != nil {
				log.Printf("warn: route rule change: apply node %s: %v", n.ID, err)
			}
		}
	}()
}

// handleBatchImport 批量创建路由规则（仅 POST）。
// 请求体：{ "rules": [ {...RouteRule fields without id...} ] }
// 响应体：{ "created": N }
func (a *routeRuleAPI) handleBatchImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	var req struct {
		Rules []routerules.RouteRule `json:"rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}

	created := 0
	for _, rule := range req.Rules {
		rule.ID = idgen.NextString()
		if _, err := a.store.Upsert(rule); err != nil {
			log.Printf("warn: batch import route rule %q: %v", rule.Name, err)
			continue
		}
		created++
	}

	if created > 0 {
		a.applyAllNodes()
	}

	writeJSON(w, http.StatusOK, map[string]any{"created": created})
}

func (a *routeRuleAPI) handleRouteRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := a.store.List()
		if err != nil {
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"rules": items})
	case http.MethodPost:
		var req routerules.RouteRule
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.Name == "" || req.RuleType == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name and rule_type are required"})
			return
		}
		if req.Patterns == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "patterns is required"})
			return
		}
		if req.ID == "" {
			req.ID = idgen.NextString()
		}
		rule, err := a.store.Upsert(req)
		if err != nil {
			internalError(w, r, err)
			return
		}
		a.applyAllNodes()
		writeJSON(w, http.StatusCreated, rule)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (a *routeRuleAPI) handleRouteRuleRoutes(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/routerules/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "rule id is required"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		rule, err := a.store.Get(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "route rule not found"})
			return
		}
		writeJSON(w, http.StatusOK, rule)
	case http.MethodPut:
		existing, err := a.store.Get(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "route rule not found"})
			return
		}
		var req routerules.RouteRule
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		req.ID = existing.ID
		rule, err := a.store.Upsert(req)
		if err != nil {
			internalError(w, r, err)
			return
		}
		a.applyAllNodes()
		writeJSON(w, http.StatusOK, rule)
	case http.MethodDelete:
		if err := a.store.Delete(id); err != nil {
			internalError(w, r, err)
			return
		}
		a.applyAllNodes()
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}
