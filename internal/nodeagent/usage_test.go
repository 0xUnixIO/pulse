package nodeagent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pulse/internal/coremanager"
)

// mockUsageAPI 计数 DoUsage 调用，返回构造的 stats。
type mockUsageAPI struct {
	mu    sync.Mutex
	calls []bool // 每次调用的 reset 参数
	stats coremanager.UsageStats
}

func (m *mockUsageAPI) DoUsage(reset bool) coremanager.UsageStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, reset)
	if reset {
		// reset 之后下次取仍返回当前 stats（baseline 由 server 决定）
		return m.stats
	}
	return m.stats
}

func (m *mockUsageAPI) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func (m *mockUsageAPI) resetCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.calls {
		if r {
			n++
		}
	}
	return n
}

func (m *mockUsageAPI) callArgs() []bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]bool(nil), m.calls...)
}

// mockSender 记录 push 与提供 ack 控制。
type mockSender struct {
	mu       sync.Mutex
	pushed   []uint64
	ackCh    map[uint64]chan struct{}
	failNext atomic.Bool
}

func newMockSender() *mockSender {
	return &mockSender{ackCh: make(map[uint64]chan struct{})}
}

func (s *mockSender) PushEvent(reqID, event string, body []byte, seq uint64) error {
	if s.failNext.Swap(false) {
		return errors.New("forced push error")
	}
	s.mu.Lock()
	s.pushed = append(s.pushed, seq)
	if _, ok := s.ackCh[seq]; !ok {
		s.ackCh[seq] = make(chan struct{})
	}
	s.mu.Unlock()
	return nil
}

func (s *mockSender) WaitAck(ctx context.Context, seq uint64) error {
	s.mu.Lock()
	ch, ok := s.ackCh[seq]
	if !ok {
		ch = make(chan struct{})
		s.ackCh[seq] = ch
	}
	s.mu.Unlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *mockSender) ack(seq uint64) {
	s.mu.Lock()
	ch, ok := s.ackCh[seq]
	if !ok {
		ch = make(chan struct{})
		s.ackCh[seq] = ch
	}
	s.mu.Unlock()
	close(ch)
}

func (s *mockSender) pushedSeqs() []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]uint64, len(s.pushed))
	copy(out, s.pushed)
	return out
}

func TestUsagePusher_PrimingFirstTickResetsNoPush(t *testing.T) {
	t.Parallel()
	api := &mockUsageAPI{}
	sender := newMockSender()
	p := NewUsagePusher(api, time.Hour)
	p.SetSender(sender)

	p.tick(context.Background())

	if api.resetCount() != 1 {
		t.Fatalf("first tick should reset baseline once, got %d", api.resetCount())
	}
	if got := sender.pushedSeqs(); len(got) != 0 {
		t.Fatalf("first tick should not push, got %v", got)
	}
}

func TestUsagePusher_ResetWhenFrameCreatedThenAck(t *testing.T) {
	t.Parallel()
	api := &mockUsageAPI{}
	sender := newMockSender()
	p := NewUsagePusher(api, time.Hour)
	p.SetAckTimeout(time.Second)
	p.SetSender(sender)

	ctx := context.Background()
	p.tick(ctx) // priming reset
	p.tick(ctx) // first real push

	pushed := sender.pushedSeqs()
	if len(pushed) != 1 {
		t.Fatalf("want 1 push, got %v", pushed)
	}
	if got := api.callArgs(); len(got) != 2 || !got[0] || !got[1] {
		t.Fatalf("usage calls=%v, want two reset reads", got)
	}
	seq := pushed[0]
	sender.ack(seq)

	// ACK only releases the saved frame; it must not reset counters again,
	// otherwise traffic accrued after frame creation would be discarded.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.PendingCount() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := api.callArgs(); len(got) != 2 {
		t.Fatalf("ack unexpectedly read usage again: calls=%v", got)
	}
	if p.PendingCount() != 0 {
		t.Fatalf("pending should be empty after ack, got %d", p.PendingCount())
	}
}

