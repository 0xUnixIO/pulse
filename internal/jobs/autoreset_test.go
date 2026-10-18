package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"pulse/internal/nodes"
	"pulse/internal/users"
)

// runOverQuotaSync 构造「alice 额度 100，本轮跑出 264（120×2.2）」的一轮 SyncUsage。
func runOverQuotaSync(t *testing.T, expireAt *time.Time, optIn bool) (users.Store, *callRecorder, SyncUsageResult) {
	t.Helper()
	nodeStore, userStore, ibStore := limitFixture(t, 100)

	alice, _ := userStore.GetUser("u1")
	alice.ExpireAt = expireAt
	if _, err := userStore.UpsertUser(alice); err != nil {
		t.Fatalf("upsert alice: %v", err)
	}
	if err := userStore.SetAutoTrafficReset("u1", optIn); err != nil {
		t.Fatalf("set auto reset: %v", err)
	}

	rec := newCallRecorder()
	dial := testDial(t, func(path string, w http.ResponseWriter, _ *http.Request) {
		rec.record(path)
		switch path {
		case "/v1/node/runtime/restart", "/v1/node/runtime/start":
			_ = json.NewEncoder(w).Encode(map[string]any{"running": true})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}
	})
	buf := nodes.NewUsageBuffer()
	if err := buf.Append("n1", 1, nodes.UsageStats{
		Available: true, Running: true,
		Users: []nodes.UserUsage{{User: "alice@vless-in", UploadTotal: 60, DownloadTotal: 60}},
	}); err != nil {
		t.Fatalf("append usage: %v", err)
	}
	res, err := SyncUsageWith(context.Background(), userStore, nodeStore, ibStore, dial, ApplyOptions{}, nil, buf)
	if err != nil {
		t.Fatalf("SyncUsageWith: %v", err)
	}
	return userStore, rec, res
}

func TestSyncUsage_AutoTrafficReset_EligibleStaysActive(t *testing.T) {
	expire := time.Now().UTC().Add(90 * 24 * time.Hour)
	store, rec, res := runOverQuotaSync(t, &expire, true)

	alice, _ := store.GetUser("u1")
	if alice.EffectiveStatus() != users.StatusActive {
		t.Fatalf("status=%s used=%d, want active after auto reset", alice.EffectiveStatus(), alice.UsedBytes)
	}
	if alice.UsedBytes != 0 || alice.UploadBytes != 0 || alice.DownloadBytes != 0 {
		t.Errorf("traffic not reset: up=%d down=%d used=%d", alice.UploadBytes, alice.DownloadBytes, alice.UsedBytes)
	}
	want := expire.AddDate(0, 0, -users.TrafficResetValidityCostDays)
	if alice.ExpireAt == nil || !alice.ExpireAt.Equal(want) {
		t.Errorf("expire_at=%v, want %v (扣减 30 天)", alice.ExpireAt, want)
	}
	if alice.LastTrafficResetAt == nil {
		t.Error("last_traffic_reset_at not set")
	}
	if res.AutoTrafficResets != 1 {
		t.Errorf("AutoTrafficResets=%d, want 1", res.AutoTrafficResets)
	}
	if got := rec.count("/v1/node/runtime/users/remove"); got != 0 {
		t.Errorf("RemoveUser=%d, want 0（自动重置后不应断开用户）", got)
	}
	if got := rec.count("/v1/node/runtime/users/kick"); got != 0 {
		t.Errorf("KickUser=%d, want 0", got)
	}
}

func TestSyncUsage_AutoTrafficReset_IneligibleGetsLimited(t *testing.T) {
	short := time.Now().UTC().Add(20 * 24 * time.Hour)
	long := time.Now().UTC().Add(90 * 24 * time.Hour)
	cases := []struct {
		name     string
		expireAt *time.Time
		optIn    bool
	}{
		{"opted-out", &long, false},
		{"validity under 30 days", &short, true},
		{"permanent account", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, rec, res := runOverQuotaSync(t, tc.expireAt, tc.optIn)
			alice, _ := store.GetUser("u1")
			if alice.EffectiveStatus() != users.StatusLimited {
				t.Fatalf("status=%s, want limited", alice.EffectiveStatus())
			}
			if alice.UsedBytes == 0 {
				t.Error("traffic should not be reset")
			}
			if tc.expireAt != nil && !alice.ExpireAt.Equal(*tc.expireAt) {
				t.Errorf("expire_at changed: %v -> %v", tc.expireAt, alice.ExpireAt)
			}
			if res.AutoTrafficResets != 0 {
				t.Errorf("AutoTrafficResets=%d, want 0", res.AutoTrafficResets)
			}
			if got := rec.count("/v1/node/runtime/users/remove"); got != 1 {
				t.Errorf("RemoveUser=%d, want 1（不满足条件时保持超限处置）", got)
			}
		})
	}
}

// 开关在用户已超限后才打开：TryAutoResetTraffic 立即重置，且不会对未超限用户重复扣减。
func TestTryAutoResetTraffic_AlreadyLimitedThenNoDoubleFire(t *testing.T) {
	expire := time.Now().UTC().Add(90 * 24 * time.Hour)
	store, _, _ := runOverQuotaSync(t, &expire, false)

	if err := store.SetAutoTrafficReset("u1", true); err != nil {
		t.Fatalf("set auto reset: %v", err)
	}
	now := time.Now().UTC()
	u, ok := TryAutoResetTraffic(context.Background(), store, "u1", now, nil)
	if !ok {
		t.Fatal("want reset for limited, opted-in, eligible user")
	}
	if u.EffectiveStatusAt(now) != users.StatusActive {
		t.Fatalf("status=%s, want active", u.EffectiveStatusAt(now))
	}
	if _, ok := TryAutoResetTraffic(context.Background(), store, "u1", now, nil); ok {
		t.Fatal("second call must not reset again (user no longer over quota)")
	}
	alice, _ := store.GetUser("u1")
	want := expire.AddDate(0, 0, -users.TrafficResetValidityCostDays)
	if !alice.ExpireAt.Equal(want) {
		t.Errorf("expire_at=%v, want %v (只扣减一次)", alice.ExpireAt, want)
	}
}
