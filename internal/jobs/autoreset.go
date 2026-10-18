package jobs

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"pulse/internal/users"
)

// ResetTrafficForValidity 在用户写锁内执行一次「扣减有效期换流量重置」。
// 门户自助重置走这里，与 SyncUsage 的读-改-写互斥，避免重置结果被流量累加的整行回写覆盖。
// 调用方不得已持有用户写锁（SyncUsage 内部请直接用 autoResetTraffic）。
func ResetTrafficForValidity(store users.Store, userID string, now time.Time) (users.User, error) {
	var (
		user users.User
		err  error
	)
	WithUserLock(func() {
		user, err = store.ResetTrafficForValidity(userID, now, users.TrafficResetValidityCostDays)
	})
	return user, err
}

// autoResetTraffic 对已开启自动重置、刚刚（或仍然）处于超限状态的用户尝试自动重置流量。
//
// 必须在持有 mu 的情况下调用（SyncUsage 的 DB 段），且 user 必须是本轮刚落库的最新值：
//   - 触发流量的那笔增量已经由调用方 UpsertUser 写入，重置不会吞掉计费记录；
//   - ResetTrafficForValidity 是带条件的原子 UPDATE，重置后 used=0，同一用户在后续节点
//     循环中重新读库时已不再超限，因此不会重复触发；
//   - 不满足条件（未开启 / 永久账户 / 有效期不足 30 天等）时保持原行为：用户被限速。
//
// 返回重置后的用户及是否实际发生了重置。
func autoResetTraffic(ctx context.Context, store users.Store, user users.User, now time.Time, alerter Alerter) (users.User, bool) {
	if !user.AutoTrafficReset || user.EffectiveStatusAt(now) != users.StatusLimited {
		return user, false
	}
	if user.CheckTrafficReset(now, users.TrafficResetValidityCostDays) != nil {
		return user, false
	}
	reset, err := store.ResetTrafficForValidity(user.ID, now, users.TrafficResetValidityCostDays)
	if err != nil {
		if !errors.Is(err, users.ErrTrafficResetNotAllowed) {
			log.Printf("auto-traffic-reset: 用户 %s (%s) 重置失败: %v", user.Username, user.ID, err)
		}
		return user, false
	}
	expire := ""
	if reset.ExpireAt != nil {
		expire = reset.ExpireAt.UTC().Format(time.RFC3339)
	}
	log.Printf("auto-traffic-reset: 用户 %s (%s) 流量用尽，已自动重置并扣减 %d 天有效期（已用 %d 字节，新到期 %s）",
		user.Username, user.ID, users.TrafficResetValidityCostDays, user.UsedBytes, expire)
	sendAlert(ctx, alerter, "自动重置流量",
		fmt.Sprintf("用户 %s 流量用尽，已自动重置并扣减 %d 天有效期，新到期时间 %s",
			user.Username, users.TrafficResetValidityCostDays, expire))
	return reset, true
}

// TryAutoResetTraffic 在用户写锁内读取最新用户数据，若已开启自动重置且当前超限、
// 满足重置条件，则立即执行一次自动重置。用于用户刚开启开关时无需等待下一轮 SyncUsage。
// 返回重置后的用户及是否实际发生了重置；调用方负责在重置后向节点下发配置。
func TryAutoResetTraffic(ctx context.Context, store users.Store, userID string, now time.Time, alerter Alerter) (users.User, bool) {
	var (
		user users.User
		ok   bool
	)
	WithUserLock(func() {
		u, err := store.GetUser(userID)
		if err != nil {
			return
		}
		user, ok = autoResetTraffic(ctx, store, u, now, alerter)
	})
	return user, ok
}
