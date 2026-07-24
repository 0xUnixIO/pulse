package jobs

import (
	"context"
	"log"
	"sync"
	"time"

	"pulse/internal/inbounds"
	"pulse/internal/nodes"
	"pulse/internal/nodes/confighash"
	"pulse/internal/outbounds"
	"pulse/internal/users"
)

// ReconcileResult 记录一次配置对账的结果摘要。
type ReconcileResult struct {
	NodesChecked  int      `json:"nodes_checked"`
	NodesMismatch int      `json:"nodes_mismatch"`
	NodesApplied  int      `json:"nodes_applied"`
	NodesCooling  int      `json:"nodes_cooling"`
	Errors        []string `json:"errors"`
}

// ReconcileCooldown 是同一节点两次对账下发之间的最小间隔。
//
// 兜底护栏：若 server 与 node 两侧 hash 算法出现系统性偏差（例如某个字段只有
// 一侧写入），没有冷却就会演变成「每轮对账重启全网节点」。有冷却时最坏情况是
// 每 10 分钟一次重启 + 持续的 mismatch 日志，足以被发现且不至于打爆线上。
const ReconcileCooldown = 10 * time.Minute

var (
	reconcileMu     sync.Mutex
	reconcileLastAt = make(map[string]time.Time)
)

// resetReconcileState 清空冷却记录。仅供测试使用。
func resetReconcileState() {
	reconcileMu.Lock()
	reconcileLastAt = make(map[string]time.Time)
	reconcileMu.Unlock()
}

// allowReconcileApply 判断某节点当前是否已过冷却窗口；返回 true 时记录本次下发时间。
func allowReconcileApply(nodeID string, now time.Time) bool {
	reconcileMu.Lock()
	defer reconcileMu.Unlock()
	if last, ok := reconcileLastAt[nodeID]; ok && now.Sub(last) < ReconcileCooldown {
		return false
	}
	reconcileLastAt[nodeID] = now
	return true
}

// ReconcileNodeConfigs 周期性对账节点实际运行配置与 server 端期望配置。
//
// 存在的理由：SyncUsageWith 只在用户状态翻转的那一轮下发变更，若目标节点当轮
// dial 失败会被跳过，而下一轮 statusChanged 恒为 false、changedUsers 为空，
// 该节点将永远收不到这次变更。self-sync（internal/jobs/selfsync.go）只在节点
// 重连的 hello 帧上对账，节点长期在线时同样不会修正。结果是超限用户在该节点上
// 无限可用——线上表现为 100 GB 额度跑到 128 GB。
//
// 实现上直接复用节点已有的 Config RPC 拉取运行态 xray 配置，用与 node 侧一致的
// confighash 算法计算实际 hash，因此无需节点端同步发版。
//
// 空配置（xray 尚未启动 / 无运行配置）跳过：这种状态由 SyncUsage 的 recover
// 路径负责恢复，两个 job 同时介入会重复重启同一节点。
func ReconcileNodeConfigs(
	ctx context.Context,
	nodeStore nodes.Store,
	userStore users.Store,
	ibStore inbounds.InboundStore,
	outboundStore outbounds.Store,
	dial NodeDialer,
	applyOpts ApplyOptions,
) (ReconcileResult, error) {
	allNodes, err := nodeStore.List()
	if err != nil {
		return ReconcileResult{}, err
	}

	result := ReconcileResult{Errors: make([]string, 0)}

	// ── 阶段 1：并发拉取各节点实际配置（网络 IO，不持锁） ──────────────────
	type nodeCheck struct {
		node   nodes.Node
		actual string // 实际配置 hash；空表示无可比对配置
		skip   bool
		errMsg string
	}
	var candidates []nodes.Node
	for _, n := range allNodes {
		if !n.Disabled {
			candidates = append(candidates, n)
		}
	}

	checks := make([]nodeCheck, len(candidates))
	var wg sync.WaitGroup
	for i, n := range candidates {
		wg.Add(1)
		go func(idx int, node nodes.Node) {
			defer wg.Done()
			c, err := dial(node.ID)
			if err != nil {
				checks[idx] = nodeCheck{node: node, skip: true, errMsg: node.ID + ": dial: " + err.Error()}
				return
			}
			cfg, err := c.Config(ctx)
			if err != nil {
				checks[idx] = nodeCheck{node: node, skip: true, errMsg: node.ID + ": config: " + err.Error()}
				return
			}
			if cfg.Config == "" {
				// xray 未运行 / 无配置，交给 SyncUsage 的 recover 路径。
				checks[idx] = nodeCheck{node: node, skip: true}
				return
			}
			checks[idx] = nodeCheck{node: node, actual: confighash.HashFromXrayJSON(cfg.Config)}
		}(i, n)
	}
	wg.Wait()

	// ── 阶段 2：比对 + 按需下发 ────────────────────────────────────────────
	now := time.Now().UTC()
	for _, chk := range checks {
		if ctx.Err() != nil {
			break
		}
		if chk.errMsg != "" {
			result.Errors = append(result.Errors, chk.errMsg)
		}
		if chk.skip {
			continue
		}
		result.NodesChecked++

		mu.Lock()
		expected, err := ComputeNodeConfigHash(ctx, chk.node.ID, userStore, ibStore, outboundStore)
		mu.Unlock()
		if err != nil {
			result.Errors = append(result.Errors, chk.node.ID+": expected hash: "+err.Error())
			continue
		}
		if expected == chk.actual {
			continue
		}

		result.NodesMismatch++
		if !allowReconcileApply(chk.node.ID, now) {
			result.NodesCooling++
			log.Printf("reconcile: 节点 %s 配置漂移（expected=%s actual=%s），冷却窗口内跳过下发",
				chk.node.ID, short(expected), short(chk.actual))
			continue
		}

		log.Printf("reconcile: 节点 %s 配置漂移（expected=%s actual=%s），重新下发配置",
			chk.node.ID, short(expected), short(chk.actual))
		if err := ApplyNode(ctx, chk.node.ID, nodeStore, userStore, ibStore, outboundStore, dial, applyOpts); err != nil {
			result.Errors = append(result.Errors, chk.node.ID+": apply: "+err.Error())
			continue
		}
		result.NodesApplied++
	}

	return result, nil
}
