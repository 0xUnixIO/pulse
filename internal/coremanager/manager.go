package coremanager

import (
	"context"
	"errors"
	"time"
)

// ErrNotRunning 核心未运行时调用 Stop 返回的标准错误。
var ErrNotRunning = errors.New("core is not running")

type Status struct {
	Running   bool      `json:"running"`
	StartedAt time.Time `json:"started_at,omitempty"`
}

type RuntimeInfo struct {
	Available   bool   `json:"available"`
	Module      string `json:"module"`
	Version     string `json:"version,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	NodeVersion string `json:"node_version,omitempty"`
}

type UserUsage struct {
	User          string   `json:"user"`
	UploadTotal   int64    `json:"upload_total"`
	DownloadTotal int64    `json:"download_total"`
	Connections   int      `json:"connections"`
	Devices       int      `json:"devices"`
	SourceIPs     []string `json:"source_ips,omitempty"`
}

type UsageStats struct {
	Available     bool        `json:"available"`
	Running       bool        `json:"running"`
	StartedAt     time.Time   `json:"started_at,omitempty"`
	UploadTotal   int64       `json:"upload_total"`
	DownloadTotal int64       `json:"download_total"`
	UploadSpeed   int64       `json:"upload_speed"`
	DownloadSpeed int64       `json:"download_speed"`
	Connections   int         `json:"connections"`
	Users         []UserUsage `json:"users"`
}

// UserConfig 描述要动态增删的单个用户凭证。
type UserConfig struct {
	InboundTag string // 目标 inbound tag
	Protocol   string // "vless" | "trojan" | "shadowsocks" | "anytls"
	Email      string // V2Ray Stats 标识，格式：username@@@tag
	UUID       string // vless 使用
	Password   string // trojan / anytls 使用
	Flow       string // vless+reality 使用，如 "xtls-rprx-vision"
}

// AccessLogEntry 表示一条 xray access log 解析结果。
type AccessLogEntry struct {
	SourceIP    string    `json:"source_ip"`
	SourcePort  string    `json:"source_port"`
	Destination string    `json:"destination"`
	RemoteIP    string    `json:"remote_ip"`   // 目标真实 IP（DNS 解析后）
	RouteTag    string    `json:"route_tag"`
	Protocol    string    `json:"protocol"`    // vless / trojan / ss2022 / anytls
	User        string    `json:"user"`
	InboundTag  string    `json:"inbound_tag"`
	Time        time.Time `json:"time"`
	SessionID   string    `json:"-"`           // 内部关联用，不序列化
}

// AccessLogDrainer 可选接口：支持批量取走 access log 缓冲区。
// xray.Manager 实现此接口；mock 实现可不实现。
type AccessLogDrainer interface {
	DrainAccessLogs() []AccessLogEntry
}

// Manager 代理核心的统一控制接口，由 Xray 实现。
type Manager interface {
	Start(config string) error
	Stop() error
	Restart(config string) error
	Status() Status
	Usage(reset bool) UsageStats
	Config() string
	Logs() []string
	Subscribe() (int64, <-chan string)
	Unsubscribe(id int64)
	SavedConfig() string
	RuntimeInfo(ctx context.Context) RuntimeInfo
	Version(ctx context.Context) (string, error)

	// AddUser 向运行中的 inbound 热增用户，不重启核心、不断现有连接。
	AddUser(ctx context.Context, cfg UserConfig) error
	// RemoveUser 从运行中的 inbound 热删用户。已有连接自然结束，新连接立即拒绝。
	RemoveUser(ctx context.Context, inboundTag, email string) error

	// KickUser 强制断开该用户的全部存量连接，返回被断开的连接数。
	//
	// 与 RemoveUser 配合使用：RemoveUser 只更新 inbound validator，拦截的是
	// 后续新连接；鉴权只在建链时做一次，已建立的连接不会被回查，会一直跑到
	// 自己结束。超限用户必须再调 KickUser 才能立即止住流量。
	// 断连精确到用户，同节点其他用户不受影响。
	KickUser(ctx context.Context, email string) (int, error)
}
