package serverapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pulse/internal/inbounds"
	"pulse/internal/nodes"
	"pulse/internal/users"
)

func TestPortalResetTrafficAllowsSubTokenAuthentication(t *testing.T) {
	store := users.NewMemoryStore()
	expireAt := time.Now().UTC().AddDate(0, 0, 60)
	_, _ = store.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive, SubToken: "sub",
		ExpireAt: &expireAt, UploadBytes: 100,
	})
	api := &portalAPI{users: store, nodes: nodes.NewMemoryStore(), inbounds: inbounds.NewMemoryStore()}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/portal/sub/reset-traffic", nil)
	api.handlePortalPost(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	got, _ := store.GetUser("u1")
	if got.UploadBytes != 0 || got.ExpireAt == nil || !got.ExpireAt.Equal(expireAt.AddDate(0, 0, -30)) {
		t.Fatalf("sub-token reset did not update user: %+v", got)
	}
}

func TestPortalResetTrafficAtomicallyDeductsValidityAndPreservesEligibility(t *testing.T) {
	now := time.Now().UTC()
	expireAt := now.AddDate(0, 0, 60)
	store := users.NewMemoryStore()
	_, _ = store.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive, SubToken: "sub",
		ExpireAt: &expireAt, UploadBytes: 100, DownloadBytes: 50,
		RawUploadBytes: 80, RawDownloadBytes: 40,
	})
	api := &portalAPI{
		users: store, nodes: nodes.NewMemoryStore(), inbounds: inbounds.NewMemoryStore(),
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/portal/sub/reset-traffic", nil)
	api.handlePortalPost(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	got, _ := store.GetUser("u1")
	if got.UploadBytes != 0 || got.DownloadBytes != 0 || got.UsedBytes != 0 ||
		got.RawUploadBytes != 0 || got.RawDownloadBytes != 0 {
		t.Fatalf("traffic was not cleared: %+v", got)
	}
	wantExpireAt := expireAt.AddDate(0, 0, -30)
	if got.ExpireAt == nil || !got.ExpireAt.Equal(wantExpireAt) {
		t.Fatalf("expire_at = %v, want %v", got.ExpireAt, wantExpireAt)
	}
	if got.LastTrafficResetAt == nil {
		t.Fatal("last_traffic_reset_at was not set")
	}
}

func TestResetTrafficForValidityRejectsInvalidStatesAndBoundaries(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		status string
		used   int64
		expire *time.Time
	}{
		{name: "zero traffic", status: users.StatusActive, expire: timePtr(now.AddDate(0, 0, 60))},
		{name: "exactly 30 days", status: users.StatusActive, used: 1, expire: timePtr(now.AddDate(0, 0, 30))},
		{name: "less than 30 days", status: users.StatusActive, used: 1, expire: timePtr(now.AddDate(0, 0, 29))},
		{name: "permanent", status: users.StatusActive, used: 1},
		{name: "disabled", status: users.StatusDisabled, used: 1, expire: timePtr(now.AddDate(0, 0, 60))},
		{name: "on hold", status: users.StatusOnHold, used: 1, expire: timePtr(now.AddDate(0, 0, 60))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := users.NewMemoryStore()
			_, _ = store.UpsertUser(users.User{
				ID: "u1", Username: "alice", Status: tt.status,
				ExpireAt: tt.expire, UploadBytes: tt.used,
			})
			if _, err := store.ResetTrafficForValidity("u1", now, 30); !errors.Is(err, users.ErrTrafficResetNotAllowed) {
				t.Fatalf("error = %v, want %v", err, users.ErrTrafficResetNotAllowed)
			}
		})
	}
}

func timePtr(t time.Time) *time.Time { return &t }

func TestPortalAutoTrafficResetToggle(t *testing.T) {
	expireAt := time.Now().UTC().AddDate(0, 0, 90)
	store := users.NewMemoryStore()
	// 已超限（stored status 仍为 active，EffectiveStatus 计算为 limited）
	_, _ = store.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive, SubToken: "sub",
		ExpireAt: &expireAt, UploadBytes: 200, TrafficLimit: 100,
	})
	api := &portalAPI{users: store, nodes: nodes.NewMemoryStore(), inbounds: inbounds.NewMemoryStore()}

	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/portal/sub/auto-traffic-reset", strings.NewReader(body))
		api.handlePortalPost(rec, req)
		return rec
	}

	if rec := post(`{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled: status=%d, want 400", rec.Code)
	}

	// 开启：已超限且满足条件 → 立即重置
	rec := post(`{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	got, _ := store.GetUser("u1")
	if !got.AutoTrafficReset || got.UsedBytes != 0 || !got.ExpireAt.Equal(expireAt.AddDate(0, 0, -30)) {
		t.Fatalf("enable did not persist flag / reset immediately: %+v", got)
	}
	if !strings.Contains(rec.Body.String(), `"reset":true`) {
		t.Errorf("response should report reset: %s", rec.Body.String())
	}

	// 关闭
	if rec := post(`{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("disable status=%d", rec.Code)
	}
	got, _ = store.GetUser("u1")
	if got.AutoTrafficReset {
		t.Fatal("flag should be off")
	}
}
