package jobs

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"pulse/internal/inbounds"
	"pulse/internal/nodes"
	"pulse/internal/outbounds"
	"pulse/internal/proxycfg"
	"pulse/internal/routerules"
	"pulse/internal/users"
)

// NodeCertStoragePath 是 pulse-node 上 certmgr 默认的证书落盘根目录。
// 必须与 sniproxy 同步逻辑、节点端 certmgr.Config.StoragePath 三处保持一致。
const NodeCertStoragePath = "/var/lib/pulse-node/certs"

// nodeCertPath 按 certmagic 约定推导节点上指定域名的 cert / key 路径。
// panel 端无法 stat 节点磁盘，只生成预测路径；运行时 xray 加载时若文件不存在会自行报错。
func nodeCertPath(domain string) (certFile, keyFile string, err error) {
	if domain == "" {
		return "", "", fmt.Errorf("hy2 cert path: domain is required")
	}
	if !isValidDomain(domain) {
		return "", "", fmt.Errorf("hy2 cert path: invalid domain %q", domain)
	}
	dir := filepath.Join(NodeCertStoragePath, "certificates",
		"acme-v02.api.letsencrypt.org-directory", domain)
	return filepath.Join(dir, domain+".crt"), filepath.Join(dir, domain+".key"), nil
}

// isValidDomain 校验域名只含合法字符（字母/数字/点/连字符），防止路径穿越。
func isValidDomain(d string) bool {
	if d == "" || len(d) > 253 || strings.HasPrefix(d, ".") || strings.HasPrefix(d, "-") {
		return false
	}
	for _, r := range d {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '-') {
			return false
		}
	}
	return !strings.Contains(d, "..")
}

// collectHy2SNIs 提取节点 inbound 列表中所有 hy2 inbound 的 SNI（去重）。
func collectHy2SNIs(ibs []inbounds.Inbound) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, ib := range ibs {
		if ib.Protocol != "hy2" {
			continue
		}
		sni := parseHy2SNI(ib.Extra)
		if sni == "" {
			continue
		}
		if _, dup := seen[sni]; dup {
			continue
		}
		seen[sni] = struct{}{}
		out = append(out, sni)
	}
	return out
}

// mu 保护三个 job 之间的数据一致性。
// 所有 job 均使用 UpsertUser 全字段覆盖写，因此读-改-写必须互斥。
// 锁只包住 DB 读写段，网络 IO（节点流量拉取、配置下发）在锁外执行。
var mu sync.Mutex

// nodeFailCount 记录每个节点的连续失败次数，用于告警防抖。
// 连续失败 alertThreshold 次才推送通知，避免单次瞬断误报。
var (
	nodeFailMu    sync.Mutex
	nodeFailCount = make(map[string]int)
	// serverStartTime 记录进程启动时间，用于启动宽限期内抑制误报。
	serverStartTime = time.Now()
)

const (
	alertThreshold = 3
	// alertStartupGrace 启动后此时间内不触发离线告警，避免冷启动时节点短暂不可达产生误报。
	alertStartupGrace = 3 * time.Minute
)

// NodeDialer 根据节点 ID 返回 RPC 客户端。
type NodeDialer func(nodeID string) (*nodes.Client, error)

// ─── SyncUsage ────────────────────────────────────────────────────────────────

// SyncUsageResult 记录一次同步的结果摘要。
type SyncUsageResult struct {
	NodesSynced   int      `json:"nodes_synced"`
	UsersUpdated  int      `json:"users_updated"`
	NodesReloaded int      `json:"nodes_reloaded"`
	NodesStopped  int      `json:"nodes_stopped"`
	Errors        []string `json:"errors"`
}

// SyncUsage 从各节点拉取流量统计，更新用户字节数，
// 若某节点上的用户启用状态发生变化则重新下发配置。
//
// 执行分三阶段，最小化锁持有时间：
//  1. 并发拉取各节点流量（网络 IO，不持锁）
//  2. 批量更新 DB（持锁，纯 DB 操作，快速）
//  3. 对状态变化的节点重新下发配置（网络 IO，不持锁）
//
// 兼容老调用方：未传 buffer 时纯走按需 hub 拉取。
func SyncUsage(ctx context.Context, store users.Store, nodeStore nodes.Store, ibStore inbounds.InboundStore, dial NodeDialer, applyOpts ApplyOptions, outboundStore outbounds.Store) (SyncUsageResult, error) {
	return SyncUsageWith(ctx, store, nodeStore, ibStore, dial, applyOpts, outboundStore, nil)
}

