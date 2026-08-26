-- name: UpsertOrder :exec
INSERT INTO orders (
    id, user_id, plan_id, email, stripe_session_id,
    stripe_subscription_id, stripe_customer_id,
    status, amount_cents, quantity, currency, created_at, paid_at, last_invoice_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT(id) DO UPDATE SET
    user_id                = excluded.user_id,
    plan_id                = excluded.plan_id,
    email                  = excluded.email,
    stripe_session_id      = excluded.stripe_session_id,
    stripe_subscription_id = excluded.stripe_subscription_id,
    stripe_customer_id     = excluded.stripe_customer_id,
    status                 = excluded.status,
    amount_cents           = excluded.amount_cents,
    quantity               = excluded.quantity,
    currency               = excluded.currency,
    created_at             = excluded.created_at,
    paid_at                = excluded.paid_at,
    last_invoice_id        = excluded.last_invoice_id;

-- name: GetOrderByID :one
SELECT id, user_id, plan_id, email, stripe_session_id,
       stripe_subscription_id, stripe_customer_id,
       status, amount_cents, quantity, currency, created_at, paid_at, last_invoice_id
FROM orders WHERE id = $1;

-- name: GetOrderByStripeSession :one
SELECT id, user_id, plan_id, email, stripe_session_id,
       stripe_subscription_id, stripe_customer_id,
       status, amount_cents, quantity, currency, created_at, paid_at, last_invoice_id
FROM orders WHERE stripe_session_id = $1;

-- name: GetOrderByStripeSubscription :one
SELECT id, user_id, plan_id, email, stripe_session_id,
       stripe_subscription_id, stripe_customer_id,
       status, amount_cents, quantity, currency, created_at, paid_at, last_invoice_id
FROM orders WHERE stripe_subscription_id = $1;

-- name: ListOrders :many
SELECT id, user_id, plan_id, email, stripe_session_id,
       stripe_subscription_id, stripe_customer_id,
       status, amount_cents, quantity, currency, created_at, paid_at, last_invoice_id
FROM orders ORDER BY created_at DESC;

-- name: ListOrdersByUser :many
SELECT id, user_id, plan_id, email, stripe_session_id,
       stripe_subscription_id, stripe_customer_id,
       status, amount_cents, quantity, currency, created_at, paid_at, last_invoice_id
FROM orders WHERE user_id = $1 ORDER BY created_at DESC;

-- name: ListOrdersByEmail :many
SELECT id, user_id, plan_id, email, stripe_session_id,
       stripe_subscription_id, stripe_customer_id,
       status, amount_cents, quantity, currency, created_at, paid_at, last_invoice_id
FROM orders WHERE email = $1 ORDER BY created_at DESC;

-- name: ClaimInvoice :execresult
UPDATE orders SET last_invoice_id = $1
WHERE id = $2 AND COALESCE(last_invoice_id, '') != $1;

-- name: UnclaimInvoice :execresult
UPDATE orders SET last_invoice_id = ''
WHERE id = $1 AND last_invoice_id = $2;

-- name: ClaimOrderPaid :execresult
UPDATE orders SET status = 'paid', paid_at = $1
WHERE id = $2 AND status = 'pending';

-- name: RevertOrderToPending :execresult
UPDATE orders SET status = 'pending', paid_at = NULL
WHERE id = $1 AND status = 'paid';

-- name: DeleteOrderByID :execresult
DELETE FROM orders WHERE id = $1;
