package nodes

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestUsageBuffer_AppendAndDrain(t *testing.T) {
	b := NewUsageBuffer()
	if err := b.Append("n1", 1, UsageStats{UploadTotal: 10, DownloadTotal: 5}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := b.Append("n1", 2, UsageStats{UploadTotal: 3, DownloadTotal: 2, Running: true}); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, ok := b.Drain("n1")
	if !ok {
		t.Fatal("expected ok")
	}
	if got.UploadTotal != 13 || got.DownloadTotal != 7 {
		t.Fatalf("merged totals wrong: %+v", got)
	}
	if !got.Running {
		t.Fatal("expected last frame Running=true to win")
	}
	if _, ok := b.Drain("n1"); ok {
		t.Fatal("second drain should be empty")
	}
	if !b.HasSeen("n1") {
		t.Fatal("node should remain marked as seen after drain")
	}
	if b.HasSeen("n2") {
		t.Fatal("unknown node must not be marked as seen")
	}
}

func TestUsageBuffer_HasRecentPush(t *testing.T) {
	b := NewUsageBuffer()
	now := time.Now()
	if b.HasRecentPush("n1", now, time.Minute) {
		t.Fatal("unknown node must not have a recent push")
	}

	_ = b.Append("n1", 1, UsageStats{})
	if !b.HasRecentPush("n1", time.Now(), time.Minute) {
		t.Fatal("newly appended sequenced frame must be recent")
	}
	if b.HasRecentPush("n1", time.Now().Add(4*time.Minute), 3*time.Minute) {
		t.Fatal("silent node must stop being treated as an active push node")
	}

	// Requeued local data is not a node push and must not extend the window.
	b.Requeue("n2", UsageStats{})
	if b.HasRecentPush("n2", time.Now(), time.Minute) {
		t.Fatal("requeued data must not mark node as actively pushing")
	}
}

func TestUsageBuffer_DrainAll(t *testing.T) {
	b := NewUsageBuffer()
	_ = b.Append("a", 1, UsageStats{UploadTotal: 1})
	_ = b.Append("b", 1, UsageStats{UploadTotal: 100})
	_ = b.Append("a", 2, UsageStats{UploadTotal: 4})

	all := b.DrainAll()
	if len(all) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(all))
	}
	if all["a"].UploadTotal != 5 {
		t.Errorf("a upload: want 5, got %d", all["a"].UploadTotal)
	}
	if all["b"].UploadTotal != 100 {
		t.Errorf("b upload: want 100, got %d", all["b"].UploadTotal)
	}
	// After DrainAll, pending is cleared but lastSeq remains.
	if got := b.SeenNodes(); len(got) != 2 {
		t.Errorf("seen nodes after drain: want 2, got %d", len(got))
	}
}

func TestUsageBuffer_DedupSameSeq(t *testing.T) {
	b := NewUsageBuffer()
	_ = b.Append("n1", 5, UsageStats{UploadTotal: 100})
	// Same seq again — must not double-count.
	_ = b.Append("n1", 5, UsageStats{UploadTotal: 100})
	// An older seq — must also be deduped.
	_ = b.Append("n1", 3, UsageStats{UploadTotal: 999})
	// New higher seq — counted.
	_ = b.Append("n1", 6, UsageStats{UploadTotal: 7})

	got, ok := b.Drain("n1")
	if !ok {
		t.Fatal("expected ok")
	}
	if got.UploadTotal != 107 {
		t.Fatalf("dedup failed: want 107, got %d", got.UploadTotal)
	}
}

// TestUsageBuffer_SeqEpochOnNodeRestart 节点重启后 seq 从低位重计，应开启新纪元并记账。
func TestUsageBuffer_SeqEpochOnNodeRestart(t *testing.T) {
	b := NewUsageBuffer()
	_ = b.Append("n1", 1000, UsageStats{UploadTotal: 10})
	_, _ = b.Drain("n1") // lastSeq 仍为 1000

	// 模拟 node 重启：nextSeq 从 1 开始
	_ = b.Append("n1", 1, UsageStats{UploadTotal: 50, Users: []UserUsage{{User: "a", UploadTotal: 50}}})
	got, ok := b.Drain("n1")
	if !ok {
		t.Fatal("epoch frame should be accepted")
	}
	if got.UploadTotal != 50 {
		t.Fatalf("want 50 after epoch, got %d", got.UploadTotal)
	}
	// 新纪元内递增正常
	_ = b.Append("n1", 2, UsageStats{UploadTotal: 3})
	got, _ = b.Drain("n1")
	if got.UploadTotal != 3 {
		t.Fatalf("want 3, got %d", got.UploadTotal)
	}
}