// SyncUsageWith 与 SyncUsage 相同，但额外接受 *nodes.UsageBuffer：
// 优先消费来自 node 主动 push 的 usage delta；只有 buffer 中没有该节点数据时
// 才回退到 c.Usage(ctx, true) 的按需 hub 拉取（兼容尚未 push 过的节点和测试）。
func SyncUsageWith(ctx context.Context, store users.Store, nodeStore nodes.Store, ibStore inbounds.InboundStore, dial NodeDialer, applyOpts ApplyOptions, outboundStore outbounds.Store, usageBuf *nodes.UsageBuffer) (SyncUsageResult, error) {
	allNodes, err := nodeStore.List()
	if err != nil {
		return SyncUsageResult{}, err
	}
	// 过滤掉已禁用的节点，禁用节点不拉取流量也不下发配置
	nodesList := make([]nodes.Node, 0, len(allNodes))
	for _, n := range allNodes {
		if !n.Disabled {
			nodesList = append(nodesList, n)
		}
	}

	// 优先 drain push buffer：所有有 push 数据的节点直接用 buffer 中的累计 delta。
	var drained map[string]nodes.UsageStats
	if usageBuf != nil {
		drained = usageBuf.DrainAll()
	}

	result := SyncUsageResult{Errors: make([]string, 0)}
	now := time.Now().UTC()

	// ── 阶段 1：并发拉取各节点流量（网络 IO，不持锁） ────────────────────────
	type nodeFetch struct {
		node     nodes.Node
		client   *nodes.Client
		usage    nodes.UsageStats
		dialErr  error
		usageErr error
	}
	fetched := make([]nodeFetch, len(nodesList))
	var wg sync.WaitGroup
	for i, node := range nodesList {
		wg.Add(1)
		go func(idx int, n nodes.Node) {
			defer wg.Done()
			c, err := dial(n.ID)
			if err != nil {
				fetched[idx] = nodeFetch{node: n, dialErr: err}
				return
			}
			// 优先走 push buffer：节点已 push 过 → 直接用聚合 delta。
			if u, ok := drained[n.ID]; ok {
				fetched[idx] = nodeFetch{node: n, client: c, usage: u}
				return
			}
			// fallback：节点尚未 push 过，按需拉取。
			u, err := c.Usage(ctx, true)
			fetched[idx] = nodeFetch{node: n, client: c, usage: u, usageErr: err}
		}(i, node)
	}
	wg.Wait()

	// ── 阶段 2：更新 DB（持锁，不含网络 IO） ─────────────────────────────────
	type pendingApply struct {
		node    nodes.Node
		client  *nodes.Client
		recover bool // true = xray 未运行，需恢复重启
	}
	var pending []pendingApply

	// 统计本轮失败节点数，用于判断是否为控制面网络问题（超半数同时失败则静默）。
	failedCount := 0
	for _, fr := range fetched {
		if fr.dialErr != nil || fr.usageErr != nil {
			failedCount++
		}
	}
	totalNodes := len(fetched)
	// 超过半数节点同时失败，视为控制面网络抖动，不触发单节点告警。
	massOutage := totalNodes > 1 && failedCount*2 > totalNodes

	mu.Lock()
	// 记录本轮已首次处理的用户，确保连接数从零开始累加而非叠加上轮旧值
	connResetUsers := make(map[string]struct{})
	// 跨节点全局 IP 集合，用于精确计算设备数（同一 IP 连多个节点只算一台）
	globalIPsByUser := make(map[string]map[string]struct{})
	date := now.Format("2006-01-02")
	// changedUsers: userID → 新的 EffectiveEnabled 状态（true=启用，false=禁用）
	// 跨节点补全时用于判断该走 AddUser 还是 RemoveUser。
	changedUsers := make(map[string]bool)
	// 记录每个节点包含的用户 ID 集合，用于补全时无需再查数据库。
	userIDsPerNode := make(map[string][]string)

	for _, fr := range fetched {
		// context 被取消（server 正在关闭）时跳过记录，避免把所有节点同时标记为离线
		if ctx.Err() != nil {
			break
		}
		// 记录本次可用性快照（无论节点是否正常，都写入一条记录）
		online := fr.dialErr == nil && fr.usageErr == nil
		running := online && (fr.usage.Running || fr.usage.Available)
		_ = nodeStore.RecordNodeUptime(fr.node.ID, online, running)

		if fr.dialErr != nil {
			result.Errors = append(result.Errors, fr.node.ID+": "+fr.dialErr.Error())
			if !massOutage && nodeOfflineAlert(fr.node.ID) {
				sendAlert(ctx, applyOpts.Alerter, "节点离线", fmt.Sprintf("无法连接节点 %s", fr.node.Name))
			}
			continue
		}
		if fr.usageErr != nil {
			result.Errors = append(result.Errors, fr.node.ID+": "+fr.usageErr.Error())
			if !massOutage && nodeOfflineAlert(fr.node.ID) {
				sendAlert(ctx, applyOpts.Alerter, "节点离线", fmt.Sprintf("无法连接节点 %s", fr.node.Name))
			}
			continue
		}
		if !fr.usage.Available {
			result.Errors = append(result.Errors, fr.node.ID+": V2Ray Stats not available")
			if !fr.usage.Running {
				sendAlert(ctx, applyOpts.Alerter, "节点异常", fmt.Sprintf("节点 %s xray 停止运行", fr.node.Name))
				pending = append(pending, pendingApply{node: fr.node, client: fr.client, recover: true})
			}
			continue
		}
		result.NodesSynced++
		nodeResetFail(fr.node.ID)

		userAccesses, err := store.ListUserInboundsByNode(fr.node.ID)
		if err != nil {
			result.Errors = append(result.Errors, fr.node.ID+": "+err.Error())
			continue
		}
		// 记录本节点用户列表（用于后续补全跨节点重下发，避免事后再查 DB）
		nodeUIDs := collectUserIDs(userAccesses)
		userIDsPerNode[fr.node.ID] = nodeUIDs
		userMap, err := store.GetUsersByIDs(nodeUIDs)
		if err != nil {
			result.Errors = append(result.Errors, fr.node.ID+": "+err.Error())
			continue
		}

		// 构建 inbound tag → TrafficRate 映射
		nodeInbounds, err := ibStore.ListInboundsByNode(fr.node.ID)
		if err != nil {
			result.Errors = append(result.Errors, fr.node.ID+": list inbounds: "+err.Error())
			continue
		}
		ibRateByTag := make(map[string]float64, len(nodeInbounds))
		for _, ib := range nodeInbounds {
			tag := ib.Tag
			if tag == "" {
				tag = fmt.Sprintf("%s-%d", ib.Protocol, ib.Port)
			}
			rate := ib.TrafficRate
			if rate <= 0 {
				rate = 1.0
			}
			ibRateByTag[tag] = rate
		}

		// 解析 V2Ray Stats 的复合用户名（username@@@tag），按真实用户名聚合流量
		type userDelta struct {
			upload      int64
			download    int64
			rawUpload   int64
			rawDownload int64
			connections int
			devices     int                 // 节点直接回报的设备数，当 sourceIPs 缺失时作为 fallback
			sourceIPs   map[string]struct{} // 跨节点/inbound 全局去重
			hasTraffic  bool
		}
		deltaByUser := make(map[string]*userDelta)
		var nodeUploadDelta, nodeDownloadDelta int64
		for _, item := range fr.usage.Users {
			nodeUploadDelta += item.UploadTotal
			nodeDownloadDelta += item.DownloadTotal

			realUser, ibTag := parseCompositeUser(item.User)
			rate := 1.0
			if ibTag != "" {
				if r, ok := ibRateByTag[ibTag]; ok {
					rate = r
				}
			}

			d, ok := deltaByUser[realUser]
			if !ok {
				d = &userDelta{sourceIPs: make(map[string]struct{})}
				deltaByUser[realUser] = d
			}
			d.rawUpload += item.UploadTotal
			d.rawDownload += item.DownloadTotal
			d.upload += applyRate(item.UploadTotal, rate)
			d.download += applyRate(item.DownloadTotal, rate)
			d.connections += item.Connections
			d.devices += item.Devices
			for _, ip := range item.SourceIPs {
				d.sourceIPs[ip] = struct{}{}
			}
			if item.UploadTotal > 0 || item.DownloadTotal > 0 {
				d.hasTraffic = true
			}
		}

		reloadNeeded := false
		seenUsers := make(map[string]struct{})
		for _, acc := range userAccesses {
			if _, seen := seenUsers[acc.UserID]; seen {
				continue
			}
			seenUsers[acc.UserID] = struct{}{}

			user, ok := userMap[acc.UserID]
			if !ok {
				continue
			}
			prevEnabled := user.EffectiveEnabledAt(now)

			// 本轮首次处理该用户时，清零连接数和设备数，确保跨节点累加从零开始
			if _, seen := connResetUsers[user.ID]; !seen {
				user.Connections = 0
				user.Devices = 0
				connResetUsers[user.ID] = struct{}{}
			}

			if d, ok := deltaByUser[user.Username]; ok {
				user.UploadBytes += d.upload
				user.DownloadBytes += d.download
				user.RawUploadBytes += d.rawUpload
				user.RawDownloadBytes += d.rawDownload
				user.Connections += d.connections
				if d.hasTraffic {
					user.OnlineAt = &now
				}
			}
			// 跨节点全局 IP 去重：将本节点 IP 合并入全局集合，然后 SET（非累加）设备数。
			// 当节点只回报 Devices 数量而不回报 SourceIPs 列表时，以节点值作为 fallback。
			if d, ok := deltaByUser[user.Username]; ok {
				if globalIPsByUser[user.Username] == nil {
					globalIPsByUser[user.Username] = make(map[string]struct{})
				}
				for ip := range d.sourceIPs {
					globalIPsByUser[user.Username][ip] = struct{}{}
				}
			}
			ipCount := len(globalIPsByUser[user.Username])
			if ipCount > 0 {
				user.Devices = ipCount
			} else if d, ok := deltaByUser[user.Username]; ok {
				// sourceIPs 为空时回退到节点上报的设备数（跨 inbound 累加，可能略有重叠）
				user.Devices = d.devices
			}

			user.UsedBytes = user.UploadBytes + user.DownloadBytes
			statusChanged := prevEnabled != user.EffectiveEnabledAt(now)
			// 使用 savedUser 接收持久化结果，不写回 userMap，避免本节点累加的脏数据
			// 在同一用户出现在多个节点时被下一个节点循环的 GetUsersByIDs 读到旧值。
			savedUser, err := store.UpsertUser(user)
			if err != nil {
				result.Errors = append(result.Errors, fr.node.ID+": "+err.Error())
				continue
			}
			if statusChanged {
				reloadNeeded = true
				changedUsers[user.ID] = savedUser.EffectiveEnabledAt(now)
				switch savedUser.EffectiveStatusAt(now) {
				case users.StatusLimited:
					sendAlert(ctx, applyOpts.Alerter, "流量超限", fmt.Sprintf("用户 %s 已超出流量限额", savedUser.Username))
				case users.StatusExpired:
					sendAlert(ctx, applyOpts.Alerter, "用户到期", fmt.Sprintf("用户 %s 已到期", savedUser.Username))
				}
			}
			result.UsersUpdated++
		}

		if nodeUploadDelta > 0 || nodeDownloadDelta > 0 {
			if err := nodeStore.AddTraffic(fr.node.ID, nodeUploadDelta, nodeDownloadDelta); err != nil {
				result.Errors = append(result.Errors, fr.node.ID+": add traffic: "+err.Error())
			}
			if err := nodeStore.AddNodeDailyUsage(fr.node.ID, date, nodeUploadDelta, nodeDownloadDelta); err != nil {
				result.Errors = append(result.Errors, fr.node.ID+": daily usage: "+err.Error())
			}
		}

		for userID, user := range userMap {
			d, ok := deltaByUser[user.Username]
			if !ok || !d.hasTraffic {
				continue
			}
			if err := store.AddUserNodeTraffic(userID, fr.node.ID, date, d.upload, d.download); err != nil {
				result.Errors = append(result.Errors, fr.node.ID+": user node traffic: "+err.Error())
			}
		}

		if reloadNeeded {
			pending = append(pending, pendingApply{node: fr.node, client: fr.client})
		}
	}

	mu.Unlock()

	// 补全跨节点状态变化漏网的节点（lock 外，无 DB 查询）。
	//
	// 两类场景需要补全：
	// 1. 顺序处理竞态：节点 A 写库后节点 B 读到的 prevEnabled 已是变更后的值，
	//    statusChanged=false，节点 B 不重下发。通过 userIDsPerNode 判断是否含变化用户。
	// 2. 节点抖动恢复：节点 B 本轮因 usageErr 被跳过，下轮恢复后 prevEnabled 与
	//    currentEnabled 都是 false，statusChanged 永远不会触发。
	//    对策：本轮有状态变化时，对所有可连接（dialErr==nil）但未在 pending 的节点
	//    一律补入——config push 与流量统计走不同接口，usageErr 不影响推送能力。
	if len(changedUsers) > 0 {
		pendingSet := make(map[string]struct{}, len(pending))
		for _, p := range pending {
			pendingSet[p.node.ID] = struct{}{}
		}
		for _, fr := range fetched {
			if fr.dialErr != nil {
				continue
			}
			if _, already := pendingSet[fr.node.ID]; already {
				continue
			}
			needApply := !fr.usage.Available || fr.usageErr != nil
			if !needApply {
				for _, uid := range userIDsPerNode[fr.node.ID] {
					if _, changed := changedUsers[uid]; changed {
						needApply = true
						break
					}
				}
			}
			if needApply {
				pending = append(pending, pendingApply{node: fr.node, client: fr.client})
			}
		}
	}

	// ── 阶段 3：下发配置（网络 IO，不持锁） ──────────────────────────────────
	for _, pa := range pending {
		// 下发前重新从 DB 读取最新数据（锁住读取段，下发本身不持锁）
		mu.Lock()
		nodeInbounds, err := ibStore.ListInboundsByNode(pa.node.ID)
		if err != nil {
			result.Errors = append(result.Errors, pa.node.ID+": load inbounds: "+err.Error())
			mu.Unlock()
			continue
		}
		nodeAccesses, err := store.ListUserInboundsByNode(pa.node.ID)
		if err != nil {
			result.Errors = append(result.Errors, pa.node.ID+": load accesses: "+err.Error())
			mu.Unlock()
			continue
		}
		applyMap, err := store.GetUsersByIDs(collectUserIDs(nodeAccesses))
		if err != nil {
			result.Errors = append(result.Errors, pa.node.ID+": load usermap: "+err.Error())
			mu.Unlock()
			continue
		}
		mu.Unlock()

		// 非故障恢复场景：优先用 AddUser/RemoveUser 热更新，避免全量重启断流。
		// 若 delta 成功则跳过 ApplyNodeUsers；若失败则回退到全量重启（兜底）。
		if !pa.recover && len(changedUsers) > 0 {
			if tryDeltaUsers(ctx, pa.client, nodeInbounds, nodeAccesses, applyMap, changedUsers) {
				result.NodesReloaded++
				continue
			}
		}

		status, _, err := ApplyNodeUsers(ctx, pa.client, nodeInbounds, nodeAccesses, applyMap, ibStore, outboundStore, applyOpts, pa.node)
		if err != nil {
			result.Errors = append(result.Errors, pa.node.ID+": apply: "+err.Error())
			continue
		}
		if pa.recover {
			if status.Running {
				result.NodesReloaded++
			} else {
				result.NodesStopped++
			}
		} else if status.Running {
			result.NodesReloaded++
		} else {
			result.NodesStopped++
		}
	}

	return result, nil
}

