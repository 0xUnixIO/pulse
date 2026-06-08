package payment

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v83"
	"pulse/internal/idgen"
	"pulse/internal/orders"
	"pulse/internal/plans"
	"pulse/internal/users"
)

// WebhookDeps holds the dependencies for webhook processing.
type WebhookDeps struct {
	OrderStore orders.Store
	PlanStore  plans.Store
	UserStore  users.Store
	// Settings 用于在每次请求时动态读取 stripe_secret_key / stripe_webhook_secret。
	Settings SettingsGetter
	// EnvSecretKey / EnvWebhookSecret 是环境变量中的回退值（可为空）。
	EnvSecretKey     string
	EnvWebhookSecret string
	// AddUserToGroups 在创建用户后将其加入对应用户组，并触发 inbound 同步。
	AddUserToGroups func(userID string, groupIDs []string) error
	// ApplyUserNodes 在用户状态/流量变更后立即将配置下发到该用户所在的所有节点。
	ApplyUserNodes func(userID string)
}

// HandleWebhook is the HTTP handler for POST /webhook/stripe.
func (d *WebhookDeps) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 65536))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}

	event, err := ConstructEventAuto(body, r.Header.Get("Stripe-Signature"), d.Settings, d.EnvWebhookSecret)
	if err != nil {
		log.Printf("payment: webhook signature error: %v", err)
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}

	// 幂等性和并发安全由数据库层保证（状态检查 + UpsertOrder），不依赖进程内锁。
	switch event.Type {
	case "checkout.session.completed":
		d.handleCheckoutCompleted(event)
	case "invoice.paid":
		d.handleInvoicePaid(event)
	case "invoice.payment_failed":
		d.handleInvoicePaymentFailed(event)
	case "customer.subscription.deleted":
		d.handleSubscriptionDeleted(event)
	}

	w.WriteHeader(http.StatusOK)
}

func (d *WebhookDeps) handleCheckoutCompleted(event stripe.Event) {
	var sess stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
		log.Printf("payment: unmarshal checkout session: %v", err)
		return
	}

	order, err := d.OrderStore.GetOrderByStripeSession(sess.ID)
	if err != nil {
		// sessionID 可能因写入失败未关联，回退到 metadata 中的 order_id
		if orderID, ok := sess.Metadata["order_id"]; ok && orderID != "" {
			order, err = d.OrderStore.GetOrder(orderID)
		}
		if err != nil {
			log.Printf("payment: get order by session %s: %v", sess.ID, err)
			return
		}
	}

	// 幂等性：已处理过则跳过
	if order.Status == orders.StatusPaid {
		return
	}

	now := time.Now().UTC()
	order.Status = orders.StatusPaid
	order.PaidAt = &now
	order.StripeSessionID = sess.ID
	if sess.Customer != nil {
		order.StripeCustomerID = sess.Customer.ID
	}
	if sess.Subscription != nil {
		order.StripeSubscriptionID = sess.Subscription.ID
	}

	plan, err := d.PlanStore.GetPlan(order.PlanID)
	if err != nil {
		log.Printf("payment: get plan %s: %v", order.PlanID, err)
		return
	}

	if order.UserID == "" {
		// 新用户：从 shop 购买
		if err := d.provisionNewUser(&order, plan, now); err != nil {
			log.Printf("payment: provision user for order %s: %v", order.ID, err)
			// 不更新订单状态，保留 pending 以便 webhook 重试可重新处理
			return
		}
	} else {
		// 已有用户续费
		if err := d.renewExistingUser(order, plan, now); err != nil {
			log.Printf("payment: renew user for order %s: %v", order.ID, err)
			// 不标记为 paid，保留 pending 以便 Stripe 重试时重新处理
			return
		}
	}

	if _, err := d.OrderStore.UpsertOrder(order); err != nil {
		log.Printf("payment: update order %s to paid: %v — MANUAL ACTION REQUIRED", order.ID, err)
		return
	}

	// 原子递增库存（超卖时只打日志，不回滚已完成的付款）
	if ok, err := d.PlanStore.IncrementStockSold(order.PlanID); err != nil {
		log.Printf("payment: increment stock for plan %s: %v", order.PlanID, err)
	} else if !ok {
		log.Printf("payment: plan %s stock exhausted after checkout (oversell by 1)", order.PlanID)
	}
}

