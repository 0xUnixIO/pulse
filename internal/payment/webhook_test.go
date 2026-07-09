package payment

import (
	"testing"
	"time"

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

func (s *fakeOrderStore) ListOrdersByUser(userID string) ([]orders.Order, error) { return nil, nil }

func (s *fakeOrderStore) ListOrdersByEmail(email string) ([]orders.Order, error) { return nil, nil }

func (s *fakeOrderStore) DeleteOrder(id string) error {
	delete(s.orders, id)
	return nil
}

func (s *fakeOrderStore) ClaimInvoice(orderID, invoiceID string) (bool, error) { return true, nil }

// fakePlanStore 是 plans.Store 的最小内存实现，仅供本测试使用。
type fakePlanStore struct {
	plans map[string]plans.Plan
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

func (s *fakePlanStore) IncrementStockSold(planID string) (bool, error) { return true, nil }

func (s *fakePlanStore) DeletePlan(id string) error {
	delete(s.plans, id)
	return nil
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
