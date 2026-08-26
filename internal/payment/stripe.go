package payment

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/stripe/stripe-go/v83"
	stripeclient "github.com/stripe/stripe-go/v83/client"
	"github.com/stripe/stripe-go/v83/webhook"
	"pulse/internal/plans"
)

// newClient 创建绑定指定 API Key 的 Stripe client，不修改全局变量，并发安全。
func newClient(secretKey string) *stripeclient.API {
	sc := &stripeclient.API{}
	sc.Init(secretKey, nil)
	return sc
}

// stripeMode 读取当前激活的 Stripe 环境（"test" 或 "live"，默认 "live"）。
func stripeMode(sg SettingsGetter) string {
	if m, ok := sg.GetSetting("stripe_mode"); ok && m == "test" {
		return "test"
	}
	return "live"
}

// ResolveSecretKey 根据当前 mode 读取对应的 stripe secret key。
// 兼容旧版 stripe_secret_key 单 key 配置。
func ResolveSecretKey(sg SettingsGetter, envFallback string) (string, bool) {
	return ResolveSecretKeyByMode(sg, stripeMode(sg), envFallback)
}

// ResolveSecretKeyByMode 按显式 mode（"live" 或 "test"）读取对应 secret key。
func ResolveSecretKeyByMode(sg SettingsGetter, mode string, envFallback string) (string, bool) {
	settingKey := "stripe_live_secret_key"
	if mode == "test" {
		settingKey = "stripe_test_secret_key"
	}
	if k, ok := sg.GetSetting(settingKey); ok && k != "" {
		return k, true
	}
	// 兼容旧版单 key
	if k, ok := sg.GetSetting("stripe_secret_key"); ok && k != "" {
		return k, true
	}
	if envFallback != "" {
		return envFallback, true
	}
	return "", false
}

func resolveWebhookSecretByMode(sg SettingsGetter, mode string, envFallback string) string {
	settingKey := "stripe_live_webhook_secret"
	if mode == "test" {
		settingKey = "stripe_test_webhook_secret"
	}
	if s, ok := sg.GetSetting(settingKey); ok && s != "" {
		return s
	}
	// 兼容旧版单 key
	if s, ok := sg.GetSetting("stripe_webhook_secret"); ok && s != "" {
		return s
	}
	return envFallback
}

// ConstructEventAuto 自动尝试 live 和 test 两套 webhook secret 验签，
// 根据请求来源（/shop 或 /shop-test）自动匹配，无需手动切换环境。
func ConstructEventAuto(payload []byte, sigHeader string, sg SettingsGetter, envFallback string) (stripe.Event, error) {
	liveSec := resolveWebhookSecretByMode(sg, "live", envFallback)
	testSec := resolveWebhookSecretByMode(sg, "test", "")
	if liveSec == "" && testSec == "" {
		return stripe.Event{}, errors.New("webhook secret not configured")
	}
	if liveSec != "" {
		if evt, err := ConstructEvent(payload, sigHeader, liveSec); err == nil {
			return evt, nil
		}
	}
	if testSec != "" {
		if evt, err := ConstructEvent(payload, sigHeader, testSec); err == nil {
			return evt, nil
		}
	}
	return stripe.Event{}, errors.New("webhook signature verification failed")
}

// CreateCheckoutSession 用指定密钥创建 Stripe Checkout session。
// 返回 (sessionID, checkoutURL, error)。
func CreateCheckoutSession(secretKey string, plan plans.Plan, quantity int, email string, orderID string, subToken string, successURL string, cancelURL string) (string, string, error) {
	sc := newClient(secretKey)

	mode := stripe.String(string(stripe.CheckoutSessionModePayment))
	if plan.Type == plans.TypeSubscription {
		mode = stripe.String(string(stripe.CheckoutSessionModeSubscription))
	}

	params := &stripe.CheckoutSessionParams{
		Mode:          mode,
		SuccessURL:    stripe.String(successURL),
		CancelURL:     stripe.String(cancelURL),
		CustomerEmail: stripe.String(email),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String(plan.StripePriceID),
				Quantity: stripe.Int64(int64(quantity)),
			},
		},
	}
	params.AddMetadata("order_id", orderID)
	params.AddMetadata("plan_id", plan.ID)
	params.AddMetadata("quantity", strconv.Itoa(quantity))
	if subToken != "" {
		params.AddMetadata("sub_token", subToken)
	}

	s, err := sc.CheckoutSessions.New(params)
	if err != nil {
		return "", "", fmt.Errorf("create checkout session: %w", err)
	}
	return s.ID, s.URL, nil
}

// ConstructEvent verifies a webhook payload signature and returns the event.
// 忽略 API 版本不匹配：stripe-go 与 Stripe Dashboard 配置的版本可能不同，
// 签名验证本身不受影响，事件结构仅在极少数字段上有差异。
func ConstructEvent(payload []byte, sigHeader string, webhookSecret string) (stripe.Event, error) {
	return webhook.ConstructEventWithOptions(payload, sigHeader, webhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
}

// StripePrice 表示一个 Stripe Price 的关键信息，用于前端下拉选择。
type StripePrice struct {
	ID          string `json:"id"`
	Nickname    string `json:"nickname"`
	UnitAmount  int64  `json:"unit_amount"` // 最小货币单位（如分）
	Currency    string `json:"currency"`
	Recurring   bool   `json:"recurring"`    // true = 订阅，false = 一次性
	ProductName string `json:"product_name"` // 关联商品名称
}

// ListPrices 拉取账号下所有活跃 Price，最多 100 条。
func ListPrices(secretKey string) ([]StripePrice, error) {
	sc := newClient(secretKey)
	params := &stripe.PriceListParams{
		Active: stripe.Bool(true),
	}
	params.AddExpand("data.product")
	params.Limit = stripe.Int64(100)

	iter := sc.Prices.List(params)
	var out []StripePrice
	for iter.Next() {
		p := iter.Price()
		sp := StripePrice{
			ID:         p.ID,
			Nickname:   p.Nickname,
			UnitAmount: p.UnitAmount,
			Currency:   string(p.Currency),
			Recurring:  p.Recurring != nil,
		}
		if p.Product != nil {
			sp.ProductName = p.Product.Name
		}
		out = append(out, sp)
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("list prices: %w", err)
	}
	return out, nil
}

// GetSubscription 用指定密钥获取 Stripe 订阅详情。
func GetSubscription(secretKey, subID string) (*stripe.Subscription, error) {
	sc := newClient(secretKey)
	return sc.Subscriptions.Get(subID, nil)
}
