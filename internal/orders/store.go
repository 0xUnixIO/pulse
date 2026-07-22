package orders

import (
	"errors"
	"time"
)

var ErrOrderNotFound = errors.New("order not found")

const (
	StatusPending  = "pending"
	StatusPaid     = "paid"
	StatusFailed   = "failed"
	StatusRefunded = "refunded"
)

// Order 记录一笔支付订单。
type Order struct {
	ID                   string     `json:"id"`
	UserID               string     `json:"user_id"`
	PlanID               string     `json:"plan_id"`
	Email                string     `json:"email"`
	StripeSessionID      string     `json:"stripe_session_id"`
	StripeSubscriptionID string     `json:"stripe_subscription_id"`
	StripeCustomerID     string     `json:"stripe_customer_id"`
	Status               string     `json:"status"`
	AmountCents          int        `json:"amount_cents"`
	Currency             string     `json:"currency"`
	CreatedAt            time.Time  `json:"created_at"`
	PaidAt               *time.Time `json:"paid_at"`
	// LastInvoiceID 记录最近处理过的 Stripe invoice ID，用于 invoice.paid 幂等去重。
	LastInvoiceID string `json:"last_invoice_id"`
}

// Store 订单持久化接口。
type Store interface {
	UpsertOrder(order Order) (Order, error)
	GetOrder(id string) (Order, error)
	GetOrderByStripeSession(sessionID string) (Order, error)
	GetOrderByStripeSubscription(subscriptionID string) (Order, error)
	ListOrders() ([]Order, error)
	ListOrdersByUser(userID string) ([]Order, error)
	ListOrdersByEmail(email string) ([]Order, error)
	DeleteOrder(id string) error
	// ClaimInvoice 原子地将 last_invoice_id 更新为 invoiceID（当且仅当当前值不等于 invoiceID 时）。
	// 返回 true 表示成功认领（应继续处理），false 表示该 invoice 已被处理（应跳过）。
	ClaimInvoice(orderID, invoiceID string) (claimed bool, err error)
	// UnclaimInvoice 在履约失败时回滚 last_invoice_id，允许 Stripe 重投后重试。
	UnclaimInvoice(orderID, invoiceID string) error
	// ClaimOrderPaid 原子地将 pending 订单标记为 paid（仅 status=pending 时成功）。
	// 返回 true 表示本实例获得履约权；false 表示已被其它并发处理。
	ClaimOrderPaid(orderID string, paidAt time.Time) (claimed bool, err error)
	// RevertOrderToPending 在履约失败时把已 claim 的 paid 订单退回 pending，允许重试。
	RevertOrderToPending(orderID string) error
}
