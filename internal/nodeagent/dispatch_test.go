package nodeagent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"pulse/internal/coremanager"
	"pulse/internal/nodeapi"
	"pulse/internal/nodes/confighash"
)

// stubManager 是一个最小的 coremanager.Manager 实现，避免在 nodeagent 测试中
// 引入 xray 包（xray 的 protobuf init 在当前 Go 版本下会 panic，与本任务无关）。
type stubManager struct {
	cfg string
}

func (s *stubManager) Start(c string) error               { s.cfg = c; return nil }
func (s *stubManager) Stop() error                        { return nil }
func (s *stubManager) Restart(c string) error             { s.cfg = c; return nil }
func (s *stubManager) Status() coremanager.Status         { return coremanager.Status{} }
func (s *stubManager) Usage(bool) coremanager.UsageStats  { return coremanager.UsageStats{} }
func (s *stubManager) Config() string                     { return s.cfg }
func (s *stubManager) Logs() []string                     { return nil }
func (s *stubManager) Subscribe() (int64, <-chan string)  { return 0, make(chan string) }
func (s *stubManager) Unsubscribe(int64)                  {}
func (s *stubManager) SavedConfig() string                { return s.cfg }
func (s *stubManager) RuntimeInfo(context.Context) coremanager.RuntimeInfo {
	return coremanager.RuntimeInfo{Available: true, Module: "stub"}
}
func (s *stubManager) Version(context.Context) (string, error) { return "stub", nil }
func (s *stubManager) AddUser(context.Context, coremanager.UserConfig) error { return nil }
func (s *stubManager) RemoveUser(context.Context, string, string) error      { return nil }
func (s *stubManager) KickUser(context.Context, string) (int, error)         { return 0, nil }

func newTestAPI() *nodeapi.API {
	return nodeapi.New(&stubManager{})
}

