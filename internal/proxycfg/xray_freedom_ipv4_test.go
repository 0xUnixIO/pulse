package proxycfg

import (
	"encoding/json"
	"testing"

	"pulse/internal/inbounds"
	"pulse/internal/users"
)

// TestDirectFreedomUsesIPv4 锁定：默认 direct 出口必须 UseIPv4。
// 未写 domainStrategy 时 xray 为 AS_IS，Go resolver 在双栈机器上优先 AAAA，
// 测延迟第二跳（cp.cloudflare.com）会走 IPv6，短超时容易红。
func TestDirectFreedomUsesIPv4(t *testing.T) {
	ib := inbounds.Inbound{
		ID:       "ib-a",
		NodeID:   "n1",
		Protocol: "vless",
		Tag:      "vless-a",
		Port:     443,
	}
	user := users.User{ID: "u-1", Username: "alice", Status: users.StatusActive, UUID: "11111111-1111-1111-1111-111111111111"}
	raw, err := BuildXrayConfig(
		[]inbounds.Inbound{ib},
		[]users.UserInbound{{ID: "ui-1", UserID: "u-1", InboundID: "ib-a", NodeID: "n1", UUID: user.UUID}},
		map[string]users.User{"u-1": user},
		BuildOptions{NodeID: "n1"},
	)
	if err != nil {
		t.Fatalf("BuildXrayConfig: %v", err)
	}

	var cfg struct {
		Outbounds []struct {
			Protocol string         `json:"protocol"`
			Tag      string         `json:"tag"`
			Settings map[string]any `json:"settings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, ob := range cfg.Outbounds {
		if ob.Tag != "direct" || ob.Protocol != "freedom" {
			continue
		}
		got, _ := ob.Settings["domainStrategy"].(string)
		if got != "UseIPv4" {
			t.Fatalf("direct freedom domainStrategy=%q，期望 UseIPv4\n%s", got, raw)
		}
		if OutboundConfigStale(raw) {
			t.Fatalf("新生成的配置不应被判定为出口过代:\n%s", raw)
		}
		return
	}
	t.Fatalf("未找到 tag=direct 的 freedom 出口:\n%s", raw)
}
