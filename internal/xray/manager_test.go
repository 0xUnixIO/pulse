package xray

import (
	"fmt"
	"sort"
	"testing"

	"github.com/0xUnixIO/Xray-core/app/stats/command"

	"pulse/internal/coremanager"
)

func TestApplyTrafficStatsUsesInboundTotals(t *testing.T) {
	result := coremanager.UsageStats{}
	applyTrafficStats(&result, []*command.Stat{
		{Name: "user>>>alice@vless-in>>>traffic>>>uplink", Value: 70},
		{Name: "user>>>alice@vless-in>>>traffic>>>downlink", Value: 30},
		{Name: "inbound>>>vless-in>>>traffic>>>uplink", Value: 170},
		{Name: "inbound>>>vless-in>>>traffic>>>downlink", Value: 230},
		{Name: "outbound>>>direct>>>traffic>>>uplink", Value: 999},
	})

	if result.UploadTotal != 170 || result.DownloadTotal != 230 {
		t.Fatalf("node traffic=%d/%d, want 170/230", result.UploadTotal, result.DownloadTotal)
	}
	if len(result.Users) != 1 || result.Users[0].UploadTotal != 70 || result.Users[0].DownloadTotal != 30 {
		t.Fatalf("user traffic=%+v, want alice 70/30", result.Users)
	}
}

func TestApplyTrafficStatsFallsBackToUserTotals(t *testing.T) {
	result := coremanager.UsageStats{}
	applyTrafficStats(&result, []*command.Stat{
		{Name: "user>>>alice@vless-in>>>traffic>>>uplink", Value: 70},
		{Name: "user>>>alice@vless-in>>>traffic>>>downlink", Value: 30},
	})

	if result.UploadTotal != 70 || result.DownloadTotal != 30 {
		t.Fatalf("node traffic=%d/%d, want fallback 70/30", result.UploadTotal, result.DownloadTotal)
	}
}

func TestParseSessionLog_AccessLog_IPv4(t *testing.T) {
	m := NewManager("")
	m.mu.Lock()
	m.parseSessionLog("from 1.2.3.4:12345 accepted tcp:example.com:443 [proxy] email: alice@vless-in")
	m.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	ips := m.sessionSourceIPs["alice@vless-in"]
	if len(ips) != 1 {
		t.Fatalf("expected 1 IP, got %d", len(ips))
	}
	if _, ok := ips["1.2.3.4"]; !ok {
		t.Errorf("expected IP 1.2.3.4, got %v", ips)
	}
}

func TestParseSessionLog_AccessLog_IPv6(t *testing.T) {
	m := NewManager("")
	m.mu.Lock()
	m.parseSessionLog("from [2001:db8::1]:8080 accepted tcp:example.com:443 [proxy] email: bob@trojan-in")
	m.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	ips := m.sessionSourceIPs["bob@trojan-in"]
	if len(ips) != 1 {
		t.Fatalf("expected 1 IP, got %d", len(ips))
	}
	if _, ok := ips["2001:db8::1"]; !ok {
		t.Errorf("expected IP 2001:db8::1, got %v", ips)
	}
}

func TestParseSessionLog_AccessLog_Rejected(t *testing.T) {
	m := NewManager("")
	m.mu.Lock()
	m.parseSessionLog("from 1.2.3.4:12345 rejected tcp:example.com:443 email: alice@vless-in")
	m.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessionSourceIPs) != 0 {
		t.Errorf("rejected connections should not be tracked, got %v", m.sessionSourceIPs)
	}
}

func TestParseSessionLog_AccessLog_NoEmail(t *testing.T) {
	m := NewManager("")
	m.mu.Lock()
	m.parseSessionLog("from 1.2.3.4:12345 accepted tcp:example.com:443 [proxy]")
	m.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessionSourceIPs) != 0 {
		t.Errorf("no email lines should not be tracked, got %v", m.sessionSourceIPs)
	}
}

