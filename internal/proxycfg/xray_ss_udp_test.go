package proxycfg

import (
	"encoding/json"
	"strings"
	"testing"

	xraynet "github.com/0xUnixIO/Xray-core/common/net"
	"github.com/0xUnixIO/Xray-core/infra/conf/serial"
	ss2022 "github.com/0xUnixIO/Xray-core/proxy/shadowsocks_2022"

	"pulse/internal/inbounds"
	"pulse/internal/users"
)

// buildSSConfig 构造一个最小的 shadowsocks inbound 场景并返回生成的 Xray 配置
// （解析后的 map 与原始 JSON 文本）。
func buildSSConfig(t *testing.T) (map[string]any, string) {
	t.Helper()

	ib := inbounds.Inbound{
		ID:       "ib-ss",
		NodeID:   "node-1",
		Protocol: "shadowsocks",
		Tag:      "ss-test",
		Port:     8388,
		Method:   "2022-blake3-aes-128-gcm",
		Password: "c2VydmVyLXBzay0xNmJ5dGVzMDA=",
	}
	user := users.User{
		ID:       "u-1",
		Username: "alice",
		Status:   users.StatusActive,
		Secret:   "global-user-secret-1",
	}
	acc := users.UserInbound{
		ID:        "ui-1",
		UserID:    "u-1",
		InboundID: "ib-ss",
		NodeID:    "node-1",
		Secret:    "user-secret-1",
	}

	raw, err := BuildXrayConfig(
		[]inbounds.Inbound{ib},
		[]users.UserInbound{acc},
		map[string]users.User{"u-1": user},
		BuildOptions{NodeID: "node-1"},
	)
	if err != nil {
		t.Fatalf("BuildXrayConfig 失败: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("生成的配置不是合法 JSON: %v\n%s", err, raw)
	}
	return cfg, raw
}

// ssInboundSettings 从配置中取出 shadowsocks inbound 的 settings。
func ssInboundSettings(t *testing.T, cfg map[string]any) map[string]any {
	t.Helper()

	ibs, ok := cfg["inbounds"].([]any)
	if !ok || len(ibs) == 0 {
		t.Fatalf("配置中没有 inbounds: %#v", cfg["inbounds"])
	}
	for _, raw := range ibs {
		ib, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if ib["protocol"] != "shadowsocks" {
			continue
		}
		settings, ok := ib["settings"].(map[string]any)
		if !ok {
			t.Fatalf("shadowsocks inbound 缺少 settings: %#v", ib)
		}
		return settings
	}
	t.Fatalf("未找到 shadowsocks inbound")
	return nil
}

// TestShadowsocksInboundEnablesUDP 复现并锁定：ss inbound 必须显式声明 network 同时包含
// tcp 和 udp。省略 network 时 Xray 侧 NetworkList.Build() 会返回 [TCP]，导致 UDP 被静默禁用
// （inbound_multi.go 的 TCP+UDP 兜底仅在 len(networks)==0 时触发，永远走不到）。
func TestShadowsocksInboundEnablesUDP(t *testing.T) {
	cfg, _ := buildSSConfig(t)
	settings := ssInboundSettings(t, cfg)

	network, ok := settings["network"].(string)
	if !ok {
		t.Fatalf("ss inbound settings 缺少 network 字段，Xray 会缺省为 TCP-only；settings=%#v", settings)
	}
	if network != "tcp,udp" {
		t.Errorf("ss inbound network = %q，期望 %q", network, "tcp,udp")
	}
}

// TestShadowsocksInboundUDPAfterXrayParse 端到端验证：把生成的配置交给 Xray 自己的解析器，
// 断言最终 MultiUserServerConfig.Network 同时包含 TCP 与 UDP。
// 只断言 JSON 文本不够——真正决定 UDP 是否可用的是 NetworkList.Build() 的结果。
func TestShadowsocksInboundUDPAfterXrayParse(t *testing.T) {
	_, raw := buildSSConfig(t)

	coreCfg, err := serial.LoadJSONConfig(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Xray 无法解析生成的配置: %v\n%s", err, raw)
	}

	for _, ih := range coreCfg.Inbound {
		inst, err := ih.ProxySettings.GetInstance()
		if err != nil {
			t.Fatalf("解析 inbound ProxySettings 失败: %v", err)
		}
		ss, ok := inst.(*ss2022.MultiUserServerConfig)
		if !ok {
			continue
		}
		var hasTCP, hasUDP bool
		for _, n := range ss.Network {
			switch n {
			case xraynet.Network_TCP:
				hasTCP = true
			case xraynet.Network_UDP:
				hasUDP = true
			}
		}
		if !hasTCP {
			t.Errorf("ss2022 inbound 未启用 TCP，Network=%v", ss.Network)
		}
		if !hasUDP {
			t.Errorf("ss2022 inbound 未启用 UDP，Network=%v", ss.Network)
		}
		return
	}
	t.Fatal("解析结果中未找到 ss2022 MultiUserServerConfig")
}
