package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"pulse/internal/nodes"
	"pulse/internal/users"
)

// xrayCfgWithClients 构造一份最小 xray 配置 JSON，字段与
// confighash.HashFromXrayJSON 的解析口径一致。
func xrayCfgWithClients(tag string, clients ...map[string]any) string {
	if clients == nil {
		clients = []map[string]any{}
	}
	cfg := map[string]any{
		"inbounds": []map[string]any{{
			"tag":         tag,
			"trafficRate": 1,
			"settings":    map[string]any{"clients": clients},
		}},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

// reconcileDial 返回一个 dialer：Config 回放给定配置，重启计数写入 restarts。
func reconcileDial(cfgJSON string, restarts *int, mu *sync.Mutex) NodeDialer {
	return func(nodeID string) (*nodes.Client, error) {
		hub := pathHub(func(path string, w http.ResponseWriter, _ *http.Request) {
			switch path {
			case "/v1/node/runtime/config":
				_ = json.NewEncoder(w).Encode(map[string]any{"config": cfgJSON})
			case "/v1/node/runtime/restart", "/v1/node/runtime/start":
				mu.Lock()
				*restarts++
				mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{"running": true})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			}
		})
		return nodes.NewClientWithHub(nodeID, hub), nil
	}
}

// TestReconcileNodeConfigs_StaleLimitedUserReapplied 复现「移除指令永久丢失」。
//
// SyncUsageWith 只在用户状态翻转的那一轮下发移除；若某节点当轮 dial 失败会被
// 跳过，下一轮 statusChanged 恒为 false、changedUsers 为空，该节点再也收不到
// 移除指令。self-sync 只在节点重连（hello）时对账，节点长期在线就永远不修正，
// 超限用户可以在该节点上无限跑流量。对账 job 必须能独立发现并修复这种漂移。
func TestReconcileNodeConfigs_StaleLimitedUserReapplied(t *testing.T) {
	resetReconcileState()
	nodeStore, userStore, ibStore := limitFixture(t, 100)

	// server 侧：alice 已超限，期望配置里不应再有它。
	alice, _ := userStore.GetUser("u1")
	alice.UploadBytes = 128
	alice.UsedBytes = 128
	if _, err := userStore.UpsertUser(alice); err != nil {
		t.Fatalf("seed limited: %v", err)
	}
	if got, _ := userStore.GetUser("u1"); got.EffectiveStatus() != users.StatusLimited {
		t.Fatalf("seed: status=%s, want limited", got.EffectiveStatus())
	}

	// 节点侧：实际运行配置里 alice 还在（移除指令丢失）。
	staleCfg := xrayCfgWithClients("vless-in", map[string]any{
		"email": "alice@vless-in",
		"id":    "11111111-1111-1111-1111-111111111111",
	})

	var mu sync.Mutex
	restarts := 0
	dial := reconcileDial(staleCfg, &restarts, &mu)

	res, err := ReconcileNodeConfigs(context.Background(), nodeStore, userStore, ibStore, nil, dial, ApplyOptions{})
	if err != nil {
		t.Fatalf("ReconcileNodeConfigs: %v", err)
	}
	if res.NodesMismatch != 1 {
		t.Errorf("NodesMismatch=%d, want 1；对账未发现节点上残留的超限用户", res.NodesMismatch)
	}
	mu.Lock()
	defer mu.Unlock()
	if restarts == 0 {
		t.Errorf("配置漂移未触发重下发：restart=0")
	}
}

// TestReconcileNodeConfigs_ExpiredUserReapplied 覆盖到期用户。
//
// 到期与超限不同：SyncUsageWith 里 prevEnabled 与变更后状态用的是同一个 now，
// 只有 UsedBytes 变化能让状态翻转，因此「用户到期」在流量同步路径上永远不会
// 触发 statusChanged，节点上的到期用户此前只能靠节点重连才被清理。
func TestReconcileNodeConfigs_ExpiredUserReapplied(t *testing.T) {
	resetReconcileState()
	nodeStore, userStore, ibStore := limitFixture(t, 1<<40)

	expired := time.Now().UTC().Add(-time.Hour)
	alice, _ := userStore.GetUser("u1")
	alice.ExpireAt = &expired
	if _, err := userStore.UpsertUser(alice); err != nil {
		t.Fatalf("seed expired: %v", err)
	}
	if got, _ := userStore.GetUser("u1"); got.EffectiveStatus() != users.StatusExpired {
		t.Fatalf("seed: status=%s, want expired", got.EffectiveStatus())
	}

	staleCfg := xrayCfgWithClients("vless-in", map[string]any{
		"email": "alice@vless-in",
		"id":    "11111111-1111-1111-1111-111111111111",
	})

	var mu sync.Mutex
	restarts := 0
	dial := reconcileDial(staleCfg, &restarts, &mu)

	res, err := ReconcileNodeConfigs(context.Background(), nodeStore, userStore, ibStore, nil, dial, ApplyOptions{})
	if err != nil {
		t.Fatalf("ReconcileNodeConfigs: %v", err)
	}
	if res.NodesMismatch != 1 {
		t.Errorf("NodesMismatch=%d, want 1；对账未发现节点上残留的到期用户", res.NodesMismatch)
	}
	mu.Lock()
	defer mu.Unlock()
	if restarts == 0 {
		t.Errorf("到期用户残留未触发重下发：restart=0")
	}
}

// TestReconcileNodeConfigs_InSyncNoApply 保证 hash 一致时对账不动节点——
// 否则这个 job 会变成周期性全网断流。
func TestReconcileNodeConfigs_InSyncNoApply(t *testing.T) {
	resetReconcileState()
	nodeStore, userStore, ibStore := limitFixture(t, 1<<40)

	// server 侧 alice 正常启用，节点配置与之一致。
	inSyncCfg := xrayCfgWithClients("vless-in", map[string]any{
		"email": "alice@vless-in",
		"id":    "11111111-1111-1111-1111-111111111111",
	})

	var mu sync.Mutex
	restarts := 0
	dial := reconcileDial(inSyncCfg, &restarts, &mu)

	res, err := ReconcileNodeConfigs(context.Background(), nodeStore, userStore, ibStore, nil, dial, ApplyOptions{})
	if err != nil {
		t.Fatalf("ReconcileNodeConfigs: %v", err)
	}
	if res.NodesChecked != 1 {
		t.Errorf("NodesChecked=%d, want 1", res.NodesChecked)
	}
	if res.NodesMismatch != 0 {
		t.Errorf("NodesMismatch=%d, want 0（hash 一致不应判定漂移）", res.NodesMismatch)
	}
	mu.Lock()
	defer mu.Unlock()
	if restarts != 0 {
		t.Errorf("hash 一致却重启了节点：restart=%d", restarts)
	}
}

// TestReconcileNodeConfigs_Cooldown 保证同一节点在冷却窗口内不会被反复重启：
// 若两侧 hash 算法出现系统性偏差，没有冷却就会演变成周期性全网断流。
func TestReconcileNodeConfigs_Cooldown(t *testing.T) {
	resetReconcileState()
	nodeStore, userStore, ibStore := limitFixture(t, 100)

	alice, _ := userStore.GetUser("u1")
	alice.UploadBytes = 128
	alice.UsedBytes = 128
	if _, err := userStore.UpsertUser(alice); err != nil {
		t.Fatalf("seed limited: %v", err)
	}

	staleCfg := xrayCfgWithClients("vless-in", map[string]any{
		"email": "alice@vless-in",
		"id":    "11111111-1111-1111-1111-111111111111",
	})

	var mu sync.Mutex
	restarts := 0
	dial := reconcileDial(staleCfg, &restarts, &mu)

	for i := 0; i < 3; i++ {
		if _, err := ReconcileNodeConfigs(context.Background(), nodeStore, userStore, ibStore, nil, dial, ApplyOptions{}); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if restarts != 1 {
		t.Errorf("restart=%d, want 1（冷却窗口内只应下发一次）", restarts)
	}
}

// TestReconcileNodeConfigs_SkipsEmptyConfig 保证节点尚无运行配置（xray 未启动）时
// 对账不介入，交给 SyncUsage 的恢复路径，避免两个 job 同时重启同一节点。
func TestReconcileNodeConfigs_SkipsEmptyConfig(t *testing.T) {
	resetReconcileState()
	nodeStore, userStore, ibStore := limitFixture(t, 1<<40)

	var mu sync.Mutex
	restarts := 0
	dial := reconcileDial("", &restarts, &mu)

	res, err := ReconcileNodeConfigs(context.Background(), nodeStore, userStore, ibStore, nil, dial, ApplyOptions{})
	if err != nil {
		t.Fatalf("ReconcileNodeConfigs: %v", err)
	}
	if res.NodesMismatch != 0 {
		t.Errorf("NodesMismatch=%d, want 0（空配置应跳过）", res.NodesMismatch)
	}
	mu.Lock()
	defer mu.Unlock()
	if restarts != 0 {
		t.Errorf("空配置不应触发重下发：restart=%d", restarts)
	}
}

// TestReconcileNodeConfigs_SkipsDisabledNode 保证禁用节点不参与对账。
func TestReconcileNodeConfigs_SkipsDisabledNode(t *testing.T) {
	resetReconcileState()
	nodeStore, userStore, ibStore := limitFixture(t, 100)

	n, _ := nodeStore.Get("n1")
	n.Disabled = true
	if _, err := nodeStore.Upsert(n); err != nil {
		t.Fatalf("disable node: %v", err)
	}

	var mu sync.Mutex
	restarts := 0
	dial := reconcileDial(xrayCfgWithClients("vless-in"), &restarts, &mu)

	res, err := ReconcileNodeConfigs(context.Background(), nodeStore, userStore, ibStore, nil, dial, ApplyOptions{})
	if err != nil {
		t.Fatalf("ReconcileNodeConfigs: %v", err)
	}
	if res.NodesChecked != 0 {
		t.Errorf("NodesChecked=%d, want 0（禁用节点应跳过）", res.NodesChecked)
	}
	mu.Lock()
	defer mu.Unlock()
	if restarts != 0 {
		t.Errorf("禁用节点被下发：restart=%d", restarts)
	}
}
