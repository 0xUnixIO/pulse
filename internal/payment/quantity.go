package payment

import (
	"fmt"
	"time"

	"pulse/internal/plans"
)

const maxCheckoutQuantity = 99

// normalizedOrderQuantity 兼容 quantity 字段上线前创建的旧订单。
func normalizedOrderQuantity(quantity int) int {
	if quantity < 1 {
		return 1
	}
	return quantity
}

// scalePlanForQuantity 将一次购买的多份套餐折算为本次应发放的总权益。
// 数量购买等价于连续购买同一套餐：流量和有效期均按份数累计。
func scalePlanForQuantity(plan plans.Plan, quantity int) (plans.Plan, error) {
	if quantity < 1 || quantity > maxCheckoutQuantity {
		return plans.Plan{}, fmt.Errorf("quantity must be between 1 and %d", maxCheckoutQuantity)
	}

	if plan.TrafficLimit > 0 {
		const maxInt64 = int64(^uint64(0) >> 1)
		if plan.TrafficLimit > maxInt64/int64(quantity) {
			return plans.Plan{}, fmt.Errorf("traffic limit is too large for quantity %d", quantity)
		}
		plan.TrafficLimit *= int64(quantity)
	}

	if plan.DurationDays > 0 {
		const maxDurationDays = int64(^uint64(0)>>1) / int64(24*time.Hour)
		if int64(plan.DurationDays) > maxDurationDays/int64(quantity) {
			return plans.Plan{}, fmt.Errorf("duration is too large for quantity %d", quantity)
		}
		plan.DurationDays *= quantity
	}

	return plan, nil
}