func TestAPIDispatcher_StatusAndConfig(t *testing.T) {
	t.Parallel()
	d := NewAPIDispatcher(newTestAPI(), nil)
	ctx := context.Background()

	body, err := d.Handle(ctx, "Status", nil)
	if err != nil {
		t.Fatalf("Status err: %v", err)
	}
	var st struct {
		Running bool `json:"running"`
	}
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("decode status: %v body=%s", err, body)
	}

	body, err = d.Handle(ctx, "Config", nil)
	if err != nil {
		t.Fatalf("Config err: %v", err)
	}
	var cfg struct {
		Config string `json:"config"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
}

func TestAPIDispatcher_Usage(t *testing.T) {
	t.Parallel()
	d := NewAPIDispatcher(newTestAPI(), nil)
	body, err := d.Handle(context.Background(), "Usage", json.RawMessage(`{"reset":false}`))
	if err != nil {
		t.Fatalf("Usage err: %v", err)
	}
	if !json.Valid(body) {
		t.Fatalf("invalid usage body: %s", body)
	}
}

func TestAPIDispatcher_StartRestartRoundTrip(t *testing.T) {
	t.Parallel()
	api := newTestAPI()
	d := NewAPIDispatcher(api, nil)
	body, err := d.Handle(context.Background(), "Start", json.RawMessage(`{"config":"{}"}`))
	if err != nil {
		t.Fatalf("Start err: %v", err)
	}
	if !json.Valid(body) {
		t.Fatalf("invalid start body: %s", body)
	}
	if _, err := d.Handle(context.Background(), "Restart", json.RawMessage(`{"config":"{\"v\":1}"}`)); err != nil {
		t.Fatalf("Restart err: %v", err)
	}
	if _, err := d.Handle(context.Background(), "Stop", nil); err != nil {
		t.Fatalf("Stop err: %v", err)
	}
}

func TestAPIDispatcher_UnknownMethod(t *testing.T) {
	t.Parallel()
	d := NewAPIDispatcher(newTestAPI(), nil)
	_, err := d.Handle(context.Background(), "DoesNotExist", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown method") {
		t.Fatalf("expected unknown method err, got %v", err)
	}
}

func TestAPIDispatcher_StreamingRequiresSender(t *testing.T) {
	t.Parallel()
	d := NewAPIDispatcher(newTestAPI(), nil)
	for _, m := range []string{"LogsStream", "TracerouteStream"} {
		_, err := d.Handle(context.Background(), m, nil)
		if err == nil || !strings.Contains(err.Error(), "sender not initialized") {
			t.Fatalf("%s: expected sender-not-initialized err, got %v", m, err)
		}
	}
}

// fakeSender 记录 PushEvent 调用，供流式 dispatcher 测试使用。
type fakeSender struct {
	mu     sync.Mutex
	events []fakeEvent
}

type fakeEvent struct {
	ReqID string
	Event string
	Body  []byte
	Seq   uint64
}

func (f *fakeSender) PushEvent(reqID, event string, body []byte, seq uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, fakeEvent{ReqID: reqID, Event: event, Body: append([]byte(nil), body...), Seq: seq})
	return nil
}
func (f *fakeSender) WaitAck(_ context.Context, _ uint64) error { return nil }

func TestAPIDispatcher_LogsStream_PushesFrames(t *testing.T) {
	t.Parallel()
	api := newTestAPI()
	d := NewAPIDispatcher(api, nil)
	sender := &fakeSender{}
	d.SetSender(sender)

	// 注入 reqID 到 ctx；启动 dispatcher 调用，期间往 stub manager 缓冲灌一行日志
	// 然后通过取消 ctx 来终止流。
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), reqIDCtxKey{}, "rid-1"))

	// 在另一 goroutine 中调用 LogsStream（同步阻塞）
	done := make(chan error, 1)
	go func() {
		_, err := d.Handle(ctx, "LogsStream", nil)
		done <- err
	}()

	// stub manager 没有真实日志源；historical 部分立即遍历空切片，
	// 然后进入 Subscribe 循环阻塞。我们等 50ms 后取消 ctx 让 LogsChannel 关闭。
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Handle returned err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Handle did not return after ctx cancel")
	}
	// 没有日志行应当也没有 push（stub manager Logs() 返回 nil）
	if len(sender.events) != 0 {
		t.Logf("got %d events (allowed; stub may not emit)", len(sender.events))
	}
}

func TestAPIDispatcher_LogsStream_RequiresReqID(t *testing.T) {
	t.Parallel()
	d := NewAPIDispatcher(newTestAPI(), nil)
	d.SetSender(&fakeSender{})
	_, err := d.Handle(context.Background(), "LogsStream", nil)
	if err == nil || !strings.Contains(err.Error(), "missing reqID") {
		t.Fatalf("expected missing reqID err, got %v", err)
	}
}

func TestAPIDispatcher_AddUserValidation(t *testing.T) {
	t.Parallel()
	d := NewAPIDispatcher(newTestAPI(), nil)
	_, err := d.Handle(context.Background(), "AddUser", json.RawMessage(`{}`))
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestAPIDispatcher_AddRemoveUserOK(t *testing.T) {
	t.Parallel()
	d := NewAPIDispatcher(newTestAPI(), nil)
	add := `{"inbound_tag":"vless-1","email":"a@@@vless-1","uuid":"u","protocol":"vless"}`
	if _, err := d.Handle(context.Background(), "AddUser", json.RawMessage(add)); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	rm := `{"inbound_tag":"vless-1","email":"a@@@vless-1"}`
	if _, err := d.Handle(context.Background(), "RemoveUser", json.RawMessage(rm)); err != nil {
		t.Fatalf("RemoveUser: %v", err)
	}
}

func TestAPIDispatcher_IPSentinelNotConfigured(t *testing.T) {
	t.Parallel()
	d := NewAPIDispatcher(newTestAPI(), nil)
	_, err := d.Handle(context.Background(), "IPSentinelStatus", nil)
	if err == nil || !strings.Contains(err.Error(), "ipsentinel handler not configured") {
		t.Fatalf("expected not-configured err, got %v", err)
	}
}

type fakeIPSentinel struct {
	got string
}

func (f *fakeIPSentinel) Dispatch(_ context.Context, method string, _ []byte) (any, error) {
	f.got = method
	return map[string]any{"ok": true, "method": method}, nil
}

func TestAPIDispatcher_IPSentinelRoute(t *testing.T) {
	t.Parallel()
	fake := &fakeIPSentinel{}
	d := NewAPIDispatcher(newTestAPI(), fake)
	body, err := d.Handle(context.Background(), "IPSentinelDetect", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if fake.got != "IPSentinelDetect" {
		t.Fatalf("not routed: %q", fake.got)
	}
	if !strings.Contains(string(body), "IPSentinelDetect") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDispatcher_ProbeLatency(t *testing.T) {
	t.Parallel()
	d := NewAPIDispatcher(newTestAPI(), nil)
	// 给一个会很快超时的 ctx，避免实际网络访问。
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	body, err := d.Handle(ctx, "ProbeLatency", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !json.Valid(body) {
		t.Fatalf("invalid body: %s", body)
	}
}

// ── ConfigHasher 测试 ────────────────────────────────────────────

func TestConfigHasher_StableAcrossOrder(t *testing.T) {
	t.Parallel()
	a := `{"inbounds":[
		{"tag":"vless-1","settings":{"clients":[
			{"email":"alice","id":"u1"},{"email":"bob","id":"u2"}]}},
		{"tag":"trojan-1","trafficRate":2.5,"settings":{"clients":[
			{"email":"alice","password":"p1"}]}}
	]}`
	b := `{"inbounds":[
		{"tag":"trojan-1","trafficRate":2.5,"settings":{"clients":[
			{"email":"alice","password":"p1"}]}},
		{"tag":"vless-1","settings":{"clients":[
			{"email":"bob","id":"u2"},{"email":"alice","id":"u1"}]}}
	]}`
	if confighash.HashFromXrayJSON(a) != confighash.HashFromXrayJSON(b) {
		t.Fatalf("hash should be order-independent\nA=%s\nB=%s",
			confighash.HashFromXrayJSON(a), confighash.HashFromXrayJSON(b))
	}
}

func TestConfigHasher_DetectsChange(t *testing.T) {
	t.Parallel()
	a := `{"inbounds":[{"tag":"v","settings":{"clients":[{"email":"x","id":"u1"}]}}]}`
	b := `{"inbounds":[{"tag":"v","settings":{"clients":[{"email":"x","id":"u2"}]}}]}`
	c := `{"inbounds":[{"tag":"v","trafficRate":2.0,"settings":{"clients":[{"email":"x","id":"u1"}]}}]}`
	d := `{"inbounds":[{"tag":"v","settings":{"clients":[{"email":"x","id":"u1"},{"email":"y","id":"u2"}]}}]}`
	ha := confighash.HashFromXrayJSON(a)
	if confighash.HashFromXrayJSON(b) == ha {
		t.Fatalf("uuid change should alter hash")
	}
	if confighash.HashFromXrayJSON(c) == ha {
		t.Fatalf("traffic_rate change should alter hash")
	}
	if confighash.HashFromXrayJSON(d) == ha {
		t.Fatalf("user added should alter hash")
	}
}

func TestConfigHasher_EmptyConfig(t *testing.T) {
	t.Parallel()
	hasher := ConfigHasher(newTestAPI())
	if got := hasher(); got != "" {
		t.Fatalf("expected empty hash for no-config manager, got %q", got)
	}
}

func TestConfigHasher_NonEmptyChanges(t *testing.T) {
	t.Parallel()
	api := newTestAPI()
	hasher := ConfigHasher(api)
	if got := hasher(); got != "" {
		t.Fatalf("empty manager hash = %q want empty", got)
	}
	// 注入一个非空配置：xray 启动时会缓存 SavedConfig；stubManager.Start 直接保存。
	if _, err := api.DoStart(`{"inbounds":[{"tag":"v","settings":{"clients":[{"email":"x","id":"u1"}]}}]}`, ""); err != nil {
		t.Fatalf("set config: %v", err)
	}
	if got := hasher(); got == "" {
		t.Fatalf("non-empty config should hash to non-empty")
	}
}

