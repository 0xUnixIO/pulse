package sniproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestUnified_Mixed 在同一个端口上混合使用两种模式：
//   - sni "term.example.com" 做 TLS 终止，后端是明文 echo
//   - sni "pass.example.com" 做透明转发，后端是 TLS echo（由后端终止 TLS）
func TestUnified_Mixed(t *testing.T) {
	// 透明转发的后端：真实 TLS server
	transparentBackend := startEchoTLS(t, "pass.example.com")
	// 终止模式的后端：纯明文 echo（模拟 xray 明文 inbound）
	terminatingBackend := startEchoPlaintext(t)

	cert := genSelfSigned(t)
	proxy := &UnifiedProxy{Addr: "127.0.0.1:0"}
	proxy.SetTLSConfig(&tls.Config{Certificates: []tls.Certificate{cert}})
	proxy.SetRoutes([]Route{
		{SNI: "term.example.com", Backend: terminatingBackend, Mode: ModeTerminating},
		{SNI: "pass.example.com", Backend: transparentBackend, Mode: ModeTransparent},
	})

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	proxy.Addr = addr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = proxy.Serve(ctx) }()
	t.Cleanup(func() { cancel(); wg.Wait() })

	waitForListen(t, addr)

	// 两种 SNI 应各自走对应模式，都能 echo
	for _, sni := range []string{"term.example.com", "pass.example.com"} {
		t.Run(sni, func(t *testing.T) {
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			tc := tls.Client(conn, &tls.Config{ServerName: sni, InsecureSkipVerify: true})
			_ = tc.SetDeadline(time.Now().Add(3 * time.Second))
			if err := tc.Handshake(); err != nil {
				t.Fatalf("handshake: %v", err)
			}
			want := []byte("ping-" + sni)
			if _, err := tc.Write(want); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, len(want))
			if _, err := io.ReadFull(tc, got); err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("echo = %q, want %q", got, want)
			}
		})
	}
}

func TestUnified_TerminatingNeedsTLSConfig(t *testing.T) {
	proxy := &UnifiedProxy{Addr: "127.0.0.1:0"}
	proxy.SetRoutes([]Route{{SNI: "x", Backend: "127.0.0.1:1", Mode: ModeTerminating}})
	err := proxy.Serve(context.Background())
	if err == nil {
		t.Error("expected error when TLSConfig missing for terminating mode")
	}
}

func TestUnified_TransparentOnlyNoTLSConfig(t *testing.T) {
	// 只有透明路由时不需要 TLSConfig
	backend := startEchoTLS(t, "x.example.com")
	proxy := &UnifiedProxy{Addr: "127.0.0.1:0"}
	proxy.SetRoutes([]Route{{SNI: "x.example.com", Backend: backend, Mode: ModeTransparent}})

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	proxy.Addr = addr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = proxy.Serve(ctx) }()
	t.Cleanup(func() { cancel(); wg.Wait() })

	waitForListen(t, addr)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tc := tls.Client(conn, &tls.Config{ServerName: "x.example.com", InsecureSkipVerify: true})
	_ = tc.SetDeadline(time.Now().Add(3 * time.Second))
	if err := tc.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
}

func TestUnified_HotReloadSwitchesMode(t *testing.T) {
	// 先配成透明，再热切到终止
	transparentBackend := startEchoTLS(t, "x.example.com")
	terminatingBackend := startEchoPlaintext(t)

	cert := genSelfSigned(t)
	proxy := &UnifiedProxy{Addr: "127.0.0.1:0"}
	proxy.SetTLSConfig(&tls.Config{Certificates: []tls.Certificate{cert}})
	proxy.SetRoutes([]Route{
		{SNI: "x.example.com", Backend: transparentBackend, Mode: ModeTransparent},
	})

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	proxy.Addr = addr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = proxy.Serve(ctx) }()
	t.Cleanup(func() { cancel(); wg.Wait() })

	waitForListen(t, addr)

	// 切到终止模式
	proxy.SetRoutes([]Route{
		{SNI: "x.example.com", Backend: terminatingBackend, Mode: ModeTerminating},
	})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tc := tls.Client(conn, &tls.Config{ServerName: "x.example.com", InsecureSkipVerify: true})
	_ = tc.SetDeadline(time.Now().Add(3 * time.Second))
	if err := tc.Handshake(); err != nil {
		t.Fatalf("handshake after hot reload: %v", err)
	}
	if _, err := tc.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 2)
	if _, err := io.ReadFull(tc, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "hi" {
		t.Errorf("got %q, want 'hi'", got)
	}
}

func TestIsBenignPeekErr(t *testing.T) {
	if !isBenignPeekErr(io.EOF) {
		t.Fatal("EOF 应视为空连接，不打日志")
	}
	if !isBenignPeekErr(ErrNotTLS) {
		t.Fatal("非 TLS（扫描）不应刷屏")
	}
	if isBenignPeekErr(ErrNoSNI) {
		t.Fatal("ClientHello 无 SNI 必须打日志")
	}
}

// TestUnified_UnknownSNILogs 复现：未命中路由表的 SNI 以前静默断开，
// 测延迟失败时 journal 里看不到原因。必须打出 sni。
func TestUnified_UnknownSNILogs(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })

	backend := startEchoPlaintext(t)
	cert := genSelfSigned(t)
	proxy := &UnifiedProxy{Addr: "127.0.0.1:0"}
	proxy.SetTLSConfig(&tls.Config{Certificates: []tls.Certificate{cert}})
	proxy.SetRoutes([]Route{
		{SNI: "term.example.com", Backend: backend, Mode: ModeTerminating},
	})

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	proxy.Addr = addr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = proxy.Serve(ctx) }()
	t.Cleanup(func() { cancel(); wg.Wait() })
	waitForListen(t, addr)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	tc := tls.Client(conn, &tls.Config{ServerName: "unknown.example.com", InsecureSkipVerify: true})
	_ = tc.SetDeadline(time.Now().Add(2 * time.Second))
	_ = tc.Handshake()
	_ = conn.Close()
	time.Sleep(50 * time.Millisecond)

	got := buf.String()
	if !strings.Contains(got, "unknown sni=unknown.example.com") {
		t.Fatalf("未知 SNI 未打日志: %q", got)
	}
}
