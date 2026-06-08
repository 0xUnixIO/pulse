package postgres

import (
	"context"
	"fmt"
	"log"
)

type migration struct {
	version int
	label   string
	stmts   []string
}

// schemaMigrations 按版本顺序定义所有 schema 变更。
// v1：完整建表（含所有当前字段），新安装直接就位。
// v2：历史补丁，老库升级时补全缺失列/索引/类型变更。
// 新增字段：在此追加 v3、v4...，不要修改已有版本。
var schemaMigrations = []migration{
	{
		version: 1,
		label:   "base schema",
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS inbounds (
				id                     TEXT PRIMARY KEY,
				node_id                TEXT NOT NULL,
				protocol               TEXT NOT NULL,
				tag                    TEXT NOT NULL,
				port                   INTEGER NOT NULL,
				method                 TEXT NOT NULL DEFAULT '',
				password               TEXT NOT NULL DEFAULT '',
				security               TEXT NOT NULL DEFAULT '',
				reality_private_key    TEXT NOT NULL DEFAULT '',
				reality_public_key     TEXT NOT NULL DEFAULT '',
				reality_handshake_addr TEXT NOT NULL DEFAULT '',
				reality_short_id       TEXT NOT NULL DEFAULT '',
				outbound_id            TEXT NOT NULL DEFAULT '',
				traffic_rate           DOUBLE PRECISION NOT NULL DEFAULT 1.0,
				extra                  TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE TABLE IF NOT EXISTS hosts (
				id                 TEXT PRIMARY KEY,
				inbound_id         TEXT NOT NULL,
				remark             TEXT NOT NULL DEFAULT '',
				address            TEXT NOT NULL DEFAULT '',
				port               INTEGER NOT NULL DEFAULT 0,
				sni                TEXT NOT NULL DEFAULT '',
				host               TEXT NOT NULL DEFAULT '',
				path               TEXT NOT NULL DEFAULT '',
				security           TEXT NOT NULL DEFAULT 'none',
				alpn               TEXT NOT NULL DEFAULT '',
				fingerprint        TEXT NOT NULL DEFAULT '',
				allow_insecure     INTEGER NOT NULL DEFAULT 0,
				mux_enable         INTEGER NOT NULL DEFAULT 0,
				reality_public_key TEXT NOT NULL DEFAULT '',
				reality_short_id   TEXT NOT NULL DEFAULT '',
				reality_spider_x   TEXT NOT NULL DEFAULT '',
				country            TEXT NOT NULL DEFAULT '',
				region             TEXT NOT NULL DEFAULT '',
				network            TEXT NOT NULL DEFAULT '',
				entry              TEXT NOT NULL DEFAULT '',
				tags               TEXT NOT NULL DEFAULT '',
				relay_node_id      TEXT NOT NULL DEFAULT '',
				https_port         INTEGER NOT NULL DEFAULT 0
			)`,
			`CREATE TABLE IF NOT EXISTS nodes (
				id                  TEXT PRIMARY KEY,
				name                TEXT NOT NULL,
				base_url            TEXT NOT NULL,
				upload_bytes        BIGINT NOT NULL DEFAULT 0,
				download_bytes      BIGINT NOT NULL DEFAULT 0,
				acme_email          TEXT NOT NULL DEFAULT '',
				panel_domain        TEXT NOT NULL DEFAULT '',
				extra_proxies       TEXT NOT NULL DEFAULT '',
				https_port          INTEGER NOT NULL DEFAULT 0,
				expire_at           TEXT,
				panel_url           TEXT NOT NULL DEFAULT '',
				remark              TEXT NOT NULL DEFAULT '',
				ip_override         TEXT NOT NULL DEFAULT '',
				disabled            INTEGER NOT NULL DEFAULT 0,
				tls_mode            TEXT NOT NULL DEFAULT '',
				is_landing          BOOLEAN NOT NULL DEFAULT TRUE
			)`,
			`CREATE TABLE IF NOT EXISTS outbounds (
				id          TEXT PRIMARY KEY,
				name        TEXT NOT NULL DEFAULT '',
				protocol    TEXT NOT NULL DEFAULT 'socks5',
				server      TEXT NOT NULL DEFAULT '',
				username    TEXT NOT NULL DEFAULT '',
				password    TEXT NOT NULL DEFAULT '',
				method      TEXT NOT NULL DEFAULT '',
				uuid        TEXT NOT NULL DEFAULT '',
				sni         TEXT NOT NULL DEFAULT '',
				public_key  TEXT NOT NULL DEFAULT '',
				short_id    TEXT NOT NULL DEFAULT '',
				fingerprint TEXT NOT NULL DEFAULT '',
				flow        TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE TABLE IF NOT EXISTS users (
				id                        TEXT PRIMARY KEY,
				username                  TEXT NOT NULL,
				status                    TEXT NOT NULL DEFAULT 'active',
				note                      TEXT NOT NULL DEFAULT '',
				expire_at                 TEXT,
				data_limit_reset_strategy TEXT NOT NULL DEFAULT 'no_reset',
				traffic_limit_bytes       BIGINT NOT NULL DEFAULT 0,
				upload_bytes              BIGINT NOT NULL DEFAULT 0,
				download_bytes            BIGINT NOT NULL DEFAULT 0,
				used_bytes                BIGINT NOT NULL DEFAULT 0,
				raw_upload_bytes          BIGINT NOT NULL DEFAULT 0,
				raw_download_bytes        BIGINT NOT NULL DEFAULT 0,
				on_hold_expire_at         TEXT,
				last_traffic_reset_at     TEXT,
				online_at                 TEXT,
				connections               INTEGER NOT NULL DEFAULT 0,
				devices                   INTEGER NOT NULL DEFAULT 0,
				created_at                TEXT NOT NULL,
				sub_token                 TEXT NOT NULL DEFAULT '',
				stripe_customer_id        TEXT NOT NULL DEFAULT '',
				current_plan_id           TEXT NOT NULL DEFAULT '',
				email                     TEXT NOT NULL DEFAULT '',
				uuid                      TEXT NOT NULL DEFAULT '',
				secret                    TEXT NOT NULL DEFAULT '',
				password                  TEXT NOT NULL DEFAULT '',
				is_admin                  BOOLEAN NOT NULL DEFAULT FALSE
			)`,
			`CREATE TABLE IF NOT EXISTS user_inbounds (
				id         TEXT PRIMARY KEY,
				user_id    TEXT NOT NULL,
				inbound_id TEXT NOT NULL DEFAULT '',
				node_id    TEXT NOT NULL DEFAULT '',
				uuid       TEXT NOT NULL DEFAULT '',
				secret     TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				group_id   TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE INDEX IF NOT EXISTS idx_user_inbounds_user_id ON user_inbounds(user_id)`,
			`CREATE INDEX IF NOT EXISTS idx_user_inbounds_node_id ON user_inbounds(node_id)`,
			`CREATE INDEX IF NOT EXISTS idx_user_inbounds_inbound_id ON user_inbounds(inbound_id)`,
			`CREATE TABLE IF NOT EXISTS user_host_exclusions (
				user_id TEXT NOT NULL,
				host_id TEXT NOT NULL,
				PRIMARY KEY (user_id, host_id)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_user_host_exclusions_user ON user_host_exclusions(user_id)`,
			`CREATE TABLE IF NOT EXISTS sessions (
				token      TEXT PRIMARY KEY,
				username   TEXT NOT NULL,
				created_at TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS settings (
				key   TEXT PRIMARY KEY,
				value TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE TABLE IF NOT EXISTS node_daily_usage (
				node_id        TEXT NOT NULL,
				date           TEXT NOT NULL,
				upload_bytes   BIGINT NOT NULL DEFAULT 0,
				download_bytes BIGINT NOT NULL DEFAULT 0,
				PRIMARY KEY (node_id, date)
			)`,
			`CREATE TABLE IF NOT EXISTS sub_access_logs (
				id          BIGSERIAL PRIMARY KEY,
				user_id     TEXT NOT NULL,
				ip          TEXT NOT NULL DEFAULT '',
				user_agent  TEXT NOT NULL DEFAULT '',
				accessed_at TEXT NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS idx_sub_access_logs_user_id ON sub_access_logs(user_id)`,
			`CREATE TABLE IF NOT EXISTS node_speedtest (
				node_id   TEXT PRIMARY KEY,
				down_bps  BIGINT NOT NULL DEFAULT 0,
				up_bps    BIGINT NOT NULL DEFAULT 0,
				tested_at TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE TABLE IF NOT EXISTS node_check_results (
				node_id    TEXT NOT NULL,
				service    TEXT NOT NULL,
				check_type TEXT NOT NULL DEFAULT 'direct',
				unlocked   INTEGER NOT NULL DEFAULT 0,
				region     TEXT NOT NULL DEFAULT '',
				note       TEXT NOT NULL DEFAULT '',
				checked_at TEXT NOT NULL,
				PRIMARY KEY (node_id, service, check_type)
			)`,
			`CREATE TABLE IF NOT EXISTS node_uptime_log (
				node_id    TEXT NOT NULL,
				checked_at TEXT NOT NULL,
				online     INTEGER NOT NULL DEFAULT 0,
				running    INTEGER NOT NULL DEFAULT 0,
				PRIMARY KEY (node_id, checked_at)
			)`,
			`CREATE TABLE IF NOT EXISTS user_node_daily_usage (
				user_id        TEXT NOT NULL,
				node_id        TEXT NOT NULL,
				date           TEXT NOT NULL,
				upload_bytes   BIGINT NOT NULL DEFAULT 0,
				download_bytes BIGINT NOT NULL DEFAULT 0,
				PRIMARY KEY (user_id, node_id, date)
			)`,
			`CREATE TABLE IF NOT EXISTS route_rules (
				id              TEXT PRIMARY KEY,
				name            TEXT NOT NULL DEFAULT '',
				rule_type       TEXT NOT NULL DEFAULT 'domain_suffix',
				patterns        TEXT NOT NULL DEFAULT '',
				outbound_id     TEXT NOT NULL DEFAULT '',
				priority        INTEGER NOT NULL DEFAULT 100,
				node_ids        TEXT NOT NULL DEFAULT '',
				inbound_ids     TEXT NOT NULL DEFAULT '',
				rule_set_url    TEXT NOT NULL DEFAULT '',
				rule_set_format TEXT NOT NULL DEFAULT 'binary'
			)`,
			`CREATE TABLE IF NOT EXISTS plans (
				id                        TEXT PRIMARY KEY,
				name                      TEXT NOT NULL DEFAULT '',
				description               TEXT NOT NULL DEFAULT '',
				type                      TEXT NOT NULL DEFAULT 'one_time',
				price_cents               INTEGER NOT NULL DEFAULT 0,
				currency                  TEXT NOT NULL DEFAULT 'usd',
				stripe_price_id           TEXT NOT NULL DEFAULT '',
				traffic_limit             BIGINT NOT NULL DEFAULT 0,
				duration_days             INTEGER NOT NULL DEFAULT 0,
				data_limit_reset_strategy TEXT NOT NULL DEFAULT 'no_reset',
				user_group_ids            TEXT NOT NULL DEFAULT '',
				sort_order                INTEGER NOT NULL DEFAULT 0,
				enabled                   INTEGER NOT NULL DEFAULT 0,
				mode                      TEXT NOT NULL DEFAULT 'live',
				stock_limit               INTEGER NOT NULL DEFAULT -1,
				stock_sold                INTEGER NOT NULL DEFAULT 0,
				created_at                TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS orders (
				id                     TEXT PRIMARY KEY,
				user_id                TEXT NOT NULL DEFAULT '',
				plan_id                TEXT NOT NULL DEFAULT '',
				email                  TEXT NOT NULL DEFAULT '',
				stripe_session_id      TEXT NOT NULL DEFAULT '',
				stripe_subscription_id TEXT NOT NULL DEFAULT '',
				stripe_customer_id     TEXT NOT NULL DEFAULT '',
				status                 TEXT NOT NULL DEFAULT 'pending',
				amount_cents           INTEGER NOT NULL DEFAULT 0,
				currency               TEXT NOT NULL DEFAULT 'usd',
				created_at             TEXT NOT NULL,
				paid_at                TEXT,
				last_invoice_id        TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE INDEX IF NOT EXISTS idx_orders_user_id ON orders(user_id)`,
			`CREATE INDEX IF NOT EXISTS idx_orders_email ON orders(email)`,
			`CREATE INDEX IF NOT EXISTS idx_orders_stripe_session_id ON orders(stripe_session_id)`,
			`CREATE INDEX IF NOT EXISTS idx_orders_stripe_subscription_id ON orders(stripe_subscription_id)`,
			`CREATE TABLE IF NOT EXISTS cf_domains (
				id        TEXT PRIMARY KEY,
				cf_token  TEXT NOT NULL,
				zone_id   TEXT NOT NULL,
				zone_name TEXT NOT NULL,
				remark    TEXT DEFAULT ''
			)`,
			`CREATE TABLE IF NOT EXISTS announcements (
				id         TEXT PRIMARY KEY,
				title      TEXT NOT NULL DEFAULT '',
				content    TEXT NOT NULL DEFAULT '',
				enabled    BOOLEAN NOT NULL DEFAULT false,
				created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS tickets (
				id         TEXT PRIMARY KEY,
				user_id    TEXT NOT NULL,
				username   TEXT NOT NULL DEFAULT '',
				title      TEXT NOT NULL DEFAULT '',
				status     TEXT NOT NULL DEFAULT 'open',
				created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS ticket_messages (
				id         TEXT PRIMARY KEY,
				ticket_id  TEXT NOT NULL,
				content    TEXT NOT NULL DEFAULT '',
				is_admin   BOOLEAN NOT NULL DEFAULT false,
				created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS ticket_images (
				id          TEXT PRIMARY KEY,
				ticket_id   TEXT NOT NULL,
				filename    TEXT NOT NULL DEFAULT '',
				stored_name TEXT NOT NULL DEFAULT '',
				size        BIGINT NOT NULL DEFAULT 0,
				created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS traceroute_snapshots (
				id         TEXT PRIMARY KEY,
				node_id    TEXT NOT NULL,
				direction  TEXT NOT NULL,
				target     TEXT NOT NULL,
				hops       TEXT NOT NULL,
				quality    TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS idx_traceroute_node_created ON traceroute_snapshots(node_id, created_at DESC)`,
			`CREATE TABLE IF NOT EXISTS ip_sentinel_configs (
				node_id          TEXT PRIMARY KEY,
				region_code      TEXT NOT NULL DEFAULT '',
				region_name      TEXT NOT NULL DEFAULT '',
				base_lat         DOUBLE PRECISION NOT NULL DEFAULT 0,
				base_lon         DOUBLE PRECISION NOT NULL DEFAULT 0,
				lang_params      TEXT NOT NULL DEFAULT 'hl=en&gl=US',
				valid_url_suffix TEXT NOT NULL DEFAULT 'com',
				enable_google    BOOLEAN NOT NULL DEFAULT false,
				enable_trust     BOOLEAN NOT NULL DEFAULT false,
				white_urls       TEXT NOT NULL DEFAULT '[]',
				keywords         TEXT NOT NULL DEFAULT '[]',
				updated_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS ip_sentinel_runs (
				id           TEXT PRIMARY KEY,
				node_id      TEXT NOT NULL,
				task_type    TEXT NOT NULL DEFAULT '',
				triggered_by TEXT NOT NULL DEFAULT 'manual',
				status       TEXT NOT NULL DEFAULT 'pending',
				output       TEXT NOT NULL DEFAULT '',
				result       TEXT NOT NULL DEFAULT '',
				started_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				finished_at  TIMESTAMPTZ
			)`,
			`CREATE INDEX IF NOT EXISTS idx_ip_sentinel_runs_node ON ip_sentinel_runs(node_id, started_at DESC)`,
			`CREATE TABLE IF NOT EXISTS node_domains (
				id           TEXT PRIMARY KEY,
				node_id      TEXT NOT NULL DEFAULT '',
				cf_domain_id TEXT NOT NULL,
				fqdn         TEXT NOT NULL,
				record_type  TEXT NOT NULL DEFAULT 'A',
				content      TEXT NOT NULL DEFAULT '',
				proxied      BOOLEAN NOT NULL DEFAULT FALSE,
				synced_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_node_domains_fqdn ON node_domains(fqdn)`,
			`CREATE TABLE IF NOT EXISTS user_groups (
				id          TEXT PRIMARY KEY,
				name        TEXT NOT NULL DEFAULT '',
				remark      TEXT NOT NULL DEFAULT '',
				inbound_ids TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE TABLE IF NOT EXISTS user_group_members (
				group_id TEXT NOT NULL,
				user_id  TEXT NOT NULL,
				PRIMARY KEY (group_id, user_id)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_user_group_members_user ON user_group_members(user_id)`,
			`CREATE TABLE IF NOT EXISTS node_latency_samples (
				id         BIGSERIAL PRIMARY KEY,
				node_id    TEXT NOT NULL,
				isp        TEXT NOT NULL,
				rtt_ms     INTEGER,
				sampled_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,
			`CREATE INDEX IF NOT EXISTS idx_nls_node_sampled ON node_latency_samples(node_id, sampled_at DESC)`,
			`CREATE TABLE IF NOT EXISTS access_logs (
				id          BIGSERIAL PRIMARY KEY,
				node_id     TEXT NOT NULL,
				username    TEXT NOT NULL,
				source_ip   TEXT NOT NULL,
				source_port TEXT NOT NULL DEFAULT '',
				destination TEXT NOT NULL,
				remote_ip   TEXT NOT NULL DEFAULT '',
				route_tag   TEXT NOT NULL DEFAULT '',
				protocol    TEXT NOT NULL DEFAULT '',
				inbound_tag TEXT NOT NULL DEFAULT '',
				created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,
			`CREATE INDEX IF NOT EXISTS idx_access_logs_created_at ON access_logs(created_at DESC)`,
			`CREATE INDEX IF NOT EXISTS idx_access_logs_username ON access_logs(username)`,
			`CREATE INDEX IF NOT EXISTS idx_access_logs_node_id ON access_logs(node_id)`,
			`CREATE TABLE IF NOT EXISTS audit_rules (
				id         TEXT PRIMARY KEY,
				type       TEXT NOT NULL,
				value      TEXT NOT NULL,
				enabled    BOOLEAN NOT NULL DEFAULT TRUE,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,
			`CREATE TABLE IF NOT EXISTS portal_sessions (
				token      TEXT PRIMARY KEY,
				user_id    TEXT NOT NULL,
				expires_at TIMESTAMPTZ NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS enroll_tokens (
				token       TEXT PRIMARY KEY,
				node_id     TEXT NOT NULL,
				expires_at  TIMESTAMPTZ NOT NULL,
				consumed_at TIMESTAMPTZ,
				created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,
			`CREATE INDEX IF NOT EXISTS idx_enroll_tokens_expires ON enroll_tokens(expires_at)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_sub_token ON users(sub_token) WHERE sub_token <> ''`,
		},
	},
	{
		version: 2,
		label:   "inbounds.extra (协议私有 JSON 配置)",
		stmts: []string{
			`ALTER TABLE inbounds ADD COLUMN IF NOT EXISTS extra TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		version: 3,
		label:   "users.plan_traffic_limit_bytes (套餐基础额度，用于流量叠加后周期重置)",
		stmts: []string{
			`ALTER TABLE users ADD COLUMN IF NOT EXISTS plan_traffic_limit_bytes BIGINT NOT NULL DEFAULT 0`,
		},
	},
}

func (db *DB) init() error {
	ctx := context.Background()

	if _, err := db.conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	rows, err := db.conn.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()
	applied := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("schema_migrations rows: %w", err)
	}

	for _, m := range schemaMigrations {
		if applied[m.version] {
			continue
		}
		log.Printf("schema migration v%d (%s) ...", m.version, m.label)
		tx, err := db.conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("migration v%d begin: %w", m.version, err)
		}
		for i, stmt := range m.stmts {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				_ = tx.Rollback(ctx)
				return fmt.Errorf("migration v%d stmt[%d]: %w\nSQL: %s", m.version, i, err, stmt)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, m.version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration v%d record: %w", m.version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migration v%d commit: %w", m.version, err)
		}
		log.Printf("schema migration v%d done", m.version)
	}

	return nil
}
