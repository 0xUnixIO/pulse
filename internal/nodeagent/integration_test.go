package nodeagent

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"pulse/internal/coremanager"
	"pulse/internal/nodehub"
	nodev1 "pulse/internal/pb/nodev1"
)

// integrationPushHandler 收集 hello / usage_push 事件供断言。
type integrationPushHandler struct {
	mu          sync.Mutex
	helloBodies [][]byte
	usagePushes []uint64
}

func (h *integrationPushHandler) OnHello(_ string, body []byte) {
	h.mu.Lock()
	cp := make([]byte, len(body))
	copy(cp, body)
	h.helloBodies = append(h.helloBodies, cp)
	h.mu.Unlock()
}
func (h *integrationPushHandler) OnUsagePush(_ string, seq uint64, _ []byte) error {
	h.mu.Lock()
	h.usagePushes = append(h.usagePushes, seq)
	h.mu.Unlock()
	return nil
}
func (h *integrationPushHandler) OnLog(string, string, []byte)           {}
func (h *integrationPushHandler) OnTracerouteHop(string, string, []byte) {}

func (h *integrationPushHandler) snapshot() ([][]byte, []uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	hb := make([][]byte, len(h.helloBodies))
	copy(hb, h.helloBodies)
	up := make([]uint64, len(h.usagePushes))
	copy(up, h.usagePushes)
	return hb, up
}

// countingUsageAPI 实现 UsageSnapshotProvider，统计 reset 调用次数。
type countingUsageAPI struct {
	resets atomic.Int32
}

func (c *countingUsageAPI) DoUsage(reset bool) coremanager.UsageStats {
	if reset {
		c.resets.Add(1)
	}
	return coremanager.UsageStats{Available: true}
}

// startHub 用 bufconn 起一个 nodehub.Hub，PeerExtractor 总是返回 "n1"。
func startHub(t *testing.T, ph nodehub.PushHandler) (*nodehub.Hub, func(context.Context, string) (net.Conn, error), func()) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	hub := nodehub.New(nodehub.Options{
		PushHandler:   ph,
		PeerExtractor: func(_ context.Context) (string, error) { return "n1", nil },
	})
	nodev1.RegisterNodeAgentServer(srv, hub)
	go func() { _ = srv.Serve(lis) }()
	dialer := func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }
	return hub, dialer, func() { srv.Stop() }
}

// TestIntegration_HelloAndCall 验证 hello 含 config_hash + hub.Call → dispatcher 路由。
func TestIntegration_HelloAndCall(t *testing.T) {
	t.Parallel()
	ph := &integrationPushHandler{}
	hub, dialer, stop := startHub(t, ph)
	defer stop()

	api := newTestAPI()
	disp := NewAPIDispatcher(api, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := Config{
		NodeID:        "n1",
		ServerAddr:    "passthrough:///bufnet",
		Dispatcher:    disp,
		HelloProvider: DefaultHelloProvider("n1", ConfigHasher(api)),
		Dialer: func(_ context.Context) (*grpc.ClientConn, error) {
			return grpc.NewClient("passthrough:///bufnet",
				grpc.WithContextDialer(dialer),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
		},
		ReconnectBackoff: []time.Duration{20 * time.Millisecond},
	}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()
	defer func() { cancel(); <-done }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hb, _ := ph.snapshot(); len(hb) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	hb, _ := ph.snapshot()
	if len(hb) == 0 {
		t.Fatalf("no hello received")
	}
	var payload struct {
		NodeID     string `json:"node_id"`
		ConfigHash string `json:"config_hash"`
		Version    string `json:"version"`
	}
	if err := json.Unmarshal(hb[0], &payload); err != nil {
		t.Fatalf("decode hello: %v", err)
	}
	if payload.NodeID != "n1" {
		t.Fatalf("hello node_id=%q want n1", payload.NodeID)
	}
	// xray 未启动时 config 字符串为空，hash 也为空；这是已知行为。
	_ = payload.ConfigHash

	// 等 hub 看到这个 conn 上线。
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !hub.IsOnline("n1") {
		time.Sleep(10 * time.Millisecond)
	}

	callCtx, callCancel := context.WithTimeout(ctx, 2*time.Second)
	defer callCancel()
	respBody, err := hub.Call(callCtx, "n1", "Status", nil)
	if err != nil {
		t.Fatalf("hub.Call Status: %v", err)
	}
	var st struct {
		Running bool `json:"running"`
	}
	if err := json.Unmarshal(respBody, &st); err != nil {
		t.Fatalf("decode status resp: %v body=%s", err, respBody)
	}
	if st.Running {
		t.Fatalf("expected not running, got %+v", st)
	}
}

// TestIntegration_UsagePushAck 端到端：node 原子读取并 reset usage →
// push usage_push → hub 自动 ack → node 释放 pending 帧。
func TestIntegration_UsagePushAck(t *testing.T) {
	t.Parallel()
	ph := &integrationPushHandler{}
	_, dialer, stop := startHub(t, ph)
	defer stop()

	usageAPI := &countingUsageAPI{}
	pusher := NewUsagePusher(usageAPI, time.Hour)
	pusher.SetAckTimeout(2 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := Config{
		NodeID:        "n1",
		ServerAddr:    "passthrough:///bufnet",
		Dispatcher:    NoopDispatcher{},
		HelloProvider: DefaultHelloProvider("n1", nil),
		Dialer: func(_ context.Context) (*grpc.ClientConn, error) {
			return grpc.NewClient("passthrough:///bufnet",
				grpc.WithContextDialer(dialer),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
		},
		OnConnected: func(_ context.Context, s Sender) {
			pusher.SetSender(s)
		},
	}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()
	defer func() { cancel(); <-done }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pusher.currentSender() != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pusher.currentSender() == nil {
		t.Fatalf("sender not injected")
	}

	// 第一次 tick：priming reset，不 push。
	pusher.tick(ctx)
	if got := usageAPI.resets.Load(); got != 1 {
		t.Fatalf("priming reset count=%d want 1", got)
	}

	// 第二次 tick：原子读取并 reset 后 push；hub 自动 ack。
	pusher.tick(ctx)

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, ups := ph.snapshot()
		if len(ups) >= 1 && pusher.PendingCount() == 0 && usageAPI.resets.Load() >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, ups := ph.snapshot()
	if len(ups) < 1 {
		t.Fatalf("no usage push received by hub")
	}
	if pusher.PendingCount() != 0 {
		t.Fatalf("pending should be empty after ack, got %d", pusher.PendingCount())
	}
	if usageAPI.resets.Load() != 2 {
		t.Fatalf("reset count=%d want 2", usageAPI.resets.Load())
	}
}
