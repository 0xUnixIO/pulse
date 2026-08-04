package proxycfg

import (
	"encoding/json"
	"testing"

	"pulse/internal/inbounds"
	"pulse/internal/nodes"
	"pulse/internal/outbounds"
	"pulse/internal/users"
)

// TestExistingInboundOutboundRetainedAfterCreate 复现：节点上已有 inbound A
// 绑定出口 ob-1，新建 inbound B（默认无出口）后重新生成配置，
// A 的出口规则必须保留，不能被重置为 direct。
func TestExistingInboundOutboundRetainedAfterCreate(t *testing.T) {
	// 已有 inbound A：绑定出口 ob-1
	ibA := inbounds.Inbound{
		ID:         "ib-a",
		NodeID:     "n1",
		Protocol:   "vless",
		Tag:        "vless-a",
		Port:       443,
		OutboundID: "ob-1",
	}
	// 新建 inbound B：默认无出口（direct）
	ibB := inbounds.Inbound{
		ID:       "ib-b",
		NodeID:   "n1",
		Protocol: "vless",
		Tag:      "vless-b",
		Port:     8443,
	}

	user := users.User{ID: "u-1", Username: "alice", Status: users.StatusActive, UUID: "11111111-1111-1111-1111-111111111111"}
	accA := users.UserInbound{ID: "ui-1", UserID: "u-1", InboundID: "ib-a", NodeID: "n1", UUID: user.UUID}

	opts := BuildOptions{
		NodeID: "n1",
		OutboundMap: map[string]outbounds.Outbound{
			"ob-1": {ID: "ob-1", Name: "HK1", Protocol: "ss", Server: "1.2.3.4:8388", Method: "aes-256-gcm", Password: "x"},
		},
	}

	raw, err := BuildXrayConfig(
		[]inbounds.Inbound{ibA, ibB},
		[]users.UserInbound{accA},
		map[string]users.User{"u-1": user},
		opts,
	)
	if err != nil {
		t.Fatalf("BuildXrayConfig: %v", err)
	}

	var cfg struct {
		Routing struct {
			Rules []struct {
				InboundTag  []string `json:"inboundTag"`
				OutboundTag string   `json:"outboundTag"`
			} `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	found := false
	for _, r := range cfg.Routing.Rules {
		for _, tag := range r.InboundTag {
			if tag == "vless-a" {
				found = true
				if r.OutboundTag == "direct" {
					t.Fatalf("已有 inbound vless-a 的出口被重置为 direct，规则: %+v", r)
				}
				if r.OutboundTag != "out-ob-1" {
					t.Fatalf("已有 inbound vless-a 出口 = %q，期望 out-ob-1", r.OutboundTag)
				}
			}
		}
	}
	if !found {
		t.Fatalf("配置中未找到 inbound vless-a 的出口规则:\n%s", raw)
	}
}

// TestExistingNodeInboundOutboundRetainedAfterCreate 覆盖 nodeib: 前缀出口
// （以另一节点上的 shadowsocks inbound 用户作为出口）。新建 inbound 后，
// 已有 inbound 的 nodeib 出口规则必须保留。
func TestExistingNodeInboundOutboundRetainedAfterCreate(t *testing.T) {
	// 出口指向的 SS inbound（在节点 n2 上）
	ibSS := inbounds.Inbound{
		ID:       "ib-ss",
		NodeID:   "n2",
		Protocol: "shadowsocks",
		Tag:      "ss-1",
		Port:     34192,
		Method:   "2022-blake3-aes-128-gcm",
		Password: "c2VydmVyLXBzay0xNmJ5dGVzMDA=",
	}
	nodeN2 := nodes.Node{ID: "n2", Name: "n2", BaseURL: "http://landing.test"}
	uibSS := users.UserInbound{ID: "ui-ss", UserID: "u-ss", InboundID: "ib-ss", NodeID: "n2", Secret: "user-secret-ss"}
	userSS := users.User{ID: "u-ss", Username: "bob", Status: users.StatusActive, Secret: "global-secret-ss"}

	// 已有 inbound A：出口 = nodeib:ib-ss:ui-ss
	ibA := inbounds.Inbound{
		ID:         "ib-a",
		NodeID:     "n1",
		Protocol:   "vless",
		Tag:        "vless-a",
		Port:       443,
		OutboundID: NodeInboundPrefix + "ib-ss:ui-ss",
	}
	// 新建 inbound B（本节点 n1）：默认无出口
	ibB := inbounds.Inbound{
		ID:       "ib-b",
		NodeID:   "n1",
		Protocol: "vless",
		Tag:      "vless-b",
		Port:     8443,
	}

	userA := users.User{ID: "u-1", Username: "alice", Status: users.StatusActive, UUID: "11111111-1111-1111-1111-111111111111"}
	accA := users.UserInbound{ID: "ui-1", UserID: "u-1", InboundID: "ib-a", NodeID: "n1", UUID: userA.UUID}

	opts := BuildOptions{
		NodeID: "n1",
		AllInboundMap: map[string]inbounds.Inbound{
			"ib-ss": ibSS,
		},
		AllNodeMap: map[string]nodes.Node{
			"n2": nodeN2,
		},
		UserInboundMap: map[string]users.UserInbound{
			"ui-ss": uibSS,
		},
	}

	raw, err := BuildXrayConfig(
		[]inbounds.Inbound{ibA, ibB},
		[]users.UserInbound{accA},
		map[string]users.User{"u-1": userA, "u-ss": userSS},
		opts,
	)
	if err != nil {
		t.Fatalf("BuildXrayConfig: %v", err)
	}

	var cfg struct {
		Routing struct {
			Rules []struct {
				InboundTag  []string `json:"inboundTag"`
				OutboundTag string   `json:"outboundTag"`
			} `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	wantTag := "out-" + NodeInboundPrefix + "ib-ss:ui-ss"
	found := false
	for _, r := range cfg.Routing.Rules {
		for _, tag := range r.InboundTag {
			if tag == "vless-a" {
				found = true
				if r.OutboundTag == "direct" {
					t.Fatalf("已有 inbound vless-a 的 nodeib 出口被重置为 direct，规则: %+v", r)
				}
				if r.OutboundTag != wantTag {
					t.Fatalf("已有 inbound vless-a 出口 = %q，期望 %q", r.OutboundTag, wantTag)
				}
			}
		}
	}
	if !found {
		t.Fatalf("配置中未找到 inbound vless-a 的 nodeib 出口规则:\n%s", raw)
	}
}
