package proxycfg

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"pulse/internal/inbounds"
	"pulse/internal/nodes"
	"pulse/internal/outbounds"
	"pulse/internal/routerules"
	"pulse/internal/users"
)

// deriveSecret 从用户全局 Secret 派生指定字节长度的 SS 2022 密钥。
// 使用 HMAC-SHA256 确保确定性，不同密钥长度得到不同结果。
func deriveSecret(secret string, keyLen int) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("ss2022-%d", keyLen)))
	key := mac.Sum(nil)[:keyLen]
	return base64.StdEncoding.EncodeToString(key)
}

// NodeInboundPrefix 是路由规则中节点 inbound 出口 ID 的前缀。
const NodeInboundPrefix = "nodeib:"

// UserInboundSep 是 V2Ray Stats 用户名中用户名与 inbound tag 的分隔符。
// xray 配置中用户名格式为 "username@inbound_tag"，以实现按 inbound 维度的流量统计。
const UserInboundSep = "@"

// ProbeInboundPort 探针专用内部 HTTP 入站端口，仅监听 127.0.0.1，
// 供 pulse-node 解锁检测时将流量经 xray 路由规则转发。
const ProbeInboundPort = 16799

// BuildOptions 控制 BuildXrayConfig 的可选行为。
type BuildOptions struct {
	OutboundMap    map[string]outbounds.Outbound
	RouteRules     []routerules.RouteRule
	NodeID         string
	AllInboundMap  map[string]inbounds.Inbound
	AllNodeMap     map[string]nodes.Node
	UserInboundMap map[string]users.UserInbound
	// CertPathFor 根据域名返回本机已托管的 TLS 证书 / 私钥路径。
	// hy2 inbound 必须由 Xray 自己加载 TLS 证书（UDP/QUIC，无法由 NodeGate 终止）。
	// 返回的路径必须是 Xray 进程可读的本地文件。nil 时 hy2 inbound 渲染会失败。
	CertPathFor func(domain string) (certFile, keyFile string, err error)
	// KnownReadyCerts 列出 panel 已确认在节点磁盘上落地的证书域名。
	// 非 nil 时，hy2 inbound 渲染会先检查 SNI 是否在该集合中：不在则跳过该 inbound
	// （不报错，仅 warning），下次 apply 待证书就绪后自动加回。nil 表示不检查。
	KnownReadyCerts map[string]bool
}

// Xray 配置顶层结构
type xrayConfig struct {
	Log       xrayLog             `json:"log"`
	API       *xrayAPI            `json:"api,omitempty"`
	Stats     *struct{}           `json:"stats,omitempty"`
	Policy    *xrayPolicy         `json:"policy,omitempty"`
	Inbounds  []xrayInbound       `json:"inbounds"`
	Outbounds []xrayOutbound      `json:"outbounds"`
	Routing   *xrayRouting        `json:"routing,omitempty"`
}

type xrayLog struct {
	Loglevel string `json:"loglevel"`
}

// xrayAPI 启用 V2Ray 流量统计 API
type xrayAPI struct {
	Tag      string   `json:"tag"`
	Services []string `json:"services"`
	Listen   string   `json:"listen"`
}

type xrayPolicy struct {
	Levels map[string]xrayPolicyLevel `json:"levels"`
	System xraySystemPolicy           `json:"system"`
}

type xrayPolicyLevel struct {
	StatsUserUplink   bool `json:"statsUserUplink"`
	StatsUserDownlink bool `json:"statsUserDownlink"`
}

type xraySystemPolicy struct {
	StatsInboundUplink   bool `json:"statsInboundUplink"`
	StatsInboundDownlink bool `json:"statsInboundDownlink"`
}

type xrayInbound struct {
	Tag            string              `json:"tag"`
	Listen         string              `json:"listen,omitempty"`
	Port           int                 `json:"port"`
	Protocol       string              `json:"protocol"`
	Settings       xrayInboundSettings `json:"settings"`
	StreamSettings *xrayStream         `json:"streamSettings,omitempty"`
}

