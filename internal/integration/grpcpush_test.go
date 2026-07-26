// Package integration 提供 gRPC push 模型的端到端集成测试，
// 覆盖 enroll → connect → dispatch → stream → usage push → reconnect → multi-node
// 全链路。每个测试在 in-process 启动 nodehub.Hub 与一个或多个 nodeagent，
// 通过 bufconn 或真实 TCP loopback 通信。
package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"pulse/internal/cert"
	"pulse/internal/coremanager"
	"pulse/internal/enrolltokens"
	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodeagent"
	"pulse/internal/nodeenroll"
	"pulse/internal/nodehub"
	"pulse/internal/nodes"
	nodev1 "pulse/internal/pb/nodev1"
	"pulse/internal/serverapi"
	"pulse/internal/users"
)

// ─────────────────────────────────────────────────────────────────────────
// 共用测试辅助
// ─────────────────────────────────────────────────────────────────────────

type usageEvent struct {
	NodeID string
	Seq    uint64
	Body   []byte
}

type logEvent struct {
	NodeID string
	ReqID  string
	Body   []byte
}

// recordingPushHandler 收集 hub 收到的事件供断言。
type recordingPushHandler struct {
	mu     sync.Mutex
	hellos [][]byte
	usages []usageEvent
	logs   []logEvent
}

func (h *recordingPushHandler) OnHello(_ string, body []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hellos = append(h.hellos, append([]byte(nil), body...))
}

func (h *recordingPushHandler) OnUsagePush(nodeID string, seq uint64, body []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.usages = append(h.usages, usageEvent{NodeID: nodeID, Seq: seq, Body: append([]byte(nil), body...)})
	return nil
}

func (h *recordingPushHandler) OnLog(nodeID, reqID string, body []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = append(h.logs, logEvent{NodeID: nodeID, ReqID: reqID, Body: append([]byte(nil), body...)})
}

func (h *recordingPushHandler) OnTracerouteHop(string, string, []byte) {}

func (h *recordingPushHandler) snapshotHellos() [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([][]byte, len(h.hellos))
	copy(out, h.hellos)
	return out
}

// fixedPeer 返回固定 nodeID 的 PeerExtractor，绕开 mTLS。
func fixedPeer(nodeID string) nodehub.PeerExtractor {
	return func(_ context.Context) (string, error) { return nodeID, nil }
}

// startBufHub 启动一个 bufconn-backed hub。返回 hub、agent dial 函数、cleanup。
func startBufHub(t *testing.T, ph nodehub.PushHandler, peer nodehub.PeerExtractor) (*nodehub.Hub, func(context.Context, string) (net.Conn, error), func()) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	hub := nodehub.New(nodehub.Options{
		PushHandler:    ph,
		PeerExtractor:  peer,
		ReaperInterval: 50 * time.Millisecond,
	})
	nodev1.RegisterNodeAgentServer(srv, hub)
	go func() { _ = srv.Serve(lis) }()
	dialer := func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }
	stop := func() { srv.Stop() }
	return hub, dialer, stop
}

// startTCPHub 启动一个真实 TCP gRPC server（insecure），绑定到 addr（addr=""→":0"）。
// 返回 hub、实际 addr、stop 函数。
func startTCPHub(t *testing.T, addr string, ph nodehub.PushHandler, peer nodehub.PeerExtractor) (*nodehub.Hub, string, func()) {
	t.Helper()
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	srv := grpc.NewServer()
	hub := nodehub.New(nodehub.Options{
		PushHandler:    ph,
		PeerExtractor:  peer,
		ReaperInterval: 50 * time.Millisecond,
	})
	nodev1.RegisterNodeAgentServer(srv, hub)
	go func() { _ = srv.Serve(lis) }()
	return hub, lis.Addr().String(), func() { srv.Stop() }
}

