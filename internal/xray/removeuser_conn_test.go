package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"
)

const (
	uuidAlice = "11111111-1111-1111-1111-111111111111"
	uuidBob   = "22222222-2222-2222-2222-222222222222"
)

// vlessServerConfig 构造 vless 服务端配置：一个 inbound 承载多个用户，出站直连。
func vlessServerConfig(listenPort int, clients []map[string]any) string {
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "debug"},
		"inbounds": []map[string]any{{
			"tag":      "vless-in",
			"listen":   "127.0.0.1",
			"port":     listenPort,
			"protocol": "vless",
			"settings": map[string]any{
				"clients":    clients,
				"decryption": "none",
			},
		}},
		// 代理类入站默认禁止访问私有地址（防内网穿透），测试的 echo 服务在
		// 回环地址上，需显式放行，否则 freedom 会 blackhole 掉连接。
		"outbounds": []map[string]any{{
			"protocol": "freedom",
			"tag":      "direct",
			"settings": map[string]any{
				"finalRules": []map[string]any{{
					"action": "allow",
					"ip":     []string{"127.0.0.0/8"},
				}},
			},
		}},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

// vlessClientConfig 构造 vless 客户端配置：dokodemo-door 入口经 vless 出站到服务端。
func vlessClientConfig(listenPort, echoPort, serverPort int, uuid string) string {
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "debug"},
		"inbounds": []map[string]any{{
			"tag":      "dk",
			"listen":   "127.0.0.1",
			"port":     listenPort,
			"protocol": "dokodemo-door",
			"settings": map[string]any{
				"address": "127.0.0.1",
				"port":    echoPort,
				"network": "tcp",
			},
		}},
		"outbounds": []map[string]any{{
			"protocol": "vless",
			"tag":      "proxy",
			"settings": map[string]any{
				"vnext": []map[string]any{{
					"address": "127.0.0.1",
					"port":    serverPort,
					"users": []map[string]any{{
						"id":         uuid,
						"encryption": "none",
					}},
				}},
			},
		}},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

