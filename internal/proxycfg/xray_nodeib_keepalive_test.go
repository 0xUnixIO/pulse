package proxycfg

import (
	"encoding/json"
	"testing"

	"pulse/internal/inbounds"
	"pulse/internal/nodes"
	"pulse/internal/users"
)

// TestNodeInboundOutboundEnablesTCPKeepAlive 落地 SS 出口必须带 TCP keepalive，
// 否则对端 NAT/住宅线抖动后半开连接会一直粘着，探测还能通、长连接回不来。
func TestNodeInboundOutboundEnablesTCPKeepAlive(t *testing.T) {
	ibSS := inbounds.Inbound{
		ID:       "ib-ss",
		NodeID:   "n2",
		Protocol: "shadowsocks",
		Tag:      "ss-1",
		Port:     23941,
		Method:   "2022-blake3-aes-128-gcm",
		Password: "c2VydmVyLXBzay0xNmJ5dGVzMDA=",
	}
	ibA := inbounds.Inbound{
		ID:         "ib-a",
		NodeID:     "n1",
		Protocol:   "anytls",
		Tag:        "anytls-33500",
		Port:       43295,
		OutboundID: NodeInboundPrefix + "ib-ss:ui-ss",
	}
	raw, err := BuildXrayConfig(
		[]inbounds.Inbound{ibA},
		[]users.UserInbound{{ID: "ui-1", UserID: "u-1", InboundID: "ib-a", NodeID: "n1", Secret: "s"}},
		map[string]users.User{
			"u-1":  {ID: "u-1", Username: "alice", Status: users.StatusActive, Secret: "s"},
			"u-ss": {ID: "u-ss", Username: "bob", Status: users.StatusActive, Secret: "global-secret-ss"},
		},
		BuildOptions{
			NodeID: "n1",
			AllInboundMap: map[string]inbounds.Inbound{
				"ib-ss": ibSS,
			},
			AllNodeMap: map[string]nodes.Node{
				"n2": {ID: "n2", Name: "att", BaseURL: "https://68.77.201.119:8081"},
			},
			UserInboundMap: map[string]users.UserInbound{
				"ui-ss": {ID: "ui-ss", UserID: "u-ss", InboundID: "ib-ss", NodeID: "n2", Secret: "user-secret-ss"},
			},
		},
	)
	if err != nil {
		t.Fatalf("BuildXrayConfig: %v", err)
	}

	var cfg struct {
		Outbounds []struct {
			Protocol       string `json:"protocol"`
			Tag            string `json:"tag"`
			StreamSettings *struct {
				Sockopt *struct {
					TCPKeepAliveIdle     int `json:"tcpKeepAliveIdle"`
					TCPKeepAliveInterval int `json:"tcpKeepAliveInterval"`
				} `json:"sockopt"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	wantTag := "out-" + NodeInboundPrefix + "ib-ss:ui-ss"
	for _, ob := range cfg.Outbounds {
		if ob.Tag != wantTag {
			continue
		}
		if ob.Protocol != "shadowsocks" {
			t.Fatalf("nodeib 出口 protocol=%q", ob.Protocol)
		}
		if ob.StreamSettings == nil || ob.StreamSettings.Sockopt == nil {
			t.Fatalf("nodeib 出口缺少 sockopt keepalive:\n%s", raw)
		}
		if ob.StreamSettings.Sockopt.TCPKeepAliveIdle <= 0 || ob.StreamSettings.Sockopt.TCPKeepAliveInterval <= 0 {
			t.Fatalf("keepalive 未启用: %+v", ob.StreamSettings.Sockopt)
		}
		return
	}
	t.Fatalf("未找到 nodeib 出口 %s:\n%s", wantTag, raw)
}
