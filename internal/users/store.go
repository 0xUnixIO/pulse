package users

import (
	"errors"
	"time"
)

var (
	ErrUserNotFound        = errors.New("user not found")
	ErrUserInboundNotFound = errors.New("user inbound not found")
	ErrUsernameTaken       = errors.New("username already exists")
)

const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusLimited  = "limited"
	StatusExpired  = "expired"
	StatusOnHold   = "on_hold"

	ResetStrategyNoReset = "no_reset"
	ResetStrategyDay     = "day"
	ResetStrategyWeek    = "week"
	ResetStrategyMonth   = "month"
	ResetStrategyYear    = "year"
)

// User 用户身份与流量统计。
type User struct {
	ID                     string     `json:"id"`
	Username               string     `json:"username"`
	Status                 string     `json:"status"`
	Note                   string     `json:"note,omitempty"`
	ExpireAt               *time.Time `json:"expire_at,omitempty"`
	DataLimitResetStrategy string     `json:"data_limit_reset_strategy"`
	TrafficLimit           int64      `json:"traffic_limit_bytes"`
	PlanTrafficLimit       int64      `json:"plan_traffic_limit_bytes"`
	UploadBytes            int64      `json:"upload_bytes"`
	DownloadBytes          int64      `json:"download_bytes"`
	UsedBytes              int64      `json:"used_bytes"`
	RawUploadBytes         int64      `json:"raw_upload_bytes"`   // 实际上行流量（不含倍率）
	RawDownloadBytes       int64      `json:"raw_download_bytes"` // 实际下行流量（不含倍率）
	OnHoldExpireAt         *time.Time `json:"on_hold_expire_at,omitempty"`
	LastTrafficResetAt     *time.Time `json:"last_traffic_reset_at,omitempty"`
	OnlineAt               *time.Time `json:"online_at,omitempty"`
	Connections            int        `json:"connections"`
	Devices                int        `json:"devices"`
	CreatedAt              time.Time  `json:"created_at"`
	SubToken               string     `json:"sub_token,omitempty"`
	StripeCustomerID       string     `json:"stripe_customer_id,omitempty"`
	CurrentPlanID          string     `json:"current_plan_id,omitempty"`
	Email                  string     `json:"email,omitempty"`
	// UUID 用户级 VLESS UUID，所有节点共用。
	UUID   string `json:"uuid,omitempty"`
	// Secret 用户级 Trojan/AnyTLS/SS 密码，所有节点共用。
	// SS 2022 通过 HMAC-SHA256 从 Secret 派生固定长度 PSK（节点配置和订阅链接派生逻辑一致）。
	Secret string `json:"secret,omitempty"`
	// Password 门户密码的 bcrypt hash，空表示无密码保护。
	// 不对外 JSON 序列化，避免泄露。
	Password string `json:"-"`
	// IsAdmin 标记此用户为管理员，用于控制面登录鉴权。
	IsAdmin bool `json:"is_admin"`
}

// UserInbound 用户对某个具体 inbound 的访问凭据（一条记录对应一个 (user_id, inbound_id) 对）。
// NodeID 从 Inbound 反推，保留用于流量聚合查询。
// 协议配置由节点的 inbounds.Inbound 定义，此处只存储凭据。
type UserInbound struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	InboundID string    `json:"inbound_id"` // 对应具体的 inbound
	NodeID    string    `json:"node_id"`    // 冗余字段，用于流量聚合
	UUID      string    `json:"uuid"`       // 用于 VLESS
	Secret    string    `json:"secret"`     // 用于 Trojan / Shadowsocks
	CreatedAt time.Time `json:"created_at"`
	GroupID   string    `json:"group_id"` // '' 表示直接分配，非空表示来自用户组
}