func TestParseSessionLog_AccessLog_MultipleIPs(t *testing.T) {
	m := NewManager("")
	m.mu.Lock()
	m.parseSessionLog("from 1.1.1.1:111 accepted tcp:a.com:443 [p] email: alice@vless-in")
	m.parseSessionLog("from 2.2.2.2:222 accepted tcp:b.com:443 [p] email: alice@vless-in")
	m.parseSessionLog("from 1.1.1.1:333 accepted tcp:c.com:443 [p] email: alice@vless-in") // duplicate IP
	m.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	ips := m.sessionSourceIPs["alice@vless-in"]
	if len(ips) != 2 {
		t.Fatalf("expected 2 unique IPs, got %d: %v", len(ips), ips)
	}
}

func TestParseSessionLog_AccessLog_MultipleUsers(t *testing.T) {
	m := NewManager("")
	m.mu.Lock()
	m.parseSessionLog("from 1.1.1.1:111 accepted tcp:a.com:443 [p] email: alice@vless-in")
	m.parseSessionLog("from 2.2.2.2:222 accepted tcp:b.com:443 [p] email: alice@trojan-in")
	m.parseSessionLog("from 3.3.3.3:333 accepted tcp:c.com:443 [p] email: bob@vless-in")
	m.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessionSourceIPs["alice@vless-in"]) != 1 {
		t.Errorf("alice@vless-in: want 1 IP, got %v", m.sessionSourceIPs["alice@vless-in"])
	}
	if len(m.sessionSourceIPs["alice@trojan-in"]) != 1 {
		t.Errorf("alice@trojan-in: want 1 IP, got %v", m.sessionSourceIPs["alice@trojan-in"])
	}
	if len(m.sessionSourceIPs["bob@vless-in"]) != 1 {
		t.Errorf("bob@vless-in: want 1 IP, got %v", m.sessionSourceIPs["bob@vless-in"])
	}
}

func TestParseSessionLog_SessionTracking(t *testing.T) {
	m := NewManager("")
	m.mu.Lock()
	// 开启会话
	m.parseSessionLog("[Info] [42] proxy/anytls: anytls: alice@vless-in tunnelling to tcp:example.com:443")
	m.parseSessionLog("[Info] [43] proxy/anytls: anytls: bob@vless-in tunnelling to tcp:example.com:443")
	m.mu.Unlock()

	m.mu.Lock()
	if len(m.activeSessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(m.activeSessions))
	}

	// 关闭一个会话
	m.parseSessionLog("[Info] [42] app/proxyman/outbound: failed to process outbound traffic")
	m.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.activeSessions) != 1 {
		t.Fatalf("expected 1 session after close, got %d", len(m.activeSessions))
	}
	if m.activeSessions["43"] != "bob@vless-in" {
		t.Errorf("unexpected remaining session: %v", m.activeSessions)
	}
}

func TestParseSessionLog_ConnectionsPerCompositeUser(t *testing.T) {
	m := NewManager("")
	m.mu.Lock()
	// alice has 2 vless connections and 1 trojan connection
	m.parseSessionLog("[Info] [10] proxy/anytls: anytls: alice@vless-in tunnelling to tcp:a.com:443")
	m.parseSessionLog("[Info] [11] proxy/anytls: anytls: alice@vless-in tunnelling to tcp:b.com:443")
	m.parseSessionLog("[Info] [12] proxy/anytls: anytls: alice@trojan-in tunnelling to tcp:c.com:443")
	m.mu.Unlock()

	// Count connections per composite user (not per real user)
	m.mu.Lock()
	defer m.mu.Unlock()
	connCount := make(map[string]int)
	for _, compositeUser := range m.activeSessions {
		connCount[compositeUser]++
	}
	if connCount["alice@vless-in"] != 2 {
		t.Errorf("alice@vless-in: want 2 connections, got %d", connCount["alice@vless-in"])
	}
	if connCount["alice@trojan-in"] != 1 {
		t.Errorf("alice@trojan-in: want 1 connection, got %d", connCount["alice@trojan-in"])
	}
}

func TestParseAccessLog_WithDetour(t *testing.T) {
	m := NewManager("")
	m.mu.Lock()
	// Access log with compound detour (e.g., "inbound >> outbound")
	m.parseSessionLog("from 10.0.0.1:5555 accepted tcp:api.example.com:443 [vless-in >> direct] email: carol@vless-in")
	m.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	ips := m.sessionSourceIPs["carol@vless-in"]
	if len(ips) != 1 {
		t.Fatalf("expected 1 IP, got %d", len(ips))
	}
	if _, ok := ips["10.0.0.1"]; !ok {
		t.Errorf("expected IP 10.0.0.1, got %v", ips)
	}
}