// tryDeltaUsers 通过 AddUser/RemoveUser 对节点做增量用户变更，避免全量重启。
// 返回 true 表示全部操作成功；返回 false 时调用方应回退到全量 Restart。
func tryDeltaUsers(
	ctx context.Context,
	client *nodes.Client,
	nodeInbounds []inbounds.Inbound,
	nodeAccesses []users.UserInbound,
	userMap map[string]users.User,
	changedUsers map[string]bool,
) bool {
	// 构建 inboundID → inbound 查找表
	ibByID := make(map[string]inbounds.Inbound, len(nodeInbounds))
	for _, ib := range nodeInbounds {
		ibByID[ib.ID] = ib
	}

	for _, acc := range nodeAccesses {
		newEnabled, changed := changedUsers[acc.UserID]
		if !changed {
			continue
		}
		u, ok := userMap[acc.UserID]
		if !ok {
			continue
		}
		ib, ok := ibByID[acc.InboundID]
		if !ok {
			continue
		}

		tag := ib.Tag
		if tag == "" {
			tag = fmt.Sprintf("%s-%d", ib.Protocol, ib.Port)
		}
		email := u.Username + proxycfg.UserInboundSep + tag

		if newEnabled {
			flow := ""
			if ib.Protocol == "vless" && ib.Security == "reality" {
				flow = "xtls-rprx-vision"
			}
			uuid := u.UUID
			if uuid == "" {
				uuid = acc.UUID
			}
			secret := u.Secret
			if secret == "" {
				secret = acc.Secret
			}
			if err := client.AddUser(ctx, nodes.UserChangeRequest{
				InboundTag: tag,
				Protocol:   ib.Protocol,
				Email:      email,
				UUID:       uuid,
				Password:   secret,
				Flow:       flow,
			}); err != nil {
				log.Printf("warn: delta AddUser %s on %s: %v — falling back to restart", email, tag, err)
				return false
			}
		} else {
			if err := client.RemoveUser(ctx, tag, email); err != nil {
				log.Printf("warn: delta RemoveUser %s on %s: %v — falling back to restart", email, tag, err)
				return false
			}
		}
	}
	return true
}

