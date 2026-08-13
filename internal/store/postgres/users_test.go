package postgres

import (
	"testing"
	"time"

	"pulse/internal/store/postgres/sqlcgen"
)

func TestToUserAcceptsPostgresTimestampText(t *testing.T) {
	expireAt := "2026-10-27 00:00:00+00"
	lastResetAt := "2026-08-13 01:47:02.204344+00"

	user, err := toUser(sqlcgen.User{
		ExpireAt:           &expireAt,
		LastTrafficResetAt: &lastResetAt,
	})
	if err != nil {
		t.Fatalf("toUser: %v", err)
	}

	wantExpireAt := time.Date(2026, time.October, 27, 0, 0, 0, 0, time.UTC)
	if user.ExpireAt == nil || !user.ExpireAt.Equal(wantExpireAt) {
		t.Fatalf("expire_at = %v, want %v", user.ExpireAt, wantExpireAt)
	}
	wantLastResetAt := time.Date(2026, time.August, 13, 1, 47, 2, 204344000, time.UTC)
	if user.LastTrafficResetAt == nil || !user.LastTrafficResetAt.Equal(wantLastResetAt) {
		t.Fatalf("last_traffic_reset_at = %v, want %v", user.LastTrafficResetAt, wantLastResetAt)
	}
}

func TestParseStoredTimeRejectsInvalidValue(t *testing.T) {
	if _, err := parseStoredTime("not-a-timestamp"); err == nil {
		t.Fatal("parseStoredTime accepted an invalid value")
	}
}
