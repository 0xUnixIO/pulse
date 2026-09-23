package xray

import (
	"testing"
	"time"
)

func TestIsSSOutboundDialFail(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{
			line: `app/proxyman/outbound: failed to process outbound traffic > proxy/shadowsocks_2022: failed to connect to server > dial tcp 68.77.201.119:23941: i/o timeout`,
			want: true,
		},
		{
			line: `proxy/shadowsocks_2022: failed to connect to server > dial tcp 1.2.3.4:1: i/o timeout`,
			want: true,
		},
		{
			line: `from 127.0.0.1:1 accepted tcp:www.gstatic.com:443 [anytls-33500 >> out-nodeib:x] email: cc@anytls-33500`,
			want: false,
		},
		{
			line: `proxy/freedom: connection ends > use of closed network connection`,
			want: false,
		},
	}
	for _, tc := range cases {
		if got := isSSOutboundDialFail(tc.line); got != tc.want {
			t.Errorf("isSSOutboundDialFail(%q)=%v want %v", tc.line, got, tc.want)
		}
	}
}

func TestNoteSSDialFailTriggersAfterThreshold(t *testing.T) {
	m := NewManager("")
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Date(2026, 9, 14, 5, 55, 0, 0, time.UTC)

	if m.noteSSDialFailLocked(now) {
		t.Fatal("1 次失败不应 Recycle")
	}
	if m.noteSSDialFailLocked(now.Add(time.Second)) {
		t.Fatal("2 次失败不应 Recycle")
	}
	if !m.noteSSDialFailLocked(now.Add(2 * time.Second)) {
		t.Fatal("窗口内 3 次 SS 拨号失败应触发 Recycle")
	}
	if m.noteSSDialFailLocked(now.Add(3 * time.Second)) {
		t.Fatal("已排队时应抑制重复 Recycle")
	}
}

func TestNoteSSDialFailCooldown(t *testing.T) {
	m := NewManager("")
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Date(2026, 9, 14, 5, 55, 0, 0, time.UTC)
	m.lastRecycleAt = now
	if m.noteSSDialFailLocked(now.Add(time.Second)) {
		t.Fatal("冷却窗口内第 1 次不应 Recycle")
	}
	if m.noteSSDialFailLocked(now.Add(2 * time.Second)) {
		t.Fatal("冷却窗口内第 2 次不应 Recycle")
	}
	if m.noteSSDialFailLocked(now.Add(3 * time.Second)) {
		t.Fatal("冷却窗口内即使凑满阈值也不应 Recycle")
	}

	t1 := now.Add(ssRecycleCooldown + time.Second)
	if m.noteSSDialFailLocked(t1) {
		t.Fatal("冷却结束后 1 次失败不应 Recycle")
	}
	if m.noteSSDialFailLocked(t1.Add(time.Second)) {
		t.Fatal("冷却结束后 2 次失败不应 Recycle")
	}
	if !m.noteSSDialFailLocked(t1.Add(2 * time.Second)) {
		t.Fatal("冷却结束后窗口内 3 次失败应再次 Recycle")
	}
}

func TestNoteSSDialFailIgnoresOldSamples(t *testing.T) {
	m := NewManager("")
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Date(2026, 9, 14, 5, 55, 0, 0, time.UTC)
	m.ssFailAt = []time.Time{
		now.Add(-ssDialFailWindow - time.Minute),
		now.Add(-ssDialFailWindow - time.Second),
	}
	if m.noteSSDialFailLocked(now) {
		t.Fatal("窗口外的失败不应计入")
	}
}
