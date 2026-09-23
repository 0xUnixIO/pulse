package payment

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"pulse/internal/plans"
	"pulse/internal/users"
)

type emptySettings struct{}

func (emptySettings) GetSetting(string) (string, bool) { return "", false }

func newTestShopAPI(planStore *fakePlanStore, orderStore *fakeOrderStore) *ShopAPI {
	return &ShopAPI{
		PlanStore:    planStore,
		OrderStore:   orderStore,
		UserStore:    users.NewMemoryStore(),
		Settings:     emptySettings{},
		EnvSecretKey: "sk_test_fake",
		BaseURL:      "https://shop.example.com",
	}
}

func performCheckoutRequest(t *testing.T, api *ShopAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	api.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/shop/checkout", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestCheckoutRejectsInvalidQuantity(t *testing.T) {
	for _, quantity := range []int{0, maxCheckoutQuantity + 1} {
		planStore := newFakePlanStore()
		orderStore := newFakeOrderStore()
		api := newTestShopAPI(planStore, orderStore)
		rec := performCheckoutRequest(t, api, `{"plan_id":"plan-1","email":"buyer@example.com","quantity":`+strconv.Itoa(quantity)+`}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("quantity %d: status = %d, want %d; body=%s", quantity, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		if len(orderStore.orders) != 0 {
			t.Fatalf("quantity %d: invalid checkout must not create an order", quantity)
		}
	}
}

func TestCheckoutRejectsQuantityAboveRemainingStock(t *testing.T) {
	planStore := newFakePlanStore()
	planStore.plans["plan-stock"] = plans.Plan{
		ID:            "plan-stock",
		Enabled:       true,
		StripePriceID: "price_test",
		PriceCents:    500,
		StockLimit:    10,
		StockSold:     8,
	}
	orderStore := newFakeOrderStore()
	rec := performCheckoutRequest(
		t,
		newTestShopAPI(planStore, orderStore),
		`{"plan_id":"plan-stock","email":"buyer@example.com","quantity":3}`,
	)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if len(orderStore.orders) != 0 {
		t.Fatal("out-of-stock checkout must not create an order")
	}
}

func TestCheckoutUsesUsernameForExistingUserPlaceholderEmail(t *testing.T) {
	planStore := newFakePlanStore()
	planStore.plans["plan-renew"] = plans.Plan{
		ID:            "plan-renew",
		Enabled:       true,
		StripePriceID: "price_test",
		PriceCents:    500,
		StockLimit:    -1,
	}
	orderStore := newFakeOrderStore()
	// 在创建 Stripe Session 前停止请求，便于断言已写入的订单内容。
	orderStore.upsertErr = errors.New("stop before Stripe")
	userStore := users.NewMemoryStore()
	_, err := userStore.UpsertUser(users.User{
		ID:       "user-existing",
		Username: "alice",
		SubToken: "token-existing",
		Email:    "user-oldrandom@noreply.local",
	})
	if err != nil {
		t.Fatalf("create existing user: %v", err)
	}
	api := newTestShopAPI(planStore, orderStore)
	api.UserStore = userStore

	rec := performCheckoutRequest(
		t,
		api,
		`{"plan_id":"plan-renew","email":"user-newrandom@noreply.local","sub_token":"token-existing"}`,
	)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	if len(orderStore.orders) != 1 {
		t.Fatalf("orders = %d, want 1", len(orderStore.orders))
	}
	for _, order := range orderStore.orders {
		if order.UserID != "user-existing" {
			t.Fatalf("user_id = %q, want user-existing", order.UserID)
		}
		if order.Email != "alice@noreply.local" {
			t.Fatalf("email = %q, want alice@noreply.local", order.Email)
		}
	}
}

func TestCheckoutEmailForUserKeepsRealEmail(t *testing.T) {
	email := checkoutEmailForUser(users.User{Username: "alice", Email: "alice@example.com"})
	if email != "alice@example.com" {
		t.Fatalf("email = %q, want alice@example.com", email)
	}
}
