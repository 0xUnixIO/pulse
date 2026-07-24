package jobs

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestScheduler_RunsImmediatelyByDefault 保证未设置 InitialDelay 的任务
// 仍在 Start 时立即执行一次（既有行为不能被延迟特性破坏）。
func TestScheduler_RunsImmediatelyByDefault(t *testing.T) {
	var runs atomic.Int32
	done := make(chan struct{})
	s := NewScheduler(nil)
	s.Add(Job{
		Name:     "immediate",
		Interval: time.Hour,
		Fn: func(context.Context) error {
			if runs.Add(1) == 1 {
				close(done)
			}
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("默认任务未在 Start 时立即执行")
	}
}

// TestScheduler_InitialDelayDefersFirstRun 保证 InitialDelay 推迟首次执行。
//
// reconcile-config 依赖此行为：server 刚启动时节点尚未完成重连与 self-sync，
// 立即对账会误判为配置漂移并重启全网节点。
func TestScheduler_InitialDelayDefersFirstRun(t *testing.T) {
	var runs atomic.Int32
	s := NewScheduler(nil)
	s.Add(Job{
		Name:         "delayed",
		Interval:     time.Hour,
		InitialDelay: 300 * time.Millisecond,
		Fn: func(context.Context) error {
			runs.Add(1)
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	time.Sleep(80 * time.Millisecond)
	if got := runs.Load(); got != 0 {
		t.Fatalf("InitialDelay 未生效，任务已执行 %d 次", got)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runs.Load() > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("InitialDelay 过后任务仍未执行")
}

// TestScheduler_InitialDelayRespectsCancel 保证延迟等待期间 ctx 取消能立即退出，
// 不会在 server 关闭后仍执行一次。
func TestScheduler_InitialDelayRespectsCancel(t *testing.T) {
	var runs atomic.Int32
	s := NewScheduler(nil)
	s.Add(Job{
		Name:         "cancelled",
		Interval:     time.Hour,
		InitialDelay: 500 * time.Millisecond,
		Fn: func(context.Context) error {
			runs.Add(1)
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	cancel()
	time.Sleep(700 * time.Millisecond)

	if got := runs.Load(); got != 0 {
		t.Fatalf("ctx 取消后任务仍执行了 %d 次", got)
	}
}
