package sniproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// RouteMode 决定一条 SNI 路由的处理方式。
// 定义为 string 类型：序列化到 JSON 和控制面 wire format 天然一致，
// 避免 int 常量与字符串枚举间静默类型错位。
type RouteMode string

const (
	// ModeTransparent 原样 TCP 转发，不终止 TLS。用于前置节点中转到落地节点。
	ModeTransparent RouteMode = "transparent"
	// ModeTerminating 在本地终止 TLS 后 TCP 明文转发。用于落地节点给本地 xray。
	// 后端按裸字节处理（AnyTLS/Trojan 协议），不做 HTTP 层解析。
	ModeTerminating RouteMode = "terminating"
	// ModeHTTPReverse 在本地终止 TLS 后作为 HTTP 反向代理转发给后端。
	// 用于面板/内部服务的 HTTPS 反代：后端是普通 HTTP 服务，sniproxy 会注入
	// X-Forwarded-For / X-Forwarded-Proto / X-Real-IP 头，并正确处理 WebSocket
	// Upgrade、HTTP/2 ↔ HTTP/1.1 等。
	ModeHTTPReverse RouteMode = "http-reverse"
)

// Route 描述 UnifiedProxy 的一条路由规则。
type Route struct {
	SNI     string    `json:"sni"`
	Backend string    `json:"backend"` // "host:port"
	Mode    RouteMode `json:"mode"`    // Transparent 或 Terminating
}

// UnifiedProxy 在单个 TCP 端口上监听，按客户端 TLS SNI 分流：
//   - 命中 ModeTransparent 的 SNI：原样透传，不终止 TLS
//   - 命中 ModeTerminating 的 SNI：终止 TLS（TLSConfig.GetCertificate 选证书）后转发明文
//
// 这是 NodeGate 的核心实现。
//
// 并发：路由表和 TLS 配置都通过 atomic.Pointer 热更新；Serve 运行期间
// 调用 SetRoutes / SetTLSConfig 均安全。
type UnifiedProxy struct {
	// Addr 监听地址，如 ":443"
	Addr string
	// HandshakeTimeout 读 ClientHello 的超时，0 = 10 秒
	HandshakeTimeout time.Duration
	// DialTimeout 连接后端的超时，0 = 5 秒
	DialTimeout time.Duration

	routes    atomic.Pointer[map[string]Route]
	tlsConfig atomic.Pointer[tls.Config]   // 启动前和运行时均可通过 SetTLSConfig 更新
	ln        atomic.Pointer[net.Listener] // Serve 成功 Listen 后写入，Close/外部可安全读
	wg        sync.WaitGroup
	closed    atomic.Bool

	// Started 在 net.Listen 成功后关闭，供 Manager 判断启动已完成。
	// Serve 返回前已 close（无论正常还是异常退出），调用方可以 select 在它上面等。
	Started chan struct{}
}

// SetTLSConfig 设置或热更新用于 ModeTerminating 路由的 TLS 配置。
// 传 nil 表示不支持终止模式。
func (p *UnifiedProxy) SetTLSConfig(cfg *tls.Config) {
	p.tlsConfig.Store(cfg)
}

// SetRoutes 热更新路由表。
func (p *UnifiedProxy) SetRoutes(rs []Route) {
	m := make(map[string]Route, len(rs))
	for _, r := range rs {
		if r.SNI != "" && r.Backend != "" {
			m[r.SNI] = r
		}
	}
	p.routes.Store(&m)
}

