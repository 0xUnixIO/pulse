package plans

import (
	"errors"
	"time"
)

var ErrPlanNotFound = errors.New("plan not found")

// Plan 定义一个可购买的套餐。
type Plan struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	Description            string    `json:"description"`
	Type                   string    `json:"type"` // "subscription" | "one_time"
	PriceCents             int       `json:"price_cents"`
	Currency               string    `json:"currency"`
	StripePriceID          string    `json:"stripe_price_id"`
	TrafficLimit           int64     `json:"traffic_limit"`
	DurationDays           int       `json:"duration_days"`
	DataLimitResetStrategy string    `json:"data_limit_reset_strategy"`
	UserGroupIDs           string    `json:"user_group_ids"` // 逗号分隔的用户组 ID
	SortOrder              int       `json:"sort_order"`
	Enabled                bool      `json:"enabled"`
	Mode                   string    `json:"mode"`        // "live" | "test"，默认 "live"
	StockLimit             int       `json:"stock_limit"` // -1 = 无限制
	StockSold              int       `json:"stock_sold"`  // 已售数量，只增不减
	CreatedAt              time.Time `json:"created_at"`
}

const (
	TypeSubscription = "subscription"
	TypeOneTime      = "one_time"
)

// Store 套餐持久化接口。
type Store interface {
	UpsertPlan(plan Plan) (Plan, error)
	GetPlan(id string) (Plan, error)
	ListPlans() ([]Plan, error)
	ListEnabledPlans() ([]Plan, error)
	// ListEnabledPlansByMode 返回指定环境（"live"/"test"）下已启用的套餐，供商店端点使用。
	ListEnabledPlansByMode(mode string) ([]Plan, error)
	// IncrementStockSold 原子地将套餐的 stock_sold 增加 quantity，仅在库存充足时执行。
	// 返回 false 表示剩余库存不足以满足本次购买。
	IncrementStockSold(planID string, quantity int) (ok bool, err error)
	DeletePlan(id string) error
}