func (d *WebhookDeps) provisionNewUser(order *orders.Order, plan plans.Plan, now time.Time) error {
	baseUsername := emailToUsername(order.Email)
	expireAt := now.Add(time.Duration(plan.DurationDays) * 24 * time.Hour)
	subToken := randomHex(16)

	newUser := users.User{
		ID:                     idgen.NextString(),
		Username:               baseUsername,
		Status:                 users.StatusActive,
		TrafficLimit:           plan.TrafficLimit,
		PlanTrafficLimit:       plan.TrafficLimit,
		DataLimitResetStrategy: plan.DataLimitResetStrategy,
		ExpireAt:               &expireAt,
		CreatedAt:              now,
		SubToken:               subToken,
		StripeCustomerID:       order.StripeCustomerID,
		CurrentPlanID:          plan.ID,
		Email:                  order.Email,
	}

	// 直接依赖数据库 UNIQUE 约束拒绝冲突，最多重试 3 次追加随机后缀，消除 TOCTOU 竞态。
	var createErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			newUser.Username = baseUsername + "-" + randomHex(3)
		}
		_, createErr = d.UserStore.UpsertUser(newUser)
		if createErr == nil {
			break
		}
		if !errors.Is(createErr, users.ErrUsernameTaken) {
			return fmt.Errorf("create user: %w", createErr)
		}
	}
	if createErr != nil {
		return fmt.Errorf("create user after retries (username conflict): %w", createErr)
	}

	order.UserID = newUser.ID

	// 立即将 UserID 持久化到订单，防止后续 UpsertOrder 失败时 webhook 重试重复创建用户。
	if _, err := d.OrderStore.UpsertOrder(*order); err != nil {
		log.Printf("payment: interim save order %s with user_id: %v", order.ID, err)
		// 继续执行 —— 下次重试时 order.UserID != "" 可跳过 provisionNewUser
	}

	// 将用户加入套餐绑定的用户组
	if plan.UserGroupIDs != "" {
		gIDs := strings.Split(plan.UserGroupIDs, ",")
		for i := range gIDs {
			gIDs[i] = strings.TrimSpace(gIDs[i])
		}
		if err := d.AddUserToGroups(newUser.ID, gIDs); err != nil {
			log.Printf("payment: add user to groups: %v", err)
		}
	}
	return nil
}

func (d *WebhookDeps) renewExistingUser(order orders.Order, plan plans.Plan, now time.Time) error {
	user, err := d.UserStore.GetUser(order.UserID)
	if err != nil {
		return fmt.Errorf("get user %s: %w", order.UserID, err)
	}

	// 到期时间：未过期则从当前到期时间延长，已过期则从现在起算
	notExpired := user.ExpireAt != nil && user.ExpireAt.After(now)
	base := now
	if notExpired {
		base = *user.ExpireAt
	}
	expireAt := base.Add(time.Duration(plan.DurationDays) * 24 * time.Hour)
	user.ExpireAt = &expireAt

	// 流量：剩余量叠加到新套餐额度，清零计数器以保证 SyncUsage delta 正确
	user.PlanTrafficLimit = plan.TrafficLimit
	if plan.TrafficLimit == 0 {
		user.TrafficLimit = 0 // 新套餐无限流量
	} else {
		remaining := user.TrafficLimit - user.UsedBytes
		if remaining < 0 {
			remaining = 0
		}
		user.TrafficLimit = remaining + plan.TrafficLimit
	}
	user.UploadBytes = 0
	user.DownloadBytes = 0
	user.UsedBytes = 0
	user.RawUploadBytes = 0
	user.RawDownloadBytes = 0
	// 未过期续费时以旧 ExpireAt 为锚点，确保下次定时重置与套餐周期对齐
	if notExpired {
		user.LastTrafficResetAt = user.ExpireAt
	} else {
		user.LastTrafficResetAt = &now
	}
	user.DataLimitResetStrategy = plan.DataLimitResetStrategy
	user.CurrentPlanID = plan.ID
	user.Status = users.StatusActive

	if _, err := d.UserStore.UpsertUser(user); err != nil {
		return fmt.Errorf("update user %s: %w", user.ID, err)
	}
	if err := d.UserStore.ClearUserNodeDailyUsage(user.ID); err != nil {
		log.Printf("payment: renew clear daily usage user %s: %v", user.ID, err)
	}

	// 加入套餐绑定的用户组
	if plan.UserGroupIDs != "" {
		gIDs := strings.Split(plan.UserGroupIDs, ",")
		for i := range gIDs {
			gIDs[i] = strings.TrimSpace(gIDs[i])
		}
		if err := d.AddUserToGroups(user.ID, gIDs); err != nil {
			log.Printf("payment: renew add user to groups: %v", err)
		}
	}
	// 无论组操作结果如何，统一触发节点下发（流量/到期已变更）
	if d.ApplyUserNodes != nil {
		go d.ApplyUserNodes(user.ID)
	}

	return nil
}

