package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"pulse/internal/inbounds"
	"pulse/internal/nodes"
	"pulse/internal/nodes/confighash"
	"pulse/internal/proxycfg"
	"pulse/internal/users"
)

// 构造一组 store，使其内容与一份 xray JSON 完全等价。两侧分别走 server hash
// 与 node hash 路径，结果必须字节一致。
func TestComputeNodeConfigHash_MatchesNodeSide(t *testing.T) {
	userStore := users.NewMemoryStore()
	ibStore := inbounds.NewMemoryStore()

	// inbound：vless 一个，trojan 一个（不同 protocol，确保 token 取值分支被覆盖）。
	// TrafficRate 用生产默认 1.0（postgres 把 <=0 写成 1）。proxycfg 不会把
	// 倍率写进 xray JSON，HashFromXrayJSON 读到的是 0；expected 若带上 DB 倍率，
	// 全网节点会永久漂移、每 10 分钟重下发。
	ibVless, _ := ibStore.UpsertInbound(inbounds.Inbound{
		ID: "ib-v", NodeID: "n1", Protocol: "vless",
		Tag: "vless-1", Port: 443, TrafficRate: 1,
	})
	ibTrojan, _ := ibStore.UpsertInbound(inbounds.Inbound{
		ID: "ib-t", NodeID: "n1", Protocol: "trojan",
		Tag: "trojan-1", Port: 8443, TrafficRate: 2,
	})

	// 两个用户，都 active；user 级 UUID/Secret 优先
	_, _ = userStore.UpsertUser(users.User{
		ID: "ua", Username: "alice", Status: users.StatusActive,
		UUID: "uuid-alice", Secret: "secret-alice",
	})
	_, _ = userStore.UpsertUser(users.User{
		ID: "ub", Username: "bob", Status: users.StatusActive,
		UUID: "uuid-bob", Secret: "secret-bob",
	})

	// alice 同时拥有 vless / trojan；bob 只有 vless
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "a-v", UserID: "ua", InboundID: ibVless.ID, NodeID: "n1",
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "a-t", UserID: "ua", InboundID: ibTrojan.ID, NodeID: "n1",
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "b-v", UserID: "ub", InboundID: ibVless.ID, NodeID: "n1",
	})

	got, err := ComputeNodeConfigHash(context.Background(), "n1", userStore, ibStore, nil)
	if err != nil {
		t.Fatalf("ComputeNodeConfigHash error = %v", err)
	}

	// 模拟 node 侧从 xray 配置 JSON 算 hash 的等价输入：
	// proxycfg 生成的 client.email = "<username>@<tag>"，UUID 走 id；trojan 走 password。
	xray := `{"inbounds":[
		{"tag":"vless-1","settings":{"clients":[
			{"id":"uuid-alice","email":"alice@vless-1"},
			{"id":"uuid-bob","email":"bob@vless-1"}
		]}},
		{"tag":"trojan-1","settings":{"clients":[
			{"password":"secret-alice","email":"alice@trojan-1"}
		]}}
	]}`
	want := confighash.HashFromXrayJSON(xray)

	if got != want {
		t.Fatalf("server vs node hash mismatch:\n  server = %s\n  node   = %s", got, want)
	}
	if len(got) != 64 {
		t.Fatalf("expected 64-hex sha256, got %q", got)
	}
}

func TestComputeNodeConfigHash_MatchesShadowsocksPSK(t *testing.T) {
	userStore := users.NewMemoryStore()
	ibStore := inbounds.NewMemoryStore()

	// 与 BuildXrayConfig 一致：method 非 2022-* 时默认 2022-blake3-aes-128-gcm，
	// client.password 是 SSUserPassword 派生 PSK，不是原始 Secret。
	ib, _ := ibStore.UpsertInbound(inbounds.Inbound{
		ID: "ib-ss", NodeID: "n1", Protocol: "shadowsocks",
		Tag: "shadowsocks-23941", Port: 23941, Method: "",
		TrafficRate: 1,
	})
	_, _ = userStore.UpsertUser(users.User{
		ID: "ua", Username: "alice", Status: users.StatusActive,
		UUID: "uuid-alice", Secret: "secret-alice",
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "a-ss", UserID: "ua", InboundID: ib.ID, NodeID: "n1",
	})

	got, err := ComputeNodeConfigHash(context.Background(), "n1", userStore, ibStore, nil)
	if err != nil {
		t.Fatalf("ComputeNodeConfigHash error = %v", err)
	}

	psk := proxycfg.SSUserPassword("secret-alice", "2022-blake3-aes-128-gcm")
	if psk == "" || psk == "secret-alice" {
		t.Fatalf("expected derived SS2022 PSK, got %q", psk)
	}
	xray := `{"inbounds":[{"tag":"shadowsocks-23941","settings":{"clients":[
		{"password":"` + psk + `","email":"alice@shadowsocks-23941"}
	]}}]}`
	want := confighash.HashFromXrayJSON(xray)
	if got != want {
		t.Fatalf("server vs node hash mismatch:\n  server = %s\n  node   = %s", got, want)
	}

	raw := confighash.HashFromXrayJSON(`{"inbounds":[{"tag":"shadowsocks-23941","settings":{"clients":[
		{"password":"secret-alice","email":"alice@shadowsocks-23941"}
	]}}]}`)
	if raw == want {
		t.Fatal("derived PSK and raw secret should produce different hashes")
	}
}

