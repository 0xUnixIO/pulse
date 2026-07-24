package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"pulse/internal/inbounds"
	"pulse/internal/nodes"
	"pulse/internal/users"
)

// callRecorder 记录节点侧收到的 RPC 路径调用次数。
type callRecorder struct {
	mu    sync.Mutex
	calls map[string]int
}

func newCallRecorder() *callRecorder {
	return &callRecorder{calls: make(map[string]int)}
}

func (c *callRecorder) record(path string) {
	c.mu.Lock()
	c.calls[path]++
	c.mu.Unlock()
}

func (c *callRecorder) count(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[path]
}

// limitFixture 构造「单节点 + 单 vless inbound + 单用户」的最小场景。
func limitFixture(t *testing.T, limit int64) (nodes.Store, users.Store, inbounds.InboundStore) {
	t.Helper()
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	ibStore := inbounds.NewMemoryStore()

	if _, err := nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"}); err != nil {
		t.Fatalf("upsert node: %v", err)
	}
	if _, err := ibStore.UpsertInbound(inbounds.Inbound{
		ID: "ib1", NodeID: "n1", Protocol: "vless", Tag: "vless-in",
		Port: 443, TrafficRate: 1,
	}); err != nil {
		t.Fatalf("upsert inbound: %v", err)
	}
	if _, err := userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "s3cret",
		TrafficLimit: limit,
	}); err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	if _, err := userStore.UpsertUserInbound(users.UserInbound{
		ID: "acc1", UserID: "u1", NodeID: "n1", InboundID: "ib1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "s3cret",
	}); err != nil {
		t.Fatalf("upsert user inbound: %v", err)
	}
	return nodeStore, userStore, ibStore
}

// TestSyncUsage_LimitedUserIsKicked 复现「超限用户只被热删、存量连接不断」。
//
// Xray 的鉴权只在建链时做一次，RemoveUser 仅把用户从 inbound validator 摘掉：
// 已建立的连接不会被回查，会继续传输且仍被 stats 计费，用户可以在超限后跑出
// 远超额度的流量（线上出现过 100 GB 额度跑到 128 GB）。
// 因此超限处置必须是 RemoveUser（拦新连接）+ KickUser（断存量连接）两步。
func TestSyncUsage_LimitedUserIsKicked(t *testing.T) {
	nodeStore, userStore, ibStore := limitFixture(t, 100)

	rec := newCallRecorder()
	dial := testDial(t, func(path string, w http.ResponseWriter, _ *http.Request) {
		rec.record(path)
		switch path {
		case "/v1/node/runtime/restart", "/v1/node/runtime/start":
			_ = json.NewEncoder(w).Encode(map[string]any{"running": true})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}
	})

	buf := nodes.NewUsageBuffer()
	if err := buf.Append("n1", 1, nodes.UsageStats{
		Available: true, Running: true,
		Users: []nodes.UserUsage{
			{User: "alice@vless-in", UploadTotal: 60, DownloadTotal: 60},
		},
	}); err != nil {
		t.Fatalf("append usage: %v", err)
	}

	if _, err := SyncUsageWith(context.Background(), userStore, nodeStore, ibStore,
		dial, ApplyOptions{}, nil, buf); err != nil {
		t.Fatalf("SyncUsageWith: %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	if alice.EffectiveStatus() != users.StatusLimited {
		t.Fatalf("alice status=%s used=%d, want limited", alice.EffectiveStatus(), alice.UsedBytes)
	}
	if got := rec.count("/v1/node/runtime/users/remove"); got != 1 {
		t.Errorf("RemoveUser=%d, want 1（应热删以拦截新连接）", got)
	}
	if got := rec.count("/v1/node/runtime/users/kick"); got != 1 {
		t.Errorf("KickUser=%d, want 1（未踢掉存量连接，超限流量不会停）", got)
	}
	if got := rec.count("/v1/node/runtime/restart"); got != 0 {
		t.Errorf("restart=%d, want 0（精确断连不应重启节点误伤他人）", got)
	}
}

// TestSyncUsage_LimitedKickScopedToAffectedNode 保证超限处置的影响面收敛到确实
// 承载该用户的节点：无关节点既不该被重启，也不该收到踢人指令。
func TestSyncUsage_LimitedKickScopedToAffectedNode(t *testing.T) {
	nodeStore, userStore, ibStore := limitFixture(t, 100)

	// 追加一个只承载 bob 的节点 n2，本轮 bob 状态不变。
	if _, err := nodeStore.Upsert(nodes.Node{ID: "n2", Name: "n2", BaseURL: "http://node2.test"}); err != nil {
		t.Fatalf("upsert node n2: %v", err)
	}
	if _, err := ibStore.UpsertInbound(inbounds.Inbound{
		ID: "ib2", NodeID: "n2", Protocol: "vless", Tag: "vless-in2",
		Port: 443, TrafficRate: 1,
	}); err != nil {
		t.Fatalf("upsert inbound ib2: %v", err)
	}
	if _, err := userStore.UpsertUser(users.User{
		ID: "u2", Username: "bob", Status: users.StatusActive,
		UUID: "22222222-2222-2222-2222-222222222222", Secret: "s3cret2",
		TrafficLimit: 1 << 40,
	}); err != nil {
		t.Fatalf("upsert bob: %v", err)
	}
	if _, err := userStore.UpsertUserInbound(users.UserInbound{
		ID: "acc2", UserID: "u2", NodeID: "n2", InboundID: "ib2",
		UUID: "22222222-2222-2222-2222-222222222222", Secret: "s3cret2",
	}); err != nil {
		t.Fatalf("upsert bob inbound: %v", err)
	}

	var mu sync.Mutex
	restartsByNode := make(map[string]int)
	kicksByNode := make(map[string]int)
	dial := func(nodeID string) (*nodes.Client, error) {
		hub := pathHub(func(path string, w http.ResponseWriter, _ *http.Request) {
			switch path {
			case "/v1/node/runtime/restart", "/v1/node/runtime/start":
				mu.Lock()
				restartsByNode[nodeID]++
				mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{"running": true})
			case "/v1/node/runtime/users/kick":
				mu.Lock()
				kicksByNode[nodeID]++
				mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{"kicked": 1})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			}
		})
		return nodes.NewClientWithHub(nodeID, hub), nil
	}

	buf := nodes.NewUsageBuffer()
	if err := buf.Append("n1", 1, nodes.UsageStats{
		Available: true, Running: true,
		Users: []nodes.UserUsage{
			{User: "alice@vless-in", UploadTotal: 60, DownloadTotal: 60},
		},
	}); err != nil {
		t.Fatalf("append usage n1: %v", err)
	}
	if err := buf.Append("n2", 1, nodes.UsageStats{
		Available: true, Running: true,
		Users: []nodes.UserUsage{
			{User: "bob@vless-in2", UploadTotal: 10, DownloadTotal: 10},
		},
	}); err != nil {
		t.Fatalf("append usage n2: %v", err)
	}

	if _, err := SyncUsageWith(context.Background(), userStore, nodeStore, ibStore,
		dial, ApplyOptions{}, nil, buf); err != nil {
		t.Fatalf("SyncUsageWith: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if kicksByNode["n1"] == 0 {
		t.Errorf("n1 承载超限用户 alice，应踢掉其存量连接，实际 kick=0")
	}
	if kicksByNode["n2"] != 0 {
		t.Errorf("n2 不承载超限用户，不应收到踢人指令，实际 kick=%d", kicksByNode["n2"])
	}
	if restartsByNode["n2"] != 0 {
		t.Errorf("n2 不应被断流，实际 restart=%d", restartsByNode["n2"])
	}
}
