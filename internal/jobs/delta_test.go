package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"pulse/internal/inbounds"
	"pulse/internal/nodes"
	"pulse/internal/users"
)

// 同一用户在同一 inbound 上同时有直接分配和组分配时，热更新只能操作一次：
// 第二次 RemoveUser 会被 Xray 以 not found 拒绝，导致回退整节点重启。
func TestTryDeltaUsers_DedupDirectAndGroupAccess(t *testing.T) {
	ibs := []inbounds.Inbound{{ID: "ib1", Protocol: "anytls", Tag: "anytls-1"}}
	accesses := []users.UserInbound{
		{ID: "a1", UserID: "u1", InboundID: "ib1", Secret: "s"},
		{ID: "a2", UserID: "u1", InboundID: "ib1", Secret: "s", GroupID: "g1"},
	}
	userMap := map[string]users.User{"u1": {ID: "u1", Username: "alice"}}

	for _, tc := range []struct {
		name    string
		enabled bool
		method  string
	}{
		{"remove", false, "RemoveUser"},
		{"add", true, "AddUser"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			hub := &fakeHub{handlers: map[string]func(string, any) (json.RawMessage, error){
				tc.method: func(string, any) (json.RawMessage, error) {
					calls++
					if calls > 1 {
						return nil, errors.New("user not found / already exists")
					}
					return json.RawMessage(`{}`), nil
				},
			}}
			client := nodes.NewClientWithHub("n1", hub)

			ok := tryDeltaUsers(context.Background(), client, ibs, accesses, userMap, map[string]bool{"u1": tc.enabled})
			if !ok {
				t.Fatalf("tryDeltaUsers=false, want true（重复分配不应回退整节点重启）")
			}
			if calls != 1 {
				t.Errorf("%s calls=%d, want 1", tc.method, calls)
			}
		})
	}
}
