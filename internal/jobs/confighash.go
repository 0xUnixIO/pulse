package jobs

import (
	"context"
	"strconv"
	"strings"

	"pulse/internal/inbounds"
	"pulse/internal/nodes/confighash"
	"pulse/internal/outbounds"
	"pulse/internal/proxycfg"
	"pulse/internal/users"
)

// ComputeNodeConfigHash 计算 server 端某节点的"应有"配置 hash。
//
// 用于 self-sync：node 在 hello 帧上报当前 hash，server 算一遍期望 hash，
// 不一致时主动推送完整配置（ApplyNode）。算法实现位于 internal/nodes/confighash
// 共享包，与 nodeagent 侧字节一致。
//
// 注意：此处不做 outbound / 路由规则 hash，因为 node 侧的 ConfigHasher 也只覆盖
// 用户列表 + inbound tag。倍率不进 hash：proxycfg 不把它写入 xray JSON。
// 如未来需要扩展 hash 输入，两侧需同步更新。
//
// nodeID 不存在或没有 inbound/access 时返回空集合的 hash（仍是合法 64-hex）。
func ComputeNodeConfigHash(
	ctx context.Context,
	nodeID string,
	userStore users.Store,
	ibStore inbounds.InboundStore,
	_ outbounds.Store, // 预留：未来可能纳入 outbound hash
) (string, error) {
	_ = ctx // store 当前接口未传 ctx；保留参数便于将来对齐

	nodeInbounds, err := ibStore.ListInboundsByNode(nodeID)
	if err != nil {
		return "", err
	}
	accesses, err := userStore.ListUserInboundsByNode(nodeID)
	if err != nil {
		return "", err
	}
	userMap, err := userStore.GetUsersByIDs(collectUserIDs(accesses))
	if err != nil {
		return "", err
	}

	ibByID := make(map[string]inbounds.Inbound, len(nodeInbounds))
	ibEntries := make([]confighash.InboundEntry, 0, len(nodeInbounds))
	for _, ib := range nodeInbounds {
		tag := inboundTag(ib)
		ibByID[ib.ID] = ib
		// proxycfg.BuildXrayConfig 不下发 trafficRate，节点从 lastConfig 解析时该
		// 字段恒为 0。postgres 又把 <=0 的倍率写成 1.0，若把 DB 值写进 expected，
		// 所有节点会永久漂移、每 10 分钟重下发。倍率只影响 server 侧计费。
		ibEntries = append(ibEntries, confighash.InboundEntry{Tag: tag})
	}

	userEntries := make([]confighash.UserEntry, 0, len(accesses))
	seen := make(map[string]struct{}, len(accesses))
	for _, acc := range accesses {
		ib, ok := ibByID[acc.InboundID]
		if !ok {
			continue
		}
		u, ok := userMap[acc.UserID]
		if !ok {
			continue
		}
		// 与 node 侧（解析 xray 配置）保持一致：
		// 仅"会出现在 xray clients 列表中的用户"参与 hash。
		// proxycfg.BuildXrayConfig 仅包含 EffectiveEnabled() 为 true 的用户。
		if !u.EffectiveEnabled() {
			continue
		}
		tag := inboundTag(ib)
		email := u.Username + proxycfg.UserInboundSep + tag

		// 与 proxycfg 一致：UUID/Secret 优先取 user 级，缺失时回退 user_inbound 级
		uuid := u.UUID
		if uuid == "" {
			uuid = acc.UUID
		}
		secret := u.Secret
		if secret == "" {
			secret = acc.Secret
		}
		// xray 中 vless/vmess 用 id；trojan/ss/anytls 用 password。
		// node 侧从 xray JSON 读取时优先 id，缺失回退 password。
		// 这里需要按协议选择对应的 token，确保两侧字节一致。
		token := uuid
		switch ib.Protocol {
		case "trojan", "anytls":
			token = secret
		case "shadowsocks":
			// 与 BuildXrayConfig 一致：非 2022-* method 时默认 128-gcm，
			// client.password 是 SSUserPassword 派生 PSK，不是原始 Secret。
			method := ib.Method
			if !strings.HasPrefix(method, "2022-") {
				method = "2022-blake3-aes-128-gcm"
			}
			token = proxycfg.SSUserPassword(secret, method)
		}

		key := email + "|" + tag
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}

		userEntries = append(userEntries, confighash.UserEntry{
			Email:      email,
			UUID:       token,
			InboundTag: tag,
			Enabled:    true,
		})
	}

	return confighash.Compute(userEntries, ibEntries), nil
}

// inboundTag 返回 inbound 的 V2Ray 配置 tag，与 proxycfg 内部回退规则保持一致。
func inboundTag(ib inbounds.Inbound) string {
	if ib.Tag != "" {
		return ib.Tag
	}
	return ib.Protocol + "-" + strconv.Itoa(ib.Port)
}
