package serverapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pulse/internal/inbounds"
	"pulse/internal/nodes"
	"pulse/internal/users"
)

type portalResetSessionStore struct {
	token  string
	userID string
}

func (s *portalResetSessionStore) Create(token, userID string, _ time.Time) error {
	s.token, s.userID = token, userID
	return nil
}
func (s *portalResetSessionStore) GetUserID(token string) (string, bool) {
	return s.userID, token == s.token && s.userID != ""
}
func (s *portalResetSessionStore) Delete(string) error         { return nil }
func (s *portalResetSessionStore) DeleteByUserID(string) error { return nil }

func TestPortalResetTrafficRequiresPasswordSession(t *testing.T) {
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

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	got, _ := store.GetUser("u1")
	if got.UploadBytes != 100 || !got.ExpireAt.Equal(expireAt) {
		t.Fatalf("unauthorized reset mutated user: %+v", got)
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
	sessions := &portalResetSessionStore{token: "session", userID: "u1"}
	api := &portalAPI{
		users: store, nodes: nodes.NewMemoryStore(), inbounds: inbounds.NewMemoryStore(),
		sessions: sessions,
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/portal/sub/reset-traffic", nil)
	req.AddCookie(&http.Cookie{Name: "pulse_portal_session", Value: "session"})
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
			if _, err := store.ResetTrafficForValidity("u1", now, 30); err != users.ErrTrafficResetNotAllowed {
				t.Fatalf("error = %v, want %v", err, users.ErrTrafficResetNotAllowed)
			}
		})
	}
}

func timePtr(t time.Time) *time.Time { return &t }