type xrayAnyTLSUser struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type xrayInboundSettings struct {
	Clients    []xrayClient        `json:"clients,omitempty"`
	Users      []xrayAnyTLSUser    `json:"users,omitempty"`      // anytls
	Decryption string              `json:"decryption,omitempty"` // vless 必须 "none"
	Network    string              `json:"network,omitempty"`    // ss
	Method     string              `json:"method,omitempty"`     // ss
	Password   string              `json:"password,omitempty"`   // ss server PSK（单用户）
	Version    int                 `json:"version,omitempty"`    // hy2 固定 2
	HyClients  []xrayHysteriaUser  `json:"-"`                    // 仅内部，序列化时若非空 -> 覆盖 Clients
}

// MarshalJSON：当 HyClients 非空时（hy2），把它序列化为 "clients"，避免与 vless/trojan
// 的 xrayClient 共用结构。其余协议保持原样。
func (s xrayInboundSettings) MarshalJSON() ([]byte, error) {
	type alias xrayInboundSettings
	if len(s.HyClients) == 0 {
		return json.Marshal(alias(s))
	}
	out := struct {
		Version int                `json:"version,omitempty"`
		Clients []xrayHysteriaUser `json:"clients"`
	}{
		Version: s.Version,
		Clients: s.HyClients,
	}
	return json.Marshal(out)
}

type xrayHysteriaUser struct {
	Auth  string `json:"auth"`
	Email string `json:"email"`
	Level int    `json:"level"`
}

type xrayHysteriaSettings struct {
	Version        int              `json:"version"`
	Auth           string           `json:"auth,omitempty"`           // obfs salamander 密码（启用混淆时）
	UdpIdleTimeout int              `json:"udpIdleTimeout,omitempty"` // 秒
	Masquerade     *xrayMasquerade  `json:"masquerade,omitempty"`
}

type xrayMasquerade struct {
	Type        string `json:"type"`                  // proxy / file / string / lazy
	URL         string `json:"url,omitempty"`         // type=proxy
	RewriteHost bool   `json:"rewriteHost,omitempty"`
}

type xrayClient struct {
	ID       string `json:"id,omitempty"`       // UUID（vless/vmess/trojan）
	Password string `json:"password,omitempty"` // trojan/ss 用户密码
	Email    string `json:"email"`              // V2Ray Stats 标识
	Flow     string `json:"flow,omitempty"`     // vless+reality: "xtls-rprx-vision"
	Level    int    `json:"level"`
}

type xrayStream struct {
	Network          string                `json:"network"`
	Security         string                `json:"security,omitempty"`
	TLSSettings      *xrayTLSSettings      `json:"tlsSettings,omitempty"`
	RealitySettings  *xrayRealitySettings  `json:"realitySettings,omitempty"`
	WSSettings       *xrayWSSettings       `json:"wsSettings,omitempty"`
	HysteriaSettings *xrayHysteriaSettings `json:"hysteriaSettings,omitempty"`
}

type xrayTLSSettings struct {
	ServerName   string             `json:"serverName,omitempty"`
	ALPN         []string           `json:"alpn,omitempty"`
	Certificates []xrayCertificate  `json:"certificates,omitempty"`
}

type xrayCertificate struct {
	CertificateFile string `json:"certificateFile"`
	KeyFile         string `json:"keyFile"`
}

type xrayRealitySettings struct {
	Show        bool     `json:"show"`
	Dest        string   `json:"dest"`
	Xver        int      `json:"xver"`
	ServerNames []string `json:"serverNames"`
	PrivateKey  string   `json:"privateKey"`
	ShortIds    []string `json:"shortIds"`
}

type xrayWSSettings struct {
	Path string `json:"path,omitempty"`
}

type xrayOutbound struct {
	Protocol       string          `json:"protocol"`
	Tag            string          `json:"tag"`
	Settings       map[string]any  `json:"settings,omitempty"`
	StreamSettings *xrayClientStream `json:"streamSettings,omitempty"`
}

// xrayClientStream 出口代理的客户端 stream 配置（与服务端 xrayStream 不同）。
type xrayClientStream struct {
	Network         string                    `json:"network"`
	Security        string                    `json:"security,omitempty"`
	RealitySettings *xrayClientRealitySettings `json:"realitySettings,omitempty"`
}

