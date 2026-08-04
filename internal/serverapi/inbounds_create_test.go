package serverapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/outbounds"
	"pulse/internal/users"
)

var errNodeNotFound = errors.New("node not found")

// fakeOutboundStore 是 outbounds.Store 的最小内存实现。
type fakeOutboundStore struct {
	items map[string]outbounds.Outbound
}

func (f *fakeOutboundStore) Upsert(ob outbounds.Outbound) (outbounds.Outbound, error) {
	if f.items == nil {
		f.items = map[string]outbounds.Outbound{}
	}
	f.items[ob.ID] = ob
	return ob, nil
}

func (f *fakeOutboundStore) Get(id string) (outbounds.Outbound, error) {
	if ob, ok := f.items[id]; ok {
		return ob, nil
	}
	return outbounds.Outbound{}, outbounds.ErrOutboundNotFound
}

func (f *fakeOutboundStore) List() ([]outbounds.Outbound, error) {
	out := make([]outbounds.Outbound, 0, len(f.items))
	for _, ob := range f.items {
		out = append(out, ob)
	}
	return out, nil
}

func (f *fakeOutboundStore) Delete(id string) error {
	delete(f.items, id)
	return nil
}

// TestCreateInboundDoesNotResetExistingOutbound 复现：节点上已有 inbound A
// 绑定出口 ob-1，通过 HTTP API 新建 inbound B，A.outbound_id 必须保持 ob-1。
func TestCreateInboundDoesNotResetExistingOutbound(t *testing.T) {
	ibStore := inbounds.NewMemoryStore()
	userStore := users.NewMemoryStore()
	nodeStore := nodes.NewMemoryStore()

	// 已有 inbound A，绑定出口 ob-1
	ibA := inbounds.Inbound{
		ID:         "ib-a",
		NodeID:     "n1",
		Protocol:   "vless",
		Tag:        "vless-a",
		Port:       443,
		OutboundID: "ob-1",
	}
	if _, err := ibStore.UpsertInbound(ibA); err != nil {
		t.Fatalf("upsert inbound A: %v", err)
	}

	// 节点与活跃用户（避免 apply 走 idle 路径）
	if _, err := nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"}); err != nil {
		t.Fatalf("upsert node: %v", err)
	}
	u := users.User{ID: "u-1", Username: "alice", Status: users.StatusActive, UUID: "11111111-1111-1111-1111-111111111111"}
	if _, err := userStore.UpsertUser(u); err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	if _, err := userStore.UpsertUserInbound(users.UserInbound{ID: "ui-1", UserID: "u-1", InboundID: "ib-a", NodeID: "n1", UUID: u.UUID}); err != nil {
		t.Fatalf("upsert user_inbound: %v", err)
	}

	mux := http.NewServeMux()
	dial := func(nodeID string) (*nodes.Client, error) {
		return nil, errNodeNotFound
	}
	RegisterInboundsAPI(mux, ibStore, userStore, nodeStore, &fakeOutboundStore{}, dial, jobs.ApplyOptions{}, nil)

	// 新建 inbound B（模拟前端 POST /v1/inbounds）
	body, _ := json.Marshal(map[string]any{
		"node_id":   "n1",
		"protocol":  "vless",
		"port":      8443,
		"tag":       "vless-b",
		"outbound_id": "",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/inbounds", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /v1/inbounds status = %d, body = %s", w.Code, w.Body.String())
	}

	// 验证已有 inbound A 的出口没有被重置
	got, err := ibStore.GetInbound("ib-a")
	if err != nil {
		t.Fatalf("get inbound A: %v", err)
	}
	if got.OutboundID != "ob-1" {
		t.Fatalf("新建 inbound 后，已有 inbound A 的出口被重置: got %q, want %q", got.OutboundID, "ob-1")
	}
}

