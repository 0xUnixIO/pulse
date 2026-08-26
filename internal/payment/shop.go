package payment

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"pulse/internal/idgen"
	"pulse/internal/orders"
	"pulse/internal/plans"
	"pulse/internal/users"
)

// isValidEmail 做最基础的 email 格式校验：必须包含 @ 且两侧均非空，同时限制长度。
func isValidEmail(email string) bool {
	if len(email) > 254 {
		return false
	}
	at := strings.LastIndex(email, "@")
	return at > 0 && at < len(email)-1
}

// checkoutRateLimiter 基于 IP 的滑动窗口限流器，用于 /shop/checkout。
type checkoutRateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	limit    int
	window   time.Duration
}

func newCheckoutRateLimiter() *checkoutRateLimiter {
	return &checkoutRateLimiter{
		requests: make(map[string][]time.Time),
		limit:    5,
		window:   time.Minute,
	}
}

func (rl *checkoutRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-rl.window)
	prev := rl.requests[ip]
	valid := prev[:0]
	for _, t := range prev {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	if len(valid) >= rl.limit {
		rl.requests[ip] = valid
		return false
	}
	rl.requests[ip] = append(valid, now)
	return true
}

// clientIP 从请求中提取客户端 IP（优先 X-Real-IP，回退 RemoteAddr）。
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// SettingsGetter 从持久化存储读取键值配置。
type SettingsGetter interface {
	GetSetting(key string) (string, bool)
}

// TokenValidator 校验 Bearer token 是否有效（复用 panel admin session）。
type TokenValidator interface {
	ValidateToken(token string) bool
}

// ShopAPI handles public shop endpoints.
type ShopAPI struct {
	PlanStore       plans.Store
	OrderStore      orders.Store
	UserStore       users.Store
	Deps            *WebhookDeps
	Settings        SettingsGetter
	EnvSecretKey    string         // 环境变量回退密钥
	BaseURL         string         // fallback，仅当 Settings 未配置 shop_base_url 时使用
	AdminAuth       TokenValidator // 非 nil 时 /shop-test/* 需要 admin token
	checkoutLimiter *checkoutRateLimiter
}

// Register registers shop routes on the given mux.
// These are PUBLIC endpoints (no auth required).
// /shop/* 使用生产密钥（真实扣款），/shop-test/* 使用沙盒密钥。
func (s *ShopAPI) Register(mux *http.ServeMux) {
	s.checkoutLimiter = newCheckoutRateLimiter()
	mux.HandleFunc("GET /shop/plans", s.listPlansHandler("live"))
	mux.HandleFunc("POST /shop/checkout", s.createCheckoutHandler("live", "/shop"))
	mux.HandleFunc("GET /shop-test/plans", s.requireAdmin(s.listPlansHandler("test")))
	mux.HandleFunc("POST /shop-test/checkout", s.requireAdmin(s.createCheckoutHandler("test", "/shop-test")))
}

// requireAdmin 要求请求携带有效的管理员 Bearer token。
func (s *ShopAPI) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.AdminAuth != nil {
			const prefix = "Bearer "
			auth := r.Header.Get("Authorization")
			token := ""
			if strings.HasPrefix(auth, prefix) {
				token = strings.TrimSpace(strings.TrimPrefix(auth, prefix))
			}
			if token == "" || !s.AdminAuth.ValidateToken(token) {
				writeJSONError(w, http.StatusUnauthorized, "admin token required")
				return
			}
		}
		next(w, r)
	}
}

func (s *ShopAPI) listPlansHandler(mode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := s.PlanStore.ListEnabledPlansByMode(mode)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to list plans")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"plans": list})
	}
}