// Serve 启动监听循环，阻塞直到 ctx 取消。
// 返回前（无论成功还是失败）会关闭 p.Started，供调用方同步等待启动完成。
func (p *UnifiedProxy) Serve(ctx context.Context) error {
	if p.Started == nil {
		p.Started = make(chan struct{})
	}
	startedClosed := false
	closeStartedOnce := func() {
		if !startedClosed {
			close(p.Started)
			startedClosed = true
		}
	}
	defer closeStartedOnce()

	routes := p.routes.Load()
	if routes == nil {
		return fmt.Errorf("sniproxy: routes not configured")
	}
	// Terminating 和 HTTPReverse 都需要终止 TLS，必须提供 TLSConfig
	hasTLSRoute := false
	for _, r := range *routes {
		if r.Mode == ModeTerminating || r.Mode == ModeHTTPReverse {
			hasTLSRoute = true
			break
		}
	}
	if hasTLSRoute {
		cfg := p.tlsConfig.Load()
		if cfg == nil {
			return fmt.Errorf("sniproxy: TLSConfig required for terminating routes")
		}
		if cfg.GetCertificate == nil && len(cfg.Certificates) == 0 {
			return fmt.Errorf("sniproxy: TLSConfig needs GetCertificate or Certificates")
		}
	}

	lnVal, err := net.Listen("tcp", p.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", p.Addr, err)
	}
	p.ln.Store(&lnVal)
	closeStartedOnce() // 监听成功 → 通知 Manager Apply 可以返回

	go func() {
		<-ctx.Done()
		p.closed.Store(true)
		_ = lnVal.Close()
	}()

	for {
		conn, err := lnVal.Accept()
		if err != nil {
			if p.closed.Load() {
				p.wg.Wait()
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		p.wg.Add(1)
		go func(c net.Conn) {
			defer p.wg.Done()
			p.handle(c)
		}(conn)
	}
}

// Close 主动关闭监听。可在任意时刻调用，包括 Serve 尚未完成 Listen 时
// （此时 p.ln 还未写入，Close 成为 no-op）。
//
// 不在此处 wg.Wait：Serve 循环结束前自己已经 wg.Wait 等完活动连接，Close 再
// 调 Wait 会和 Serve 循环里的 wg.Add 形成 race（Add / Wait 并发是非法的）。
// 调用方如需等 in-flight 连接真正结束，应等 Serve goroutine 返回（例如读
// Manager 的 serveErr channel）。
func (p *UnifiedProxy) Close() error {
	p.closed.Store(true)
	if ln := p.ln.Load(); ln != nil {
		_ = (*ln).Close()
	}
	return nil
}

func (p *UnifiedProxy) handle(conn net.Conn) {
	defer conn.Close()

	hsTimeout := p.HandshakeTimeout
	if hsTimeout == 0 {
		hsTimeout = 10 * time.Second
	}
	_ = conn.SetReadDeadline(time.Now().Add(hsTimeout))

	// 先 peek SNI（会消耗 ClientHello 字节，需要后面回放或重新握手）
	sni, peeked, err := PeekSNI(conn)
	if err != nil {
		if !isBenignPeekErr(err) {
			log.Printf("sniproxy: peek sni=%q: %v", sni, err)
		}
		return
	}

	routes := p.routes.Load()
	if routes == nil {
		return
	}
	route, ok := (*routes)[sni]
	if !ok {
		log.Printf("sniproxy: unknown sni=%s", sni)
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	dialTimeout := p.DialTimeout
	if dialTimeout == 0 {
		dialTimeout = 5 * time.Second
	}

	switch route.Mode {
	case ModeTransparent:
		upstream, err := net.DialTimeout("tcp", route.Backend, dialTimeout)
		if err != nil {
			log.Printf("sniproxy: transparent dial %s (sni=%s): %v", route.Backend, sni, err)
			return
		}
		defer upstream.Close()
		if _, err := upstream.Write(peeked); err != nil {
			return
		}
		pipe(conn, upstream)

	case ModeTerminating:
		tlsCfg := p.tlsConfig.Load()
		if tlsCfg == nil {
			return
		}
		// 用 prefixedConn 把已 peek 的字节重新放回流前，让 tls.Server 看到完整 ClientHello
		prefixed := &prefixedConn{Conn: conn, prefix: peeked}
		tlsConn := tls.Server(prefixed, tlsCfg)
		_ = tlsConn.SetDeadline(time.Now().Add(hsTimeout))
		if err := tlsConn.Handshake(); err != nil {
			log.Printf("sniproxy: handshake sni=%s: %v", sni, err)
			return
		}
		_ = tlsConn.SetDeadline(time.Time{})

		upstream, err := net.DialTimeout("tcp", route.Backend, dialTimeout)
		if err != nil {
			log.Printf("sniproxy: terminating dial %s (sni=%s): %v", route.Backend, sni, err)
			return
		}
		defer upstream.Close()
		pipe(tlsConn, upstream)

	case ModeHTTPReverse:
		tlsCfg := p.tlsConfig.Load()
		if tlsCfg == nil {
			return
		}
		prefixed := &prefixedConn{Conn: conn, prefix: peeked}
		tlsConn := tls.Server(prefixed, tlsCfg)
		_ = tlsConn.SetDeadline(time.Now().Add(hsTimeout))
		if err := tlsConn.Handshake(); err != nil {
			log.Printf("sniproxy: handshake sni=%s: %v", sni, err)
			return
		}
		_ = tlsConn.SetDeadline(time.Time{})

		handleHTTPReverse(tlsConn, route.Backend, sni)
	}
}

// isBenignPeekErr 空连接、超时、明文扫描不打日志，避免 443 被扫时刷屏。
// ClientHello 解析失败、无 SNI 必须留下，才能分清测延迟失败是包没到还是 Hello 坏了。
func isBenignPeekErr(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, ErrNotTLS) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// handleHTTPReverse 用 httputil.ReverseProxy 在已握好的 TLS 连接上服务 HTTP，
// 代理到本地 HTTP 后端。注入 X-Forwarded-For/Proto/X-Real-IP。
// WebSocket Upgrade、HTTP/2 ↔ HTTP/1.1 翻译、SSE 流式由 Go 标准库处理。
func handleHTTPReverse(tlsConn *tls.Conn, backend, sni string) {
	backendURL, err := url.Parse("http://" + backend)
	if err != nil {
		log.Printf("sniproxy: http-reverse invalid backend %q (sni=%s): %v", backend, sni, err)
		return
	}

	clientIP, _, _ := net.SplitHostPort(tlsConn.RemoteAddr().String())

	rp := httputil.NewSingleHostReverseProxy(backendURL)
	originalDirector := rp.Director
	rp.Director = func(r *http.Request) {
		originalDirector(r)
		// Host 用原始 SNI，否则后端会看到 127.0.0.1:xxx
		r.Host = sni
		if clientIP != "" {
			r.Header.Set("X-Real-IP", clientIP)
			// 保留上游已有的 X-Forwarded-For 链路
			if prior := r.Header.Get("X-Forwarded-For"); prior != "" {
				r.Header.Set("X-Forwarded-For", prior+", "+clientIP)
			} else {
				r.Header.Set("X-Forwarded-For", clientIP)
			}
		}
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", sni)
	}
	rp.FlushInterval = -1 // SSE / 流式响应无缓冲
	rp.ErrorLog = log.New(log.Writer(), fmt.Sprintf("sniproxy http-reverse %s: ", sni), 0)

	// wg 跟踪 handler goroutine 生命周期。WebSocket Upgrade 时 http.Server 会
	// hijack 连接并继续在 handler goroutine 里做双向 pipe；srv.Serve 在
	// StateHijacked 触发 ln.Close() 后立即返回，但 pipe goroutine 仍在跑。
	// wg.Wait() 确保 handleHTTPReverse 在 pipe 真正结束前不退出，避免外层
	// handle() 的 defer conn.Close() 提前关闭底层 TCP 连接杀死 WebSocket。
	var wg sync.WaitGroup
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wg.Add(1)
		defer wg.Done()
		rp.ServeHTTP(w, r)
	})

	ln := &singleConnListener{conn: tlsConn, done: make(chan struct{})}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second, // 浏览器保留连接复用 TLS，但空闲 60s 后回收
	}
	srv.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateClosed || s == http.StateHijacked {
			_ = ln.Close()
		}
	}
	_ = srv.Serve(ln)
	wg.Wait()
}

// singleConnListener 是 http.Server.Serve 所需的 net.Listener 适配器：
// 第一次 Accept 返回预置的 conn，之后阻塞在 done channel 上。
// 由 http.Server.ConnState 回调在连接关闭时 Close 此 listener 解阻塞，
// Serve 返回。这样 Serve 的生命周期严格跟 conn 对齐，调用方可以同步等 Serve 返回。
type singleConnListener struct {
	conn   net.Conn
	served atomic.Bool
	done   chan struct{}
	once   sync.Once
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	if l.served.CompareAndSwap(false, true) {
		return l.conn, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *singleConnListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *singleConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

// prefixedConn 是一个 net.Conn 包装，Read 时先返回 prefix 中的字节再读真实 Conn。
// 用于把 PeekSNI 消耗的 ClientHello 字节回放给 tls.Server。
type prefixedConn struct {
	net.Conn
	prefix []byte
}

func (c *prefixedConn) Read(b []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(b, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(b)
}