func (d *WebhookDeps) handleInvoicePaid(event stripe.Event) {
	var invoice struct {
		ID            string `json:"id"`
		Subscription  string `json:"subscription"`
		Customer      string `json:"customer"`
		BillingReason string `json:"billing_reason"`
	}
	if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
		log.Printf("payment: unmarshal invoice: %v", err)
		return
	}
	if invoice.Subscription == "" {
		return
	}
	// 首次订阅已由 checkout.session.completed 处理，跳过避免双倍延期
	if invoice.BillingReason == "subscription_create" {
		return
	}

	order, err := d.OrderStore.GetOrderByStripeSubscription(invoice.Subscription)
	if err != nil {
		log.Printf("payment: get order by subscription %s: %v", invoice.Subscription, err)
		return
	}
	if order.UserID == "" {
		return
	}

	// 幂等性：原子地认领 invoice，防止并发重试导致双倍续费（Stripe 保证至少一次投递）。
	if invoice.ID != "" {
		claimed, err := d.OrderStore.ClaimInvoice(order.ID, invoice.ID)
		if err != nil {
			log.Printf("payment: claim invoice %s for order %s: %v", invoice.ID, order.ID, err)
			return
		}
		if !claimed {
			log.Printf("payment: invoice %s already processed for order %s, skipping", invoice.ID, order.ID)
			return
		}
	}

	plan, err := d.PlanStore.GetPlan(order.PlanID)
	if err != nil {
		log.Printf("payment: get plan %s for invoice: %v", order.PlanID, err)
		return
	}

	user, err := d.UserStore.GetUser(order.UserID)
	if err != nil {
		log.Printf("payment: get user %s for invoice: %v", order.UserID, err)
		return
	}

	now := time.Now().UTC()
	base := now
	if user.ExpireAt != nil && user.ExpireAt.After(now) {
		base = *user.ExpireAt
	}
	expireAt := base.Add(time.Duration(plan.DurationDays) * 24 * time.Hour)
	user.ExpireAt = &expireAt
	user.Status = users.StatusActive

	// 流量：剩余量叠加到套餐额度，清零计数器以保证 SyncUsage delta 正确
	user.PlanTrafficLimit = plan.TrafficLimit
	if plan.TrafficLimit == 0 {
		user.TrafficLimit = 0 // 套餐无限流量
	} else {
		remaining := user.TrafficLimit - user.UsedBytes
		if remaining < 0 {
			remaining = 0
		}
		user.TrafficLimit = remaining + plan.TrafficLimit
	}
	user.UploadBytes = 0
	user.DownloadBytes = 0
	user.UsedBytes = 0
	user.RawUploadBytes = 0
	user.RawDownloadBytes = 0

	if _, err := d.UserStore.UpsertUser(user); err != nil {
		log.Printf("payment: update user %s for invoice: %v — MANUAL ACTION REQUIRED", user.ID, err)
		return
	}

	if d.ApplyUserNodes != nil {
		go d.ApplyUserNodes(user.ID)
	}
}