func TestComputeNodeConfigHash_SkipsInactiveUsers(t *testing.T) {
	userStore := users.NewMemoryStore()
	ibStore := inbounds.NewMemoryStore()

	ib, _ := ibStore.UpsertInbound(inbounds.Inbound{
		ID: "ib-v", NodeID: "n1", Protocol: "vless", Tag: "vless-1", Port: 443,
	})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u-active", Username: "a", Status: users.StatusActive, UUID: "ua",
	})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u-disabled", Username: "d", Status: users.StatusDisabled, UUID: "ud",
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "ai", UserID: "u-active", InboundID: ib.ID, NodeID: "n1",
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "di", UserID: "u-disabled", InboundID: ib.ID, NodeID: "n1",
	})

	hWith, _ := ComputeNodeConfigHash(context.Background(), "n1", userStore, ibStore, nil)

	// 删除 disabled 用户的 access，结果应不变（因为 disabled 本来就不计入）
	_ = userStore.DeleteUserInbound("di")
	hWithout, _ := ComputeNodeConfigHash(context.Background(), "n1", userStore, ibStore, nil)
	if hWith != hWithout {
		t.Fatalf("disabled user should not affect hash; %s != %s", hWith, hWithout)
	}
}

// ── SelfSyncHandler tests ────────────────────────────────────────────────

// recordHubCaller 用于异步等待 ApplyNode 调用 dial（即使最终因为没有 NodeStore 失败）。
type recordHubCaller struct {
	called chan struct{}
}

func (r *recordHubCaller) Call(ctx context.Context, nodeID, method string, body any) (json.RawMessage, error) {
	select {
	case r.called <- struct{}{}:
	default:
	}
	return nil, nil
}

func TestSelfSyncHandler_HashMatch_NoApply(t *testing.T) {
	userStore := users.NewMemoryStore()
	ibStore := inbounds.NewMemoryStore()
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{
		ID: "ib", NodeID: "n1", Protocol: "vless", Tag: "v", Port: 443,
	})

	expected, _ := ComputeNodeConfigHash(context.Background(), "n1", userStore, ibStore, nil)

	hc := &recordHubCaller{called: make(chan struct{}, 1)}
	h := &SelfSyncHandler{
		UserStore:    userStore,
		InboundStore: ibStore,
		HubCaller:    hc,
	}

	body, _ := json.Marshal(map[string]string{
		"node_id":     "n1",
		"config_hash": expected,
		"version":     "test",
	})
	h.OnHello("n1", body)

	if h.MismatchCount() != 0 {
		t.Fatalf("expected no mismatch, got %d", h.MismatchCount())
	}
	// hash 匹配时 OnHello 同步返回，不会启动 apply goroutine。
	if h.ApplyOKCount()+h.ApplyErrCount() != 0 {
		t.Fatalf("apply should not run on hash match; ok=%d err=%d",
			h.ApplyOKCount(), h.ApplyErrCount())
	}
	select {
	case <-hc.called:
		t.Fatal("HubCaller.Call should not be invoked when hash matches")
	default:
	}
}

func TestSelfSyncHandler_HashMismatch_TriggersApply(t *testing.T) {
	userStore := users.NewMemoryStore()
	ibStore := inbounds.NewMemoryStore()
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{
		ID: "ib", NodeID: "n1", Protocol: "vless", Tag: "v", Port: 443,
	})

	hc := &recordHubCaller{called: make(chan struct{}, 4)}
	nodeStore := nodes.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	h := &SelfSyncHandler{
		UserStore:    userStore,
		InboundStore: ibStore,
		NodeStore:    nodeStore,
		HubCaller:    hc,
	}

	body, _ := json.Marshal(map[string]string{
		"node_id":     "n1",
		"config_hash": "stale-hash",
		"version":     "test",
	})
	h.OnHello("n1", body)

	if got := h.MismatchCount(); got != 1 {
		t.Fatalf("expected 1 mismatch, got %d", got)
	}

	// 等异步 apply 完成（HubCaller 注入但 nodes.NewHubClient 工厂未注入 → ApplyNode
	// 路径走 hubDialer 返回错误，应计入 ApplyErrCount）。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.ApplyOKCount()+h.ApplyErrCount() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h.ApplyOKCount()+h.ApplyErrCount() != 1 {
		t.Fatalf("expected 1 apply attempt within 2s; ok=%d err=%d",
			h.ApplyOKCount(), h.ApplyErrCount())
	}
}

func TestSelfSyncHandler_BadHelloBody_NoPanic(t *testing.T) {
	h := &SelfSyncHandler{
		UserStore:    users.NewMemoryStore(),
		InboundStore: inbounds.NewMemoryStore(),
	}
	// 不应 panic
	h.OnHello("n1", []byte("not json"))
	if h.MismatchCount() != 0 {
		t.Fatalf("bad body should be ignored, got mismatch=%d", h.MismatchCount())
	}
}
