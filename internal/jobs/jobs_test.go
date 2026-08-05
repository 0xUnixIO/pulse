package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"pulse/internal/inbounds"
	"pulse/internal/nodes"
	"pulse/internal/users"
)

// ─── ShouldResetTraffic ───────────────────────────────────────────────────────

func TestShouldResetTraffic_NoReset(t *testing.T) {
	created := time.Now().Add(-365 * 24 * time.Hour)
	if ShouldResetTraffic(users.ResetStrategyNoReset, created, nil, time.Now()) {
		t.Fatal("no_reset 策略不应触发重置")
	}
}

func TestShouldResetTraffic_Day(t *testing.T) {
	created := time.Now().Add(-25 * time.Hour)
	if !ShouldResetTraffic(users.ResetStrategyDay, created, nil, time.Now()) {
		t.Fatal("超过 24h 应触发 day 重置")
	}
	created = time.Now().Add(-23 * time.Hour)
	if ShouldResetTraffic(users.ResetStrategyDay, created, nil, time.Now()) {
		t.Fatal("未超过 24h 不应触发 day 重置")
	}
}

func TestShouldResetTraffic_Month(t *testing.T) {
	// 用固定时间避免月末 AddDate 规范化边界问题（如 3/30 - 1month = Feb30 → March2）
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	created := now.AddDate(0, -1, -1) // 2026-02-14，超过一个月
	if !ShouldResetTraffic(users.ResetStrategyMonth, created, nil, now) {
		t.Fatal("超过一个月应触发 month 重置")
	}
	created = now.AddDate(0, 0, -15) // 2026-02-28，未超过一个月
	if ShouldResetTraffic(users.ResetStrategyMonth, created, nil, now) {
		t.Fatal("未超过一个月不应触发 month 重置")
	}
}

func TestShouldResetTraffic_UseLastResetAt(t *testing.T) {
	created := time.Now().Add(-100 * 24 * time.Hour)
	lastReset := time.Now().Add(-23 * time.Hour)
	// 上次重置在 23h 前，day 策略还没到
	if ShouldResetTraffic(users.ResetStrategyDay, created, &lastReset, time.Now()) {
		t.Fatal("上次重置在 23h 前，day 策略不应再次触发")
	}
	lastReset = time.Now().Add(-25 * time.Hour)
	if !ShouldResetTraffic(users.ResetStrategyDay, created, &lastReset, time.Now()) {
		t.Fatal("上次重置在 25h 前，day 策略应触发")
	}
}

// ─── ResetTraffic ─────────────────────────────────────────────────────────────

func TestResetTraffic_ResetsLimitedUser(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})

	// 用户创建于 25h 前，使用了 500 bytes，限额 400
	created := time.Now().Add(-25 * time.Hour)
	u := users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		DataLimitResetStrategy: users.ResetStrategyDay,
		TrafficLimit:           400,
		UploadBytes:            300, DownloadBytes: 200,
		CreatedAt: created,
	}
	u.UsedBytes = u.UploadBytes + u.DownloadBytes
	_, _ = userStore.UpsertUser(u)

	// 创建对应的 UserInbound
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID:     "u1-ib0",
		UserID: "u1",
		NodeID: "n1",
		UUID:   "11111111-1111-1111-1111-111111111111",
		Secret: "test-secret",
	})

	ibStore := inbounds.NewMemoryStore()
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{
		ID:       "ib-vless",
		NodeID:   "n1",
		Protocol: "vless",
		Tag:      "pulse-vless-n1",
		Port:     443,
	})

	var restarted bool
	dial := testDial(t, func(path string, w http.ResponseWriter, r *http.Request) {
		if path == "/v1/node/runtime/restart" {
			restarted = true
			_ = json.NewEncoder(w).Encode(map[string]any{"running": true})
		}
	})

	result, err := ResetTraffic(context.Background(), userStore, nodeStore, ibStore, dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("ResetTraffic() error = %v", err)
	}
	if result.UsersReset != 1 {
		t.Fatalf("expected 1 user reset, got %d", result.UsersReset)
	}
	if !restarted {
		t.Fatal("expected node restart after reset")
	}

	alice, _ := userStore.GetUser("u1")
	if alice.UsedBytes != 0 || alice.UploadBytes != 0 || alice.DownloadBytes != 0 {
		t.Fatalf("expected bytes cleared, got used=%d", alice.UsedBytes)
	}
	if alice.LastTrafficResetAt == nil {
		t.Fatal("expected LastTrafficResetAt set")
	}
	// 重置后 EffectiveStatus 应该回到 active
	if !alice.EffectiveEnabled() {
		t.Fatal("expected alice active after reset")
	}
}