// dialWithRetry 重试拨号，等待 inbound 起监听。
func dialWithRetry(t *testing.T, port int) net.Conn {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err == nil {
			return c
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("dial 127.0.0.1:%d 超时", port)
	return nil
}

// vlessPair 起一组 vless 服务端 + 每用户一个客户端，返回服务端 manager 与各用户入口端口。
func vlessPair(t *testing.T, emails ...string) (*Manager, map[string]int) {
	t.Helper()
	_, echoPort := startEchoServer(t)
	serverPort := freePort(t)

	uuidByEmail := map[string]string{"alice": uuidAlice, "bob": uuidBob}
	clients := make([]map[string]any, 0, len(emails))
	for _, e := range emails {
		clients = append(clients, map[string]any{"id": uuidByEmail[e], "email": e})
	}

	server := NewManager(t.TempDir() + "/server.json")
	if err := server.Start(vlessServerConfig(serverPort, clients)); err != nil {
		t.Fatalf("start vless server: %v", err)
	}
	t.Cleanup(func() { _ = server.Stop() })

	ports := make(map[string]int, len(emails))
	for _, e := range emails {
		p := freePort(t)
		client := NewManager(t.TempDir() + "/client-" + e + ".json")
		if err := client.Start(vlessClientConfig(p, echoPort, serverPort, uuidByEmail[e])); err != nil {
			t.Fatalf("start vless client %s: %v", e, err)
		}
		t.Cleanup(func() { _ = client.Stop() })
		ports[e] = p
	}
	return server, ports
}

// TestRemoveUserDoesNotDropEstablishedConnection 实测「把用户移除」是否能阻止
// 超限用户继续跑流量。
//
// 结论：不能。xray 的鉴权只在连接建立时做一次，RemoveUser 仅执行
// validator.Del(email)，影响的是后续新连接；已建立的连接不再回查 validator，
// 会一直跑到自己结束。这正是 100 GB 额度能跑到 128 GB 的原因。
func TestRemoveUserDoesNotDropEstablishedConnection(t *testing.T) {
	server, ports := vlessPair(t, "alice")

	conn := dialWithRetry(t, ports["alice"])
	defer conn.Close()

	if err := roundTrip(conn, "before-remove"); err != nil {
		t.Fatalf("建链后基线收发失败: %v", err)
	}

	if err := server.RemoveUser(context.Background(), "vless-in", "alice"); err != nil {
		t.Fatalf("RemoveUser: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	// 存量连接：移除用户后仍然通——这就是问题所在。
	errExisting := roundTrip(conn, "after-remove")

	// 新连接：应当已被拒绝，证明 RemoveUser 本身确实生效了。
	newConn := dialWithRetry(t, ports["alice"])
	defer newConn.Close()
	errNew := roundTrip(newConn, "new-conn")

	if errNew == nil {
		t.Fatal("RemoveUser 后新连接仍能建立，测试前提不成立")
	}
	if errExisting != nil {
		t.Fatalf("存量连接已断开（%v）——与预期的 xray 行为不符，"+
			"若 xray 已支持移除即断连，则超限执行无需重启", errExisting)
	}
	t.Log("已确认：RemoveUser 拒绝新连接，但存量连接不受影响，仍可继续跑流量")
}

// TestKickUserDropsOnlyTargetUser 验证按用户精确断连：超限用户的存量连接被立即
// 切断，同节点其他用户的连接不受任何影响。
//
// 这是「超限必须立刻止血」与「不能误伤其他用户」两个要求的交点：重启实例能断连
// 但会波及全节点，RemoveUser 不波及他人却断不掉存量连接，只有 KickUser 两者兼顾。
func TestKickUserDropsOnlyTargetUser(t *testing.T) {
	server, ports := vlessPair(t, "alice", "bob")

	aliceConn := dialWithRetry(t, ports["alice"])
	defer aliceConn.Close()
	bobConn := dialWithRetry(t, ports["bob"])
	defer bobConn.Close()

	if err := roundTrip(aliceConn, "alice-before"); err != nil {
		t.Fatalf("alice 基线收发失败: %v", err)
	}
	if err := roundTrip(bobConn, "bob-before"); err != nil {
		t.Fatalf("bob 基线收发失败: %v", err)
	}

	// 模拟超限处置：先热删拦新连接，再踢掉存量连接。
	if err := server.RemoveUser(context.Background(), "vless-in", "alice"); err != nil {
		t.Fatalf("RemoveUser(alice): %v", err)
	}
	kicked, err := server.KickUser(context.Background(), "alice")
	if err != nil {
		t.Fatalf("KickUser(alice): %v", err)
	}
	if kicked == 0 {
		t.Fatal("KickUser 未断开任何连接，alice 的存量连接仍在跑")
	}
	time.Sleep(300 * time.Millisecond)

	if err := roundTrip(aliceConn, "alice-after"); err == nil {
		t.Error("alice 的存量连接在 KickUser 后仍可收发，超限流量没有被止住")
	}

	// 关键：bob 完全不受影响。
	if err := roundTrip(bobConn, "bob-after"); err != nil {
		t.Errorf("bob 的连接被误伤: %v", err)
	}
	t.Logf("已断开 alice 的 %d 条连接，bob 不受影响", kicked)
}

// TestKickUserUnknownEmailIsNoop 保证踢一个不存在/无连接的用户是安全的空操作。
func TestKickUserUnknownEmailIsNoop(t *testing.T) {
	server, ports := vlessPair(t, "alice")

	conn := dialWithRetry(t, ports["alice"])
	defer conn.Close()
	if err := roundTrip(conn, "alice-before"); err != nil {
		t.Fatalf("alice 基线收发失败: %v", err)
	}

	kicked, err := server.KickUser(context.Background(), "nobody")
	if err != nil {
		t.Fatalf("KickUser(nobody): %v", err)
	}
	if kicked != 0 {
		t.Errorf("踢不存在的用户断开了 %d 条连接", kicked)
	}
	if err := roundTrip(conn, "alice-after"); err != nil {
		t.Errorf("alice 被误伤: %v", err)
	}
}