// ─── ActivateExpiredOnHold ────────────────────────────────────────────────────

// ActivateExpiredOnHold 将 on_hold_expire_at 已到期的 on_hold 用户状态改为 active，
// 并对涉及的节点重新下发配置。
func ActivateExpiredOnHold(ctx context.Context, store users.Store, nodeStore nodes.Store, ibStore inbounds.InboundStore, dial NodeDialer, applyOpts ApplyOptions, outboundStore outbounds.Store) error {
	now := time.Now().UTC()
	dirtySet := make(map[string]struct{})

	// ── DB 段（持锁） ─────────────────────────────────────────────────────────
	mu.Lock()
	allUsers, err := store.ListUsers()
	if err != nil {
		mu.Unlock()
		return err
	}
	for _, u := range allUsers {
		if u.Status != users.StatusOnHold {
			continue
		}
		if u.OnHoldExpireAt == nil || u.OnHoldExpireAt.IsZero() || now.Before(*u.OnHoldExpireAt) {
			continue
		}
		u.Status = users.StatusActive
		u.OnHoldExpireAt = nil
		if _, err := store.UpsertUser(u); err != nil {
			log.Printf("ActivateExpiredOnHold: 激活用户 %s (%s) 失败: %v", u.Username, u.ID, err)
			continue
		}
		log.Printf("ActivateExpiredOnHold: 用户 %s (%s) 已激活", u.Username, u.ID)
		accesses, _ := store.ListUserInboundsByUser(u.ID)
		for _, acc := range accesses {
			dirtySet[acc.NodeID] = struct{}{}
		}
	}
	mu.Unlock()

	// ── 下发配置（网络 IO，不持锁） ───────────────────────────────────────────
	for nodeID := range dirtySet {
		client, err := dial(nodeID)
		if err != nil {
			continue
		}
		mu.Lock()
		nodeInbounds, err := ibStore.ListInboundsByNode(nodeID)
		if err != nil {
			mu.Unlock()
			continue
		}
		nodeAccesses, err := store.ListUserInboundsByNode(nodeID)
		if err != nil {
			mu.Unlock()
			continue
		}
		userMap, err := store.GetUsersByIDs(collectUserIDs(nodeAccesses))
		if err != nil {
			mu.Unlock()
			continue
		}
		node, _ := nodeStore.Get(nodeID)
		mu.Unlock()

		ApplyNodeUsers(ctx, client, nodeInbounds, nodeAccesses, userMap, ibStore, outboundStore, applyOpts, node) //nolint:errcheck
	}

	return nil
}