// ─── SyncUsage ────────────────────────────────────────────────────────────────

func TestSyncUsage_UpdatesBytesAndReloads(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 100,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID:     "u1-ib0",
		UserID: "u1",
		NodeID: "n1",
		UUID:   "11111111-1111-1111-1111-111111111111",
		Secret: "test-secret",
	})

	dial := testDial(t, func(path string, w http.ResponseWriter, r *http.Request) {
		switch path {
		case "/v1/node/runtime/usage":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"available": true, "running": true,
				"users": []map[string]any{
					{"user": "alice", "upload_total": 80, "download_total": 30},
				},
			})
		case "/v1/node/runtime/restart":
			_ = json.NewEncoder(w).Encode(map[string]any{"running": true})
		}
	})

	result, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}
	if result.UsersUpdated != 1 || result.NodesReloaded != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}

	alice, _ := userStore.GetUser("u1")
	if alice.UsedBytes != 110 {
		t.Fatalf("expected used=110, got %d", alice.UsedBytes)
	}
	if alice.EffectiveEnabled() {
		t.Fatal("expected alice limited after exceeding quota")
	}
}

// ─── SyncUsage TrafficRate ────────────────────────────────────────────────────

func TestSyncUsage_TrafficRate_Double(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	ibStore := inbounds.NewMemoryStore()
	// 倍率 2.0：用户计费流量应翻倍（倍率绑定在 inbound 上）
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{ID: "ib1", NodeID: "n1", Protocol: "vless", Tag: "vless-reality", Port: 443, TrafficRate: 2.0})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", InboundID: "ib1", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "test-secret",
	})

	// V2Ray Stats 上报复合用户名
	dial := usageDial(t, "alice@vless-reality", 80, 30)

	_, err := SyncUsage(context.Background(), userStore, nodeStore, ibStore, dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	// 用户计费流量 = (80+30) * 2 = 220
	if alice.UploadBytes != 160 {
		t.Errorf("upload: want 160, got %d", alice.UploadBytes)
	}
	if alice.DownloadBytes != 60 {
		t.Errorf("download: want 60, got %d", alice.DownloadBytes)
	}
	if alice.UsedBytes != 220 {
		t.Errorf("used: want 220, got %d", alice.UsedBytes)
	}

	// 节点记录实际流量（转发放大口径 ×2），不受倍率影响
	node, _ := nodeStore.Get("n1")
	if node.UploadBytes != 160 {
		t.Errorf("node upload: want 160, got %d", node.UploadBytes)
	}
	if node.DownloadBytes != 60 {
		t.Errorf("node download: want 60, got %d", node.DownloadBytes)
	}
}