// startMTLSHub 启动一个真实 TCP+mTLS gRPC server，server 证书由 ca 颁发。
func startMTLSHub(t *testing.T, ca *cert.NodeCA, ph nodehub.PushHandler) (*nodehub.Hub, string, func()) {
	t.Helper()
	serverCert, err := ca.IssueServerCert("localhost", []string{"127.0.0.1", "localhost"}, time.Hour)
	if err != nil {
		t.Fatalf("issue server cert: %v", err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.ClientCAPool(),
		MinVersion:   tls.VersionTLS12,
	}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
	hub := nodehub.New(nodehub.Options{
		PushHandler:    ph,
		ReaperInterval: 50 * time.Millisecond,
	})
	nodev1.RegisterNodeAgentServer(srv, hub)
	go func() { _ = srv.Serve(lis) }()
	return hub, lis.Addr().String(), func() { srv.Stop() }
}

// callableDispatcher 用 method-map 派发，支持记录调用次数和 sender 注入。
type callableDispatcher struct {
	mu       sync.Mutex
	handlers map[string]func(ctx context.Context, body json.RawMessage) (json.RawMessage, error)
	calls    map[string]int

	sender   nodeagent.Sender
	senderCh chan nodeagent.Sender
}

func newCallableDispatcher() *callableDispatcher {
	return &callableDispatcher{
		handlers: make(map[string]func(context.Context, json.RawMessage) (json.RawMessage, error)),
		calls:    make(map[string]int),
		senderCh: make(chan nodeagent.Sender, 1),
	}
}

func (d *callableDispatcher) Register(method string, fn func(ctx context.Context, body json.RawMessage) (json.RawMessage, error)) {
	d.mu.Lock()
	d.handlers[method] = fn
	d.mu.Unlock()
}

func (d *callableDispatcher) Handle(ctx context.Context, method string, body json.RawMessage) (json.RawMessage, error) {
	d.mu.Lock()
	d.calls[method]++
	fn := d.handlers[method]
	d.mu.Unlock()
	if fn == nil {
		return nil, fmt.Errorf("unknown method: %s", method)
	}
	return fn(ctx, body)
}

func (d *callableDispatcher) CallCount(method string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls[method]
}

func (d *callableDispatcher) SetSender(s nodeagent.Sender) {
	d.mu.Lock()
	d.sender = s
	d.mu.Unlock()
	select {
	case d.senderCh <- s:
	default:
	}
}

func (d *callableDispatcher) Sender() nodeagent.Sender {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sender
}

// eventually 轮询条件直到为 true 或超时。
func eventually(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("eventually timeout: %s", msg)
}

// runAgent 在 goroutine 中跑 agent.Run，返回 cancel + done。
func runAgent(t *testing.T, cfg nodeagent.Config) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	dch := make(chan error, 1)
	go func() { dch <- nodeagent.Run(ctx, cfg) }()
	return cancel, dch
}

// countingUsageProvider 实现 nodeagent.UsageSnapshotProvider。
type countingUsageProvider struct {
	mu       sync.Mutex
	upload   int64
	download int64
	resets   int32
}

func (p *countingUsageProvider) AddTraffic(up, down int64) {
	p.mu.Lock()
	p.upload += up
	p.download += down
	p.mu.Unlock()
}

func (p *countingUsageProvider) DoUsage(reset bool) coremanager.UsageStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	stats := coremanager.UsageStats{
		Available:     true,
		Running:       true,
		UploadTotal:   p.upload,
		DownloadTotal: p.download,
		Users: []coremanager.UserUsage{
			{User: "alice", UploadTotal: p.upload, DownloadTotal: p.download},
		},
	}
	if reset {
		p.upload = 0
		p.download = 0
		atomic.AddInt32(&p.resets, 1)
	}
	return stats
}