// TestSyncGroupForUserKeepsNodeInboundOutboundID 复现：用户组同步（新建 inbound
// 加入用户组时触发）会删除并重建组内用户的 user_inbounds 记录，且使用新 ID。
// 若某 inbound 的出口以 nodeib:<ibID>:<uibID> 引用该记录，重建后引用失效，
// 面板会显示为 direct。
func TestSyncGroupForUserKeepsNodeInboundOutboundID(t *testing.T) {
	ibStore := inbounds.NewMemoryStore()
	userStore := users.NewMemoryStore()

	// SS inbound S：作为出口目标（节点 n2）
	ibS, err := ibStore.UpsertInbound(inbounds.Inbound{
		ID:       "ib-ss",
		NodeID:   "n2",
		Protocol: "shadowsocks",
		Tag:      "ss-1",
		Port:     34192,
		Method:   "2022-blake3-aes-128-gcm",
		Password: "c2VydmVyLXBzay0xNmJ5dGVzMDA=",
	})
	if err != nil {
		t.Fatalf("upsert SS inbound: %v", err)
	}

	u := users.User{ID: "u-1", Username: "alice", Status: users.StatusActive, Secret: "secret-1"}
	if _, err := userStore.UpsertUser(u); err != nil {
		t.Fatalf("upsert user: %v", err)
	}

	// u1 通过用户组 g 在 SS inbound S 上有一条记录，ID = ui-ss（被 nodeib 出口引用）
	const uibID = "ui-ss"
	if _, err := userStore.UpsertGroupUserInbound(users.UserInbound{
		ID:        uibID,
		UserID:    "u-1",
		InboundID: ibS.ID,
		NodeID:    ibS.NodeID,
		Secret:    "secret-1",
		GroupID:   "g",
	}); err != nil {
		t.Fatalf("upsert group user_inbound: %v", err)
	}

	a := &userGroupAPI{userStore: userStore, ibStore: ibStore}

	// 模拟新建 inbound 加入用户组 g 后触发的组成员同步
	affected, err := a.syncGroupForUser("g", "u-1", []string{ibS.ID})
	if err != nil {
		t.Fatalf("syncGroupForUser: %v", err)
	}
	if len(affected) != 1 || affected[0] != "n2" {
		t.Fatalf("affected nodes = %v, want [n2]", affected)
	}

	// 关键断言：被 nodeib 出口引用的 user_inbounds 记录 ID 必须保持稳定
	accs, err := userStore.ListUserInboundsByUser("u-1")
	if err != nil {
		t.Fatalf("list user inbounds: %v", err)
	}
	var found *users.UserInbound
	for i := range accs {
		if accs[i].InboundID == ibS.ID {
			found = &accs[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("同步后 u1 在 SS inbound 上的记录丢失")
	}
	if found.ID != uibID {
		t.Fatalf("同步后 user_inbounds ID 变化: got %q, want %q（nodeib 出口引用失效，面板将显示 direct）", found.ID, uibID)
	}
}

// TestPutInboundKeepsOutboundWhenOmitted 复现：PUT 更新 inbound 时若请求体省略
// outbound_id，不应把已有出口清空为 direct（部分更新语义）。
func TestPutInboundKeepsOutboundWhenOmitted(t *testing.T) {
	ibStore := inbounds.NewMemoryStore()
	userStore := users.NewMemoryStore()
	nodeStore := nodes.NewMemoryStore()

	if _, err := ibStore.UpsertInbound(inbounds.Inbound{
		ID:         "ib-a",
		NodeID:     "n1",
		Protocol:   "vless",
		Tag:        "vless-a",
		Port:       443,
		OutboundID: "ob-1",
	}); err != nil {
		t.Fatalf("upsert inbound A: %v", err)
	}

	mux := http.NewServeMux()
	dial := func(nodeID string) (*nodes.Client, error) {
		return nil, errNodeNotFound
	}
	RegisterInboundsAPI(mux, ibStore, userStore, nodeStore, &fakeOutboundStore{}, dial, jobs.ApplyOptions{}, nil)

	// PUT 只改端口，省略 outbound_id 字段（部分更新）
	body, _ := json.Marshal(map[string]any{
		"node_id": "n1",
		"protocol": "vless",
		"port":    8443,
	})
	req := httptest.NewRequest(http.MethodPut, "/v1/inbounds/ib-a", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /v1/inbounds/ib-a status = %d, body = %s", w.Code, w.Body.String())
	}

	got, err := ibStore.GetInbound("ib-a")
	if err != nil {
		t.Fatalf("get inbound A: %v", err)
	}
	if got.OutboundID != "ob-1" {
		t.Fatalf("PUT 省略 outbound_id 后出口被清空: got %q, want %q", got.OutboundID, "ob-1")
	}
}