func TestSyncUsage_TrafficRate_Half(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	ibStore := inbounds.NewMemoryStore()
	// 倍率 0.5：用户计费流量应减半
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{ID: "ib1", NodeID: "n1", Protocol: "vless", Tag: "vless-reality", Port: 443, TrafficRate: 0.5})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", InboundID: "ib1", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "test-secret",
	})

	dial := usageDial(t, "alice@vless-reality", 80, 30)

	_, err := SyncUsage(context.Background(), userStore, nodeStore, ibStore, dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	// 用户计费流量 = (80+30) * 0.5 = 55
	if alice.UploadBytes != 40 {
		t.Errorf("upload: want 40, got %d", alice.UploadBytes)
	}
	if alice.DownloadBytes != 15 {
		t.Errorf("download: want 15, got %d", alice.DownloadBytes)
	}
	if alice.UsedBytes != 55 {
		t.Errorf("used: want 55, got %d", alice.UsedBytes)
	}

	// 节点记录实际流量（转发放大口径 ×2），不受倍率影响
	node, _ := nodeStore.Get("n1")
	if node.UploadBytes != 160 {
		t.Errorf("node upload: want 160, got %d", node.UploadBytes)
	}
	if node.DownloadBytes != 60 {
		t.Errorf("node download: want 60, got %d", node.DownloadBytes)
	}
}

func TestSyncUsage_TrafficRate_QuotaWithRate(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	ibStore := inbounds.NewMemoryStore()
	// 倍率 2.0，限额 100：实际产生 60 bytes 但计费 120，应触发 limited
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{ID: "ib1", NodeID: "n1", Protocol: "vless", Tag: "vless-reality", Port: 443, TrafficRate: 2.0})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 100,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", InboundID: "ib1", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "test-secret",
	})

	dial := usageDial(t, "alice@vless-reality", 40, 20) // 真实 60 bytes，计费 120

	_, err := SyncUsage(context.Background(), userStore, nodeStore, ibStore, dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	if alice.UsedBytes != 120 {
		t.Errorf("used: want 120, got %d", alice.UsedBytes)
	}
	if alice.EffectiveEnabled() {
		t.Error("alice should be limited (120 > 100) but is still enabled")
	}
}

// ─── SyncUsage MultiInbound ───────────────────────────────────────────────────

func TestSyncUsage_MultiInbound_NoDuplicateCounting(t *testing.T) {
	// 回归测试：一个用户在同一节点有多条 inbound 时，流量只应计算一次。
	// 修复前，流量会被乘以 inbound 数量（例如 4 条 inbound → 4 倍流量）。
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
	})
	// 同一个用户在同一节点上有 3 条 inbound（模拟 VLESS、Trojan、Shadowsocks）
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", InboundID: "ib-vless", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "s1",
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib2", UserID: "u1", InboundID: "ib-trojan", NodeID: "n1",
		UUID: "33333333-3333-3333-3333-333333333333", Secret: "s3",
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib3", UserID: "u1", InboundID: "ib-ss", NodeID: "n1",
		UUID: "44444444-4444-4444-4444-444444444444", Secret: "s4",
	})

	// 节点报告 alice 总共 upload=80, download=30（聚合值，不分 inbound）
	dial := usageDial(t, "alice", 80, 30)

	_, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	// 流量应只计一次：80 + 30 = 110，而非 4 倍的 440
	if alice.UploadBytes != 80 {
		t.Errorf("upload: want 80, got %d", alice.UploadBytes)
	}
	if alice.DownloadBytes != 30 {
		t.Errorf("download: want 30, got %d", alice.DownloadBytes)
	}
	if alice.UsedBytes != 110 {
		t.Errorf("used: want 110, got %d", alice.UsedBytes)
	}

	// 节点流量也只应计一次（转发放大口径 ×2）
	node, _ := nodeStore.Get("n1")
	if node.UploadBytes != 160 {
		t.Errorf("node upload: want 160, got %d", node.UploadBytes)
	}
	if node.DownloadBytes != 60 {
		t.Errorf("node download: want 60, got %d", node.DownloadBytes)
	}
}