type xrayClientRealitySettings struct {
	Show        bool   `json:"show"`
	Fingerprint string `json:"fingerprint"`
	ServerName  string `json:"serverName"`
	PublicKey   string `json:"publicKey"`
	ShortID     string `json:"shortId"`
	SpiderX     string `json:"spiderX,omitempty"`
}

type xrayRouting struct {
	Rules []xrayRoutingRule `json:"rules"`
}

type xrayRoutingRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	OutboundTag string   `json:"outboundTag"`
}

// xrayIdleConfig 无 inbound 时的最小 Xray 配置，保持进程存活。
const xrayIdleConfig = `{"log":{"loglevel":"warning"},"inbounds":[{"tag":"pulse-probe","listen":"127.0.0.1","port":10808,"protocol":"socks"}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`

// BuildXrayConfig 根据节点 inbound 配置和用户凭据生成 Xray 配置 JSON。
func BuildXrayConfig(nodeInbounds []inbounds.Inbound, userAccesses []users.UserInbound, userMap map[string]users.User, opts BuildOptions) (string, error) {
	if len(nodeInbounds) == 0 {
		return "", fmt.Errorf("at least one inbound is required")
	}

	// 过滤已启用的用户访问记录
	activeAccesses := make([]users.UserInbound, 0, len(userAccesses))
	for _, acc := range userAccesses {
		u, ok := userMap[acc.UserID]
		if ok && u.EffectiveEnabled() {
			activeAccesses = append(activeAccesses, acc)
		}
	}
	// 端口转发 inbound 不需要用户列表；只有当节点存在非 portforward 的 inbound 时才要求活跃用户
	hasProxyInbound := false
	for _, ib := range nodeInbounds {
		if ib.Protocol != "portforward" {
			hasProxyInbound = true
			break
		}
	}
	if hasProxyInbound && len(activeAccesses) == 0 {
		return "", fmt.Errorf("at least one active user is required")
	}

	// in-process 模式：api.listen 留空，commander 不对外暴露 gRPC 端口。
	// 流量统计通过 in-process API 直接访问，不走网络。
	const apiListenAddr = ""

	xrayInbounds := make([]xrayInbound, 0, len(nodeInbounds))
	var routingRules []xrayRoutingRule

	// 收集用于 V2Ray Stats 的用户 email 列表
	seenUsers := make(map[string]struct{})

	for _, ib := range nodeInbounds {
		tag := ib.Tag
		if tag == "" {
			tag = fmt.Sprintf("%s-%d", ib.Protocol, ib.Port)
		}

		// 过滤出被分配到此 inbound 的用户凭据，按 UserID 去重（同一用户可能同时有直接分配和组分配）
		ibAccesses := make([]users.UserInbound, 0, len(activeAccesses))
		seenUserID := make(map[string]struct{})
		for _, acc := range activeAccesses {
			if acc.InboundID == "" || acc.InboundID == ib.ID {
				if _, dup := seenUserID[acc.UserID]; !dup {
					seenUserID[acc.UserID] = struct{}{}
					ibAccesses = append(ibAccesses, acc)
				}
			}
		}

		// portforward：由 NodeGate layer4 处理，Xray 不需要生成任何配置。
		if ib.Protocol == "portforward" {
			continue
		}

		// hy2：UDP/QUIC，Xray 自己监听公网并加载 TLS 证书（NodeGate 不能终止 UDP）。
		if ib.Protocol == "hy2" {
			extra := parseHy2Extra(ib.Extra)
			if opts.CertPathFor == nil {
				return "", fmt.Errorf("hy2 inbound %q: BuildOptions.CertPathFor is required", ib.Tag)
			}
			if extra.SNI == "" {
				return "", fmt.Errorf("hy2 inbound %q: extra.sni is required (must match a managed cert domain)", ib.Tag)
			}
			// 证书未就绪时跳过：xray 加载不存在的证书会让整个 restart 失败。
			// 等节点 ACME 完成，下一次 apply 会把该 inbound 加回来。
			if opts.KnownReadyCerts != nil && !opts.KnownReadyCerts[extra.SNI] {
				log.Printf("warn: hy2 inbound %q skipped (cert for %s not ready yet)", ib.Tag, extra.SNI)
				continue
			}
			certFile, keyFile, err := opts.CertPathFor(extra.SNI)
			if err != nil {
				return "", fmt.Errorf("hy2 inbound %q: load cert for %s: %w", ib.Tag, extra.SNI, err)
			}
			hyUsers := make([]xrayHysteriaUser, 0, len(ibAccesses))
			for _, acc := range ibAccesses {
				u, ok := userMap[acc.UserID]
				if !ok {
					continue
				}
				email := u.Username + UserInboundSep + tag
				hyUsers = append(hyUsers, xrayHysteriaUser{Auth: u.Secret, Email: email, Level: 0})
				if _, dup := seenUsers[email]; !dup {
					seenUsers[email] = struct{}{}
				}
			}
			sort.Slice(hyUsers, func(i, j int) bool { return hyUsers[i].Email < hyUsers[j].Email })
			hySettings := &xrayHysteriaSettings{
				Version:        2,
				Auth:           ib.Password, // obfs-password（仅在启用 obfs 时由 fork 校验）
				UdpIdleTimeout: extra.UDPIdleTimeoutSec,
			}
			if extra.MasqueradeURL != "" {
				hySettings.Masquerade = &xrayMasquerade{Type: "proxy", URL: extra.MasqueradeURL, RewriteHost: true}
			}
			stream := &xrayStream{
				Network:  "hysteria",
				Security: "tls",
				TLSSettings: &xrayTLSSettings{
					ServerName:   extra.SNI,
					ALPN:         []string{"h3"},
					Certificates: []xrayCertificate{{CertificateFile: certFile, KeyFile: keyFile}},
				},
				HysteriaSettings: hySettings,
			}
			xib := xrayInbound{
				Tag:            tag,
				Listen:         "0.0.0.0",
				Port:           ib.Port,
				Protocol:       "hysteria",
				Settings:       xrayInboundSettings{Version: 2, HyClients: hyUsers},
				StreamSettings: stream,
			}
			xrayInbounds = append(xrayInbounds, xib)
			continue
		}

		// AnyTLS：NodeGate 终止 TLS，转发明文给 Xray，仅监听本地
		if ib.Protocol == "anytls" {
			anytlsUsers := make([]xrayAnyTLSUser, 0, len(ibAccesses))
			for _, acc := range ibAccesses {
				u, ok := userMap[acc.UserID]
				if !ok {
					continue
				}
				secret := u.Secret
				email := u.Username + UserInboundSep + tag
				anytlsUsers = append(anytlsUsers, xrayAnyTLSUser{Email: email, Password: secret})
				if _, dup := seenUsers[email]; !dup {
					seenUsers[email] = struct{}{}
				}
			}
			sort.Slice(anytlsUsers, func(i, j int) bool { return anytlsUsers[i].Email < anytlsUsers[j].Email })
			stream, err := tlsStreamForNode(ib, opts)
			if err != nil {
				return "", err
			}
			const anytlsListen = "127.0.0.1"
			xib := xrayInbound{
				Tag:            tag,
				Listen:         anytlsListen,
				Port:           ib.Port,
				Protocol:       "anytls",
				Settings:       xrayInboundSettings{Users: anytlsUsers},
				StreamSettings: stream,
			}
			xrayInbounds = append(xrayInbounds, xib)
			continue
		}

		// trojan 由 NodeGate 终止 TLS 后转发明文，监听本地；vless/shadowsocks 协议自带加密，直接监听公网
		listenAddr := "0.0.0.0"
		if ib.Protocol == "trojan" {
			listenAddr = "127.0.0.1"
		} else if ib.Protocol == "shadowsocks" {
			listenAddr = "" // 省略 listen 字段，Xray 走 AnyIP 路径，Go net.Listen(":port") 双栈
		}

		xib := xrayInbound{
			Tag:      tag,
			Listen:   listenAddr,
			Port:     ib.Port,
			Protocol: ib.Protocol,
		}

		switch ib.Protocol {
		case "vless":
			clients := make([]xrayClient, 0, len(ibAccesses))
			for _, acc := range ibAccesses {
				u, ok := userMap[acc.UserID]
				if !ok {
					continue
				}
				uuid := u.UUID
				email := u.Username + UserInboundSep + tag
				flow := ""
				if ib.Security == "reality" {
					flow = "xtls-rprx-vision"
				}
				clients = append(clients, xrayClient{
					ID:    uuid,
					Email: email,
					Flow:  flow,
					Level: 0,
				})
				if _, dup := seenUsers[email]; !dup {
					seenUsers[email] = struct{}{}
				}
			}
			sort.Slice(clients, func(i, j int) bool { return clients[i].Email < clients[j].Email })
			xib.Settings = xrayInboundSettings{
				Clients:    clients,
				Decryption: "none",
			}
			xib.StreamSettings = xrayStreamForInbound(ib)

		case "trojan":
			clients := make([]xrayClient, 0, len(ibAccesses))
			for _, acc := range ibAccesses {
				u, ok := userMap[acc.UserID]
				if !ok {
					continue
				}
				secret := u.Secret
				email := u.Username + UserInboundSep + tag
				clients = append(clients, xrayClient{
					Password: secret,
					Email:    email,
					Level:    0,
				})
				if _, dup := seenUsers[email]; !dup {
					seenUsers[email] = struct{}{}
				}
			}
			sort.Slice(clients, func(i, j int) bool { return clients[i].Email < clients[j].Email })
			xib.Settings = xrayInboundSettings{
				Clients: clients,
			}
			stream, err := tlsStreamForNode(ib, opts)
			if err != nil {
				return "", err
			}
			xib.StreamSettings = stream

		case "shadowsocks":
			// Xray 仅支持 SS 2022 多用户模式（2022-blake3-aes-*-gcm）。
			// method 为空时默认 2022-blake3-aes-128-gcm；server PSK 来自 ib.Password。
			method := ib.Method
			if !strings.HasPrefix(method, "2022-") {
				method = "2022-blake3-aes-128-gcm"
			}
			clients := make([]xrayClient, 0, len(ibAccesses))
			for _, acc := range ibAccesses {
				u, ok := userMap[acc.UserID]
				if !ok {
					continue
				}
				email := u.Username + UserInboundSep + tag
				// SS 2022 密钥从全局 Secret 派生，保证跨节点一致
				userSecret := u.Secret
				keyLen := 16
				if strings.Contains(method, "256") || strings.Contains(method, "chacha20") {
					keyLen = 32
				}
				var psk string
				if strings.HasPrefix(ib.Method, "2022-") && userSecret != "" {
					psk = deriveSecret(userSecret, keyLen)
				} else {
					psk = userSecret
				}
				clients = append(clients, xrayClient{
					Password: psk,
					Email:    email,
					Level:    0,
				})
				if _, dup := seenUsers[email]; !dup {
					seenUsers[email] = struct{}{}
				}
			}
			sort.Slice(clients, func(i, j int) bool { return clients[i].Email < clients[j].Email })
			xib.Settings = xrayInboundSettings{
				Method:   method,
				Password: ib.Password, // server PSK
				Clients:  clients,
			}
		}

		xrayInbounds = append(xrayInbounds, xib)
	}

	sort.Slice(xrayInbounds, func(i, j int) bool {
		if xrayInbounds[i].Port == xrayInbounds[j].Port {
			return xrayInbounds[i].Tag < xrayInbounds[j].Tag
		}
		return xrayInbounds[i].Port < xrayInbounds[j].Port
	})

	// 构建出口列表与路由规则
	xrayOutbounds := []xrayOutbound{
		{Protocol: "freedom", Tag: "direct"},
		{Protocol: "blackhole", Tag: "block"},
	}
	seenOutboundIDs := make(map[string]struct{})

	// ensureOutbound 确保出口已加入列表，返回其 tag。
	// 支持两种格式：
	//   普通出口 ID                       → 从 OutboundMap 查找
	//   "nodeib:<ibID>:<userInboundID>"  → 从 AllInboundMap + AllNodeMap 构建节点 SS 出口
	ensureOutbound := func(obID string) string {
		if obID == "" {
			return "direct"
		}
		if strings.HasPrefix(obID, NodeInboundPrefix) {
			if _, seen := seenOutboundIDs[obID]; seen {
				return "out-" + obID
			}
			rest := obID[len(NodeInboundPrefix):]
			sep := strings.LastIndex(rest, ":")
			if sep < 0 {
				return "direct"
			}
			ibID, uibID := rest[:sep], rest[sep+1:]
			ib, ok := opts.AllInboundMap[ibID]
			if !ok {
				return "direct"
			}
			n, ok := opts.AllNodeMap[ib.NodeID]
			if !ok {
				return "direct"
			}
			uib := opts.UserInboundMap[uibID]
			user := userMap[uib.UserID]
			obTag := "out-" + obID
			seenOutboundIDs[obID] = struct{}{}
			xrayOutbounds = append(xrayOutbounds, buildXrayOutboundFromNodeInbound(ib, n, uib, user, obTag))
			return obTag
		}
		ob, ok := opts.OutboundMap[obID]
		if !ok {
			return "direct"
		}
		obTag := "out-" + ob.ID
		if _, seen := seenOutboundIDs[ob.ID]; !seen {
			seenOutboundIDs[ob.ID] = struct{}{}
			xrayOutbounds = append(xrayOutbounds, buildXrayOutboundBlock(ob, obTag))
		}
		return obTag
	}

	// 全局分流规则（xray 不支持 rule_set，跳过该类型）
	for _, rr := range opts.RouteRules {
		if rr.RuleType == "rule_set" {
			continue
		}
		if rr.NodeIDs != "" && opts.NodeID != "" {
			allowed := false
			for _, nid := range strings.Split(rr.NodeIDs, ",") {
				if strings.TrimSpace(nid) == opts.NodeID {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}
		rule := xrayRoutingRule{Type: "field"}
		if rr.InboundIDs != "" {
			var ibTags []string
			for _, ibID := range strings.Split(rr.InboundIDs, ",") {
				ibID = strings.TrimSpace(ibID)
				if ib, ok := opts.AllInboundMap[ibID]; ok {
					if opts.NodeID != "" && ib.NodeID != opts.NodeID {
						continue
					}
					tag := ib.Tag
					if tag == "" {
						tag = fmt.Sprintf("%s-%d", ib.Protocol, ib.Port)
					}
					ibTags = append(ibTags, tag)
				}
			}
			if len(ibTags) == 0 {
				continue
			}
			rule.InboundTag = ibTags
		}
		patterns := splitPatterns(rr.Patterns)
		if len(patterns) == 0 {
			continue
		}
		for _, p := range patterns {
			// Surge 前缀推断的类型优先，否则 fallback 到 rr.RuleType
			effectiveType := p.surgeRuleType
			if effectiveType == "" {
				effectiveType = rr.RuleType
			}
			switch effectiveType {
			case "domain_suffix":
				rule.Domain = append(rule.Domain, "domain:"+p.value)
			case "domain_keyword":
				rule.Domain = append(rule.Domain, "keyword:"+p.value)
			case "domain":
				rule.Domain = append(rule.Domain, p.value)
			case "ip_cidr":
				rule.IP = append(rule.IP, p.value)
			}
		}
		rule.OutboundTag = ensureOutbound(rr.OutboundID)
		routingRules = append(routingRules, rule)
	}

	// per-inbound 出口绑定
	for _, ib := range nodeInbounds {
		ibTag := ib.Tag
		if ibTag == "" {
			ibTag = fmt.Sprintf("%s-%d", ib.Protocol, ib.Port)
		}
		// portforward 由 NodeGate 处理，Xray 无需路由规则
		if ib.Protocol == "portforward" {
			continue
		}
		if ib.OutboundID == "" {
			continue
		}
		obTag := ensureOutbound(ib.OutboundID)
		if obTag == "direct" {
			continue
		}
		routingRules = append(routingRules, xrayRoutingRule{
			Type:        "field",
			InboundTag:  []string{ibTag},
			OutboundTag: obTag,
		})
	}

	cfg := xrayConfig{
		Log: xrayLog{Loglevel: "info"},
		API: &xrayAPI{
			Tag:      "api",
			Services: []string{"HandlerService", "LoggerService", "StatsService"},
			Listen:   apiListenAddr,
		},
		Stats: &struct{}{},
		Policy: &xrayPolicy{
			Levels: map[string]xrayPolicyLevel{
				"0": {
					StatsUserUplink:   true,
					StatsUserDownlink: true,
				},
			},
			System: xraySystemPolicy{
				StatsInboundUplink:   true,
				StatsInboundDownlink: true,
			},
		},
		Inbounds:  xrayInbounds,
		Outbounds: xrayOutbounds,
		Routing: &xrayRouting{
			Rules: routingRules,
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal xray config: %w", err)
	}
	return string(data), nil
}

// surgeTypePrefix 将 Surge/Clash 类型前缀映射到内部规则类型。
var surgeTypePrefix = map[string]string{
	"DOMAIN-SUFFIX":  "domain_suffix",
	"DOMAIN-KEYWORD": "domain_keyword",
	"DOMAIN":         "domain",
	"IP-CIDR":        "ip_cidr",
	"IP-CIDR6":       "ip_cidr",
	"GEOIP":          "",
	"RULE-SET":       "",
}

type parsedPattern struct {
	// surgeRuleType 非空时表示从 Surge/Clash 前缀推断出的类型，优先于 RouteRule.RuleType。
	surgeRuleType string
	value         string
}

func splitPatterns(s string) []parsedPattern {
	// 先按换行切，每行再按第一个逗号分割，支持 "DOMAIN-SUFFIX,example.com" 格式。
	var result []parsedPattern
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if idx := strings.IndexByte(line, ','); idx > 0 {
			prefix := strings.ToUpper(line[:idx])
			if ruleType, ok := surgeTypePrefix[prefix]; ok {
				value := strings.TrimSpace(line[idx+1:])
				// 丢弃第三段（策略）
				if i := strings.IndexByte(value, ','); i > 0 {
					value = strings.TrimSpace(value[:i])
				}
				if value != "" && ruleType != "" {
					result = append(result, parsedPattern{surgeRuleType: ruleType, value: value})
				}
				continue
			}
		}
		// 普通逗号分隔列表
		for _, p := range strings.Split(line, ",") {
			if t := strings.TrimSpace(p); t != "" {
				result = append(result, parsedPattern{value: t})
			}
		}
	}
	return result
}

// xrayStreamForInbound 根据 inbound 配置生成 Xray streamSettings。
func xrayStreamForInbound(ib inbounds.Inbound) *xrayStream {
	if ib.Protocol == "vless" && ib.Security == "reality" {
		return xrayRealityStreamFor(ib)
	}
	return nil
}

// xrayRealityStreamFor 生成 Reality streamSettings。
func xrayRealityStreamFor(ib inbounds.Inbound) *xrayStream {
	if ib.RealityPrivateKey == "" {
		return nil
	}

	dest := "www.google.com:443"
	if ib.RealityHandshakeAddr != "" {
		host, portStr, err := net.SplitHostPort(ib.RealityHandshakeAddr)
		if err == nil {
			dest = host + ":" + portStr
		} else {
			dest = ib.RealityHandshakeAddr + ":443"
		}
	}

	shortIDs := []string{""}
	if ib.RealityShortID != "" {
		shortIDs = []string{ib.RealityShortID}
	}

	// 解析 dest 拿 host 部分作为 serverNames
	destHost := dest
	if h, _, err := net.SplitHostPort(dest); err == nil {
		destHost = h
	}

	return &xrayStream{
		Network:  "tcp",
		Security: "reality",
		RealitySettings: &xrayRealitySettings{
			Show:        false,
			Dest:        dest,
			Xver:        0,
			ServerNames: []string{destHost},
			PrivateKey:  ib.RealityPrivateKey,
			ShortIds:    shortIDs,
		},
	}
}

// buildXrayOutboundFromNodeInbound 将节点 Inbound 转换为 xray SS outbound。
// 地址取自 Node.BaseURL 的 hostname，端口取自 Inbound.Port。
// SS2022 密码格式：server_psk:user_psk（user_psk 优先用全局凭证 user.Secret）。
func buildXrayOutboundFromNodeInbound(ib inbounds.Inbound, n nodes.Node, uib users.UserInbound, user users.User, tag string) xrayOutbound {
	u, err := url.Parse(n.BaseURL)
	if err != nil || u.Hostname() == "" {
		return xrayOutbound{Protocol: "freedom", Tag: tag}
	}
	method := ib.Method
	if method == "" {
		method = "2022-blake3-aes-128-gcm"
	}
	// 优先使用用户全局 Secret，fallback 到 UserInbound.Secret
	userSecret := user.Secret
	if userSecret == "" {
		userSecret = uib.Secret
	}
	password := ib.Password
	if userSecret != "" {
		if strings.HasPrefix(method, "2022-") {
			// SS 2022 user PSK 需与落地节点入站派生后的值一致
			keyLen := 16
			if strings.Contains(method, "256") || strings.Contains(method, "chacha20") {
				keyLen = 32
			}
			password = ib.Password + ":" + deriveSecret(userSecret, keyLen)
		} else {
			password = userSecret
		}
	}
	return xrayOutbound{
		Protocol: "shadowsocks",
		Tag:      tag,
		Settings: map[string]any{
			"servers": []map[string]any{{
				"address":  u.Hostname(),
				"port":     ib.Port,
				"method":   method,
				"password": password,
			}},
		},
	}
}

// buildXrayOutboundBlock 根据自定义 Outbound 配置生成 xray outbound。
func buildXrayOutboundBlock(ob outbounds.Outbound, tag string) xrayOutbound {
	host, portStr, err := net.SplitHostPort(ob.Server)
	if err != nil {
		return xrayOutbound{Protocol: "freedom", Tag: tag}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return xrayOutbound{Protocol: "freedom", Tag: tag}
	}
	switch ob.Protocol {
	case "ss":
		return xrayOutbound{
			Protocol: "shadowsocks",
			Tag:      tag,
			Settings: map[string]any{
				"servers": []map[string]any{{
					"address":  host,
					"port":     port,
					"method":   ob.Method,
					"password": ob.Password,
				}},
			},
		}
	case "vless":
		ob := ob // 捕获局部变量
		out := xrayOutbound{
			Protocol: "vless",
			Tag:      tag,
			Settings: map[string]any{
				"vnext": []map[string]any{{
					"address": host,
					"port":    port,
					"users": []map[string]any{{
						"id":         ob.UUID,
						"flow":       ob.Flow,
						"encryption": "none",
						"level":      0,
					}},
				}},
			},
		}
		// reality 需要 streamSettings（客户端格式）
		if ob.PublicKey != "" {
			fp := ob.Fingerprint
			if fp == "" {
				fp = "chrome"
			}
			out.StreamSettings = &xrayClientStream{
				Network:  "tcp",
				Security: "reality",
				RealitySettings: &xrayClientRealitySettings{
					Show:        false,
					Fingerprint: fp,
					ServerName:  ob.SNI,
					PublicKey:   ob.PublicKey,
					ShortID:     ob.ShortID,
				},
			}
		}
		return out
	default:
		return xrayOutbound{Protocol: "freedom", Tag: tag}
	}
}

// tlsStreamForNode 生成 Trojan / AnyTLS 的 StreamSettings。
// NodeGate 终止 TLS：Trojan 使用 WS transport，AnyTLS 使用裸 TCP；均无 TLS（由 NodeGate 终止）。
func tlsStreamForNode(ib inbounds.Inbound, opts BuildOptions) (*xrayStream, error) {
	return tlsStreamNodeGateFallback(ib.Protocol), nil
}

func tlsStreamNodeGateFallback(protocol string) *xrayStream {
	if protocol == "trojan" {
		return &xrayStream{
			Network:    "ws",
			Security:   "none",
			WSSettings: &xrayWSSettings{Path: "/ws"},
		}
	}
	return &xrayStream{Network: "tcp", Security: "none"}
}

// hy2Extra 是 Inbound.Extra 中 hy2 协议约定的字段集合。
type hy2Extra struct {
	SNI               string `json:"sni"`
	MasqueradeURL     string `json:"masquerade_url"`
	UDPIdleTimeoutSec int    `json:"udp_idle_timeout_sec"`
}

// parseHy2Extra 容错解析 Inbound.Extra；非法 JSON 视为空配置。
func parseHy2Extra(raw string) hy2Extra {
	var e hy2Extra
	if raw == "" {
		return e
	}
	_ = json.Unmarshal([]byte(raw), &e)
	if e.UDPIdleTimeoutSec <= 0 {
		e.UDPIdleTimeoutSec = 60
	}
	return e
}