func (s *ShopAPI) createCheckoutHandler(mode, shopPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.checkoutLimiter.Allow(clientIP(r)) {
			writeJSONError(w, http.StatusTooManyRequests, "too many requests, please try again later")
			return
		}

		secretKey, ok := ResolveSecretKeyByMode(s.Settings, mode, s.EnvSecretKey)
		if !ok {
			writeJSONError(w, http.StatusServiceUnavailable, "payment not configured")
			return
		}

		var planID, email, subToken string
		quantity := 1

		ct := r.Header.Get("Content-Type")
		if strings.Contains(ct, "application/json") {
			var body struct {
				PlanID   string `json:"plan_id"`
				Email    string `json:"email"`
				SubToken string `json:"sub_token"`
				Quantity *int   `json:"quantity"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid json")
				return
			}
			planID = body.PlanID
			email = body.Email
			subToken = body.SubToken
			if body.Quantity != nil {
				quantity = *body.Quantity
			}
		} else {
			if err := r.ParseForm(); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid form data")
				return
			}
			planID = r.FormValue("plan_id")
			email = r.FormValue("email")
			subToken = r.FormValue("sub_token")
			if rawQuantity := r.FormValue("quantity"); rawQuantity != "" {
				parsed, err := strconv.Atoi(rawQuantity)
				if err != nil {
					writeJSONError(w, http.StatusBadRequest, "quantity must be an integer")
					return
				}
				quantity = parsed
			}
		}

		if planID == "" || email == "" {
			writeJSONError(w, http.StatusBadRequest, "plan_id and email are required")
			return
		}
		if !isValidEmail(email) {
			writeJSONError(w, http.StatusBadRequest, "invalid email format")
			return
		}
		if quantity < 1 || quantity > maxCheckoutQuantity {
			writeJSONError(w, http.StatusBadRequest, "quantity must be between 1 and "+strconv.Itoa(maxCheckoutQuantity))
			return
		}

		plan, err := s.PlanStore.GetPlan(planID)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "plan not found")
			return
		}
		if !plan.Enabled {
			writeJSONError(w, http.StatusBadRequest, "plan is not available")
			return
		}
		if plan.StockLimit != -1 {
			remaining := plan.StockLimit - plan.StockSold
			if remaining <= 0 {
				writeJSONError(w, http.StatusConflict, "该套餐库存已售罄")
				return
			}
			if quantity > remaining {
				writeJSONError(w, http.StatusConflict, "库存不足，当前仅剩 "+strconv.Itoa(remaining)+" 份")
				return
			}
		}
		if plan.StripePriceID == "" {
			writeJSONError(w, http.StatusBadRequest, "plan has no Stripe price configured")
			return
		}
		if _, err := scalePlanForQuantity(plan, quantity); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}

		amountCents := int64(plan.PriceCents) * int64(quantity)
		const maxOrderAmountCents = int64(1<<31 - 1)
		if amountCents < 0 || amountCents > maxOrderAmountCents {
			writeJSONError(w, http.StatusBadRequest, "order amount is out of range")
			return
		}

		orderID := idgen.NextString()

		var userID string
		if subToken != "" {
			user, err := s.UserStore.GetUserBySubToken(subToken)
			if err == nil {
				userID = user.ID
			}
		}

		order := orders.Order{
			ID:          orderID,
			UserID:      userID,
			PlanID:      plan.ID,
			Email:       email,
			Status:      orders.StatusPending,
			AmountCents: int(amountCents),
			Quantity:    quantity,
			Currency:    plan.Currency,
		}

		// 先落库订单，再调用 Stripe，防止 Stripe Session 创建成功但订单未入库
		// 导致付款成功后 webhook 找不到订单、账号无法激活
		if _, err := s.OrderStore.UpsertOrder(order); err != nil {
			log.Printf("payment: save order %s: %v", orderID, err)
			writeJSONError(w, http.StatusUnprocessableEntity, "failed to save order")
			return
		}

		baseURL := s.BaseURL
		if s.Settings != nil {
			if v, ok := s.Settings.GetSetting("shop_base_url"); ok && v != "" {
				baseURL = v
			}
		}
		successURL := baseURL + shopPath + "/success?session_id={CHECKOUT_SESSION_ID}"
		cancelURL := baseURL + shopPath

		sessionID, checkoutURL, err := CreateCheckoutSession(secretKey, plan, quantity, email, orderID, subToken, successURL, cancelURL)
		if err != nil {
			log.Printf("payment: create checkout session: %v", err)
			writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		order.StripeSessionID = sessionID
		if _, err := s.OrderStore.UpsertOrder(order); err != nil {
			log.Printf("payment: update order session %s: %v", orderID, err)
			// 订单已入库，sessionID 更新失败时 webhook 通过 orderID metadata 仍可关联
		}

		// JSON 请求返回 url，form 请求直接重定向
		if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			writeJSON(w, http.StatusOK, map[string]string{"url": checkoutURL})
		} else {
			http.Redirect(w, r, checkoutURL, http.StatusSeeOther)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("payment: write json: %v", err)
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