func TestSyncUsage_MultiInbound_NewInboundAdded(t *testing.T) {
	// With V2Ray Stats reset=true, there are no cursors.
	// Each call returns the delta since last reset.
	// A new inbound doesn't affect traffic counting.
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
	})
	// Old inbound
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", InboundID: "ib-vless", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "s1",
	})
	// New inbound added
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib1", UserID: "u1", InboundID: "ib-trojan", NodeID: "n1",
		UUID: "22222222-2222-2222-2222-222222222222", Secret: "s2",
	})

	// V2Ray Stats returns delta directly (reset=true), no cursor needed
	dial := usageDial(t, "alice", 30, 10)

	_, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	// Delta is directly 30/10 from V2Ray Stats
	if alice.UploadBytes != 30 {
		t.Errorf("upload: want 30, got %d", alice.UploadBytes)
	}
	if alice.DownloadBytes != 10 {
		t.Errorf("download: want 10, got %d", alice.DownloadBytes)
	}
	if alice.UsedBytes != 40 {
		t.Errorf("used: want 40, got %d", alice.UsedBytes)
	}
}

// ─── SyncUsage 边界场景 ──────────────────────────────────────────────────────

func TestSyncUsage_DialError_SkipsNode(t *testing.T) {
	// 节点不可达时应跳过并记录错误，不影响其他节点
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})

	dial := func(nodeID string) (*nodes.Client, error) {
		return nil, fmt.Errorf("connection refused")
	}

	result, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() should not return error, got %v", err)
	}
	if result.NodesSynced != 0 {
		t.Errorf("nodes synced: want 0, got %d", result.NodesSynced)
	}
	if len(result.Errors) != 1 {
		t.Errorf("errors: want 1, got %d", len(result.Errors))
	}
}

func TestSyncUsage_SingboxNotRunning_TriggersRestart(t *testing.T) {
	// 复现 bug：xray 停止后（Available=false, Running=false），
	// SyncUsage 应主动触发 ApplyNodeUsers 恢复 xray，而不是跳过节点。
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	ibStore := inbounds.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", InboundID: "ib-vless", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "s1",
	})
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{
		ID:       "ib-vless",
		NodeID:   "n1",
		Protocol: "vless",
		Tag:      "pulse-vless-n1",
		Port:     443,
	})

	var restarted bool
	dial := testDial(t, func(path string, w http.ResponseWriter, r *http.Request) {
		switch path {
		case "/v1/node/runtime/usage":
			// xray 未运行：Available=false, Running=false
			_ = json.NewEncoder(w).Encode(map[string]any{
				"available": false, "running": false,
			})
		case "/v1/node/runtime/restart":
			restarted = true
			_ = json.NewEncoder(w).Encode(map[string]any{"running": true})
		}
	})

	_, err := SyncUsage(context.Background(), userStore, nodeStore, ibStore, dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}
	if !restarted {
		t.Error("xray 未运行时，SyncUsage 应触发 restart 恢复，但未触发")
	}
}

