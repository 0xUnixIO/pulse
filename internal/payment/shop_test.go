package payment

import (
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