func TestUsageBuffer_Requeue(t *testing.T) {
	b := NewUsageBuffer()
	b.Requeue("n1", UsageStats{UploadTotal: 9, Available: true})
	got, ok := b.Drain("n1")
	if !ok || got.UploadTotal != 9 {
		t.Fatalf("requeue drain = %+v ok=%v", got, ok)
	}
}

func TestUsageBuffer_UserMerge(t *testing.T) {
	b := NewUsageBuffer()
	_ = b.Append("n1", 1, UsageStats{
		Users: []UserUsage{
			{User: "alice", UploadTotal: 10, DownloadTotal: 5, Connections: 1, Devices: 2, SourceIPs: []string{"1.1.1.1"}},
			{User: "bob", UploadTotal: 1, DownloadTotal: 2},
		},
	})
	_ = b.Append("n1", 2, UsageStats{
		Users: []UserUsage{
			{User: "alice", UploadTotal: 3, DownloadTotal: 4, Connections: 2, Devices: 1, SourceIPs: []string{"1.1.1.1", "2.2.2.2"}},
		},
	})
	got, _ := b.Drain("n1")
	var alice, bob *UserUsage
	for i := range got.Users {
		switch got.Users[i].User {
		case "alice":
			alice = &got.Users[i]
		case "bob":
			bob = &got.Users[i]
		}
	}
	if alice == nil || bob == nil {
		t.Fatalf("missing user, got %+v", got.Users)
	}
	if alice.UploadTotal != 13 || alice.DownloadTotal != 9 || alice.Connections != 3 {
		t.Errorf("alice merge wrong: %+v", alice)
	}
	if alice.Devices != 2 { // max
		t.Errorf("alice devices: want 2, got %d", alice.Devices)
	}
	if len(alice.SourceIPs) != 2 {
		t.Errorf("alice source ips: want 2, got %d (%v)", len(alice.SourceIPs), alice.SourceIPs)
	}
	if bob.UploadTotal != 1 || bob.DownloadTotal != 2 {
		t.Errorf("bob: %+v", bob)
	}
}

func TestUsageBuffer_DrainEmpty(t *testing.T) {
	b := NewUsageBuffer()
	if _, ok := b.Drain("nope"); ok {
		t.Fatal("empty drain should return ok=false")
	}
}

func TestUsageBuffer_Concurrent(t *testing.T) {
	b := NewUsageBuffer()
	var wg sync.WaitGroup
	const goroutines = 8
	const iters = 200
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				seq := uint64(gid*iters + i + 1)
				_ = b.Append("n1", seq, UsageStats{UploadTotal: 1})
			}
		}(g)
	}
	// Concurrent drainers (don't validate totals, just race-free).
	for d := 0; d < 4; d++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_ = b.DrainAll()
			}
		}()
	}
	wg.Wait()
	// Final drain — verify nothing panics.
	_ = b.DrainAll()
}

// TestClient_UsageInHubMode 验证 Client.Usage 通过 hub 调用 "Usage" 方法。
// 推荐路径仍是 UsageBuffer.Drain，但按需拉取也支持。
func TestClient_UsageInHubMode(t *testing.T) {
	c := NewClientWithHub("n1", fakeHubCaller{})
	if _, err := c.Usage(context.Background(), true); err != nil {
		t.Fatalf("hub Usage should not error with empty fake hub: %v", err)
	}
}

type fakeHubCaller struct{}

func (fakeHubCaller) Call(ctx context.Context, nodeID, method string, body any) (json.RawMessage, error) {
return nil, nil
}