func TestSyncUsage_UsageEndpointError_SkipsNode(t *testing.T) {
	// usage 端点返回错误时应跳过该节点
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})

	dial := testDial(t, func(path string, w http.ResponseWriter, r *http.Request) {
		if path == "/v1/node/runtime/usage" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"xray crashed"}`))
		}
	})

	result, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() should not return error, got %v", err)
	}
	if result.NodesSynced != 0 {
		t.Errorf("nodes synced: want 0, got %d", result.NodesSynced)
	}
	if len(result.Errors) == 0 {
		t.Error("expected at least one error recorded")
	}
}

func TestSyncUsage_NodeRestart_DeltaFromZero(t *testing.T) {
	// With V2Ray Stats reset=true, node restart simply means counters start from 0.
	// The delta returned is whatever traffic occurred since restart, no cursor involved.
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
		UploadBytes: 100, DownloadBytes: 50, // 已有历史流量
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", InboundID: "ib-vless", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "s1",
	})

	// Node restarted and reports 10/5 delta since restart
	dial := usageDial(t, "alice", 10, 5)

	_, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	// V2Ray Stats returns delta directly: 10/5
	if alice.UploadBytes != 110 {
		t.Errorf("upload: want 110 (100+10), got %d", alice.UploadBytes)
	}
	if alice.DownloadBytes != 55 {
		t.Errorf("download: want 55 (50+5), got %d", alice.DownloadBytes)
	}
}

func TestSyncUsage_MultiNode_ConnectionsReset(t *testing.T) {
	// 验证跨节点连接数从零累加，而非叠加上轮旧值
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://n1.test"})
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n2", Name: "n2", BaseURL: "http://n2.test"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
		Connections:  999, // 上一轮的旧值，应被清零
		Devices:      888,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-n1", UserID: "u1", InboundID: "ib1", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "s1",
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-n2", UserID: "u1", InboundID: "ib2", NodeID: "n2",
		UUID: "22222222-2222-2222-2222-222222222222", Secret: "s2",
	})

	// 两个节点报相同的 IP 集合 → 全局去重后 2 台设备（而非累加的 4）
	dial := testDial(t, func(path string, w http.ResponseWriter, r *http.Request) {
		if path == "/v1/node/runtime/usage" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"available": true, "running": true,
				"users": []map[string]any{
					{"user": "alice", "upload_total": 10, "download_total": 5,
						"connections": 3, "source_ips": []string{"1.1.1.1", "2.2.2.2"}},
				},
			})
		}
	})

	_, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	// 两个节点各报 3 连接，累加 = 6
	if alice.Connections != 6 {
		t.Errorf("connections: want 6, got %d", alice.Connections)
	}
	// 两个节点报相同 2 个 IP，全局去重后 2 台设备（不是累加的 4）
	if alice.Devices != 2 {
		t.Errorf("devices: want 2, got %d", alice.Devices)
	}
}

func TestSyncUsage_OnlineAt_UpdatedOnTraffic(t *testing.T) {
	// 有新增流量时 OnlineAt 应更新
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", InboundID: "ib1", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "s1",
	})

	beforeSync := time.Now().Add(-1 * time.Second)
	dial := usageDial(t, "alice", 50, 20)

	_, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	if alice.OnlineAt == nil {
		t.Fatal("expected OnlineAt to be set")
	}
	if alice.OnlineAt.Before(beforeSync) {
		t.Errorf("OnlineAt should be recent, got %v", alice.OnlineAt)
	}
}

func TestSyncUsage_NoTraffic_OnlineAtNotSet(t *testing.T) {
	// V2Ray Stats with reset=true returns 0 delta when no new traffic
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", InboundID: "ib1", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "s1",
	})

	// Node reports zero delta (no new traffic since last reset)
	dial := usageDial(t, "alice", 0, 0)

	_, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	if alice.OnlineAt != nil {
		t.Errorf("expected OnlineAt nil (no new traffic), got %v", alice.OnlineAt)
	}
}

func TestSyncUsage_NodeTraffic_AccumulatedCorrectly(t *testing.T) {
	// 验证节点维度流量（真实值，不含倍率）被正确累加
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	ibStore := inbounds.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{ID: "ib1", NodeID: "n1", Protocol: "vless", Tag: "vless-reality", Port: 443, TrafficRate: 3.0})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive, TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u2", Username: "bob", Status: users.StatusActive, TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", InboundID: "ib1", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "s1",
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u2-ib0", UserID: "u2", InboundID: "ib1", NodeID: "n1",
		UUID: "22222222-2222-2222-2222-222222222222", Secret: "s2",
	})

	dial := testDial(t, func(path string, w http.ResponseWriter, r *http.Request) {
		if path == "/v1/node/runtime/usage" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"available": true, "running": true,
				"users": []map[string]any{
					{"user": "alice@vless-reality", "upload_total": 100, "download_total": 50},
					{"user": "bob@vless-reality", "upload_total": 40, "download_total": 20},
				},
			})
		}
	})

	_, err := SyncUsage(context.Background(), userStore, nodeStore, ibStore, dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	node, _ := nodeStore.Get("n1")
	// 节点流量 = alice(100+50) + bob(40+20) 的实际值 ×2（转发放大口径）
	if node.UploadBytes != 280 {
		t.Errorf("node upload: want 280, got %d", node.UploadBytes)
	}
	if node.DownloadBytes != 140 {
		t.Errorf("node download: want 140, got %d", node.DownloadBytes)
	}

	// 用户计费流量含倍率 3.0
	alice, _ := userStore.GetUser("u1")
	if alice.UploadBytes != 300 {
		t.Errorf("alice upload: want 300 (100*3), got %d", alice.UploadBytes)
	}
	bob, _ := userStore.GetUser("u2")
	if bob.UploadBytes != 120 {
		t.Errorf("bob upload: want 120 (40*3), got %d", bob.UploadBytes)
	}
}

func TestSyncUsage_UserNotInUsage_NoChange(t *testing.T) {
	// 用户有 inbound 但节点未报告该用户的流量（例如用户从未连接过）
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", InboundID: "ib1", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "s1",
	})

	// 节点返回空用户列表
	dial := testDial(t, func(path string, w http.ResponseWriter, r *http.Request) {
		if path == "/v1/node/runtime/usage" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"available": true, "running": true,
				"users":     []map[string]any{},
			})
		}
	})

	_, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	if alice.UsedBytes != 0 {
		t.Errorf("used: want 0, got %d", alice.UsedBytes)
	}
}

func TestSyncUsage_DeletedUser_NodeTrafficStillCounted(t *testing.T) {
	// 回归测试：节点上报了已删除用户的流量时，该流量仍应计入节点维度统计。
	// 修复前，已删除用户的流量在 reset=true 后被丢弃，导致节点累计值偏低。
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})

	// 只有 alice 在本地系统中，bob 已被删除（但节点上仍有活跃连接）
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", InboundID: "ib1", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "s1",
	})

	// 节点上报 alice + bob（已删除）的流量
	dial := testDial(t, func(path string, w http.ResponseWriter, r *http.Request) {
		if path == "/v1/node/runtime/usage" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"available": true, "running": true,
				"users": []map[string]any{
					{"user": "alice", "upload_total": 100, "download_total": 50},
					{"user": "bob", "upload_total": 40, "download_total": 20},
				},
			})
		}
	})

	_, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	// alice 的用户流量应正常累加
	alice, _ := userStore.GetUser("u1")
	if alice.UploadBytes != 100 {
		t.Errorf("alice upload: want 100, got %d", alice.UploadBytes)
	}

	// 节点流量应包含 alice + bob = 140 upload, 70 download（×2 转发放大 = 280/140）
	node, _ := nodeStore.Get("n1")
	if node.UploadBytes != 280 {
		t.Errorf("node upload: want 280 (alice 100 + bob 40) ×2, got %d", node.UploadBytes)
	}
	if node.DownloadBytes != 140 {
		t.Errorf("node download: want 140 (alice 50 + bob 20) ×2, got %d", node.DownloadBytes)
	}
}

// ─── 辅助 ─────────────────────────────────────────────────────────────────────

// usageDial 返回一个固定上报 upload/download 的 dial 函数。
func usageDial(t *testing.T, username string, upload, download int64) NodeDialer {
	t.Helper()
	return testDial(t, func(path string, w http.ResponseWriter, r *http.Request) {
		switch path {
		case "/v1/node/runtime/usage":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"available": true, "running": true,
				"users": []map[string]any{
					{"user": username, "upload_total": upload, "download_total": download},
				},
			})
		case "/v1/node/runtime/restart":
			_ = json.NewEncoder(w).Encode(map[string]any{"running": true})
		}
	})
}



func testDial(t *testing.T, handler func(path string, w http.ResponseWriter, r *http.Request)) NodeDialer {
	t.Helper()
	return hubDial(pathHub(handler))
}
