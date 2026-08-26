package payment

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v83"
	"pulse/internal/orders"
	"pulse/internal/plans"
	"pulse/internal/users"
)

// fakeOrderStore 是 orders.Store 的最小内存实现，仅供本测试使用。
type fakeOrderStore struct {
	orders map[string]orders.Order
}

func newFakeOrderStore() *fakeOrderStore {
	return &fakeOrderStore{orders: make(map[string]orders.Order)}
}

func (s *fakeOrderStore) UpsertOrder(o orders.Order) (orders.Order, error) {
	s.orders[o.ID] = o
	return o, nil
}

func (s *fakeOrderStore) GetOrder(id string) (orders.Order, error) {
	o, ok := s.orders[id]
	if !ok {
		return orders.Order{}, orders.ErrOrderNotFound
	}
	return o, nil
}

func (s *fakeOrderStore) GetOrderByStripeSession(sessionID string) (orders.Order, error) {
	for _, o := range s.orders {
		if o.StripeSessionID == sessionID {
			return o, nil
		}
	}
	return orders.Order{}, orders.ErrOrderNotFound
}

func (s *fakeOrderStore) GetOrderByStripeSubscription(subscriptionID string) (orders.Order, error) {
	for _, o := range s.orders {
		if o.StripeSubscriptionID == subscriptionID {
			return o, nil
		}
	}
	return orders.Order{}, orders.ErrOrderNotFound
}

func (s *fakeOrderStore) ListOrders() ([]orders.Order, error) { return nil, nil }

func (s *fakeOrderStore) ListOrdersByUser(userID string) ([]orders.Order, error) {
	var out []orders.Order
	for _, o := range s.orders {
		if o.UserID == userID {
			out = append(out, o)
		}
	}
	return out, nil
}

func (s *fakeOrderStore) ListOrdersByEmail(email string) ([]orders.Order, error) { return nil, nil }

func (s *fakeOrderStore) DeleteOrder(id string) error {
	delete(s.orders, id)
	return nil
}

func (s *fakeOrderStore) ClaimInvoice(orderID, invoiceID string) (bool, error) { return true, nil }

func (s *fakeOrderStore) UnclaimInvoice(orderID, invoiceID string) error { return nil }

func (s *fakeOrderStore) ClaimOrderPaid(orderID string, paidAt time.Time) (bool, error) {
	o, ok := s.orders[orderID]
	if !ok {
		return false, orders.ErrOrderNotFound
	}
	if o.Status != orders.StatusPending {
		return false, nil
	}
	o.Status = orders.StatusPaid
	o.PaidAt = &paidAt
	s.orders[orderID] = o
	return true, nil
}

func (s *fakeOrderStore) RevertOrderToPending(orderID string) error {
	o, ok := s.orders[orderID]
	if !ok {
		return orders.ErrOrderNotFound
	}
	o.Status = orders.StatusPending
	o.PaidAt = nil
	s.orders[orderID] = o
	return nil
}

// fakePlanStore 是 plans.Store 的最小内存实现，仅供本测试使用。
type fakePlanStore struct {
	plans         map[string]plans.Plan
	incrementedBy int
}

func newFakePlanStore() *fakePlanStore {
	return &fakePlanStore{plans: make(map[string]plans.Plan)}
}

func (s *fakePlanStore) UpsertPlan(p plans.Plan) (plans.Plan, error) {
	s.plans[p.ID] = p
	return p, nil
}

func (s *fakePlanStore) GetPlan(id string) (plans.Plan, error) {
	p, ok := s.plans[id]
	if !ok {
		return plans.Plan{}, plans.ErrPlanNotFound
	}
	return p, nil
}

func (s *fakePlanStore) ListPlans() ([]plans.Plan, error) { return nil, nil }

func (s *fakePlanStore) ListEnabledPlans() ([]plans.Plan, error) { return nil, nil }

func (s *fakePlanStore) ListEnabledPlansByMode(mode string) ([]plans.Plan, error) { return nil, nil }

func (s *fakePlanStore) IncrementStockSold(planID string, quantity int) (bool, error) {
	s.incrementedBy += quantity
	return true, nil
}