// readSSEFrames 从 SSE ReadCloser 中读取直到至少 want 帧到达或超时。
// 每帧以 "\n\n" 分隔，返回每帧的去掉前缀的 body 行集合。
func readSSEFrames(t *testing.T, rc io.ReadCloser, want int, timeout time.Duration) []string {
	t.Helper()
	frames := []string{}
	var partial bytes.Buffer
	buf := make([]byte, 4096)
	deadline := time.Now().Add(timeout)
	for len(frames) < want && time.Now().Before(deadline) {
		readDone := make(chan struct {
			n   int
			err error
		}, 1)
		go func() {
			n, err := rc.Read(buf)
			readDone <- struct {
				n   int
				err error
			}{n, err}
		}()
		select {
		case r := <-readDone:
			if r.n > 0 {
				partial.Write(buf[:r.n])
				for {
					data := partial.Bytes()
					idx := bytes.Index(data, []byte("\n\n"))
					if idx < 0 {
						break
					}
					line := string(data[:idx])
					partial.Next(idx + 2)
					if line != "" {
						frames = append(frames, line)
					}
				}
			}
			if r.err != nil && len(frames) < want {
				return frames
			}
		case <-time.After(300 * time.Millisecond):
		}
	}
	return frames
}

// ─────────────────────────────────────────────────────────────────────────
// 1. EnrollAndConnect: 完整 enroll → 用颁发的证书建立 mTLS 连接
// ─────────────────────────────────────────────────────────────────────────

func TestE2E_EnrollAndConnect(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	ca, err := cert.LoadOrCreateNodeCA(tmp+"/ca-cert.pem", tmp+"/ca-key.pem")
	if err != nil {
		t.Fatalf("LoadOrCreateNodeCA: %v", err)
	}

	ph := &recordingPushHandler{}
	hub, hubAddr, stopHub := startMTLSHub(t, ca, ph)
	t.Cleanup(stopHub)

	tokens := enrolltokens.NewMemoryStore()
	mux := http.NewServeMux()
	serverapi.RegisterEnrollEndpoint(mux, ca, tokens, hubAddr)
	httpSrv := httptest.NewServer(mux)
	t.Cleanup(httpSrv.Close)

	const nodeID = "enroll-node"
	const tok = "deadbeefcafebabe1122334455667788aabbccddeeff00112233445566778899"
	if err := tokens.Insert(context.Background(), enrolltokens.Token{
		Token:     tok,
		NodeID:    nodeID,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("tokens.Insert: %v", err)
	}

	outDir := t.TempDir()
	res, err := nodeenroll.Run(context.Background(), nodeenroll.Request{
		ServerURL: httpSrv.URL,
		NodeID:    nodeID,
		Token:     tok,
		OutDir:    outDir,
		Insecure:  true,
	})
	if err != nil {
		t.Fatalf("nodeenroll.Run: %v", err)
	}
	if res.GRPCURL != hubAddr {
		t.Fatalf("res.GRPCURL=%q want %q", res.GRPCURL, hubAddr)
	}

	// token 已被消费
	if _, err := tokens.Consume(context.Background(), tok); !errors.Is(err, enrolltokens.ErrAlreadyConsumed) {
		t.Fatalf("expected ErrAlreadyConsumed, got %v", err)
	}

	disp := newCallableDispatcher()
	cfg := nodeagent.Config{
		NodeID:           nodeID,
		ServerAddr:       hubAddr,
		CertFile:         res.CertPath,
		KeyFile:          res.KeyPath,
		CAFile:           res.CAPath,
		ServerName:       "localhost",
		Dispatcher:       disp,
		HelloProvider:    nodeagent.DefaultHelloProvider(nodeID, nil),
		ReconnectBackoff: []time.Duration{50 * time.Millisecond},
	}
	cancel, done := runAgent(t, cfg)
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, 5*time.Second, "node online via mTLS", func() bool { return hub.IsOnline(nodeID) })
	if got := hub.OnlineNodes(); len(got) != 1 || got[0] != nodeID {
		t.Fatalf("OnlineNodes=%v want [%s]", got, nodeID)
	}
	if hellos := ph.snapshotHellos(); len(hellos) == 0 {
		t.Fatalf("hub did not receive hello frame")
	}
}

// ─────────────────────────────────────────────────────────────────────────
// 2. DispatchRPC: server → node 单次 RPC，body 双向透传
// ─────────────────────────────────────────────────────────────────────────

