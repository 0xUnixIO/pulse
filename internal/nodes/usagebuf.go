package nodes

import (
	"sync"
)

// UsageBuffer 缓存来自 node 主动 push 的 usage 数据。
//
// 设计思路：
//   - hub 模式下 nodeagent.UsagePusher 周期性把 usage delta 通过双向流 push
//     到 server；server 端的 nodehub.MultiPushHandler.UsagePushHandler 把每帧
//     转交给 UsageBuffer.Append 做去重 + 缓存。
//   - SyncUsage job 周期性 Drain 整个 buffer，把累积的 delta 一次性写库。
//   - dedup：对同一 nodeID，记录最大已见 seq；重复进来的 seq <= lastSeq 直接 ack
//     不二次入库（防止 node 端 ack 丢失导致重发造成重复计费）。
//
// 所有方法 goroutine-safe。
type UsageBuffer struct {
	mu      sync.Mutex
	pending map[string][]usageEntry // nodeID → 待消费帧列表
	lastSeq map[string]uint64       // nodeID → 已合并入 pending 的最大 seq
}

type usageEntry struct {
	Seq   uint64
	Delta UsageStats
}

// NewUsageBuffer 构造一个空 buffer。
func NewUsageBuffer() *UsageBuffer {
	return &UsageBuffer{
		pending: make(map[string][]usageEntry),
		lastSeq: make(map[string]uint64),
	}
}

// seqEpochGap 判定节点 seq 计数器重置（进程重启）的最小回退幅度。
// 正常重传/乱序回退通常为 1～数个序号；node 重启后 nextSeq 从 1 起而 server
// lastSeq 可能已达数千，差值远大于此阈值。
const seqEpochGap = uint64(64)

// Append 接收一帧 push usage。返回值表示 ack 是否成功（始终 nil，表示立即 ack）。
//
// 去重：同一 nodeID 上 seq <= 已记录的最大值会被丢弃（不入 pending），但仍返回 nil
// 让 node 端 ack 推进 baseline。这样能容忍 node 重发已成功处理但 ack 丢失的帧。
//
// 纪元切换：若 seq 明显回退（last-seq >= seqEpochGap），视为节点进程重启后
// nextSeq 归零，重置 lastSeq 并接受该帧，避免长时间丢流量。
func (b *UsageBuffer) Append(nodeID string, seq uint64, delta UsageStats) error {
	if nodeID == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if last, ok := b.lastSeq[nodeID]; ok && seq != 0 && seq <= last {
		if last-seq >= seqEpochGap {
			// 节点重启：seq 从低位重新计数，开启新纪元。
			b.lastSeq[nodeID] = seq
			b.pending[nodeID] = append(b.pending[nodeID], usageEntry{Seq: seq, Delta: delta})
			return nil
		}
		// 重复或乱序的旧帧：ack 但不重复计入。
		return nil
	}
	b.pending[nodeID] = append(b.pending[nodeID], usageEntry{Seq: seq, Delta: delta})
	if seq > b.lastSeq[nodeID] {
		b.lastSeq[nodeID] = seq
	}
	return nil
}

// Requeue 将已 Drain 的聚合 delta 重新入队（seq=0，始终接受）。
// 用于 SyncUsage 在落库失败时回填，避免 push 流量静默丢失。
func (b *UsageBuffer) Requeue(nodeID string, delta UsageStats) {
	if b == nil || nodeID == "" {
		return
	}
	_ = b.Append(nodeID, 0, delta)
}

// Drain 取走某节点所有 pending 帧，把多帧合并为单一累计 delta。
// 没有任何 pending 时返回 zero stats + ok=false。
//
// 合并语义：
//   - UploadTotal / DownloadTotal：累加（多帧 delta 之和）
//   - Available / Running：取最后一帧
//   - StartedAt：取最后一帧（最近一次的状态）
//   - UploadSpeed / DownloadSpeed / Connections：取最后一帧（瞬时值）
//   - Users：按 user 名累计 upload/download/connections，devices 取最大值，
//     SourceIPs 合并去重
func (b *UsageBuffer) Drain(nodeID string) (UsageStats, bool) {
	b.mu.Lock()
	entries := b.pending[nodeID]
	delete(b.pending, nodeID)
	b.mu.Unlock()
	if len(entries) == 0 {
		return UsageStats{}, false
	}
	return mergeUsage(entries), true
}

// DrainAll 一次性消费所有节点的 pending，返回 nodeID → 累计 delta。
func (b *UsageBuffer) DrainAll() map[string]UsageStats {
	b.mu.Lock()
	all := b.pending
	b.pending = make(map[string][]usageEntry)
	b.mu.Unlock()
	out := make(map[string]UsageStats, len(all))
	for nodeID, entries := range all {
		if len(entries) == 0 {
			continue
		}
		out[nodeID] = mergeUsage(entries)
	}
	return out
}

// HasSeen reports whether the node has successfully pushed at least one
// sequenced usage frame. Once true, callers must not mix in reset-based pulls:
// a pull racing with the next push can account the same xray counters twice.
func (b *UsageBuffer) HasSeen(nodeID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.lastSeq[nodeID]
	return ok
}

// SeenNodes 返回有过 push 记录的 nodeID 列表（即使当前 pending 为空也会包含）。
// 用于诊断 / 监控。
func (b *UsageBuffer) SeenNodes() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.lastSeq))
	for id := range b.lastSeq {
		out = append(out, id)
	}
	return out
}

// mergeUsage 把多帧 UsageStats delta 合并成一帧。entries 必须非空。
func mergeUsage(entries []usageEntry) UsageStats {
	merged := UsageStats{}
	userIdx := make(map[string]int)
	ipSets := make(map[string]map[string]struct{})
	for i, e := range entries {
		merged.UploadTotal += e.Delta.UploadTotal
		merged.DownloadTotal += e.Delta.DownloadTotal
		// 状态字段取最后一帧。
		if i == len(entries)-1 {
			merged.Available = e.Delta.Available
			merged.Running = e.Delta.Running
			merged.StartedAt = e.Delta.StartedAt
			merged.UploadSpeed = e.Delta.UploadSpeed
			merged.DownloadSpeed = e.Delta.DownloadSpeed
			merged.Connections = e.Delta.Connections
		}
		for _, u := range e.Delta.Users {
			idx, ok := userIdx[u.User]
			if !ok {
				merged.Users = append(merged.Users, UserUsage{User: u.User})
				idx = len(merged.Users) - 1
				userIdx[u.User] = idx
				ipSets[u.User] = make(map[string]struct{})
			}
			merged.Users[idx].UploadTotal += u.UploadTotal
			merged.Users[idx].DownloadTotal += u.DownloadTotal
			merged.Users[idx].Connections += u.Connections
			if u.Devices > merged.Users[idx].Devices {
				merged.Users[idx].Devices = u.Devices
			}
			for _, ip := range u.SourceIPs {
				ipSets[u.User][ip] = struct{}{}
			}
		}
	}
	for user, ips := range ipSets {
		idx := userIdx[user]
		if len(ips) == 0 {
			continue
		}
		out := make([]string, 0, len(ips))
		for ip := range ips {
			out = append(out, ip)
		}
		merged.Users[idx].SourceIPs = out
	}
	return merged
}