// ─── ResetTraffic ─────────────────────────────────────────────────────────────

// ResetTrafficResult 记录一次流量重置的结果摘要。
type ResetTrafficResult struct {
	UsersReset    int      `json:"users_reset"`
	NodesReloaded int      `json:"nodes_reloaded"`
	Errors        []string `json:"errors"`
}

// ResetTraffic 检查所有用户的流量重置策略，到期则清零并重新下发节点配置。
func ResetTraffic(ctx context.Context, store users.Store, nodeStore nodes.Store, ibStore inbounds.InboundStore, dial NodeDialer, applyOpts ApplyOptions, outboundStore outbounds.Store) (ResetTrafficResult, error) {
	result := ResetTrafficResult{Errors: make([]string, 0)}
	now := time.Now().UTC()
	dirtySet := make(map[string]struct{})

	// ── DB 段（持锁） ─────────────────────────────────────────────────────────
	mu.Lock()
	allUsers, err := store.ListUsers()
	if err != nil {
		mu.Unlock()
		return result, err
	}
	for _, user := range allUsers {
		if !ShouldResetTraffic(user.DataLimitResetStrategy, user.CreatedAt, user.LastTrafficResetAt, now) {
			continue
		}
		user.UploadBytes = 0
		user.DownloadBytes = 0
		user.UsedBytes = 0
		user.RawUploadBytes = 0
		user.RawDownloadBytes = 0
		// 有套餐基础额度时还原，消除叠加量；0 表示无限或未设置，保持不变
		if user.PlanTrafficLimit > 0 {
			user.TrafficLimit = user.PlanTrafficLimit
		}
		user.LastTrafficResetAt = &now
		if _, err := store.UpsertUser(user); err != nil {
			result.Errors = append(result.Errors, user.ID+": "+err.Error())
			continue
		}
		if err := store.ClearUserNodeDailyUsage(user.ID); err != nil {
			result.Errors = append(result.Errors, user.ID+": clear node usage: "+err.Error())
		}
		userAccesses, err := store.ListUserInboundsByUser(user.ID)
		if err != nil {
			result.Errors = append(result.Errors, user.ID+": list accesses: "+err.Error())
			continue
		}
		for _, acc := range userAccesses {
			dirtySet[acc.NodeID] = struct{}{}
		}
		result.UsersReset++
	}
	mu.Unlock()

	if len(dirtySet) == 0 {
		return result, nil
	}

	// ── 下发配置（网络 IO，不持锁） ───────────────────────────────────────────
	for nodeID := range dirtySet {
		client, err := dial(nodeID)
		if err != nil {
			result.Errors = append(result.Errors, nodeID+": "+err.Error())
			continue
		}
		mu.Lock()
		nodeInbounds, err := ibStore.ListInboundsByNode(nodeID)
		if err != nil {
			result.Errors = append(result.Errors, nodeID+": list inbounds: "+err.Error())
			mu.Unlock()
			continue
		}
		nodeAccesses, err := store.ListUserInboundsByNode(nodeID)
		if err != nil {
			result.Errors = append(result.Errors, nodeID+": list accesses: "+err.Error())
			mu.Unlock()
			continue
		}
		userMap, err := store.GetUsersByIDs(collectUserIDs(nodeAccesses))
		if err != nil {
			result.Errors = append(result.Errors, nodeID+": get users: "+err.Error())
			mu.Unlock()
			continue
		}
		node, _ := nodeStore.Get(nodeID)
		mu.Unlock()

		status, _, err := ApplyNodeUsers(ctx, client, nodeInbounds, nodeAccesses, userMap, ibStore, outboundStore, applyOpts, node)
		if err != nil {
			result.Errors = append(result.Errors, nodeID+": reload: "+err.Error())
			continue
		}
		if status.Running {
			result.NodesReloaded++
		}
	}

	return result, nil
}