func TestUsagePusher_AckTimeoutKeepsPending(t *testing.T) {
	t.Parallel()
	api := &mockUsageAPI{}
	sender := newMockSender()
	p := NewUsagePusher(api, time.Hour)
	p.SetAckTimeout(20 * time.Millisecond)
	p.SetSender(sender)

	ctx := context.Background()
	p.tick(ctx) // prime
	p.tick(ctx) // push seq=1

	// 等 ack timeout 触发但 pending 应保留。
	time.Sleep(80 * time.Millisecond)
	if p.PendingCount() != 1 {
		t.Fatalf("pending should still be 1 after ack timeout, got %d", p.PendingCount())
	}

	// 下一轮 tick：只重发 seq=1，不得发新 seq（避免累计快照重叠双计）。
	p.tick(ctx)
	pushed := sender.pushedSeqs()
	count1 := 0
	hasNew := false
	for _, s := range pushed {
		if s == 1 {
			count1++
		}
		if s == 2 {
			hasNew = true
		}
	}
	if count1 < 2 {
		t.Fatalf("seq=1 should have been re-pushed, total occurrences=%d, all=%v", count1, pushed)
	}
	if hasNew {
		t.Fatalf("must not open seq=2 while seq=1 pending, got pushes %v", pushed)
	}
	if p.PendingCount() != 1 {
		t.Fatalf("pending should still be 1, got %d", p.PendingCount())
	}
}

func TestUsagePusher_RetryWaitsForAck(t *testing.T) {
	t.Parallel()
	api := &mockUsageAPI{}
	sender := newMockSender()
	p := NewUsagePusher(api, time.Hour)
	p.SetAckTimeout(20 * time.Millisecond)
	p.SetSender(sender)

	ctx := context.Background()
	p.tick(ctx) // prime
	p.tick(ctx) // push seq=1
	time.Sleep(50 * time.Millisecond)
	if p.PendingCount() != 1 {
		t.Fatalf("pending should survive the first ack timeout, got %d", p.PendingCount())
	}

	p.SetAckTimeout(time.Second)
	p.tick(ctx) // re-push seq=1 and install a fresh ack waiter
	sender.ack(1)

	deadline := time.Now().Add(time.Second)
	for p.PendingCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p.PendingCount() != 0 {
		t.Fatal("retry ack should remove the pending frame")
	}
}

// TestUsagePusher_NoNewSeqUntilAck 验证 ack 前持续 tick 不会累积多个 pending seq。
func TestUsagePusher_NoNewSeqUntilAck(t *testing.T) {
	t.Parallel()
	api := &mockUsageAPI{}
	sender := newMockSender()
	p := NewUsagePusher(api, time.Hour)
	p.SetAckTimeout(time.Hour) // 故意不 ack
	p.SetSender(sender)

	ctx := context.Background()
	p.tick(ctx) // prime
	p.tick(ctx) // seq=1
	p.tick(ctx) // re-push only
	p.tick(ctx) // re-push only
	if p.PendingCount() != 1 {
		t.Fatalf("PendingCount=%d, want 1", p.PendingCount())
	}
	for _, s := range sender.pushedSeqs() {
		if s != 1 {
			t.Fatalf("unexpected seq %d while first still pending", s)
		}
	}
}

func TestUsagePusher_NoSenderSkips(t *testing.T) {
	t.Parallel()
	api := &mockUsageAPI{}
	p := NewUsagePusher(api, time.Hour)
	p.tick(context.Background())
	if api.callCount() != 0 {
		t.Fatalf("no sender should skip DoUsage, got calls=%d", api.callCount())
	}
}

func TestUsagePusher_RunCancelExits(t *testing.T) {
	t.Parallel()
	api := &mockUsageAPI{}
	p := NewUsagePusher(api, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("Run did not return on cancel")
	}
}
