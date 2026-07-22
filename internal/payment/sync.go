package payment

import (
	"context"
	"log"
	"time"

	"github.com/stripe/stripe-go/v83"

	"pulse/internal/jobs"
	"pulse/internal/orders"
	"pulse/internal/users"
)

// SyncSubscriptions checks all active subscription orders against Stripe
// to catch any missed webhook events (cancellations, failures, etc.).
// sg 用于动态读取 Stripe 密钥；若未配置则静默跳过。
func SyncSubscriptions(ctx context.Context, sg SettingsGetter, envSecretKey string, orderStore orders.Store, userStore users.Store) error {
	liveKey, hasLive := ResolveSecretKeyByMode(sg, "live", envSecretKey)
	testKey, hasTest := ResolveSecretKeyByMode(sg, "test", "")
	if !hasLive && !hasTest {
		return nil
	}

	allOrders, err := orderStore.ListOrders()
	if err != nil {
		return err
	}

	for _, order := range allOrders {
		if order.StripeSubscriptionID == "" || order.Status != orders.StatusPaid {
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var sub *stripe.Subscription
		if hasLive {
			sub, err = GetSubscription(liveKey, order.StripeSubscriptionID)
		}
		if err != nil && hasTest {
			sub, err = GetSubscription(testKey, order.StripeSubscriptionID)
		}
		if err != nil {
			log.Printf("sync-stripe: get subscription %s: %v", order.StripeSubscriptionID, err)
			continue
		}

		switch sub.Status {
		case stripe.SubscriptionStatusCanceled,
			stripe.SubscriptionStatusUnpaid,
			stripe.SubscriptionStatusIncompleteExpired:
			// 不覆盖已退款订单的终态
			if order.Status == orders.StatusRefunded {
				continue
			}
			order.Status = orders.StatusFailed
			if _, err := orderStore.UpsertOrder(order); err != nil {
				log.Printf("sync-stripe: update order %s: %v", order.ID, err)
			}
			if order.UserID != "" {
				// 若用户还有其它 paid 订阅订单，不禁用
				if hasOtherPaidSub(orderStore, order.UserID, order.StripeSubscriptionID) {
					log.Printf("sync-stripe: skip disable user %s — other paid subscription remains", order.UserID)
					continue
				}
				jobs.WithUserLock(func() {
					user, err := userStore.GetUser(order.UserID)
					if err == nil && user.Status == users.StatusActive {
						user.Status = users.StatusDisabled
						user.CurrentPlanID = ""
						if _, err := userStore.UpsertUser(user); err != nil {
							log.Printf("sync-stripe: disable user %s: %v", user.ID, err)
						} else {
							log.Printf("sync-stripe: disabled user %s (subscription %s cancelled)", user.Username, order.StripeSubscriptionID)
						}
					}
				})
			}

		case stripe.SubscriptionStatusPastDue:
			log.Printf("sync-stripe: subscription %s is past_due for user %s", order.StripeSubscriptionID, order.UserID)

		case stripe.SubscriptionStatusActive, stripe.SubscriptionStatusTrialing:
			// In stripe-go v82, CurrentPeriodEnd is on each SubscriptionItem.
			var periodEnd int64
			if sub.Items != nil {
				for _, item := range sub.Items.Data {
					if item.CurrentPeriodEnd > periodEnd {
						periodEnd = item.CurrentPeriodEnd
					}
				}
			}
			if periodEnd > 0 && order.UserID != "" {
				newExpiry := time.Unix(periodEnd, 0).UTC()
				jobs.WithUserLock(func() {
					user, err := userStore.GetUser(order.UserID)
					if err == nil && (user.ExpireAt == nil || !user.ExpireAt.Equal(newExpiry)) {
						user.ExpireAt = &newExpiry
						if _, err := userStore.UpsertUser(user); err != nil {
							log.Printf("sync-stripe: update expiry for user %s: %v", user.ID, err)
						}
					}
				})
			}
		}
	}
	return nil
}

func hasOtherPaidSub(orderStore orders.Store, userID, exceptSubID string) bool {
	list, err := orderStore.ListOrdersByUser(userID)
	if err != nil {
		return false
	}
	for _, o := range list {
		if o.Status != orders.StatusPaid || o.StripeSubscriptionID == "" {
			continue
		}
		if exceptSubID != "" && o.StripeSubscriptionID == exceptSubID {
			continue
		}
		return true
	}
	return false
}
