package xray

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// startEchoServer 启动一个本地 TCP echo 服务，作为 xray 转发的目标。
func startEchoServer(t *testing.T) (addr string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(c)
		}
	}()

	tcpAddr := ln.Addr().(*net.TCPAddr)
	return ln.Addr().String(), tcpAddr.Port
}

// freePort 取一个当前空闲的端口号供 xray 监听。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe free port: %v", err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return p
}

// dokodemoConfig 构造一份最小 xray 配置：一个 dokodemo-door 入站直通到 echo 服务。
// extraTag 非空时追加一个无关的额外入站，用来让配置字节发生变化，
// 从而绕过 Manager.Restart 的 config == lastConfig 短路。
func dokodemoConfig(listenPort, echoPort, extraPort int, extraTag string) string {
	inbounds := []map[string]any{{
		"tag":      "dk",
		"listen":   "127.0.0.1",
		"port":     listenPort,
		"protocol": "dokodemo-door",
		"settings": map[string]any{
			"address": "127.0.0.1",
			"port":    echoPort,
			"network": "tcp",
		},
	}}
	if extraTag != "" {
		inbounds = append(inbounds, map[string]any{
			"tag":      extraTag,
			"listen":   "127.0.0.1",
			"port":     extraPort,
			"protocol": "dokodemo-door",
			"settings": map[string]any{
				"address": "127.0.0.1",
				"port":    echoPort,
				"network": "tcp",
			},
		})
	}
	cfg := map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  inbounds,
		"outbounds": []map[string]any{{"protocol": "freedom", "tag": "direct"}},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

// roundTrip 在连接上写一行并读回，验证链路仍然通。
func roundTrip(c net.Conn, payload string) error {
	if err := c.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	if _, err := c.Write([]byte(payload)); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(c, buf); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if string(buf) != payload {
		return fmt.Errorf("echo mismatch: got %q want %q", buf, payload)
	}
	return nil
}

// TestRestartKeepsEstablishedConnections 锁定 Restart 的优雅重载语义：
// 配置下发只换监听与新连接的处理实例，存量连接留在旧实例上自然结束，
// 不因一次配置变更把在线用户全部断掉。
//
// 这也解释了为什么超限止血不能靠重启：即使重启，超限用户的存量连接照样在跑。
// 需要立刻切断某个用户时用 KickUser（见 removeuser_conn_test.go），
// 它精确到用户且不波及同节点其他人。
func TestRestartKeepsEstablishedConnections(t *testing.T) {
	_, echoPort := startEchoServer(t)
	listenPort := freePort(t)
	extraPort := freePort(t)

	m := NewManager(t.TempDir() + "/xray.json")
	cfg1 := dokodemoConfig(listenPort, echoPort, 0, "")
	if err := m.Start(cfg1); err != nil {
		t.Fatalf("start xray: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop() })

	conn := dialWithRetry(t, listenPort)
	defer conn.Close()

	// 建链后先确认通路正常。
	if err := roundTrip(conn, "hello"); err != nil {
		t.Fatalf("baseline round trip failed: %v", err)
	}

	// 换一份配置触发真实重启（避免 config == lastConfig 短路）。
	cfg2 := dokodemoConfig(listenPort, echoPort, extraPort, "extra")
	if err := m.Restart(cfg2); err != nil {
		t.Fatalf("restart xray: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	// 关键断言：重启不应误伤存量连接。
	if err := roundTrip(conn, "after-restart"); err != nil {
		t.Fatalf("配置重载断开了存量连接，会误伤在线用户: %v", err)
	}
}

// TestStopClosesEstablishedConnections 验证显式 Stop 会断开存量连接：
// 服务停止后连接不应继续存活。这与 Restart 的优雅重载是两种不同语义。
func TestStopClosesEstablishedConnections(t *testing.T) {
	_, echoPort := startEchoServer(t)
	listenPort := freePort(t)

	m := NewManager(t.TempDir() + "/xray.json")
	if err := m.Start(dokodemoConfig(listenPort, echoPort, 0, "")); err != nil {
		t.Fatalf("start xray: %v", err)
	}

	conn := dialWithRetry(t, listenPort)
	defer conn.Close()
	if err := roundTrip(conn, "hello"); err != nil {
		t.Fatalf("baseline round trip failed: %v", err)
	}

	if err := m.Stop(); err != nil {
		t.Fatalf("stop xray: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	if err := roundTrip(conn, "after-stop"); err == nil {
		t.Fatal("Stop 后存量连接仍可收发数据")
	}
}
