package xray

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestRestartUnchangedConfigIsNoop 锁定当前 Restart 的短路：配置字节不变就
// 不换实例。落地 SS 出口对端抖动后配置哈希仍匹配，单靠下发配置无法自愈。
func TestRestartUnchangedConfigIsNoop(t *testing.T) {
	_, echoPort := startEchoServer(t)
	listenPort := freePort(t)

	m := NewManager("")
	cfg := dokodemoConfig(listenPort, echoPort, 0, "")
	if err := m.Start(cfg); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop() })

	first := m.Status().StartedAt
	if first.IsZero() {
		t.Fatal("startedAt should be set")
	}
	time.Sleep(20 * time.Millisecond)

	if err := m.Restart(cfg); err != nil {
		t.Fatalf("restart same config: %v", err)
	}
	if got := m.Status().StartedAt; !got.Equal(first) {
		t.Fatalf("Restart 配置未变却换了实例: before=%s after=%s", first, got)
	}
}

// TestRecycleReloadsUnchangedConfig 根治：Recycle 必须用当前配置做一次优雅重载，
// 即使 lastConfig 完全相同。
func TestRecycleReloadsUnchangedConfig(t *testing.T) {
	_, echoPort := startEchoServer(t)
	listenPort := freePort(t)

	m := NewManager("")
	cfg := dokodemoConfig(listenPort, echoPort, 0, "")
	if err := m.Start(cfg); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop() })

	first := m.Status().StartedAt
	time.Sleep(20 * time.Millisecond)

	if err := m.Recycle(); err != nil {
		t.Fatalf("recycle: %v", err)
	}
	got := m.Status().StartedAt
	if got.Equal(first) {
		t.Fatal("Recycle 配置未变却没有换实例，粘死的 SS 出口无法自愈")
	}
	if !m.Status().Running {
		t.Fatal("Recycle 后核心应仍在运行")
	}
}

// TestRecycleKeepsEstablishedConnections Recycle 与 Restart 一样走优雅重载：
// 存量连接留在旧实例，不能把还活着的 VLESS 用户一并踢掉。
func TestRecycleKeepsEstablishedConnections(t *testing.T) {
	_, echoPort := startEchoServer(t)
	listenPort := freePort(t)

	m := NewManager("")
	if err := m.Start(dokodemoConfig(listenPort, echoPort, 0, "")); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop() })

	conn := dialWithRetry(t, listenPort)
	defer conn.Close()
	if err := roundTrip(conn, "hello"); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	if err := m.Recycle(); err != nil {
		t.Fatalf("recycle: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	if err := roundTrip(conn, "after-recycle"); err != nil {
		t.Fatalf("Recycle 断开了存量连接，会误伤在线用户: %v", err)
	}
}

func TestRecycleNotRunning(t *testing.T) {
	m := NewManager("")
	if err := m.Recycle(); err == nil {
		t.Fatal("未运行时应返回错误")
	}
}

// TestRecycleAfterStopDoesNotStart 管理员 Stop 之后 lastConfig 还在。
// 迟到的 watchdog 若把 ErrNotRunning 当成可以 Start，会把刚停掉的核心拉起来。
func TestRecycleAfterStopDoesNotStart(t *testing.T) {
	_, echoPort := startEchoServer(t)
	listenPort := freePort(t)

	m := NewManager("")
	if err := m.Start(dokodemoConfig(listenPort, echoPort, 0, "")); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	err := m.Recycle()
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Recycle after Stop = %v, want ErrNotRunning", err)
	}
	if m.Status().Running {
		t.Fatal("Recycle 把已停止的核心拉起来了")
	}
}

// TestRestartInvalidConfigKeepsCore 配置解析失败必须发生在拆旧实例之前。
// 否则一次坏下发会把正在服务的核心停掉，并且磁盘上的可恢复配置也被删掉。
func TestRestartInvalidConfigKeepsCore(t *testing.T) {
	_, echoPort := startEchoServer(t)
	listenPort := freePort(t)
	path := t.TempDir() + "/xray.json"

	m := NewManager(path)
	cfg := dokodemoConfig(listenPort, echoPort, 0, "")
	if err := m.Start(cfg); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop() })

	if err := m.Restart("{"); err == nil {
		t.Fatal("非法 JSON 应返回错误")
	}
	if !m.Status().Running {
		t.Fatal("解析失败的 Restart 停掉了正在运行的核心")
	}
	if m.SavedConfig() == "" {
		t.Fatal("解析失败的 Restart 删掉了磁盘上的可恢复配置")
	}
}

// TestRecycleDrainClosesOldConnection 排空窗口内存量连接还在，到期后必须取消。
// 否则半开连接把旧 core 留在进程里，watchdog 每 10 分钟再叠一个。
func TestRecycleDrainClosesOldConnection(t *testing.T) {
	_, echoPort := startEchoServer(t)
	listenPort := freePort(t)

	m := NewManager("")
	m.replacedDrain = 2 * time.Second
	if err := m.Start(dokodemoConfig(listenPort, echoPort, 0, "")); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop() })

	conn := dialWithRetry(t, listenPort)
	defer conn.Close()
	if err := roundTrip(conn, "hello"); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if err := m.Recycle(); err != nil {
		t.Fatalf("recycle: %v", err)
	}
	if err := roundTrip(conn, "during-drain"); err != nil {
		t.Fatalf("排空窗口内不应断开存量连接: %v", err)
	}
	time.Sleep(3 * time.Second)
	if err := roundTrip(conn, "after-drain"); err == nil {
		t.Fatal("排空到期后旧连接仍可用，旧 core 不会释放")
	}
}

func TestRestartRecycleDoNotOverlap(t *testing.T) {
	_, echoPort := startEchoServer(t)
	listenPort := freePort(t)

	m := NewManager("")
	if err := m.Start(dokodemoConfig(listenPort, echoPort, 0, "")); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop() })

	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			errCh <- m.Recycle()
		}()
		go func(i int) {
			defer wg.Done()
			extra := freePort(t)
			errCh <- m.Restart(dokodemoConfig(listenPort, echoPort, extra, fmt.Sprintf("extra-%d", i)))
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("lifecycle: %v", err)
		}
	}
	if !m.Status().Running {
		t.Fatal("并发 Restart/Recycle 之后核心不在运行")
	}
}