// ─── ShouldResetTraffic ───────────────────────────────────────────────────────

// ShouldResetTraffic 判断是否应按策略重置用户流量（纯函数，便于测试）。
func ShouldResetTraffic(strategy string, createdAt time.Time, lastResetAt *time.Time, now time.Time) bool {
	if strategy == users.ResetStrategyNoReset || strategy == "" {
		return false
	}

	ref := createdAt
	if lastResetAt != nil && !lastResetAt.IsZero() {
		ref = *lastResetAt
	}

	var next time.Time
	switch strategy {
	case users.ResetStrategyDay:
		next = ref.Add(24 * time.Hour)
	case users.ResetStrategyWeek:
		next = ref.AddDate(0, 0, 7)
	case users.ResetStrategyMonth:
		next = ref.AddDate(0, 1, 0)
	case users.ResetStrategyYear:
		next = ref.AddDate(1, 0, 0)
	default:
		return false
	}

	return !now.Before(next)
}

// ─── ApplyNode ────────────────────────────────────────────────────────────────

// ApplyNode 根据节点 ID 从各 Store 汇集数据后调用 ApplyNodeUsers，
// 适合在 inbound 变更（新增/修改/删除）后立即调用。
func ApplyNode(ctx context.Context, nodeID string, nodeStore nodes.Store, userStore users.Store, ibStore inbounds.InboundStore, outboundStore outbounds.Store, dial NodeDialer, opts ApplyOptions) error {
	node, err := nodeStore.Get(nodeID)
	if err != nil {
		return err
	}
	if node.Disabled {
		return nil // 禁用节点跳过配置下发
	}
	client, err := dial(nodeID)
	if err != nil {
		return err
	}
	nodeInbounds, err := ibStore.ListInboundsByNode(nodeID)
	if err != nil {
		return err
	}
	userAccesses, err := userStore.ListUserInboundsByNode(nodeID)
	if err != nil {
		return err
	}
	userMap, err := userStore.GetUsersByIDs(collectUserIDs(userAccesses))
	if err != nil {
		return err
	}
	_, _, err = ApplyNodeUsers(ctx, client, nodeInbounds, userAccesses, userMap, ibStore, outboundStore, opts, node)
	return err
}

// ─── ApplyNodeUsers ───────────────────────────────────────────────────────────

// Alerter 告警发送接口，nil 表示不发送。
type Alerter interface {
	Send(ctx context.Context, title, body string) error
}

// SettingGetter 用于读取系统配置项（如 CF Token）。
type SettingGetter interface {
	GetSetting(key string) (string, bool)
}

// getCFToken 读取 Cloudflare API Token。
func getCFToken(s SettingGetter) string {
	if s == nil {
		return ""
	}
	tok, _ := s.GetSetting("cf_token")
	return tok
}

