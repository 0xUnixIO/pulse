package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"pulse/internal/inbounds"
	"pulse/internal/nodes"
	"pulse/internal/users"
)

// TestSyncUsage_PushPath 验证 SyncUsageWith 优先消费 UsageBuffer 中的 push 帧，
// 不会触发对 nodes.Client.Usage 的按需 hub 拉取。
func TestSyncUsage_PushPath(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "x",
	})

	// dial 返回的 client 只用于 ApplyNode 时的下发；usage 端点故意不实现。
	usageCalls := 0
	dial := testDial(t, func(path string, w http.ResponseWriter, r *http.Request) {
		if path == "/v1/node/runtime/usage" {
			usageCalls++
		}
		if path == "/v1/node/runtime/restart" {
			w.Write([]byte(`{"running":true}`))
		}
	})

	buf := nodes.NewUsageBuffer()
	_ = buf.Append("n1", 1, nodes.UsageStats{
		Available: true, Running: true,
		Users: []nodes.UserUsage{
			{User: "alice", UploadTotal: 70, DownloadTotal: 30},
		},
	})

	result, err := SyncUsageWith(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil, buf)
	if err != nil {
		t.Fatalf("SyncUsageWith: %v", err)
	}
	if result.UsersUpdated != 1 {
		t.Errorf("UsersUpdated=%d, want 1; result=%+v", result.UsersUpdated, result)
	}
	alice, _ := userStore.GetUser("u1")
	if alice.UsedBytes != 100 {
		t.Errorf("alice.UsedBytes=%d, want 100", alice.UsedBytes)
	}
	if usageCalls != 0 {
		t.Errorf("expected no on-demand usage call (push path), got %d", usageCalls)
	}

	// The frame was drained, but this node is now permanently in push mode.
	// Falling back to Usage(reset=true) here can race with the next in-flight
	// push and account the same xray counters twice.
	result, err = SyncUsageWith(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil, buf)
	if err != nil {
		t.Fatalf("second SyncUsageWith: %v", err)
	}
	if result.UsersUpdated != 0 {
		t.Errorf("second UsersUpdated=%d, want 0; result=%+v", result.UsersUpdated, result)
	}
	if usageCalls != 0 {
		t.Errorf("seen push node must not fall back to on-demand usage, got %d calls", usageCalls)
	}
	alice, _ = userStore.GetUser("u1")
	if alice.UsedBytes != 100 {
		t.Errorf("alice.UsedBytes after idle push cycle=%d, want 100", alice.UsedBytes)
	}
}

// TestSyncUsage_OnDemandFallback 验证空 buffer 时仍走按需 hub 拉取（向后兼容）。
func TestSyncUsage_OnDemandFallback(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "x",
	})

	dial := usageDial(t, "alice", 50, 20)
	buf := nodes.NewUsageBuffer() // 空 buffer

	result, err := SyncUsageWith(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil, buf)
	if err != nil {
		t.Fatalf("SyncUsageWith: %v", err)
	}
	if result.UsersUpdated != 1 {
		t.Errorf("UsersUpdated=%d, want 1; errors=%v", result.UsersUpdated, result.Errors)
	}
	alice, _ := userStore.GetUser("u1")
	if alice.UsedBytes != 70 {
		t.Errorf("alice.UsedBytes=%d, want 70", alice.UsedBytes)
	}
}

// TestSyncUsage_MixedPushAndOnDemand 验证多节点：
//   - n1 有 push 数据 → 走 buffer
//   - n2 没有 push → 走按需 hub 拉取
func TestSyncUsage_MixedPushAndOnDemand(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n2", Name: "n2", BaseURL: "http://node.test"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive, TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-n1", UserID: "u1", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "x",
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-n2", UserID: "u1", NodeID: "n2",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "x",
	})

	usageCallsByNode := map[string]int{}
	hub := &fakeHub{handlers: map[string]func(string, any) (json.RawMessage, error){
		"Usage": func(nodeID string, _ any) (json.RawMessage, error) {
			usageCallsByNode[nodeID]++
			return json.RawMessage(`{"available":true,"running":true,"users":[{"user":"alice","upload_total":11,"download_total":22}]}`), nil
		},
		"Restart": func(_ string, _ any) (json.RawMessage, error) {
			return json.RawMessage(`{"running":true}`), nil
		},
	}}
	dial := hubDial(hub)

	buf := nodes.NewUsageBuffer()
	_ = buf.Append("n1", 1, nodes.UsageStats{
		Available: true, Running: true,
		Users: []nodes.UserUsage{{User: "alice", UploadTotal: 100, DownloadTotal: 200}},
	})

	_, err := SyncUsageWith(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil, buf)
	if err != nil {
		t.Fatalf("SyncUsageWith: %v", err)
	}
	if usageCallsByNode["n1"] != 0 {
		t.Errorf("n1 should not be called via on-demand (push path), got %d", usageCallsByNode["n1"])
	}
	if usageCallsByNode["n2"] != 1 {
		t.Errorf("n2 on-demand fallback should be called once, got %d", usageCallsByNode["n2"])
	}
	alice, _ := userStore.GetUser("u1")
	// n1 push delta (100+200) + n2 on-demand fallback (11+22) = 333
	if alice.UsedBytes != 333 {
		t.Errorf("alice.UsedBytes=%d, want 333 (push n1=300 + on-demand n2=33)", alice.UsedBytes)
	}
}

