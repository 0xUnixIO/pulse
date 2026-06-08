-- ─── Users ────────────────────────────────────────────────────────────────────

-- name: UpsertUser :exec
INSERT INTO users (
    id, username, status, note, expire_at, data_limit_reset_strategy,
    traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
    raw_upload_bytes, raw_download_bytes,
    on_hold_expire_at, last_traffic_reset_at, online_at, connections, devices,
    created_at, sub_token, stripe_customer_id, current_plan_id, email, uuid, secret,
    plan_traffic_limit_bytes
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
    $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24,
    $25
)
ON CONFLICT(id) DO UPDATE SET
    username                  = excluded.username,
    status                    = excluded.status,
    note                      = excluded.note,
    expire_at                 = excluded.expire_at,
    data_limit_reset_strategy = excluded.data_limit_reset_strategy,
    traffic_limit_bytes       = excluded.traffic_limit_bytes,
    upload_bytes              = excluded.upload_bytes,
    download_bytes            = excluded.download_bytes,
    used_bytes                = excluded.used_bytes,
    raw_upload_bytes          = excluded.raw_upload_bytes,
    raw_download_bytes        = excluded.raw_download_bytes,
    on_hold_expire_at         = excluded.on_hold_expire_at,
    last_traffic_reset_at     = excluded.last_traffic_reset_at,
    online_at                 = excluded.online_at,
    connections               = excluded.connections,
    devices                   = excluded.devices,
    created_at                = excluded.created_at,
    sub_token                 = excluded.sub_token,
    stripe_customer_id        = excluded.stripe_customer_id,
    current_plan_id           = excluded.current_plan_id,
    email                     = excluded.email,
    uuid                      = excluded.uuid,
    secret                    = excluded.secret,
    plan_traffic_limit_bytes  = excluded.plan_traffic_limit_bytes;

-- name: GetUserByID :one
SELECT id, username, status, note, expire_at, data_limit_reset_strategy,
       traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
       raw_upload_bytes, raw_download_bytes,
       on_hold_expire_at, last_traffic_reset_at, online_at, connections, devices,
       created_at, sub_token, stripe_customer_id, current_plan_id, email, uuid, secret,
       plan_traffic_limit_bytes
FROM users
WHERE id = $1;

-- name: GetUserBySubToken :one
SELECT id, username, status, note, expire_at, data_limit_reset_strategy,
       traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
       raw_upload_bytes, raw_download_bytes,
       on_hold_expire_at, last_traffic_reset_at, online_at, connections, devices,
       created_at, sub_token, stripe_customer_id, current_plan_id, email, uuid, secret,
       plan_traffic_limit_bytes
FROM users
WHERE sub_token = $1;

-- name: GetUserByStripeCustomerID :one
SELECT id, username, status, note, expire_at, data_limit_reset_strategy,
       traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
       raw_upload_bytes, raw_download_bytes,
       on_hold_expire_at, last_traffic_reset_at, online_at, connections, devices,
       created_at, sub_token, stripe_customer_id, current_plan_id, email, uuid, secret,
       plan_traffic_limit_bytes
FROM users
WHERE stripe_customer_id = $1;

-- name: ListUsers :many
SELECT id, username, status, note, expire_at, data_limit_reset_strategy,
       traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
       raw_upload_bytes, raw_download_bytes,
       on_hold_expire_at, last_traffic_reset_at, online_at, connections, devices,
       created_at, sub_token, stripe_customer_id, current_plan_id, email, uuid, secret,
       plan_traffic_limit_bytes
FROM users
ORDER BY id;

-- name: GetUsersByIDs :many
SELECT id, username, status, note, expire_at, data_limit_reset_strategy,
       traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
       raw_upload_bytes, raw_download_bytes,
       on_hold_expire_at, last_traffic_reset_at, online_at, connections, devices,
       created_at, sub_token, stripe_customer_id, current_plan_id, email, uuid, secret,
       plan_traffic_limit_bytes