func (d *WebhookDeps) handleInvoicePaymentFailed(event stripe.Event) {
	var invoice struct {
		Subscription string `json:"subscription"`
		Customer     string `json:"customer"`
	}
	if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
		log.Printf("payment: unmarshal invoice failed event: %v", err)
		return
	}

	// 优先通过 subscription_id → order → user 路径，避免多订阅时误伤其他活跃订阅账号
	if invoice.Subscription != "" {
		order, err := d.OrderStore.GetOrderByStripeSubscription(invoice.Subscription)
		if err != nil {
			log.Printf("payment: get order by subscription %s (payment failed): %v", invoice.Subscription, err)
			return
		}
		if order.UserID == "" {
			return
		}
		user, err := d.UserStore.GetUser(order.UserID)
		if err != nil {
			log.Printf("payment: get user %s for payment failed: %v", order.UserID, err)
			return
		}
		user.Status = users.StatusOnHold
		if _, err := d.UserStore.UpsertUser(user); err != nil {
			log.Printf("payment: set user %s on_hold: %v", user.ID, err)
			return
		}
		if d.ApplyUserNodes != nil {
			go d.ApplyUserNodes(user.ID)
		}
		return
	}

	// 回退：无 subscription_id 时（一次性付款失败）通过 customer_id 查找
	if invoice.Customer == "" {
		return
	}
	user, err := d.UserStore.GetUserByStripeCustomerID(invoice.Customer)
	if err != nil {
		log.Printf("payment: get user by customer %s: %v", invoice.Customer, err)
		return
	}
	user.Status = users.StatusOnHold
	if _, err := d.UserStore.UpsertUser(user); err != nil {
		log.Printf("payment: set user %s on_hold: %v", user.ID, err)
		return
	}
	if d.ApplyUserNodes != nil {
		go d.ApplyUserNodes(user.ID)
	}
}

func (d *WebhookDeps) handleSubscriptionDeleted(event stripe.Event) {
	var sub struct {
		ID       string `json:"id"`
		Customer string `json:"customer"`
	}
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		log.Printf("payment: unmarshal subscription deleted event: %v", err)
		return
	}

	// 通过 subscription_id → order → user 路径，避免旧订阅删除时误禁用仍有新订阅的账号
	if sub.ID != "" {
		order, err := d.OrderStore.GetOrderByStripeSubscription(sub.ID)
		if err != nil {
			log.Printf("payment: get order by subscription %s (sub deleted): %v", sub.ID, err)
			return
		}
		if order.UserID == "" {
			return
		}
		user, err := d.UserStore.GetUser(order.UserID)
		if err != nil {
			log.Printf("payment: get user %s for subscription deleted: %v", order.UserID, err)
			return
		}
		user.Status = users.StatusDisabled
		if _, err := d.UserStore.UpsertUser(user); err != nil {
			log.Printf("payment: disable user %s: %v", user.ID, err)
			return
		}
		if d.ApplyUserNodes != nil {
			go d.ApplyUserNodes(user.ID)
		}
		return
	}

	// 回退：无 subscription_id 时通过 customer_id 查找
	if sub.Customer == "" {
		return
	}
	user, err := d.UserStore.GetUserByStripeCustomerID(sub.Customer)
	if err != nil {
		log.Printf("payment: get user by customer %s: %v", sub.Customer, err)
		return
	}
	user.Status = users.StatusDisabled
	if _, err := d.UserStore.UpsertUser(user); err != nil {
		log.Printf("payment: disable user %s: %v", user.ID, err)
		return
	}
	if d.ApplyUserNodes != nil {
		go d.ApplyUserNodes(user.ID)
	}
}

func emailToUsername(email string) string {
	parts := strings.SplitN(email, "@", 2)
	name := parts[0]
	// 只保留字母、数字、连字符、下划线、点
	var b strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			b.WriteRune(c)
		}
	}
	result := b.String()
	if result == "" {
		// 8 字节 hex（64 bits 熵）确保唯一性
		result = "user-" + randomHex(8)
	}
	return result
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 在正常系统上不可能失败；若失败则 panic 而非回退到可预测值
		panic(fmt.Sprintf("payment: crypto/rand.Read failed: %v", err))
	}
	return fmt.Sprintf("%x", buf)
}