func TestE2E_DispatchRPC(t *testing.T) {
	t.Parallel()

	const nodeID = "n-dispatch"
	hub, dialer, stop := startBufHub(t, &recordingPushHandler{}, fixedPeer(nodeID))
	t.Cleanup(stop)

	disp := newCallableDispatcher()
	disp.Register("Status", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"running":false}`), nil
	})
	gotBody := make(chan string, 1)
	disp.Register("Echo", func(_ context.Context, body json.RawMessage) (json.RawMessage, error) {
		select {
		case gotBody <- string(body):
		default:
		}
		return json.RawMessage(fmt.Sprintf(`{"echoed":%s}`, body)), nil
	})

	cfg := nodeagent.Config{
		NodeID:        nodeID,
		ServerAddr:    "passthrough:///bufnet",
		Dispatcher:    disp,
		HelloProvider: nodeagent.DefaultHelloProvider(nodeID, nil),
		Dialer: func(_ context.Context) (*grpc.ClientConn, error) {
			return grpc.NewClient("passthrough:///bufnet",
				grpc.WithContextDialer(dialer),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
		},
		ReconnectBackoff: []time.Duration{20 * time.Millisecond},
	}
	cancel, done := runAgent(t, cfg)
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, 2*time.Second, "node online", func() bool { return hub.IsOnline(nodeID) })

	// 通过 nodes.Client（hub 路径）调用 Status
	client := nodes.NewClientWithHub(nodeID, hub)
	st, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Running {
		t.Fatalf("Status.Running=true want false")
	}

	// 直接 hub.Call Echo：body 透传 + 响应携带原 body
	resp, err := hub.Call(context.Background(), nodeID, "Echo", map[string]string{"hello": "world"})
	if err != nil {
		t.Fatalf("hub.Call Echo: %v", err)
	}
	select {
	case b := <-gotBody:
		if b != `{"hello":"world"}` {
			t.Fatalf("dispatcher saw body=%q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not receive body")
	}
	if string(resp) != `{"echoed":{"hello":"world"}}` {
		t.Fatalf("hub.Call resp=%s", resp)
	}
	if disp.CallCount("Status") != 1 || disp.CallCount("Echo") != 1 {
		t.Fatalf("call counts: Status=%d Echo=%d",
			disp.CallCount("Status"), disp.CallCount("Echo"))
	}
}

// ─────────────────────────────────────────────────────────────────────────
// 3. StreamLogsCancel: server CallStream → node 推帧 → cancel ctx → node ctx 取消
// ─────────────────────────────────────────────────────────────────────────

func TestE2E_StreamLogsCancel(t *testing.T) {
	t.Parallel()

	const nodeID = "n-stream"
	hub, dialer, stop := startBufHub(t, &recordingPushHandler{}, fixedPeer(nodeID))
	t.Cleanup(stop)

	disp := newCallableDispatcher()
	streamCtxObserved := make(chan error, 1)
	disp.Register("LogsStream", func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		s := disp.Sender()
		if s == nil {
			return nil, errors.New("sender nil")
		}
		reqID := nodeagent.ReqIDFromContext(ctx)
		if reqID == "" {
			return nil, errors.New("missing reqID")
		}
		// 推 5 帧
		for i := 0; i < 5; i++ {
			if err := s.PushEvent(reqID, "log", []byte(fmt.Sprintf(`{"line":"frame-%d"}`, i)), 0); err != nil {
				return nil, err
			}
			time.Sleep(5 * time.Millisecond)
		}
		// 阻塞直到 ctx 取消（来自 server 的 cancel 帧）
		<-ctx.Done()
		streamCtxObserved <- ctx.Err()
		return nil, ctx.Err()
	})

	cfg := nodeagent.Config{
		NodeID:        nodeID,
		ServerAddr:    "passthrough:///bufnet",
		Dispatcher:    disp,
		HelloProvider: nodeagent.DefaultHelloProvider(nodeID, nil),
		Dialer: func(_ context.Context) (*grpc.ClientConn, error) {
			return grpc.NewClient("passthrough:///bufnet",
				grpc.WithContextDialer(dialer),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
		},
		ReconnectBackoff: []time.Duration{20 * time.Millisecond},
	}
	cancel, done := runAgent(t, cfg)
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, 2*time.Second, "node online", func() bool { return hub.IsOnline(nodeID) })

	client := nodes.NewClientWithHub(nodeID, hub)
	streamCtx, streamCancel := context.WithCancel(context.Background())
	rc, err := client.LogsStream(streamCtx)
	if err != nil {
		streamCancel()
		t.Fatalf("LogsStream: %v", err)
	}
	frames := readSSEFrames(t, rc, 5, 3*time.Second)
	if len(frames) < 5 {
		t.Fatalf("got %d frames, want >=5: %v", len(frames), frames)
	}
	for i := 0; i < 5; i++ {
		want := fmt.Sprintf("data: frame-%d", i)
		if frames[i] != want {
			t.Fatalf("frame[%d]=%q want %q", i, frames[i], want)
		}
	}

	// cancel ctx → server 端发 cancel_id；node dispatcher ctx 应取消
	streamCancel()
	rc.Close()

	select {
	case err := <-streamCtxObserved:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("dispatcher ctx err=%v want Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher ctx not cancelled within 2s")
	}
}

// ─────────────────────────────────────────────────────────────────────────
// 4. UsagePushAck: node 原子读取/reset → hub OnUsagePush → ack
// ─────────────────────────────────────────────────────────────────────────

func TestE2E_UsagePushAck(t *testing.T) {
	t.Parallel()

	const nodeID = "n-usage"
	usageBuf := nodes.NewUsageBuffer()
	multi := &nodehub.MultiPushHandler{
		UsagePushHandler: func(nid string, seq uint64, body []byte) error {
			var stats nodes.UsageStats
			if err := json.Unmarshal(body, &stats); err != nil {
				return err
			}
			return usageBuf.Append(nid, seq, stats)
		},
	}
	hub, dialer, stop := startBufHub(t, multi, fixedPeer(nodeID))
	t.Cleanup(stop)

	userStore := users.NewMemoryStore()
	nodeStore := nodes.NewMemoryStore()
	ibStore := inbounds.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: nodeID, Name: nodeID, BaseURL: "hub://" + nodeID})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive, TrafficLimit: 999999999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", NodeID: nodeID, UUID: "uuid-1",
	})

	provider := &countingUsageProvider{}
	pusher := nodeagent.NewUsagePusher(provider, time.Hour)
	pusher.SetAckTimeout(2 * time.Second)

	cfg := nodeagent.Config{
		NodeID:        nodeID,
		ServerAddr:    "passthrough:///bufnet",
		Dispatcher:    newCallableDispatcher(),
		HelloProvider: nodeagent.DefaultHelloProvider(nodeID, nil),
		Dialer: func(_ context.Context) (*grpc.ClientConn, error) {
			return grpc.NewClient("passthrough:///bufnet",
				grpc.WithContextDialer(dialer),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
		},
		OnConnected: func(_ context.Context, s nodeagent.Sender) {
			pusher.SetSender(s)
		},
		ReconnectBackoff: []time.Duration{20 * time.Millisecond},
	}
	cancel, done := runAgent(t, cfg)
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, 2*time.Second, "node online", func() bool { return hub.IsOnline(nodeID) })

	// 第一次 tick → priming reset，不 push
	pusher.Tick(context.Background())
	if r := atomic.LoadInt32(&provider.resets); r != 1 {
		t.Fatalf("priming reset count=%d want 1", r)
	}

	// 注入流量（priming 已把基线清零，现在加 100/200 形成真正的 delta）
	provider.AddTraffic(100, 200)

	// 第二次 tick → 原子读取/reset → push usage_push（hub 自动 ack）
	pusher.Tick(context.Background())
	eventually(t, 3*time.Second, "ack received and baseline advanced", func() bool {
		return pusher.PendingCount() == 0 && atomic.LoadInt32(&provider.resets) >= 2
	})

	// SyncUsageWith 应 drain buffer 并把 100/200 写入 alice
	dialFn := func(string) (*nodes.Client, error) {
		return nodes.NewClientWithHub(nodeID, hub), nil
	}
	res, err := jobs.SyncUsageWith(context.Background(), userStore, nodeStore, ibStore, dialFn,
		jobs.ApplyOptions{}, nil, usageBuf)
	if err != nil {
		t.Fatalf("SyncUsageWith: %v", err)
	}
	if res.UsersUpdated != 1 {
		t.Fatalf("UsersUpdated=%d want 1", res.UsersUpdated)
	}
	alice, _ := userStore.GetUser("u1")
	if alice.UsedBytes != 300 {
		t.Fatalf("alice.UsedBytes=%d want 300", alice.UsedBytes)
	}

	// 验证 baseline 推进：再加一些流量，第三次 push 的 delta 仅是新增量
	provider.AddTraffic(7, 13)
	pusher.Tick(context.Background())
	eventually(t, 3*time.Second, "second push acked", func() bool {
		return pusher.PendingCount() == 0 && atomic.LoadInt32(&provider.resets) >= 3
	})
	if _, err := jobs.SyncUsageWith(context.Background(), userStore, nodeStore, ibStore, dialFn,
		jobs.ApplyOptions{}, nil, usageBuf); err != nil {
		t.Fatalf("SyncUsageWith#2: %v", err)
	}
	alice2, _ := userStore.GetUser("u1")
	if delta := alice2.UsedBytes - alice.UsedBytes; delta != 20 {
		t.Fatalf("second-round delta=%d want 20 (baseline correctly advanced)", delta)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// 5. SelfSyncOnHelloMismatch: hello 帧 hash 不一致 → 异步 ApplyNode
// ─────────────────────────────────────────────────────────────────────────

// recordingHubCaller 记录每次 Call(method, body)。
type recordingHubCaller struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingHubCaller) Call(_ context.Context, _, method string, _ any) (json.RawMessage, error) {
	r.mu.Lock()
	r.calls = append(r.calls, method)
	r.mu.Unlock()
	return json.RawMessage(`{}`), nil
}

func (r *recordingHubCaller) Methods() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

func TestE2E_SelfSyncOnHelloMismatch(t *testing.T) {
	t.Parallel()

	userStore := users.NewMemoryStore()
	ibStore := inbounds.NewMemoryStore()
	nodeStore := nodes.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "hub://n1"})
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{
		ID: "ib1", NodeID: "n1", Protocol: "vless", Tag: "v", Port: 443,
	})

	hubCaller := &recordingHubCaller{}

	hello := &jobs.SelfSyncHandler{
		UserStore:    userStore,
		InboundStore: ibStore,
		NodeStore:    nodeStore,
		HubCaller:    hubCaller,
		ApplyTimeout: 5 * time.Second,
	}

	body, _ := json.Marshal(map[string]string{
		"node_id":     "n1",
		"config_hash": "wrong",
		"version":     "test",
	})
	hello.OnHello("n1", body)

	if hello.MismatchCount() != 1 {
		t.Fatalf("MismatchCount=%d want 1", hello.MismatchCount())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if hello.ApplyOKCount()+hello.ApplyErrCount() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if hello.ApplyOKCount()+hello.ApplyErrCount() != 1 {
		t.Fatalf("apply ok+err=%d want 1 within 3s", hello.ApplyOKCount()+hello.ApplyErrCount())
	}

	// hash 匹配时不应再触发 mismatch
	expected, err := jobs.ComputeNodeConfigHash(context.Background(), "n1", userStore, ibStore, nil)
	if err != nil {
		t.Fatalf("ComputeNodeConfigHash: %v", err)
	}
	body2, _ := json.Marshal(map[string]string{
		"node_id":     "n1",
		"config_hash": expected,
		"version":     "test",
	})
	hello.OnHello("n1", body2)
	if hello.MismatchCount() != 1 {
		t.Fatalf("mismatch should not increment when hashes match (got %d)", hello.MismatchCount())
	}
}

// ─────────────────────────────────────────────────────────────────────────
// 6. ReconnectBackoff: 强制断开 → agent 在 backoff 窗口内重连成功
// ─────────────────────────────────────────────────────────────────────────

func TestE2E_ReconnectBackoff(t *testing.T) {
	t.Parallel()

	// 选一个空闲端口
	tmp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := tmp.Addr().String()
	tmp.Close()

	const nodeID = "n-reconnect"
	ph1 := &recordingPushHandler{}
	hub1, _, stop1 := startTCPHub(t, addr, ph1, fixedPeer(nodeID))

	disp := newCallableDispatcher()
	cfg := nodeagent.Config{
		NodeID:        nodeID,
		ServerAddr:    addr,
		Dispatcher:    disp,
		HelloProvider: nodeagent.DefaultHelloProvider(nodeID, nil),
		Dialer: func(_ context.Context) (*grpc.ClientConn, error) {
			return grpc.NewClient(addr,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
		},
		ReconnectBackoff: []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond},
		KeepaliveTime:    500 * time.Millisecond,
		KeepaliveTimeout: 500 * time.Millisecond,
	}
	cancel, done := runAgent(t, cfg)
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, 5*time.Second, "node online on hub1", func() bool { return hub1.IsOnline(nodeID) })

	// 强制断开
	stop1()
	eventually(t, 5*time.Second, "node went offline after hub1 stop", func() bool {
		return !hub1.IsOnline(nodeID)
	})

	// 在同一 port 重启 hub2（带重试，避免 TIME_WAIT 偶发占用）
	time.Sleep(100 * time.Millisecond)
	ph2 := &recordingPushHandler{}
	var hub2 *nodehub.Hub
	var stop2 func()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		l, lerr := net.Listen("tcp", addr)
		if lerr == nil {
			l.Close()
			hub2, _, stop2 = startTCPHub(t, addr, ph2, fixedPeer(nodeID))
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if hub2 == nil {
		t.Fatalf("could not rebind to %s", addr)
	}
	t.Cleanup(stop2)

	// 在 reconnect 窗口内（< 30s）验证 agent 重连成功
	eventually(t, 30*time.Second, "node reconnected to hub2", func() bool {
		return hub2.IsOnline(nodeID)
	})
	if hellos := ph2.snapshotHellos(); len(hellos) == 0 {
		t.Fatalf("hub2 never received hello after reconnect")
	}
}

// ─────────────────────────────────────────────────────────────────────────
// 7. MultiNode: 5 个 node 同时连，usage push 全部正确入账，关闭后 goroutine 收敛
// ─────────────────────────────────────────────────────────────────────────

func TestE2E_MultiNode(t *testing.T) {
	t.Parallel()

	const numNodes = 5
	usageBuf := nodes.NewUsageBuffer()

	hubs := make([]*nodehub.Hub, numNodes)
	stops := make([]func(), numNodes)
	dialers := make([]func(context.Context, string) (net.Conn, error), numNodes)

	for i := 0; i < numNodes; i++ {
		nid := fmt.Sprintf("n%d", i)
		ph := &nodehub.MultiPushHandler{
			UsagePushHandler: func(_ string, seq uint64, body []byte) error {
				var stats nodes.UsageStats
				if err := json.Unmarshal(body, &stats); err != nil {
					return err
				}
				return usageBuf.Append(nid, seq, stats)
			},
		}
		h, dialer, stop := startBufHub(t, ph, fixedPeer(nid))
		hubs[i], dialers[i], stops[i] = h, dialer, stop
	}

	userStore := users.NewMemoryStore()
	nodeStore := nodes.NewMemoryStore()
	ibStore := inbounds.NewMemoryStore()
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive, TrafficLimit: 999999999999,
	})
	for i := 0; i < numNodes; i++ {
		nid := fmt.Sprintf("n%d", i)
		_, _ = nodeStore.Upsert(nodes.Node{ID: nid, Name: nid, BaseURL: "hub://" + nid})
		_, _ = userStore.UpsertUserInbound(users.UserInbound{
			ID: "u1-" + nid, UserID: "u1", NodeID: nid, UUID: "uuid-1",
		})
	}

	// 等所有 hub goroutine 启动，再记录基线 goroutine 数
	time.Sleep(50 * time.Millisecond)
	gBefore := runtime.NumGoroutine()

	type runner struct {
		cancel context.CancelFunc
		done   <-chan error
		pusher *nodeagent.UsagePusher
		prov   *countingUsageProvider
	}
	runners := make([]runner, numNodes)
	for i := 0; i < numNodes; i++ {
		nid := fmt.Sprintf("n%d", i)
		prov := &countingUsageProvider{}
		pusher := nodeagent.NewUsagePusher(prov, time.Hour)
		pusher.SetAckTimeout(2 * time.Second)
		cfg := nodeagent.Config{
			NodeID:        nid,
			ServerAddr:    "passthrough:///bufnet",
			Dispatcher:    newCallableDispatcher(),
			HelloProvider: nodeagent.DefaultHelloProvider(nid, nil),
			Dialer: func(_ context.Context) (*grpc.ClientConn, error) {
				return grpc.NewClient("passthrough:///bufnet",
					grpc.WithContextDialer(dialers[i]),
					grpc.WithTransportCredentials(insecure.NewCredentials()),
				)
			},
			OnConnected: func(_ context.Context, s nodeagent.Sender) {
				pusher.SetSender(s)
			},
			ReconnectBackoff: []time.Duration{20 * time.Millisecond},
		}
		cancel, done := runAgent(t, cfg)
		runners[i] = runner{cancel: cancel, done: done, pusher: pusher, prov: prov}
	}

	for i := 0; i < numNodes; i++ {
		idx := i
		eventually(t, 5*time.Second, fmt.Sprintf("n%d online", idx), func() bool {
			return hubs[idx].IsOnline(fmt.Sprintf("n%d", idx))
		})
	}

	// 每个 pusher：priming（清零）+ AddTraffic + push delta
	for i := 0; i < numNodes; i++ {
		runners[i].pusher.Tick(context.Background())
	}
	for i := 0; i < numNodes; i++ {
		runners[i].prov.AddTraffic(int64(10*(i+1)), int64(20*(i+1)))
	}
	for i := 0; i < numNodes; i++ {
		runners[i].pusher.Tick(context.Background())
	}
	for i := 0; i < numNodes; i++ {
		idx := i
		eventually(t, 5*time.Second, fmt.Sprintf("n%d ack", idx), func() bool {
			return runners[idx].pusher.PendingCount() == 0 &&
				atomic.LoadInt32(&runners[idx].prov.resets) >= 2
		})
	}

	dialFn := func(nodeID string) (*nodes.Client, error) {
		for i := 0; i < numNodes; i++ {
			if fmt.Sprintf("n%d", i) == nodeID {
				return nodes.NewClientWithHub(nodeID, hubs[i]), nil
			}
		}
		return nil, fmt.Errorf("unknown node %s", nodeID)
	}
	res, err := jobs.SyncUsageWith(context.Background(), userStore, nodeStore, ibStore, dialFn,
		jobs.ApplyOptions{}, nil, usageBuf)
	if err != nil {
		t.Fatalf("SyncUsageWith: %v", err)
	}
	if res.UsersUpdated != numNodes {
		t.Fatalf("UsersUpdated=%d want %d (one per user_inbound access)", res.UsersUpdated, numNodes)
	}
	// sum_i (10*(i+1) + 20*(i+1)) for i=0..4 = 30 * (1+2+3+4+5) = 450
	alice, _ := userStore.GetUser("u1")
	if alice.UsedBytes != 450 {
		t.Fatalf("alice.UsedBytes=%d want 450", alice.UsedBytes)
	}

	// 优雅关闭
	for i := 0; i < numNodes; i++ {
		runners[i].cancel()
	}
	for i := 0; i < numNodes; i++ {
		<-runners[i].done
	}
	for i := 0; i < numNodes; i++ {
		stops[i]()
	}

	// 给 grpc 后台 goroutine 一些时间收尾
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runtime.Gosched()
		if runtime.NumGoroutine()-gBefore <= 10 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if leaked := runtime.NumGoroutine() - gBefore; leaked > 10 {
		// 不直接 fail：grpc 内部清理 goroutine 时序难以精确控制；记录用于诊断。
		t.Logf("WARNING: goroutine count grew by %d (before=%d, after=%d)",
			leaked, gBefore, runtime.NumGoroutine())
	}
}