FROM users
WHERE id = ANY($1::text[]);

-- name: DeleteUserByID :execresult
DELETE FROM users WHERE id = $1;

-- name: SetUserCredentials :exec
UPDATE users SET uuid = $2, secret = $3 WHERE id = $1;

-- ─── UserInbounds ─────────────────────────────────────────────────────────────

-- name: UpsertUserInbound :exec
INSERT INTO user_inbounds (id, user_id, inbound_id, node_id, uuid, secret, created_at, group_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT(id) DO UPDATE SET
    user_id    = excluded.user_id,
    inbound_id = excluded.inbound_id,
    node_id    = excluded.node_id,
    uuid       = excluded.uuid,
    secret     = excluded.secret,
    created_at = excluded.created_at,
    group_id   = excluded.group_id;

-- name: GetUserInboundByID :one
SELECT id, user_id, inbound_id, node_id, uuid, secret, created_at, group_id
FROM user_inbounds WHERE id = $1;

-- name: ListUserInboundsByUser :many
SELECT id, user_id, inbound_id, node_id, uuid, secret, created_at, group_id
FROM user_inbounds WHERE user_id = $1 ORDER BY id;

-- name: ListActiveUserInboundsByUser :many
SELECT ui.id, ui.user_id, ui.inbound_id, ui.node_id, ui.uuid, ui.secret, ui.created_at, ui.group_id
FROM user_inbounds ui
LEFT JOIN inbounds ib ON ui.inbound_id != '' AND ib.id = ui.inbound_id
LEFT JOIN nodes n ON n.id = COALESCE(NULLIF(ui.node_id, ''), ib.node_id)
WHERE ui.user_id = $1
AND (n.id IS NULL OR n.disabled = 0)
ORDER BY ui.id;

-- name: ListUserInboundsByNode :many
SELECT id, user_id, inbound_id, node_id, uuid, secret, created_at, group_id
FROM user_inbounds WHERE node_id = $1 ORDER BY id;

-- name: ListUserInboundsByInbound :many
SELECT id, user_id, inbound_id, node_id, uuid, secret, created_at, group_id
FROM user_inbounds WHERE inbound_id = $1 ORDER BY id;

-- name: ListDirectUserInboundsByUser :many
SELECT id, user_id, inbound_id, node_id, uuid, secret, created_at, group_id
FROM user_inbounds WHERE user_id = $1 AND group_id = '' ORDER BY id;

-- name: DeleteGroupUserInbounds :exec
DELETE FROM user_inbounds WHERE user_id = $1 AND group_id = $2;

-- name: DeleteAllInboundsForGroup :exec
DELETE FROM user_inbounds WHERE group_id = $1;

-- name: UpsertGroupUserInbound :exec
INSERT INTO user_inbounds (id, user_id, inbound_id, node_id, uuid, secret, group_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT(id) DO UPDATE SET
    inbound_id = excluded.inbound_id,
    node_id    = excluded.node_id,
    uuid       = excluded.uuid,
    secret     = excluded.secret,
    group_id   = excluded.group_id;

-- name: CountUsersByInbound :many
SELECT inbound_id, COUNT(DISTINCT user_id)::bigint AS count
FROM user_inbounds
GROUP BY inbound_id;

-- name: DeleteUserInboundByID :execresult
DELETE FROM user_inbounds WHERE id = $1;

-- name: DeleteUserInboundsByUserID :exec
DELETE FROM user_inbounds WHERE user_id = $1;

-- name: DeleteUserInboundsByUserIDTx :exec
DELETE FROM user_inbounds WHERE user_id = $1;

-- name: UpdateUserInboundsNodeID :exec
UPDATE user_inbounds SET node_id = $1 WHERE inbound_id = $2;

-- ─── sub_access_logs ──────────────────────────────────────────────────────────

-- name: InsertSubAccessLog :exec
INSERT INTO sub_access_logs (user_id, ip, user_agent, accessed_at)
VALUES ($1, $2, $3, $4);

-- name: ListSubAccessLogs :many
SELECT id, user_id, ip, user_agent, accessed_at
FROM sub_access_logs
WHERE user_id = $1
ORDER BY id DESC
LIMIT $2;

-- name: DeleteSubAccessLogsByUserID :exec
DELETE FROM sub_access_logs WHERE user_id = $1;

-- name: TrimSubAccessLogsByUser :exec
-- 插入后调用，保留每个用户最近 $2 条，超出部分按 id 升序删除（即最旧的先删）
DELETE FROM sub_access_logs AS sal
WHERE sal.user_id = $1
  AND sal.id NOT IN (
    SELECT id FROM sub_access_logs
    WHERE user_id = $1
    ORDER BY id DESC
    LIMIT $2
  );

-- ─── user_node_daily_usage ────────────────────────────────────────────────────

-- name: UpsertUserNodeTraffic :exec
INSERT INTO user_node_daily_usage (user_id, node_id, date, upload_bytes, download_bytes)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT(user_id, node_id, date) DO UPDATE SET
    upload_bytes   = user_node_daily_usage.upload_bytes   + excluded.upload_bytes,
    download_bytes = user_node_daily_usage.download_bytes + excluded.download_bytes;

-- name: ListUserDailyUsage :many
SELECT date, SUM(upload_bytes)::bigint AS upload_bytes, SUM(download_bytes)::bigint AS download_bytes
FROM user_node_daily_usage
WHERE user_id = $1 AND date >= $2
GROUP BY date
ORDER BY date;

-- name: ListUserNodeUsage :many
SELECT node_id,
       SUM(upload_bytes)::bigint   AS upload_bytes,
       SUM(download_bytes)::bigint AS download_bytes
FROM user_node_daily_usage
WHERE user_id = $1
GROUP BY node_id
ORDER BY SUM(upload_bytes) + SUM(download_bytes) DESC;

-- name: ListTodayUserStats :many
SELECT u.username,
       SUM(d.upload_bytes)::bigint   AS upload_bytes,
       SUM(d.download_bytes)::bigint AS download_bytes
FROM user_node_daily_usage d
JOIN users u ON u.id = d.user_id
WHERE d.date = $1
GROUP BY d.user_id, u.username
HAVING SUM(d.upload_bytes) + SUM(d.download_bytes) > 0
ORDER BY SUM(d.upload_bytes) + SUM(d.download_bytes) DESC
LIMIT $2;

-- name: ListTodayUserNodeStats :many
SELECT n.id   AS node_id,
       n.name AS node_name,
       SUM(d.upload_bytes)::bigint   AS upload_bytes,
       SUM(d.download_bytes)::bigint AS download_bytes
FROM user_node_daily_usage d
JOIN users u ON u.id = d.user_id
JOIN nodes n ON n.id = d.node_id
WHERE d.date = $1
  AND u.username = $2
GROUP BY n.id, n.name
HAVING SUM(d.upload_bytes) + SUM(d.download_bytes) > 0
ORDER BY SUM(d.upload_bytes) + SUM(d.download_bytes) DESC;

-- name: ListTodayNodeUserStats :many
SELECT u.username,
       SUM(d.upload_bytes)::bigint   AS upload_bytes,
       SUM(d.download_bytes)::bigint AS download_bytes
FROM user_node_daily_usage d
JOIN users u ON u.id = d.user_id
WHERE d.date = $1
  AND d.node_id = $2
GROUP BY d.user_id, u.username
HAVING SUM(d.upload_bytes) + SUM(d.download_bytes) > 0
ORDER BY SUM(d.upload_bytes) + SUM(d.download_bytes) DESC
LIMIT $3;

-- name: DeleteUserNodeDailyUsageByUserID :exec
DELETE FROM user_node_daily_usage WHERE user_id = $1;
