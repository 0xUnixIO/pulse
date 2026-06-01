package jobs

import (
	"testing"

	"pulse/internal/inbounds"
	"pulse/internal/nodes"
)

// 构造一个只含单个本地 AnyTLS inbound + host 的内存 store。
// xrayPort 为 Xray 本地监听端口（terminating backend），hostPort 为客户端订阅/连接端口。
func newAnyTLSStore(t *testing.T, nodeID, protocol string, xrayPort, hostPort int) inbounds.InboundStore {
	t.Helper()
	store := inbounds.NewMemoryStore()
	if _, err := store.UpsertInbound(inbounds.Inbound{
		ID:       "ib1",
		NodeID:   nodeID,
		Protocol: protocol,
		Port:     xrayPort,
	}); err != nil {
		t.Fatalf("upsert inbound: %v", err)
	}
	if _, err := store.UpsertHost(inbounds.Host{
		ID:        "h1",
		InboundID: "ib1",
		Address:   "cdn-d25c.wam.qzz.io",
		SNI:       "cdn-d25c.wam.qzz.io",
		Port:      hostPort,
	}); err != nil {
		t.Fatalf("upsert host: %v", err)
	}
	return store
}

// 复现 bug：本地直连 AnyTLS host 填了端口 4433，NodeGate 监听端口应当跟随该端口，
// 而不是兜底到 443（导致与系统已占用的 443 冲突，且与客户端订阅端口不一致）。
func TestBuildSNIProxySyncReq_AnyTLSLocalHostPortDrivesListen(t *testing.T) {
	node := nodes.Node{ID: "n1"} // HTTPSPort 未设置（=0）
	store := newAnyTLSStore(t, "n1", "anytls", 29371, 4433)

	req := BuildSNIProxySyncReq(node, mustListInbounds(t, store), store,
		map[string]nodes.Node{"n1": node}, "", 0)

	if req.Listen != ":4433" {
		t.Fatalf("NodeGate 监听端口应跟随连接地址端口 4433，实际 = %q", req.Listen)
	}
}

// node.HTTPSPort 显式设置时优先级最高，覆盖 host 端口。
func TestBuildSNIProxySyncReq_NodeHTTPSPortWins(t *testing.T) {
	node := nodes.Node{ID: "n1", HTTPSPort: 8443}
	store := newAnyTLSStore(t, "n1", "anytls", 29371, 4433)

	req := BuildSNIProxySyncReq(node, mustListInbounds(t, store), store,
		map[string]nodes.Node{"n1": node}, "", 0)

	if req.Listen != ":8443" {
		t.Fatalf("node.HTTPSPort 应优先，期望 :8443，实际 = %q", req.Listen)
	}
}

// host 端口为 0 时回退到默认 443（保持旧行为）。
func TestBuildSNIProxySyncReq_FallbackTo443(t *testing.T) {
	node := nodes.Node{ID: "n1"}
	store := newAnyTLSStore(t, "n1", "anytls", 29371, 0)

	req := BuildSNIProxySyncReq(node, mustListInbounds(t, store), store,
		map[string]nodes.Node{"n1": node}, "", 0)

	if req.Listen != ":443" {
		t.Fatalf("无任何端口配置时应回退 :443，实际 = %q", req.Listen)
	}
}

// trojan 与 anytls 行为一致（同为 NodeGate terminating 直连）。
func TestBuildSNIProxySyncReq_TrojanLocalHostPortDrivesListen(t *testing.T) {
	node := nodes.Node{ID: "n1"}
	store := newAnyTLSStore(t, "n1", "trojan", 12345, 4433)

	req := BuildSNIProxySyncReq(node, mustListInbounds(t, store), store,
		map[string]nodes.Node{"n1": node}, "", 0)

	if req.Listen != ":4433" {
		t.Fatalf("trojan 本地 host 端口应驱动监听端口，期望 :4433，实际 = %q", req.Listen)
	}
}

func mustListInbounds(t *testing.T, store inbounds.InboundStore) []inbounds.Inbound {
	t.Helper()
	ibs, err := store.ListInbounds()
	if err != nil {
		t.Fatalf("list inbounds: %v", err)
	}
	return ibs
}
