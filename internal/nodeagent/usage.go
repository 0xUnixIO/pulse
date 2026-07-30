package nodeagent

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"pulse/internal/coremanager"
	"pulse/internal/nodeapi"
)

// UsageSnapshotProvider 是 UsagePusher 与 nodeapi 解耦的接口：
// reset=true 时调用方应清零 xray 内部计数（推进 baseline）。
type UsageSnapshotProvider interface {
	DoUsage(reset bool) coremanager.UsageStats
}

// UsagePusher 周期性把 usage delta 主动 push 给 server。创建新帧时原子读取并
// reset xray 计数器，随后将帧保存在 pending 中直到 server ack。
//
// 重启容灾：第一次启动时先做一次 reset 清零 xray 累计计数（但不 push），
// 避免上报历史累计数据；之后的轮次在无未 ack 帧时取当前 delta 并 push。
//
// 并发约束（防双计）：任意时刻最多 1 个未 ack 的 seq。若仍有 pending，
// 本轮只重发旧帧，不取新快照、不分配新 seq。新产生的流量继续留在 xray
// 计数器中，待旧帧 ack 后的下一轮再读取。
type UsagePusher struct {
	api      UsageSnapshotProvider
	interval time.Duration
	logger   *slog.Logger
	ackWait  time.Duration

	mu     sync.Mutex
	sender Sender

	nextSeq atomic.Uint64
	pending sync.Map // seq → pendingUsage

	primed bool // 是否已完成第一次清零（baseline 建立）
}

type pendingUsage struct {
	seq  uint64
	body []byte
}

// NewUsagePusher 构造一个 pusher。interval==0 时取默认 60s。
func NewUsagePusher(api UsageSnapshotProvider, interval time.Duration) *UsagePusher {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &UsagePusher{
		api:      api,
		interval: interval,
		logger:   slog.Default(),
		ackWait:  interval, // 默认 ack 超时 = 一个 interval
	}
}

// SetAckTimeout 自定义等待 ack 的超时；<=0 时复用 interval。
func (p *UsagePusher) SetAckTimeout(d time.Duration) {
	if d > 0 {
		p.ackWait = d
	}
}

// SetSender 注入当前 session 的 Sender。传 nil 时下一轮 push 会被跳过。
// 一般在 Config.OnConnected 回调里调用：
//
//	cfg.OnConnected = func(ctx context.Context, s nodeagent.Sender) {
//	    pusher.SetSender(s)
//	}
func (p *UsagePusher) SetSender(s Sender) {
	p.mu.Lock()
	p.sender = s
	p.mu.Unlock()
}

func (p *UsagePusher) currentSender() Sender {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sender
}

// Run 阻塞循环 push usage，直到 ctx done。
func (p *UsagePusher) Run(ctx context.Context) error {
	t := time.NewTicker(p.interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			p.tick(ctx)
		}
	}
}

// Tick 执行单次推送循环。仅供测试 / 集成验证使用：生产路径走 Run。
func (p *UsagePusher) Tick(ctx context.Context) { p.tick(ctx) }

// tick 执行单次推送循环。导出名仅用于测试。
func (p *UsagePusher) tick(ctx context.Context) {
	sender := p.currentSender()
	if sender == nil {
		return
	}

	// 第一次启动：清零 xray 累计，建立 baseline，不 push。
	if !p.primed {
		_ = p.api.DoUsage(true)
		p.primed = true
		return
	}

	// 仍有未 ack 帧：只重发，不发新 seq（避免累计快照重叠双计）。
	var pendings []pendingUsage
	p.pending.Range(func(_, v any) bool {
		pendings = append(pendings, v.(pendingUsage))
		return true
	})
	if len(pendings) > 0 {
		sort.Slice(pendings, func(i, j int) bool { return pendings[i].seq < pendings[j].seq })
		for _, pu := range pendings {
			if err := sender.PushEvent("", "usage_push", pu.body, pu.seq); err != nil {
				p.logger.Warn("nodeagent: re-push usage failed", "seq", pu.seq, "err", err)
				return
			}
			p.waitAck(ctx, sender, pu.seq)
		}
		return
	}

	// 无 pending：原子读取并清零当前 delta，再 push 新 seq。即使同时发生
	// reset-based fallback，两次读取也只会有一次拿到这批计数。
	stats := p.api.DoUsage(true)
	body, err := json.Marshal(stats)
	if err != nil {
		p.logger.Warn("nodeagent: marshal usage failed", "err", err)
		return
	}

	seq := p.nextSeq.Add(1)
	p.pending.Store(seq, pendingUsage{seq: seq, body: body})

	if err := sender.PushEvent("", "usage_push", body, seq); err != nil {
		p.logger.Warn("nodeagent: push usage failed", "seq", seq, "err", err)
		return
	}

	p.waitAck(ctx, sender, seq)
}

// waitAck 异步等待 ack：成功后删除 pending；超时则保留给下轮重发。
// 重发也必须重新等待，否则一次超时或断线会让同一帧永久卡在 pending。
func (p *UsagePusher) waitAck(ctx context.Context, sender Sender, seq uint64) {
	go func(s Sender, seq uint64) {
		waitCtx, cancel := context.WithTimeout(ctx, p.ackWait)
		defer cancel()
		if err := s.WaitAck(waitCtx, seq); err != nil {
			p.logger.Debug("nodeagent: usage ack timeout", "seq", seq, "err", err)
			return
		}
		p.pending.Delete(seq)
	}(sender, seq)
}

// PendingCount 返回当前未 ack 的 seq 数量（仅供测试/监控用）。
func (p *UsagePusher) PendingCount() int {
	n := 0
	p.pending.Range(func(_, _ any) bool { n++; return true })
	return n
}

// 静态接口绑定，确保 *nodeapi.API 满足 UsageSnapshotProvider。
var _ UsageSnapshotProvider = (*nodeapi.API)(nil)
