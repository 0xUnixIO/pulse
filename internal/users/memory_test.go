package users

import "testing"

// UpsertUser 整行回写不得覆盖自动重置开关（与 Postgres 实现一致）。
func TestMemoryStore_UpsertUserKeepsAutoTrafficReset(t *testing.T) {
	s := NewMemoryStore()
	if _, err := s.UpsertUser(User{ID: "u1", Username: "a"}); err != nil {
		t.Fatal(err)
	}
	stale, _ := s.GetUser("u1")
	if err := s.SetAutoTrafficReset("u1", true); err != nil {
		t.Fatal(err)
	}
	stale.UploadBytes = 10
	if _, err := s.UpsertUser(stale); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetUser("u1")
	if !got.AutoTrafficReset {
		t.Fatal("stale UpsertUser cleared auto_traffic_reset")
	}
	if err := s.SetAutoTrafficReset("missing", true); err != ErrUserNotFound {
		t.Fatalf("err=%v, want ErrUserNotFound", err)
	}
}