// ApplyOptions 控制 ApplyNodeUsers 的行为。
type ApplyOptions struct {
	Alerter        Alerter          // nil 时不发送告警
	RouteRuleStore routerules.Store // nil 时不应用全局分流规则
	// UserStore 和 NodeStore 用于将节点 inbound 作为分流出口（nodeib: 前缀）时查找凭据和地址。
	// nil 时跳过节点 inbound 出口构建。
	UserStore   users.Store
	NodeStore   nodes.Store
	Settings    SettingGetter  // nil 时跳过 CF Token 等系统配置读取
	PanelPort   int            // 0 时 NodeGate 不配置 panel 反代端口
}

// ApplyNodeUsers 根据节点 inbound 配置和用户凭据生成配置并下发到节点。
// nodeInbounds 是节点 inbound 定义，userAccesses 是用户凭据列表（每用户一条）。
func ApplyNodeUsers(ctx context.Context, client *nodes.Client, nodeInbounds []inbounds.Inbound, userAccesses []users.UserInbound, userMap map[string]users.User, ibStore inbounds.InboundStore, outboundStore outbounds.Store, applyOpts ApplyOptions, node nodes.Node) (nodes.Status, string, error) {
	// 过滤出已启用用户
	activeAccesses := filterEnabled(userAccesses, userMap)
	if len(activeAccesses) == 0 || len(nodeInbounds) == 0 {
		// 没有活跃用户或 Inbound 时，用最小配置保持核心进程存活
		idleCfg := proxycfg.BuildIdleFor()
		status, err := client.Restart(ctx, nodes.ConfigRequest{Config: idleCfg})
		if err == nil {
			// 空配置下发给 sniproxy 让它停止代理
			if syncErr := client.SyncSNIProxy(ctx, nodes.SNIProxySyncRequest{}); syncErr != nil {
				log.Printf("warn: sniproxy sync (idle): %v", syncErr)
			}
		}
		return status, idleCfg, err
	}

	// 加载出口 map
	outboundMap := make(map[string]outbounds.Outbound)
	if outboundStore != nil {
		list, _ := outboundStore.List()
		for _, ob := range list {
			outboundMap[ob.ID] = ob
		}
	}

	// 加载全局分流规则
	var globalRouteRules []routerules.RouteRule
	if applyOpts.RouteRuleStore != nil {
		globalRouteRules, _ = applyOpts.RouteRuleStore.List()
	}

	// 加载所有节点 inbound 出口所需数据（用于 nodeib: 前缀出口）
	allInboundMap := make(map[string]inbounds.Inbound)
	allNodeMap := make(map[string]nodes.Node)
	userInboundMap := make(map[string]users.UserInbound)
	if applyOpts.UserStore != nil && applyOpts.NodeStore != nil && ibStore != nil {
		if allIbs, err := ibStore.ListInbounds(); err == nil {
			for _, ib := range allIbs {
				allInboundMap[ib.ID] = ib
				if accs, err := applyOpts.UserStore.ListUserInboundsByInbound(ib.ID); err == nil {
					for _, acc := range accs {
						userInboundMap[acc.ID] = acc
					}
				}
			}
		}
		if allNodes, err := applyOpts.NodeStore.List(); err == nil {
			for _, n := range allNodes {
				allNodeMap[n.ID] = n
			}
		}
	}

	// 1) 先同步 NodeGate（注册 hy2 SNI 到 CertDomains，触发 ACME 申请）。
	//    必须在 build xray 之前，否则 hy2 inbound 的证书路径不存在 → xray 启动失败 → 整个
	//    apply 报错回滚 → certmgr 永远收不到 sync → 死循环。
	if ibStore != nil {
		cfToken := ""
		if applyOpts.Settings != nil {
			cfToken = getCFToken(applyOpts.Settings)
		}
		sniReq := BuildSNIProxySyncReq(node, nodeInbounds, ibStore, allNodeMap, cfToken, applyOpts.PanelPort)
		if syncErr := client.SyncSNIProxy(ctx, sniReq); syncErr != nil {
			log.Printf("warn: sniproxy sync: %v", syncErr)
		}
	}

	// 2) 若有 hy2 inbound，轮询节点等待证书就绪（最多 60s）。
	//    超时未就绪的 hy2 inbound 会被本次 build 跳过（仅 warning），下次 apply 自动加回。
	knownReadyCerts := make(map[string]bool)
	hy2Domains := collectHy2SNIs(nodeInbounds)
	if len(hy2Domains) > 0 {
		waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		ticker := time.NewTicker(3 * time.Second)
	pollLoop:
		for {
			ready, err := client.SNIProxyCertReady(waitCtx)
			if err == nil {
				for _, d := range ready {
					knownReadyCerts[d] = true
				}
				allReady := true
				for _, d := range hy2Domains {
					if !knownReadyCerts[d] {
						allReady = false
						break
					}
				}
				if allReady {
					break pollLoop
				}
			}
			select {
			case <-waitCtx.Done():
				break pollLoop
			case <-ticker.C:
			}
		}
		ticker.Stop()
		cancel()
		for _, d := range hy2Domains {
			if !knownReadyCerts[d] {
				log.Printf("warn: hy2 cert not ready for %s, skipping inbound this round", d)
			}
		}
	}

	cfg, err := proxycfg.Build(nodeInbounds, userAccesses, userMap, proxycfg.BuildOptions{
		OutboundMap:    outboundMap,
		RouteRules:     globalRouteRules,
		NodeID:         node.ID,
		AllInboundMap:  allInboundMap,
		AllNodeMap:     allNodeMap,
		UserInboundMap: userInboundMap,
		CertPathFor:    nodeCertPath,
		KnownReadyCerts: knownReadyCerts,
	})
	if err != nil {
		return nodes.Status{}, "", err
	}

	status, err := client.Restart(ctx, nodes.ConfigRequest{Config: cfg})
	if err != nil {
		return status, cfg, err
	}

	return status, cfg, nil
}