// SubAccessLog 记录一次订阅拉取行为。
type SubAccessLog struct {
	ID         int64     `json:"id"`
	UserID     string    `json:"user_id"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	AccessedAt time.Time `json:"accessed_at"`
}

// Store 用户和入站数据的持久化接口。
type Store interface {
	// User CRUD
	UpsertUser(user User) (User, error)
	GetUser(id string) (User, error)
	GetUserBySubToken(token string) (User, error)
	GetUserByStripeCustomerID(customerID string) (User, error)
	ListUsers() ([]User, error)
	DeleteUser(id string) error
	// SetCredentials 设置用户级全局凭证（UUID 用于 VLESS，Secret 用于 Trojan/AnyTLS/SS）。
	// 所有节点的 xray 配置将使用这两个值，实现跨节点密码统一。
	SetCredentials(userID, uuid, secret string) error
	// SetPassword 设置门户密码（bcrypt hash），传空字符串表示清除密码。
	SetPassword(userID, hash string) error
	// GetPasswordBySubToken 根据 sub_token 返回用户 ID 和门户密码 hash，供登录验证使用。
	GetPasswordBySubToken(subToken string) (userID string, hash string, err error)
	// GetPasswordByUsername 根据 username 返回用户 ID、密码 hash、sub_token，供 /user 账号登录使用。
	GetPasswordByUsername(username string) (userID string, hash string, subToken string, err error)
	// GetAdminUser 返回第一个 is_admin=true 的用户，没找到返回 ErrUserNotFound。
	GetAdminUser() (User, error)
	// SetIsAdmin 设置指定用户的管理员标记。
	SetIsAdmin(userID string, isAdmin bool) error
	// UpdateUsername 只更新用户名，不影响其他字段。
	UpdateUsername(userID, username string) error

	// UserInbound CRUD（每个用户在每个 inbound 上只有一条凭据记录）
	UpsertUserInbound(inbound UserInbound) (UserInbound, error)
	GetUserInbound(id string) (UserInbound, error)
	ListUserInboundsByUser(userID string) ([]UserInbound, error)
	// ListActiveUserInboundsByUser 仅返回对应节点未禁用的记录（用于订阅生成）。
	ListActiveUserInboundsByUser(userID string) ([]UserInbound, error)
	ListUserInboundsByNode(nodeID string) ([]UserInbound, error)
	ListUserInboundsByInbound(inboundID string) ([]UserInbound, error)
	// CountUsersByInbound 返回每个 inbound 的用户数量 map[inbound_id]count。
	CountUsersByInbound() (map[string]int, error)
	DeleteUserInbound(id string) error
	DeleteUserInboundsByUser(userID string) error
	// UpdateUserInboundsNode 将指定 inbound 的所有 user_inbounds.node_id 更新为新节点。
	UpdateUserInboundsNode(inboundID, newNodeID string) error

	// GetUsersByIDs 批量获取 User，返回 map[userID]User。
	GetUsersByIDs(ids []string) (map[string]User, error)

	// 订阅访问日志
	LogSubAccess(userID, ip, userAgent string) error
	ListSubAccessLogs(userID string, limit int) ([]SubAccessLog, error)

	// 用户节点流量统计
	AddUserNodeTraffic(userID, nodeID, date string, upload, download int64) error
	ListUserNodeUsage(userID string) ([]UserNodeUsage, error)
	ClearUserNodeDailyUsage(userID string) error

	// ListUserDailyUsage 返回用户近 days 天的每日流量（跨节点合并）。
	ListUserDailyUsage(userID string, days int) ([]UserDailyUsage, error)
	// ListTodayUserStats 返回今日有流量的用户统计（跨节点合并），按总流量降序，最多 limit 条。
	ListTodayUserStats(limit int) ([]TodayUserStat, error)
	// ListTodayNodeUserStats 返回今日指定节点有流量的用户统计，按总流量降序，最多 limit 条。
	ListTodayNodeUserStats(nodeID string, limit int) ([]TodayUserStat, error)
	// ListTodayUserNodeStats 返回今日指定用户在各节点的流量分布。
	ListTodayUserNodeStats(username string) ([]TodayNodeStat, error)

	// Host 排除（用户自选订阅节点）
	ListHostExclusionsByUser(userID string) ([]string, error)
	SetHostExclusion(userID, hostID string) error
	ClearHostExclusion(userID, hostID string) error

	// 用户组相关的 inbound 操作
	ListDirectUserInboundsByUser(userID string) ([]UserInbound, error)
	DeleteGroupUserInbounds(userID, groupID string) error
	DeleteAllInboundsForGroup(groupID string) error
	UpsertGroupUserInbound(acc UserInbound) (UserInbound, error)
	ListUserGroupsByUser(userID string) ([]string, error) // 从 user_group_members 读取 groupIDs
}

// UserNodeUsage 某用户在某节点的累计流量（所有日期汇总）。
type UserNodeUsage struct {
	NodeID        string
	UploadBytes   int64
	DownloadBytes int64
}

// UserDailyUsage 某用户某天的合并流量（跨节点求和）。
type UserDailyUsage struct {
	Date          string `json:"date"` // YYYY-MM-DD
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
}

// TodayUserStat 今日某用户的流量统计（跨节点合并）。
type TodayUserStat struct {
	Username      string `json:"username"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
	TotalBytes    int64  `json:"total_bytes"`
}

// TodayNodeStat 今日某用户在某节点的流量统计。
type TodayNodeStat struct {
	NodeID        string `json:"node_id"`
	NodeName      string `json:"node_name"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
	TotalBytes    int64  `json:"total_bytes"`
}

// EffectiveStatusAt 使用给定时间计算用户的实际运行时状态（不写库，仅计算）。
// 在同一同步周期内传入相同的 now 可保证结果确定。
func (u User) EffectiveStatusAt(now time.Time) string {
	if u.Status == StatusDisabled {
		return u.Status
	}
	if u.Status == StatusOnHold {
		// OnHoldExpireAt 到期则自动视为 active（实际状态更新由 job 负责）
		if u.OnHoldExpireAt != nil && !u.OnHoldExpireAt.IsZero() && now.After(*u.OnHoldExpireAt) {
			return StatusActive
		}
		return u.Status
	}
	if u.ExpireAt != nil && !u.ExpireAt.IsZero() && now.After(*u.ExpireAt) {
		return StatusExpired
	}
	if u.TrafficLimit > 0 && u.UsedBytes >= u.TrafficLimit {
		return StatusLimited
	}
	return StatusActive
}

// EffectiveStatus 计算用户的实际运行时状态（便捷方法，使用当前时间）。
func (u User) EffectiveStatus() string {
	return u.EffectiveStatusAt(time.Now())
}

// EffectiveEnabledAt 使用给定时间判断用户是否应被下发到节点。
func (u User) EffectiveEnabledAt(now time.Time) bool {
	return u.EffectiveStatusAt(now) == StatusActive
}

// EffectiveEnabled 判断用户是否应被下发到节点（便捷方法，使用当前时间）。
func (u User) EffectiveEnabled() bool {
	return u.EffectiveStatus() == StatusActive
}