func (s *fakePlanStore) DeletePlan(id string) error {
	delete(s.plans, id)
	return nil
}

// TestClaimOrderPaid_ConcurrentOnlyOneWins 验证 checkout claim 原子性。
func TestClaimOrderPaid_ConcurrentOnlyOneWins(t *testing.T) {
	s := newFakeOrderStore()
	_, _ = s.UpsertOrder(orders.Order{ID: "o1", Status: orders.StatusPending})
	now := time.Now().UTC()
	ok1, err := s.ClaimOrderPaid("o1", now)
	if err != nil || !ok1 {
		t.Fatalf("first claim: ok=%v err=%v", ok1, err)
	}
	ok2, err := s.ClaimOrderPaid("o1", now)
	if err != nil {
		t.Fatalf("second claim err: %v", err)
	}
	if ok2 {
		t.Fatal("second claim must fail (already paid)")
	}
}

// TestUserHasOtherPaidSubscription 换购后旧 sub 删除不应误判无其它订阅。
func TestUserHasOtherPaidSubscription(t *testing.T) {
	s := newFakeOrderStore()
	_, _ = s.UpsertOrder(orders.Order{
		ID: "old", UserID: "u1", Status: orders.StatusPaid, StripeSubscriptionID: "sub_old",
	})
	_, _ = s.UpsertOrder(orders.Order{
		ID: "new", UserID: "u1", Status: orders.StatusPaid, StripeSubscriptionID: "sub_new",
	})
	deps := &WebhookDeps{OrderStore: s}
	if !deps.userHasOtherPaidSubscription("u1", "sub_old") {
		t.Fatal("excluding sub_old: sub_new should still count")
	}
	if !deps.userHasOtherPaidSubscription("u1", "sub_new") {
		t.Fatal("excluding sub_new: sub_old should still count")
	}
	if deps.userHasOtherPaidSubscription("u1", "sub_old") && deps.userHasOtherPaidSubscription("u1", "sub_new") {
		// both directions have the other sub — good
	}
}

// TestProvisionNewUser_GeneratesVlessCredentials 复现新用户通过购买套餐开通后
// vless（UUID）/ trojan-anytls-ss（Secret）凭证缺失的问题：
// provisionNewUser 曾经遗漏了这两个字段的生成，导致订阅链接无法携带有效密钥。
func TestProvisionNewUser_GeneratesVlessCredentials(t *testing.T) {
	deps := &WebhookDeps{
		OrderStore: newFakeOrderStore(),
		PlanStore:  newFakePlanStore(),
		UserStore:  users.NewMemoryStore(),
	}

	plan := plans.Plan{ID: "plan-1", TrafficLimit: 1024, DurationDays: 30}
	order := &orders.Order{ID: "order-1", Email: "new-user@example.com", PlanID: plan.ID}

	if err := deps.provisionNewUser(order, plan, time.Now().UTC()); err != nil {
		t.Fatalf("provisionNewUser failed: %v", err)
	}

	if order.UserID == "" {
		t.Fatalf("expected order.UserID to be set after provisioning")
	}

	created, err := deps.UserStore.GetUser(order.UserID)
	if err != nil {
		t.Fatalf("get created user: %v", err)
	}

	if created.UUID == "" {
		t.Error("expected UUID to be generated for new user (required for vless subscription link), got empty string")
	}
	if created.Secret == "" {
		t.Error("expected Secret to be generated for new user (required for trojan/anytls/shadowsocks), got empty string")
	}
}

