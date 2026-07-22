package serverapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/outbounds"
	"pulse/internal/users"
)

type systemAPI struct {
	users         users.Store
	nodes         nodes.Store
	inboundStore  inbounds.InboundStore
	outboundStore outbounds.Store
	dial          jobs.NodeDialer
	usageBuf      *nodes.UsageBuffer
	applyOpts     jobs.ApplyOptions
}

// RegisterSystemAPIWithInbounds 注册 system API（含 inboundStore，用于流量同步）。
// dial / usageBuf 应与定时 SyncUsage 共用，避免手动同步绕过 buffer 导致双计。
func RegisterSystemAPIWithInbounds(mux *http.ServeMux, usersStore users.Store, nodesStore nodes.Store, ibStore inbounds.InboundStore, applyOpts jobs.ApplyOptions, dial jobs.NodeDialer, usageBuf *nodes.UsageBuffer) {
	api := &systemAPI{
		users:         usersStore,
		nodes:         nodesStore,
		inboundStore:  ibStore,
		outboundStore: nil,
		dial:          dial,
		usageBuf:      usageBuf,
		applyOpts:     applyOpts,
	}
	mux.HandleFunc("/v1/system/sync-usage", api.handleSyncUsage)
}

func (a *systemAPI) handleSyncUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if a.inboundStore == nil {
		internalError(w, r, errors.New("inbound store not configured"))
		return
	}
	if a.dial == nil {
		internalError(w, r, errors.New("node dialer not configured"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	result, err := jobs.SyncUsageWith(ctx, a.users, a.nodes, a.inboundStore, a.dial, a.applyOpts, a.outboundStore, a.usageBuf)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}
