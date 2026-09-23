// Package confighash 提供 server 与 node 共用的"节点应有配置"hash 算法。
//
// 用途：node 在 hello 帧里上报当前 xray 配置 hash；server 端按相同规则计算
// 期望 hash，不一致即触发一次 ApplyNode（self-sync），从而在 node 重连后无需
// server 维护离线指令队列。
//
// 算法：
//  1. 将 user 集合按 (Email, InboundTag) 字典序排序；
//  2. 将 inbound 集合按 Tag 字典序排序；
//  3. json.Marshal 一个固定字段顺序的结构 {inbounds, users}；
//  4. SHA256(canonical_json) → hex 字符串。
//
// TrafficRate 仍留在 canonical inbound 里以保持哈希形状稳定，但 server / node
// 两侧都应传 0：proxycfg 不把倍率写入 xray JSON，HashFromXrayJSON 读到的也是 0。
//
// 字段、排序、struct 标签必须保持稳定。任何修改都会改变 hash 输出，
// 节点与控制面必须同步发版才能保证一致性。
package confighash

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// UserEntry 描述一个用户在某 inbound 上的下发态特征。
type UserEntry struct {
	Email      string
	UUID       string
	InboundTag string
	Enabled    bool
}

// InboundEntry 描述一个 inbound 的下发态特征。
type InboundEntry struct {
	Tag         string
	TrafficRate float64
}

type canonicalUser struct {
	Email      string `json:"email"`
	UUID       string `json:"uuid"`
	InboundTag string `json:"inbound_tag"`
	Enabled    bool   `json:"enabled"`
}

type canonicalInbound struct {
	Tag         string  `json:"tag"`
	TrafficRate float64 `json:"traffic_rate"`
}

// Compute 计算给定用户与 inbound 集合的规范化 SHA256 hex hash。
// 输入顺序无关（内部会排序）。
func Compute(usersIn []UserEntry, inboundsIn []InboundEntry) string {
	cusers := make([]canonicalUser, len(usersIn))
	for i, u := range usersIn {
		cusers[i] = canonicalUser(u)
	}
	cinb := make([]canonicalInbound, len(inboundsIn))
	for i, ib := range inboundsIn {
		cinb[i] = canonicalInbound(ib)
	}

	sort.Slice(cinb, func(i, j int) bool { return cinb[i].Tag < cinb[j].Tag })
	sort.Slice(cusers, func(i, j int) bool {
		if cusers[i].Email != cusers[j].Email {
			return cusers[i].Email < cusers[j].Email
		}
		return cusers[i].InboundTag < cusers[j].InboundTag
	})

	canon, _ := json.Marshal(struct {
		Inbounds []canonicalInbound `json:"inbounds"`
		Users    []canonicalUser    `json:"users"`
	}{Inbounds: cinb, Users: cusers})

	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:])
}

// HashFromXrayJSON 解析 xray 配置 JSON 并按相同算法计算 hash。
// 解析失败时回退为对原文做 SHA256，保证不同输入仍能得到稳定签名。
//
// xray 配置中 client.email 形如 "username@inbound_tag"；若没有显式 enabled
// 字段，视为 true（出现在配置即表示生效）。client.id 优先取，缺失时取
// password 字段（trojan/anytls 等协议）。
func HashFromXrayJSON(cfgJSON string) string {
	if cfgJSON == "" {
		return ""
	}
	var cfg struct {
		Inbounds []struct {
			Tag         string  `json:"tag"`
			TrafficRate float64 `json:"trafficRate,omitempty"`
			Settings    struct {
				Clients []map[string]any `json:"clients"`
				Users   []map[string]any `json:"users"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		sum := sha256.Sum256([]byte(cfgJSON))
		return hex.EncodeToString(sum[:])
	}

	var ibEntries []InboundEntry
	var userEntries []UserEntry
	for _, ib := range cfg.Inbounds {
		ibEntries = append(ibEntries, InboundEntry{Tag: ib.Tag, TrafficRate: ib.TrafficRate})
		all := append([]map[string]any{}, ib.Settings.Clients...)
		all = append(all, ib.Settings.Users...)
		for _, c := range all {
			email, _ := c["email"].(string)
			uuid, _ := c["id"].(string)
			if uuid == "" {
				if pw, ok := c["password"].(string); ok {
					uuid = pw
				}
			}
			enabled := true
			if v, ok := c["enabled"].(bool); ok {
				enabled = v
			}
			userEntries = append(userEntries, UserEntry{
				Email:      email,
				UUID:       uuid,
				InboundTag: ib.Tag,
				Enabled:    enabled,
			})
		}
	}

	return Compute(userEntries, ibEntries)
}
