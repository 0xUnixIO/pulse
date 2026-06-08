package serverapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/users"
)

// setupTestMux 创建带 mock 节点的测试 mux，返回 mux 和 ibStore。
// nodeHandler 按 RPC 方法名（如 "Restart"、"Status"）注册响应。
func setupTestMux(t *testing.T, nodeHandlers map[string]func(body any) (json.RawMessage, error)) (*http.ServeMux, *inbounds.MemoryStore) {
	t.Helper()

	hub := &fakeHub{handlers: nodeHandlers}

	nodeStore := nodes.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{
		ID:      "node-1",
		Name:    "node 1",
		BaseURL: "http://node.test",
	})

	baseAPI := New(nodeStore)
	baseAPI.clientFactory = fakeHubClientFactory(hub)

	ibStore := inbounds.NewMemoryStore()
	userAPI := newUserAPI(users.NewMemoryStore(), nodeStore, ibStore, nil, baseAPI, nil, jobs.ApplyOptions{}, nil)
	mux := http.NewServeMux()
	userAPI.Register(mux)

	return mux, ibStore
}

// createUser 辅助：POST /v1/users，返回 user ID。
func createUser(t *testing.T, mux *http.ServeMux, body string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/users", bytes.NewReader([]byte(body)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create user status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&out)
	return out["id"].(string)
}

// createUserInbound 辅助：POST /v1/users/{userID}/inbounds，返回 user_inbound 记录 ID。
func createUserInbound(t *testing.T, mux *http.ServeMux, userID, inboundID string) string {
	t.Helper()
	body := `{"inbound_id":"` + inboundID + `"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/users/"+userID+"/inbounds", bytes.NewReader([]byte(body)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create user inbound status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&out)
	return out["id"].(string)
}

func TestUserSubscriptionAndApplyFlow(t *testing.T) {
	var capturedConfig string
	mux, ibStore := setupTestMux(t, map[string]func(body any) (json.RawMessage, error){
		"Restart": func(b any) (json.RawMessage, error) {
			req, _ := b.(nodes.ConfigRequest)
			capturedConfig = req.Config
			if !strings.Contains(req.Config, "\"protocol\": \"vless\"") {
				return nil, &nodeAPIError{"bad config"}
			}
			return json.RawMessage(`{"running":true}`), nil
		},
	})

	// 在 ibStore 中注册 vless 入站和对应 host
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{
		ID:       "ib-vless",
		NodeID:   "node-1",
		Protocol: "vless",
		Tag:      "pulse-vless-node-1",
		Port:     443,
	})
	_, _ = ibStore.UpsertHost(inbounds.Host{
		ID:        "host-1",
		InboundID: "ib-vless",
		Address:   "example.com",
		Port:      443,
	})

	// 创建 alice 并绑定 inbound
	aliceID := createUser(t, mux, `{"id":"user-1","username":"alice"}`)
	ibID := createUserInbound(t, mux, aliceID, "ib-vless")

	// 获取订阅链接
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/users/"+aliceID+"/inbounds/"+ibID+"/subscription", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("subscription status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "vless://") {
		t.Fatalf("expected vless link, got %s", rec.Body.String())
	}

	// 下发配置
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/users/"+aliceID+"/inbounds/"+ibID+"/apply", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"running\":true") {
		t.Fatalf("expected running node status, got %s", rec.Body.String())
	}

	// 创建 bob 并绑定同一 inbound
	bobID := createUser(t, mux, `{"id":"user-2","username":"bob"}`)
	ibID2 := createUserInbound(t, mux, bobID, "ib-vless")

	// 下发 bob 的配置
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/users/"+bobID+"/inbounds/"+ibID2+"/apply", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply second user status = %d body=%s", rec.Code, rec.Body.String())
	}

	// 配置中应包含两个用户（复合用户名格式：username@tag）
	if !strings.Contains(capturedConfig, "alice@") || !strings.Contains(capturedConfig, "bob@") {
		t.Fatalf("expected aggregated config with both users, got %s", capturedConfig)
	}
}

func TestCreateUserAutoGeneratesID(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{
		ID:      "node-1",
		Name:    "node 1",
		BaseURL: "http://node.test",
	})

	baseAPI := New(nodeStore)
	userAPI := newUserAPI(users.NewMemoryStore(), nodeStore, inbounds.NewMemoryStore(), nil, baseAPI, nil, jobs.ApplyOptions{}, nil)
	mux := http.NewServeMux()
	userAPI.Register(mux)

	body := []byte(`{"username":"alice"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/users", bytes.NewReader(body))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create user status = %d body=%s", rec.Code, rec.Body.String())
	}

	var out users.User
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	if out.ID == "" {
		t.Fatal("expected generated user id")
	}
}

