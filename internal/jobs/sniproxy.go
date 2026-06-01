package jobs

import (
	"encoding/json"
	"fmt"
	"strings"

	"pulse/internal/inbounds"
	"pulse/internal/nodes"
)

// BuildSNIProxySyncReq 从节点当前生效的 inbound/host 配置生成 NodeGate (sniproxy) 配置。
//
// 覆盖三种路由：
//   - Terminating：本节点 AnyTLS/Trojan inbound 的 SNI → 127.0.0.1:<xray_port>
//   - Transparent：其他节点以本节点为 Relay 的 Host SNI → <landing_ip>:<landing_port>
//   - HTTPReverse：node.PanelDomain（反代到 pulse-server panelPort）
//                  以及 node.ExtraProxies（每行 "domain:port" 反代到 127.0.0.1:port）
//
// 监听端口优先级：node.HTTPSPort > 本地直连 Host.Port > Host.HTTPSPort(中转) > 443。
func BuildSNIProxySyncReq(
	node nodes.Node,
	nodeInbounds []inbounds.Inbound,
	ibStore inbounds.InboundStore,
	allNodeMap map[string]nodes.Node,
	cfToken string,
	panelPort int,
) nodes.SNIProxySyncRequest {
	var routes []nodes.SNIProxyRoute
	hostHTTPSPort := 0
	// 本地直连 AnyTLS/Trojan host 的对外监听端口：客户端直连 NodeGate，
	// 故 NodeGate 必须监听该 Host.Port（= 订阅链接里写给客户端的端口）。
	localListenPort := 0

	// 1. 本节点 AnyTLS/Trojan inbound 路由：terminating，NodeGate 终止 TLS 转发明文给 Xray
	seenLocal := make(map[string]struct{})
	for _, ib := range nodeInbounds {
		if ib.Protocol != "anytls" && ib.Protocol != "trojan" {
			continue
		}
		hosts, hErr := ibStore.ListHostsByInbound(ib.ID)
		if hErr != nil {
			continue
		}
		for _, h := range hosts {
			// Relay 的 Host：路由由前置节点负责
			if h.RelayNodeID != "" {
				if h.HTTPSPort > 0 && hostHTTPSPort == 0 {
					hostHTTPSPort = h.HTTPSPort
				}
				continue
			}
			sni := h.SNI
			if sni == "" {
				sni = h.Address
			}
			if sni == "" {
				continue
			}
			if _, dup := seenLocal[sni]; dup {
				continue
			}
			seenLocal[sni] = struct{}{}
			// 取第一个非 0 的本地 Host.Port 作为 NodeGate 监听端口
			// （NodeGate 单端口监听，按 SNI 分流，与各 host 的 Xray 后端端口无关）。
			if localListenPort == 0 && h.Port > 0 {
				localListenPort = h.Port
			}
			routes = append(routes, nodes.SNIProxyRoute{
				SNI:     sni,
				Backend: fmt.Sprintf("127.0.0.1:%d", ib.Port),
				Mode:    "terminating",
			})
		}
	}

	// 2. Transparent 路由：其他节点的 Host 把本节点作为 RelayNodeID 时，
	//    在本节点 SNI 透传到落地节点 IP:port。
	if allHosts, err := ibStore.ListHosts(); err == nil {
		allIbs, _ := ibStore.ListInbounds()
		ibMap := make(map[string]inbounds.Inbound, len(allIbs))
		for _, ib := range allIbs {
			ibMap[ib.ID] = ib
		}
		nodeIPMap := BuildNodeIPMap(allNodeMap)
		seenTransparent := make(map[string]struct{})
		var transparentListenPort int
		for _, h := range allHosts {
			if h.RelayNodeID != node.ID || h.Port <= 0 {
				continue
			}
			ib, ok := ibMap[h.InboundID]
			if !ok {
				continue
			}
			targetIP := nodeIPMap[ib.NodeID]
			if targetIP == "" {
				continue
			}
			sni := h.SNI
			if sni == "" {
				sni = h.Address
			}
			if sni == "" {
				continue
			}
			if _, dup := seenTransparent[sni]; dup {
				continue
			}
			// 只接受与第一条透传路由 ListenPort 一致的 Host，其余跳过
			// （UnifiedProxy 目前单端口，不同监听端口的路由走不到这条通道）
			if transparentListenPort == 0 {
				transparentListenPort = h.Port
			}
			if h.Port != transparentListenPort {
				continue
			}
			seenTransparent[sni] = struct{}{}

			backendNode := allNodeMap[ib.NodeID]
			httpsPort := h.HTTPSPort
			if httpsPort == 0 {
				httpsPort = backendNode.HTTPSPort
			}
			targetPort := PortforwardTargetPort(ib, httpsPort)
			routes = append(routes, nodes.SNIProxyRoute{
				SNI:     sni,
				Backend: fmt.Sprintf("%s:%d", targetIP, targetPort),
				Mode:    "transparent",
			})
		}
	}

	// 3. HTTPReverse 路由：面板多域名（逗号/换行分隔）统一反代到 panelPort
	if node.PanelDomain != "" && panelPort > 0 {
		for _, d := range splitDomains(node.PanelDomain) {
			routes = append(routes, nodes.SNIProxyRoute{
				SNI:     d,
				Backend: fmt.Sprintf("127.0.0.1:%d", panelPort),
				Mode:    "http-reverse",
			})
		}
	}

	// 4. HTTPReverse 路由：用户自定义额外反代，每行一条
	// 新格式："domain host:port"（空格分隔，支持任意后端地址）
	// 旧格式（兼容）："domain:port"（仅端口，后端固定为 127.0.0.1）
	for _, line := range strings.Split(node.ExtraProxies, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var domain, backend string
		if spaceIdx := strings.Index(line, " "); spaceIdx > 0 {
			domain = strings.TrimSpace(line[:spaceIdx])
			backend = strings.TrimSpace(line[spaceIdx+1:])
		} else {
			colonIdx := strings.LastIndex(line, ":")
			if colonIdx <= 0 {
				continue
			}
			domain = strings.TrimSpace(line[:colonIdx])
			backend = "127.0.0.1:" + strings.TrimSpace(line[colonIdx+1:])
		}
		if domain == "" || backend == "" {
			continue
		}
		routes = append(routes, nodes.SNIProxyRoute{
			SNI:     domain,
			Backend: backend,
			Mode:    "http-reverse",
		})
	}

	// 监听端口决策
	listenPort := 0
	switch {
	case node.HTTPSPort > 0:
		listenPort = node.HTTPSPort
	case localListenPort > 0:
		listenPort = localListenPort
	case hostHTTPSPort > 0:
		listenPort = hostHTTPSPort
	case len(routes) > 0:
		listenPort = 443
	}

	req := nodes.SNIProxySyncRequest{
		ACMEEmail:       node.ACMEEmail,
		CloudflareToken: cfToken,
		CertStoragePath: NodeCertStoragePath,
		Routes:          routes,
	}

	// hy2 inbound 的 SNI 不参与 NodeGate TCP 路由，但需要 certmgr 通过 ACME 申请证书。
	// 优先用 Inbound.Extra.sni，其次回退到该 inbound 关联 Host 的 SNI / Address。
	hy2Seen := make(map[string]struct{})
	for _, ib := range nodeInbounds {
		if ib.Protocol != "hy2" {
			continue
		}
		var domain string
		if e := parseHy2SNI(ib.Extra); e != "" {
			domain = e
		} else {
			hosts, _ := ibStore.ListHostsByInbound(ib.ID)
			for _, h := range hosts {
				if h.SNI != "" {
					domain = h.SNI
					break
				}
				if h.Address != "" {
					domain = h.Address
					break
				}
			}
		}
		if domain == "" {
			continue
		}
		if _, dup := hy2Seen[domain]; dup {
			continue
		}
		hy2Seen[domain] = struct{}{}
		req.CertDomains = append(req.CertDomains, domain)
	}

	if listenPort > 0 && len(routes) > 0 {
		req.Listen = fmt.Sprintf(":%d", listenPort)
	}
	return req
}

// parseHy2SNI 容错解析 Inbound.Extra 中 hy2 的 sni 字段。
func parseHy2SNI(extra string) string {
	if extra == "" {
		return ""
	}
	var e struct {
		SNI string `json:"sni"`
	}
	if err := json.Unmarshal([]byte(extra), &e); err != nil {
		return ""
	}
	return e.SNI
}

// splitDomains 把 PanelDomain 字符串按逗号/换行分割成干净的域名列表。
func splitDomains(s string) []string {
	var out []string
	for _, d := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' }) {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}