func filterEnabled(accesses []users.UserInbound, userMap map[string]users.User) []users.UserInbound {
	out := make([]users.UserInbound, 0, len(accesses))
	for _, acc := range accesses {
		u, ok := userMap[acc.UserID]
		if ok && u.EffectiveEnabled() {
			out = append(out, acc)
		}
	}
	return out
}

// collectUserIDs 从 userAccesses 列表中提取去重后的 UserID。
func collectUserIDs(accesses []users.UserInbound) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, acc := range accesses {
		if _, ok := seen[acc.UserID]; !ok {
			seen[acc.UserID] = struct{}{}
			out = append(out, acc.UserID)
		}
	}
	return out
}

// sendAlert 在后台 goroutine 中发送告警，不阻塞主流程。a 为 nil 时静默跳过。
func sendAlert(ctx context.Context, a Alerter, title, body string) {
	if a == nil {
		return
	}
	go func() {
		alertCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.Send(alertCtx, title, body); err != nil {
			log.Printf("alert send error: %v", err)
		}
	}()
}

// nodeOfflineAlert 记录节点连续失败次数，返回 true 表示应发送告警。
// 启动宽限期内或连续失败未达阈值时静默，避免冷启动误报。
func nodeOfflineAlert(nodeID string) bool {
	if time.Since(serverStartTime) < alertStartupGrace {
		return false
	}
	nodeFailMu.Lock()
	defer nodeFailMu.Unlock()
	nodeFailCount[nodeID]++
	return nodeFailCount[nodeID] == alertThreshold
}

// nodeResetFail 节点恢复在线后清零连续失败计数。
func nodeResetFail(nodeID string) {
	nodeFailMu.Lock()
	delete(nodeFailCount, nodeID)
	nodeFailMu.Unlock()
}

// applyRate 将 delta 乘以倍率并防止 int64 溢出。
// delta 理论上不会为负（V2Ray Stats reset=true 返回的是增量），
// 但节点重启或计数器跳变时可能出现异常负值，直接截断为 0 避免流量统计写入负数。
func applyRate(delta int64, rate float64) int64 {
	if delta <= 0 {
		return 0
	}
	// float64(1<<63 - 1) 在 float64 中向上取整为 2^63 = 9.223372036854776e+18，
	// 任何 >= 该值的 float64 转换为 int64 都会溢出，因此用它作为上界。
	const maxInt64Float = float64(1<<63 - 1)
	scaled := float64(delta) * rate
	if scaled >= maxInt64Float {
		return 1<<63 - 1 // math.MaxInt64
	}
	return int64(scaled)
}

// parseCompositeUser 解析 V2Ray Stats 中的复合用户名 "username@@@tag"。
// 若不含分隔符（向后兼容旧节点），返回原始用户名和空 tag。
func parseCompositeUser(composite string) (username, ibTag string) {
	if idx := strings.LastIndex(composite, proxycfg.UserInboundSep); idx >= 0 {
		return composite[:idx], composite[idx+len(proxycfg.UserInboundSep):]
	}
	return composite, ""
}

// CleanupLogsWithRetention 按指定保留天数清理历史数据。
func CleanupLogsWithRetention(_ context.Context, nodeStore nodes.Store, uptimeDays, dailyDays int) error {
	if err := nodeStore.CleanupOldDailyUsage(dailyDays); err != nil {
		return fmt.Errorf("cleanup daily usage: %w", err)
	}
	if err := nodeStore.CleanupOldNodeUptime(uptimeDays); err != nil {
		return fmt.Errorf("cleanup node uptime: %w", err)
	}
	return nil
}

// BuildNodeIPMap 从节点 map 构建节点 ID → IP 的映射。
// 优先使用 IPOverride，否则从 BaseURL 解析 hostname。
func BuildNodeIPMap(allNodeMap map[string]nodes.Node) map[string]string {
	m := make(map[string]string, len(allNodeMap))
	for _, n := range allNodeMap {
		ip := n.IPOverride
		if ip == "" {
			if u, err := url.Parse(n.BaseURL); err == nil {
				ip = u.Hostname()
			}
		}
		m[n.ID] = ip
	}
	return m
}

// PortforwardTargetPort 根据入站协议决定前置节点应转发到的目标端口。
// AnyTLS/Trojan 经 NodeGate 终止 TLS，默认 443，可通过 httpsPort 覆盖。
func PortforwardTargetPort(ib inbounds.Inbound, httpsPort int) int {
	switch ib.Protocol {
	case "anytls", "trojan":
		if httpsPort > 0 {
			return httpsPort
		}
		return 443
	default:
		return ib.Port
	}
}