func TestUserSupportsMultipleProtocols(t *testing.T) {
	mux, ibStore := setupTestMux(t, map[string]func(body any) (json.RawMessage, error){
		"Restart": func(any) (json.RawMessage, error) {
			return json.RawMessage(`{"running":true}`), nil
		},
	})

	// 在 node-1 上注册 trojan 和 shadowsocks 入站
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{
		ID:       "ib-trojan",
		NodeID:   "node-1",
		Protocol: "trojan",
		Tag:      "pulse-trojan-node-1",
		Port:     443,
	})
	_, _ = ibStore.UpsertHost(inbounds.Host{
		ID:        "host-trojan",
		InboundID: "ib-trojan",
		Address:   "example.com",
		Port:      443,
	})
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{
		ID:       "ib-ss",
		NodeID:   "node-1",
		Protocol: "shadowsocks",
		Tag:      "pulse-ss-node-1",
		Port:     8443,
		Method:   "aes-256-gcm",
	})
	_, _ = ibStore.UpsertHost(inbounds.Host{
		ID:        "host-ss",
		InboundID: "ib-ss",
		Address:   "example.com",
		Port:      8443,
	})

	// 创建用户，分别绑定 trojan 和 ss 两个 inbound
	userID := createUser(t, mux, `{"id":"user-multi","username":"alice"}`)
	trojanAccessID := createUserInbound(t, mux, userID, "ib-trojan")
	ssAccessID := createUserInbound(t, mux, userID, "ib-ss")

	// trojan 绑定的订阅只包含 trojan 链接
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/users/"+userID+"/inbounds/"+trojanAccessID+"/subscription", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("trojan subscription status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "trojan://") {
		t.Fatalf("expected trojan link, got %s", rec.Body.String())
	}

	// ss 绑定的订阅只包含 ss 链接
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/users/"+userID+"/inbounds/"+ssAccessID+"/subscription", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ss subscription status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ss://") {
		t.Fatalf("expected ss link, got %s", rec.Body.String())
	}
}

func TestSyncUsageDisablesLimitedUserAndReloadsNode(t *testing.T) {
	var capturedConfig string
	var removedEmails []string
	nodeStore := nodes.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{
		ID:      "node-1",
		Name:    "node 1",
		BaseURL: "http://node.test",
	})

	hub := &fakeHub{handlers: map[string]func(body any) (json.RawMessage, error){
		"Restart": func(b any) (json.RawMessage, error) {
			req, _ := b.(nodes.ConfigRequest)
			capturedConfig = req.Config
			return json.RawMessage(`{"running":true}`), nil
		},
		"RemoveUser": func(b any) (json.RawMessage, error) {
			if m, ok := b.(map[string]string); ok {
				removedEmails = append(removedEmails, m["email"])
			}
			return json.RawMessage(`{}`), nil
		},
	}}
	baseAPI := New(nodeStore)
	baseAPI.clientFactory = fakeHubClientFactory(hub)

	ibStore := inbounds.NewMemoryStore()
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{
		ID:       "ib-vless",
		NodeID:   "node-1",
		Protocol: "vless",
		Tag:      "pulse-vless-node-1",
		Port:     443,
	})

	userStore := users.NewMemoryStore()
	_, _ = userStore.UpsertUser(users.User{ID: "u1", Username: "alice", Status: users.StatusActive, TrafficLimit: 100})
	_, _ = userStore.UpsertUser(users.User{ID: "u2", Username: "bob", Status: users.StatusActive})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{ID: "u1-ib0", UserID: "u1", InboundID: "ib-vless", NodeID: "node-1", UUID: "uuid-alice", Secret: "secret-alice"})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{ID: "u2-ib0", UserID: "u2", InboundID: "ib-vless", NodeID: "node-1", UUID: "uuid-bob", Secret: "secret-bob"})

	// usage 通过 push buffer 提供（hub 模式下 c.Usage 不可用）。
	usageBuf := nodes.NewUsageBuffer()
	_ = usageBuf.Append("node-1", 1, nodes.UsageStats{
		Available: true, Running: true,
		UploadTotal: 100, DownloadTotal: 200, Connections: 1,
		Users: []nodes.UserUsage{
			{User: "alice@pulse-vless-node-1", UploadTotal: 80, DownloadTotal: 40, Connections: 1},
			{User: "bob@pulse-vless-node-1", UploadTotal: 10, DownloadTotal: 10, Connections: 0},
		},
	})

	result, err := jobs.SyncUsageWith(t.Context(), userStore, nodeStore, ibStore, baseAPI.Dial, jobs.ApplyOptions{}, nil, usageBuf)
	if err != nil {
		t.Fatalf("SyncUsageWith() error = %v", err)
	}
	if result.NodesReloaded != 1 {
		t.Fatalf("expected 1 node reload, got %#v", result)
	}

	alice, err := userStore.GetUser("u1")
	if err != nil {
		t.Fatalf("GetUser(alice) error = %v", err)
	}
	if alice.EffectiveEnabled() {
		t.Fatalf("expected alice disabled after exceeding limit: %#v", alice)
	}
	if alice.UsedBytes != 120 {
		t.Fatalf("expected alice used bytes 120, got %d", alice.UsedBytes)
	}

	bob, err := userStore.GetUser("u2")
	if err != nil {
		t.Fatalf("GetUser(bob) error = %v", err)
	}
	if !bob.EffectiveEnabled() {
		t.Fatalf("expected bob to remain enabled")
	}
	// 删除被禁用的 alice 优先走 delta 路径（RemoveUser 热更新），不会触发全量 Restart。
	if len(removedEmails) == 0 {
		t.Fatalf("expected RemoveUser to be called for disabled user, got none")
	}
	foundAlice := false
	for _, e := range removedEmails {
		if strings.Contains(e, "alice") {
			foundAlice = true
		}
		if strings.Contains(e, "bob") {
			t.Fatalf("did not expect bob to be removed, got %q", e)
		}
	}
	if !foundAlice {
		t.Fatalf("expected alice to be removed via RemoveUser, got %v", removedEmails)
	}
	_ = capturedConfig
}