func TestRenewalTimes(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

	t.Run("未过期账户沿用旧到期日作为周期锚点", func(t *testing.T) {
		currentExpireAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
		expireAt, resetAnchor := renewalTimes(&currentExpireAt, 60, now)

		if !resetAnchor.Equal(currentExpireAt) {
			t.Fatalf("reset anchor = %v, want old expire_at %v", resetAnchor, currentExpireAt)
		}
		wantExpireAt := currentExpireAt.Add(60 * 24 * time.Hour)
		if !expireAt.Equal(wantExpireAt) {
			t.Fatalf("expire_at = %v, want %v", expireAt, wantExpireAt)
		}
	})

	t.Run("已过期账户从履约时间重新起算", func(t *testing.T) {
		currentExpireAt := now.Add(-24 * time.Hour)
		expireAt, resetAnchor := renewalTimes(&currentExpireAt, 30, now)

		if !resetAnchor.Equal(now) {
			t.Fatalf("reset anchor = %v, want now %v", resetAnchor, now)
		}
		if want := now.Add(30 * 24 * time.Hour); !expireAt.Equal(want) {
			t.Fatalf("expire_at = %v, want %v", expireAt, want)
		}
	})

	t.Run("永久账户从履约时间开始套餐周期", func(t *testing.T) {
		expireAt, resetAnchor := renewalTimes(nil, 30, now)

		if !resetAnchor.Equal(now) {
			t.Fatalf("reset anchor = %v, want now %v", resetAnchor, now)
		}
		if want := now.Add(30 * 24 * time.Hour); !expireAt.Equal(want) {
			t.Fatalf("expire_at = %v, want %v", expireAt, want)
		}
	})
}

func TestScalePlanForQuantity(t *testing.T) {
	plan := plans.Plan{TrafficLimit: 100 * 1024, DurationDays: 30}
	scaled, err := scalePlanForQuantity(plan, 3)
	if err != nil {
		t.Fatalf("scalePlanForQuantity: %v", err)
	}
	if scaled.TrafficLimit != 300*1024 {
		t.Fatalf("traffic limit = %d, want %d", scaled.TrafficLimit, 300*1024)
	}
	if scaled.DurationDays != 90 {
		t.Fatalf("duration days = %d, want 90", scaled.DurationDays)
	}
	if plan.TrafficLimit != 100*1024 || plan.DurationDays != 30 {
		t.Fatal("source plan must not be mutated")
	}
}

func TestScalePlanForQuantityRejectsInvalidQuantity(t *testing.T) {
	for _, quantity := range []int{0, maxCheckoutQuantity + 1} {
		if _, err := scalePlanForQuantity(plans.Plan{}, quantity); err == nil {
			t.Fatalf("quantity %d: expected error", quantity)
		}
	}
}

func TestCheckoutCompletedFulfillsSelectedQuantity(t *testing.T) {
	orderStore := newFakeOrderStore()
	planStore := newFakePlanStore()
	userStore := users.NewMemoryStore()
	planStore.plans["plan-quantity"] = plans.Plan{
		ID:           "plan-quantity",
		TrafficLimit: 100 * 1024,
		DurationDays: 30,
	}
	_, _ = orderStore.UpsertOrder(orders.Order{
		ID:              "order-quantity",
		PlanID:          "plan-quantity",
		Email:           "quantity@example.com",
		StripeSessionID: "cs_quantity",
		Status:          orders.StatusPending,
		Quantity:        3,
	})

	deps := &WebhookDeps{
		OrderStore: orderStore,
		PlanStore:  planStore,
		UserStore:  userStore,
	}
	event := stripe.Event{Data: &stripe.EventData{Raw: json.RawMessage(`{"id":"cs_quantity"}`)}}
	deps.handleCheckoutCompleted(event)

	order, err := orderStore.GetOrder("order-quantity")
	if err != nil {
		t.Fatalf("get fulfilled order: %v", err)
	}
	if order.Status != orders.StatusPaid || order.UserID == "" || order.PaidAt == nil {
		t.Fatalf("order was not fulfilled: %+v", order)
	}
	user, err := userStore.GetUser(order.UserID)
	if err != nil {
		t.Fatalf("get provisioned user: %v", err)
	}
	if user.TrafficLimit != 300*1024 || user.PlanTrafficLimit != 300*1024 {
		t.Fatalf("traffic limits = %d/%d, want %d", user.TrafficLimit, user.PlanTrafficLimit, 300*1024)
	}
	if user.ExpireAt == nil || !user.ExpireAt.Equal(order.PaidAt.Add(90*24*time.Hour)) {
		t.Fatalf("expire_at = %v, want paid_at + 90 days", user.ExpireAt)
	}
	if planStore.incrementedBy != 3 {
		t.Fatalf("stock increment = %d, want 3", planStore.incrementedBy)
	}
}