// TestSyncUsage_PushSurvivesDialError 验证 push 数据在 dial 失败时仍入账，且 buffer 已被消费。
func TestSyncUsage_PushSurvivesDialError(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "hub://n1"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive, TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", NodeID: "n1", UUID: "uuid-1",
	})

	buf := nodes.NewUsageBuffer()
	_ = buf.Append("n1", 1, nodes.UsageStats{
		Available: true, Running: true,
		Users: []nodes.UserUsage{{User: "alice", UploadTotal: 70, DownloadTotal: 30}},
	})

	dialFail := func(string) (*nodes.Client, error) {
		return nil, errors.New("node offline")
	}
	res, err := SyncUsageWith(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dialFail, ApplyOptions{}, nil, buf)
	if err != nil {
		t.Fatalf("SyncUsageWith: %v", err)
	}
	if res.UsersUpdated != 1 {
		t.Fatalf("UsersUpdated=%d want 1; errors=%v", res.UsersUpdated, res.Errors)
	}
	alice, _ := userStore.GetUser("u1")
	if alice.UsedBytes != 100 {
		t.Fatalf("alice.UsedBytes=%d want 100 (push must survive dial error)", alice.UsedBytes)
	}
	node, _ := nodeStore.Get("n1")
	if node.UploadBytes != 70 || node.DownloadBytes != 30 {
		t.Fatalf("node traffic=%d/%d want 70/30", node.UploadBytes, node.DownloadBytes)
	}
}

// TestSyncUsage_DisabledNodeKeepsPushBuffer 验证禁用节点不消费 buffer，重新启用后可入账。
func TestSyncUsage_DisabledNodeKeepsPushBuffer(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "hub://n1", Disabled: true})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive, TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", NodeID: "n1", UUID: "uuid-1",
	})

	buf := nodes.NewUsageBuffer()
	_ = buf.Append("n1", 1, nodes.UsageStats{
		Available: true, Running: true,
		Users: []nodes.UserUsage{{User: "alice", UploadTotal: 40, DownloadTotal: 10}},
	})

	dialOK := func(string) (*nodes.Client, error) {
		return nodes.NewClientWithHub("n1", nil), nil
	}
	_, err := SyncUsageWith(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dialOK, ApplyOptions{}, nil, buf)
	if err != nil {
		t.Fatalf("SyncUsageWith(disabled): %v", err)
	}
	alice, _ := userStore.GetUser("u1")
	if alice.UsedBytes != 0 {
		t.Fatalf("disabled node must not apply traffic, used=%d", alice.UsedBytes)
	}

	// 重新启用后应能从保留的 buffer 入账
	n, _ := nodeStore.Get("n1")
	n.Disabled = false
	_, _ = nodeStore.Upsert(n)

	_, err = SyncUsageWith(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dialOK, ApplyOptions{}, nil, buf)
	if err != nil {
		t.Fatalf("SyncUsageWith(enabled): %v", err)
	}
	alice, _ = userStore.GetUser("u1")
	if alice.UsedBytes != 50 {
		t.Fatalf("after re-enable used=%d want 50", alice.UsedBytes)
	}
}

// TestSyncUsage_UnavailableWithBytesStillCounts 有用户字节时即使 Available=false 也记账。
func TestSyncUsage_UnavailableWithBytesStillCounts(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "hub://n1"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive, TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", NodeID: "n1", UUID: "uuid-1",
	})

	buf := nodes.NewUsageBuffer()
	_ = buf.Append("n1", 1, nodes.UsageStats{
		Available: false, Running: false,
		Users: []nodes.UserUsage{{User: "alice", UploadTotal: 12, DownloadTotal: 8}},
	})
	dialOK := func(string) (*nodes.Client, error) {
		return nodes.NewClientWithHub("n1", nil), nil
	}
	_, err := SyncUsageWith(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dialOK, ApplyOptions{}, nil, buf)
	if err != nil {
		t.Fatalf("SyncUsageWith: %v", err)
	}
	alice, _ := userStore.GetUser("u1")
	if alice.UsedBytes != 20 {
		t.Fatalf("used=%d want 20", alice.UsedBytes)
	}
}
