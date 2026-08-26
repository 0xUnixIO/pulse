-- name: UpsertPlan :exec
INSERT INTO plans (
    id, name, description, type, price_cents, currency,
    stripe_price_id, traffic_limit, duration_days,
    data_limit_reset_strategy, user_group_ids, sort_order, enabled, mode,
    stock_limit, stock_sold, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
ON CONFLICT(id) DO UPDATE SET
    name                      = excluded.name,
    description               = excluded.description,
    type                      = excluded.type,
    price_cents               = excluded.price_cents,
    currency                  = excluded.currency,
    stripe_price_id           = excluded.stripe_price_id,
    traffic_limit             = excluded.traffic_limit,
    duration_days             = excluded.duration_days,
    data_limit_reset_strategy = excluded.data_limit_reset_strategy,
    user_group_ids               = excluded.user_group_ids,
    sort_order                = excluded.sort_order,
    enabled                   = excluded.enabled,
    mode                      = excluded.mode,
    stock_limit               = excluded.stock_limit,
    stock_sold                = excluded.stock_sold,
    created_at                = excluded.created_at;

-- name: GetPlanByID :one
SELECT id, name, description, type, price_cents, currency,
       stripe_price_id, traffic_limit, duration_days,
       data_limit_reset_strategy, user_group_ids, sort_order, enabled, mode,
       stock_limit, stock_sold, created_at
FROM plans WHERE id = $1;

-- name: ListPlans :many
SELECT id, name, description, type, price_cents, currency,
       stripe_price_id, traffic_limit, duration_days,
       data_limit_reset_strategy, user_group_ids, sort_order, enabled, mode,
       stock_limit, stock_sold, created_at
FROM plans ORDER BY sort_order, id;

-- name: ListEnabledPlans :many
SELECT id, name, description, type, price_cents, currency,
       stripe_price_id, traffic_limit, duration_days,
       data_limit_reset_strategy, user_group_ids, sort_order, enabled, mode,
       stock_limit, stock_sold, created_at
FROM plans WHERE enabled = 1 ORDER BY sort_order, id;

-- name: ListEnabledPlansByMode :many
SELECT id, name, description, type, price_cents, currency,
       stripe_price_id, traffic_limit, duration_days,
       data_limit_reset_strategy, user_group_ids, sort_order, enabled, mode,
       stock_limit, stock_sold, created_at
FROM plans WHERE enabled = 1 AND mode = $1 ORDER BY sort_order, id;

-- name: IncrementPlanStockSold :execresult
UPDATE plans SET stock_sold = stock_sold + sqlc.arg(quantity)
WHERE id = sqlc.arg(plan_id)
  AND sqlc.arg(quantity) > 0
  AND (stock_limit = -1 OR stock_sold + sqlc.arg(quantity) <= stock_limit);

-- name: DeletePlanByID :execresult
DELETE FROM plans WHERE id = $1;