func TestParseAccessLog_ResetClearsIPs(t *testing.T) {
	m := NewManager("")

	// Simulate access log entries
	m.mu.Lock()
	m.parseSessionLog("from 1.1.1.1:111 accepted tcp:a.com:443 [p] email: alice@vless-in")
	m.parseSessionLog("from 2.2.2.2:222 accepted tcp:b.com:443 [p] email: alice@vless-in")
	m.mu.Unlock()

	// Simulate Usage(reset=true): snapshot and clear
	m.mu.Lock()
	snapshot := make(map[string][]string)
	for user, ips := range m.sessionSourceIPs {
		list := make([]string, 0, len(ips))
		for ip := range ips {
			list = append(list, ip)
		}
		sort.Strings(list)
		snapshot[user] = list
	}
	m.sessionSourceIPs = make(map[string]map[string]struct{})
	m.mu.Unlock()

	// Verify snapshot
	ips := snapshot["alice@vless-in"]
	if len(ips) != 2 {
		t.Fatalf("snapshot: want 2 IPs, got %d", len(ips))
	}

	// After reset, new access logs should start fresh
	m.mu.Lock()
	m.parseSessionLog("from 3.3.3.3:333 accepted tcp:c.com:443 [p] email: alice@vless-in")
	m.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	newIPs := m.sessionSourceIPs["alice@vless-in"]
	if len(newIPs) != 1 {
		t.Fatalf("after reset: want 1 IP, got %d", len(newIPs))
	}
	if _, ok := newIPs["3.3.3.3"]; !ok {
		t.Errorf("expected 3.3.3.3, got %v", newIPs)
	}
}

func TestParseSessionLog_NonLogLines(t *testing.T) {
	m := NewManager("")
	m.mu.Lock()
	// These should not panic or modify state
	m.parseSessionLog("")
	m.parseSessionLog("random text")
	m.parseSessionLog("xray started (in-process, version 1.0)")
	m.parseSessionLog("[Info] no session id here")
	m.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.activeSessions) != 0 {
		t.Errorf("expected no sessions, got %v", m.activeSessions)
	}
	if len(m.sessionSourceIPs) != 0 {
		t.Errorf("expected no IPs, got %v", m.sessionSourceIPs)
	}
}

func TestParseAccessLog_InvalidIP(t *testing.T) {
	m := NewManager("")
	m.mu.Lock()
	// "baddata" is not a valid IP, should be rejected
	m.parseSessionLog("from baddata accepted tcp:a.com:443 [p] email: alice@vless-in")
	m.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessionSourceIPs) != 0 {
		t.Errorf("invalid IP should not be tracked, got %v", m.sessionSourceIPs)
	}
}

func TestParseAccessLog_IPCap(t *testing.T) {
	m := NewManager("")
	m.mu.Lock()
	// Add maxSourceIPsPerUser + 50 unique IPs
	for i := 0; i < maxSourceIPsPerUser+50; i++ {
		ip := fmt.Sprintf("10.%d.%d.%d", i/65536%256, i/256%256, i%256)
		m.parseSessionLog(fmt.Sprintf("from %s:1234 accepted tcp:a.com:443 [p] email: alice@vless-in", ip))
	}
	m.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	got := len(m.sessionSourceIPs["alice@vless-in"])
	if got != maxSourceIPsPerUser {
		t.Errorf("IP cap: want %d, got %d", maxSourceIPsPerUser, got)
	}
}

func TestParseAccessLog_EmailInDestination(t *testing.T) {
	// Edge case: " email: " appears in both destination and actual email field.
	// strings.LastIndex should pick the real email at the end.
	m := NewManager("")
	m.mu.Lock()
	m.parseSessionLog("from 1.2.3.4:5678 accepted tcp:evil.com/path?q= email: fake@tag [proxy] email: alice@vless-in")
	m.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessionSourceIPs["alice@vless-in"]; !ok {
		t.Errorf("should track the real email, got %v", m.sessionSourceIPs)
	}
	if _, ok := m.sessionSourceIPs["fake@tag [proxy]"]; ok {
		t.Errorf("should not track the fake email embedded in destination")
	}
}
