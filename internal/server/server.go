package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/soheilhy/cmux"
	"golang.org/x/sync/errgroup"

	"pulse/internal/alert"
	"pulse/internal/announcements"
	"path/filepath"
	"pulse/internal/auth"
	"pulse/internal/geoip"
	"pulse/internal/backup"
	"pulse/internal/buildinfo"
	"pulse/internal/cert"
	"pulse/internal/config"
	"pulse/internal/idgen"
	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/outbounds"
	"pulse/internal/panel"
	"pulse/internal/payment"
	"pulse/internal/serverapi"
	"pulse/internal/spa"
	pgStore "pulse/internal/store/postgres"
	"pulse/internal/tickets"
	"pulse/internal/usage"
	"pulse/internal/users"
	web "pulse/web"
)

func Run() error {
	// 在函数最顶部创建信号 context，使整个 server 生命周期统一受 SIGTERM/Ctrl-C 控制。
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg := config.Load()

	db, err := pgStore.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	var store nodes.Store = db.NodeStore()
	var userStore users.Store = db.UserStore()
	var inboundStore inbounds.InboundStore = db.InboundStore()
	var outboundStore outbounds.Store = db.OutboundStore()
	accessLogStore := db.AccessLogStore()
	auditRuleStore := db.AuditRuleStore()
	routeRuleStore := db.RouteRuleStore()
	settingsStore := db.SettingsStore()
	geoipDB := geoip.NewDB(filepath.Join(cfg.DataDir, "geoip"))
	authManager := auth.NewManager(db.SessionStore(), &adminStoreAdapter{userStore})

	// usageBuf 接收来自 node 的主动 push usage 帧；SyncUsage job 从此 buffer
	// drain 累计 delta。
	usageBuf := nodes.NewUsageBuffer()

	// 启动调度器
	applyOpts := jobs.ApplyOptions{
		Alerter:        alert.NewBarkSender(db.SettingsStore()),
		RouteRuleStore: routeRuleStore,
		UserStore:      userStore,
		NodeStore:      store,
		Settings:       db.SettingsStore(),
		PanelPort:      serverapi.ServerPort(),
	}
	nodeAPI := serverapi.NewWithUsers(store, userStore, inboundStore, outboundStore, applyOpts, settingsStore)
	nodeAPI.SetAccessLogStore(accessLogStore)
	nodeAPI.SetAuditRuleStore(auditRuleStore)
	scheduler := jobs.NewScheduler(nil)
	scheduler.Add(jobs.Job{
		Name:     "sync-usage",
		Interval: 30 * time.Second,
		Fn: func(ctx context.Context) error {
			_, err := jobs.SyncUsageWith(ctx, userStore, store, inboundStore, nodeAPI.Dial, applyOpts, outboundStore, usageBuf)
			return err
		},
	})
	scheduler.Add(jobs.Job{
		Name:     "reset-traffic",
		Interval: 1 * time.Minute,
		Fn: func(ctx context.Context) error {
			_, err := jobs.ResetTraffic(ctx, userStore, store, inboundStore, nodeAPI.Dial, applyOpts, outboundStore)
			return err
		},
	})
	scheduler.Add(jobs.Job{
		Name:     "activate-on-hold",
		Interval: 1 * time.Minute,
		Fn: func(ctx context.Context) error {
			return jobs.ActivateExpiredOnHold(ctx, userStore, store, inboundStore, nodeAPI.Dial, applyOpts, outboundStore)
		},
	})
	scheduler.Add(jobs.Job{
		Name:     "sync-accesslogs",
		Interval: 30 * time.Second,
		Fn: func(ctx context.Context) error {
			return jobs.SyncAccessLogs(ctx, store, accessLogStore, auditRuleStore, applyOpts.Alerter, nodeAPI.Dial)
		},
	})
	scheduler.Add(jobs.Job{
		Name:     "cleanup-accesslogs",
		Interval: 1 * time.Hour,
		Fn: func(ctx context.Context) error {
			return jobs.CleanupAccessLogs(ctx, accessLogStore)
		},
	})
	scheduler.Add(jobs.Job{
		Name:     "cleanup-logs",
		Interval: 24 * time.Hour,
		Fn: func(ctx context.Context) error {
			uptimeDays, dailyDays := 30, 180
			if v, ok := settingsStore.GetSetting("log_uptime_retain_days"); ok && v != "" {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					uptimeDays = n
				}
			}
			if v, ok := settingsStore.GetSetting("log_daily_retain_days"); ok && v != "" {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					dailyDays = n
				}
			}
			if err := jobs.CleanupLogsWithRetention(ctx, store, uptimeDays, dailyDays); err != nil {
				return err
			}
			return nil
		},
	})
	scheduler.Add(jobs.Job{
		Name:     "geoip-update",
		Interval: 24 * time.Hour,
		Fn: func(ctx context.Context) error {
			key, ok := settingsStore.GetSetting("maxmind_license_key")
			if !ok || key == "" {
				return nil // 未配置 key，跳过
			}
			lastStr, _ := settingsStore.GetSetting("maxmind_last_download")
			if lastStr != "" {
				if last, err := time.Parse(time.RFC3339, lastStr); err == nil {
					if time.Since(last) < 7*24*time.Hour {
						return nil // 距上次下载不足 7 天，跳过
					}
				}
			}
			if err := geoipDB.Download(key); err != nil {
				return fmt.Errorf("geoip weekly update: %w", err)
			}
			return settingsStore.SetSetting("maxmind_last_download", time.Now().UTC().Format(time.RFC3339))
		},
	})
	scheduler.Add(jobs.Job{
		Name:     "backup-db",
		Interval: 5 * time.Minute,
		Fn: func(ctx context.Context) error {
			intervalStr, _ := settingsStore.GetSetting("backup_interval_hours")
			intervalHours, _ := strconv.Atoi(intervalStr)
			if intervalHours <= 0 {
				return nil
			}
			lastAtStr, _ := settingsStore.GetSetting("backup_last_at")
			if lastAtStr != "" {
				if lastAt, err := time.Parse(time.RFC3339, lastAtStr); err == nil {
					if time.Since(lastAt) < time.Duration(intervalHours)*time.Hour {
						return nil
					}
				}
			}
			return jobs.BackupDB(ctx, cfg.DatabaseURL, settingsStore)
		},
	})

	scheduler.Add(jobs.Job{
		Name:     "ip-sentinel",
		Interval: 30 * time.Minute, // 每 30 分钟检查一次，实际间隔由 ip_sentinel_interval_hours 控制（默认 1 小时）
		Fn: func(ctx context.Context) error {
			return jobs.RunIPSentinel(ctx, db.IPSentinelStore(), nodeAPI.Dial, store, settingsStore)
		},
	})

	scheduler.Add(jobs.Job{
		Name:     "sample-latency",
		Interval: 1 * time.Minute,
		Fn: func(ctx context.Context) error {
			return jobs.SampleLatency(ctx, store, nodeAPI.Dial)
		},
	})

	scheduler.Add(jobs.Job{
		Name:     "cleanup-latency",
		Interval: 24 * time.Hour,
		Fn: func(ctx context.Context) error {
			return jobs.CleanupLatencySamples(ctx, store, 7)
		},
	})

	scheduler.Add(jobs.Job{
		Name:     "cleanup-enroll-tokens",
		Interval: 1 * time.Hour,
		Fn: func(ctx context.Context) error {
			// 删除过期超过 24h 的 token，保留近期记录便于排查。
			cutoff := time.Now().Add(-24 * time.Hour)
			_, err := db.EnrollTokenStore().CleanupExpired(ctx, cutoff)
			return err
		},
	})

	schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
	defer cancelScheduler()
	// scheduler.Start 在 SetNodeHub 之后调用，确保首次立即执行时 hub 已注入。

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"service": "pulse-server",
			"status":  "ok",
			"role":    "control-plane",
		})
	})
	// setup 端点（公开，无需认证）
	serverapi.RegisterSetupAPI(mux, userStore)
	mux.HandleFunc("/v1/auth/login", authManager.HandleLogin)
	mux.Handle("/v1/auth/logout", authManager.Middleware(http.HandlerFunc(authManager.HandleLogout)))
	mux.Handle("/v1/auth/me", authManager.Middleware(http.HandlerFunc(authManager.HandleMe)))
	protectedV1 := http.NewServeMux()
	protectedV1.HandleFunc("/v1/system/info", func(w http.ResponseWriter, r *http.Request) {
		summary, err := usage.Build(store, userStore, 14)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"name":                 "pulse-server",
			"description":          "pulse control plane",
			"version":              buildinfo.Version,
			"commit":               buildinfo.Commit,
			"build_date":           buildinfo.BuildDate,
			"addr":                 cfg.ServerAddr,
			"nodes_count":          summary.NodesCount,
			"users_count":          summary.UsersCount,
			"total_upload_bytes":         summary.TotalUploadBytes,
			"total_download_bytes":        summary.TotalDownloadBytes,
			"total_used_bytes":            summary.TotalUsedBytes,
			"total_billed_upload_bytes":   summary.TotalBilledUploadBytes,
			"total_billed_download_bytes": summary.TotalBilledDownloadBytes,
			"total_billed_used_bytes":     summary.TotalBilledUsedBytes,
			"active_users_count":   summary.ActiveUsersCount,
			"limited_users_count":  summary.LimitedUsersCount,
			"disabled_users_count": summary.DisabledUsersCount,
			"expired_users_count":  summary.ExpiredUsersCount,
		})
	})


	// 日志保留设置
	protectedV1.HandleFunc("/v1/settings/logs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			uptimeDays := 30
			dailyDays := 180
			if v, ok := settingsStore.GetSetting("log_uptime_retain_days"); ok && v != "" {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					uptimeDays = n
				}
			}
			if v, ok := settingsStore.GetSetting("log_daily_retain_days"); ok && v != "" {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					dailyDays = n
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"uptime_retain_days": uptimeDays,
				"daily_retain_days":  dailyDays,
			})
		case http.MethodPut:
			var body struct {
				UptimeRetainDays int `json:"uptime_retain_days"`
				DailyRetainDays  int `json:"daily_retain_days"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
				return
			}
			if body.UptimeRetainDays > 0 {
				settingsStore.SetSetting("log_uptime_retain_days", strconv.Itoa(body.UptimeRetainDays))
			}
			if body.DailyRetainDays > 0 {
				settingsStore.SetSetting("log_daily_retain_days", strconv.Itoa(body.DailyRetainDays))
			}
			writeJSON(w, http.StatusOK, map[string]any{"saved": true})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
	})

	// 立即清理日志
	protectedV1.HandleFunc("POST /v1/system/logs/cleanup", func(w http.ResponseWriter, r *http.Request) {
		uptimeDays, dailyDays := 30, 180
		if v, ok := settingsStore.GetSetting("log_uptime_retain_days"); ok && v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				uptimeDays = n
			}
		}
		if v, ok := settingsStore.GetSetting("log_daily_retain_days"); ok && v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				dailyDays = n
			}
		}
		if err := store.CleanupOldNodeUptime(uptimeDays); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if err := store.CleanupOldDailyUsage(dailyDays); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"uptime_retain_days": uptimeDays,
			"daily_retain_days":  dailyDays,
		})
	})
	protectedV1.HandleFunc("GET /v1/system/db/stats", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, db.Stats(""))
	})

	protectedV1.HandleFunc("POST /v1/system/db/cleanup", func(w http.ResponseWriter, r *http.Request) {
		result, err := db.Cleanup()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, result)
	})

	protectedV1.HandleFunc("POST /v1/system/db/query", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SQL string `json:"sql"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		// 安全校验：只允许 SELECT 和 PRAGMA
		trimmed := strings.TrimSpace(strings.ToUpper(body.SQL))
		if !strings.HasPrefix(trimmed, "SELECT") && !strings.HasPrefix(trimmed, "PRAGMA") {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "只允许 SELECT 和 PRAGMA 查询"})
			return
		}
		result, err := db.Query(body.SQL)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, result)
	})

	protectedV1.HandleFunc("/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		days := 14
		switch r.URL.Query().Get("days") {
		case "7":
			days = 7
		case "30":
			days = 30
		case "90":
			days = 90
		}
		summary, err := usage.Build(store, userStore, days)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if ts, err2 := db.TicketStore().ListTickets(); err2 == nil {
			for _, t := range ts {
				if t.Status == tickets.StatusOpen {
					summary.OpenTicketsCount++
				}
			}
		}
		writeJSON(w, http.StatusOK, summary)
	})

	// 今日节点用户分布：GET /v1/stats/nodes/{nodeId}/today-users
	protectedV1.HandleFunc("/v1/stats/nodes/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		// 路径：/v1/stats/nodes/{nodeId}/today-users
		path := strings.TrimPrefix(r.URL.Path, "/v1/stats/nodes/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[1] != "today-users" {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		nodeID := parts[0]
		if nodeID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing node id"})
			return
		}
		stats, err := userStore.ListTodayNodeUserStats(nodeID, 50)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if stats == nil {
			stats = []users.TodayUserStat{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": stats})
	})

	// 今日用户节点分布：GET /v1/stats/users/{username}/today-nodes
	protectedV1.HandleFunc("/v1/stats/users/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		// 路径：/v1/stats/users/{username}/today-nodes
		path := strings.TrimPrefix(r.URL.Path, "/v1/stats/users/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[1] != "today-nodes" {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		username := parts[0]
		if username == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing username"})
			return
		}
		stats, err := userStore.ListTodayUserNodeStats(username)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if stats == nil {
			stats = []users.TodayNodeStat{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"nodes": stats})
	})

	// 公告 CRUD API
	annStore := db.AnnouncementStore()
	protectedV1.HandleFunc("/v1/announcements", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			list, err := annStore.List()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			if list == nil {
				list = []announcements.Announcement{}
			}
			writeJSON(w, http.StatusOK, map[string]any{"announcements": list})
		case http.MethodPost:
			var body struct {
				Title   string `json:"title"`
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
				return
			}
			a := &announcements.Announcement{ID: idgen.NextString(), Title: body.Title, Content: body.Content}
			if err := annStore.Create(a); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, a)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
	})
	protectedV1.HandleFunc("/v1/announcements/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		switch r.Method {
		case http.MethodPut:
			existing, err := annStore.Get(id)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
				return
			}
			var body struct {
				Title   string `json:"title"`
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
				return
			}
			existing.Title = body.Title
			existing.Content = body.Content
			if err := annStore.Update(existing); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, existing)
		case http.MethodDelete:
			if err := annStore.Delete(id); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
	})
	protectedV1.HandleFunc("POST /v1/announcements/{id}/activate", func(w http.ResponseWriter, r *http.Request) {
		if err := annStore.SetActive(r.PathValue("id")); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	protectedV1.HandleFunc("POST /v1/announcements/{id}/disable", func(w http.ResponseWriter, r *http.Request) {
		if err := annStore.Disable(r.PathValue("id")); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// 工单 CRUD API
	ticketStore := db.TicketStore()
	uploadsDir := filepath.Join(cfg.DataDir, "uploads", "tickets")
	os.MkdirAll(uploadsDir, 0o755)

	protectedV1.HandleFunc("/v1/tickets", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			list, err := ticketStore.ListTickets()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			if list == nil {
				list = []tickets.Ticket{}
			}
			writeJSON(w, http.StatusOK, map[string]any{"tickets": list})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
	})
	protectedV1.HandleFunc("/v1/tickets/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		switch r.Method {
		case http.MethodGet:
			t, err := ticketStore.GetTicket(id)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
				return
			}
			msgs, _ := ticketStore.ListMessages(id)
			if msgs == nil {
				msgs = []tickets.Message{}
			}
			imgs, _ := ticketStore.ListImages(id)
			if imgs == nil {
				imgs = []tickets.Image{}
			}
			writeJSON(w, http.StatusOK, map[string]any{"ticket": t, "messages": msgs, "images": imgs})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
	})
	protectedV1.HandleFunc("POST /v1/tickets/{id}/reply", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := ticketStore.GetTicket(id); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		msg := &tickets.Message{ID: idgen.NextString(), TicketID: id, Content: body.Content, IsAdmin: true}
		if err := ticketStore.AddMessage(msg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		ticketStore.UpdateTicketStatus(id, tickets.StatusReplied)
		writeJSON(w, http.StatusOK, msg)
	})
	protectedV1.HandleFunc("POST /v1/tickets/{id}/close", func(w http.ResponseWriter, r *http.Request) {
		if err := ticketStore.UpdateTicketStatus(r.PathValue("id"), tickets.StatusClosed); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	protectedV1.HandleFunc("POST /v1/tickets/{id}/reopen", func(w http.ResponseWriter, r *http.Request) {
		if err := ticketStore.UpdateTicketStatus(r.PathValue("id"), tickets.StatusOpen); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	protectedV1.HandleFunc("POST /v1/tickets/{id}/images", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := ticketStore.GetTicket(id); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "ticket not found"})
			return
		}
		if err := r.ParseMultipartForm(5 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "file too large (max 5MB)"})
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing file"})
			return
		}
		defer file.Close()

		ext := strings.ToLower(filepath.Ext(header.Filename))
		allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true}
		if !allowed[ext] {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unsupported file type"})
			return
		}

		storedName := idgen.NextString() + ext
		dst, err := os.Create(filepath.Join(uploadsDir, storedName))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "save file failed"})
			return
		}
		defer dst.Close()
		written, err := io.Copy(dst, file)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "save file failed"})
			return
		}

		img := &tickets.Image{
			ID: idgen.NextString(), TicketID: id,
			Filename: header.Filename, StoredName: storedName, Size: written,
		}
		if err := ticketStore.AddImage(img); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, img)
	})

	// 图片访问（公开端点）
	mux.HandleFunc("GET /v1/uploads/tickets/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		// 防止路径遍历
		if strings.Contains(name, "/") || strings.Contains(name, "..") {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(uploadsDir, name))
	})

	// 备份配置 API
	backupFn := func(ctx context.Context) error {
		return jobs.BackupDB(ctx, cfg.DatabaseURL, settingsStore)
	}
	protectedV1.HandleFunc("/v1/settings/backup", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			accountID, _ := settingsStore.GetSetting("backup_cf_account_id")
			accessKeyID, _ := settingsStore.GetSetting("backup_cf_access_key_id")
			bucket, _ := settingsStore.GetSetting("backup_cf_bucket_name")
			intervalHours, _ := settingsStore.GetSetting("backup_interval_hours")
			lastAt, _ := settingsStore.GetSetting("backup_last_at")
			keepCount, _ := settingsStore.GetSetting("backup_keep_count")
			writeJSON(w, http.StatusOK, map[string]any{
				"account_id":     accountID,
				"access_key_id":  accessKeyID,
				"bucket_name":    bucket,
				"interval_hours": intervalHours,
				"last_at":        lastAt,
				"keep_count":     keepCount,
			})
		case http.MethodPut:
			var body struct {
				AccountID     string `json:"account_id"`
				AccessKeyID   string `json:"access_key_id"`
				SecretKey     string `json:"secret_key"`
				BucketName    string `json:"bucket_name"`
				IntervalHours string `json:"interval_hours"`
				KeepCount     string `json:"keep_count"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
				return
			}
			fields := map[string]string{
				"backup_cf_account_id":    body.AccountID,
				"backup_cf_access_key_id": body.AccessKeyID,
				"backup_cf_bucket_name":   body.BucketName,
				"backup_interval_hours":   body.IntervalHours,
				"backup_keep_count":       body.KeepCount,
			}
			if body.SecretKey != "" {
				fields["backup_cf_secret_key"] = body.SecretKey
			}
			for k, v := range fields {
				if err := settingsStore.SetSetting(k, v); err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
					return
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"saved": true})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
	})
	protectedV1.HandleFunc("POST /v1/settings/backup/run", func(w http.ResponseWriter, r *http.Request) {
		if err := backupFn(r.Context()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		lastAt, _ := settingsStore.GetSetting("backup_last_at")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "last_at": lastAt})
	})
	protectedV1.HandleFunc("GET /v1/settings/backup/list", func(w http.ResponseWriter, r *http.Request) {
		cfg := backup.ConfigFromSettings(settingsStore)
		if !cfg.Valid() {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "R2 未配置"})
			return
		}
		objects, err := backup.ListBackups(r.Context(), cfg)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"backups": objects})
	})
	protectedV1.HandleFunc("POST /v1/settings/backup/restore", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 key 参数"})
			return
		}
		bkpCfg := backup.ConfigFromSettings(settingsStore)
		if !bkpCfg.Valid() {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "R2 未配置"})
			return
		}
		data, err := backup.DownloadFromR2(r.Context(), bkpCfg, body.Key)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if err := backup.RestoreFromDump(r.Context(), cfg.DatabaseURL, data); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// Shop 设置 API（站点地址）
	protectedV1.HandleFunc("/v1/settings/shop", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			baseURL, _ := settingsStore.GetSetting("shop_base_url")
			writeJSON(w, http.StatusOK, map[string]any{"base_url": baseURL})
		case http.MethodPut:
			var body struct {
				BaseURL string `json:"base_url"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
				return
			}
			if err := settingsStore.SetSetting("shop_base_url", body.BaseURL); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"saved": true})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
	})

	// Stripe 配置 API
	protectedV1.HandleFunc("/v1/settings/stripe", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			mode, _ := settingsStore.GetSetting("stripe_mode")
			if mode == "" {
				mode = "live"
			}
			get := func(key string) string { v, _ := settingsStore.GetSetting(key); return v }
			writeJSON(w, http.StatusOK, map[string]any{
				"mode": mode,
				"test": map[string]any{
					"secret_key":     get("stripe_test_secret_key"),
					"webhook_secret": get("stripe_test_webhook_secret"),
				},
				"live": map[string]any{
					"secret_key":     get("stripe_live_secret_key"),
					"webhook_secret": get("stripe_live_webhook_secret"),
				},
			})
		case http.MethodPut:
			var body struct {
				Mode              string `json:"mode"`
				TestSecretKey     string `json:"test_secret_key"`
				TestWebhookSecret string `json:"test_webhook_secret"`
				LiveSecretKey     string `json:"live_secret_key"`
				LiveWebhookSecret string `json:"live_webhook_secret"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
				return
			}
			for _, kv := range []struct{ k, v string }{
				{"stripe_mode", body.Mode},
				{"stripe_test_secret_key", body.TestSecretKey},
				{"stripe_test_webhook_secret", body.TestWebhookSecret},
				{"stripe_live_secret_key", body.LiveSecretKey},
				{"stripe_live_webhook_secret", body.LiveWebhookSecret},
			} {
				if kv.v == "" {
					continue
				}
				if err := settingsStore.SetSetting(kv.k, kv.v); err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
					return
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"saved": true})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
	})

	// Stripe Price 列表（供前端下拉选择，避免手动填写 price_xxx）
	protectedV1.HandleFunc("GET /v1/settings/stripe/prices", func(w http.ResponseWriter, r *http.Request) {
		secretKey, ok := payment.ResolveSecretKey(settingsStore, cfg.StripeSecretKey)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "stripe not configured"})
			return
		}
		prices, err := payment.ListPrices(secretKey)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"prices": prices})
	})

	// Bark 告警设置 API
	protectedV1.HandleFunc("/v1/settings/alert", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			barkURL, _ := settingsStore.GetSetting("alert_bark_url")
			writeJSON(w, http.StatusOK, map[string]any{"bark_url": barkURL})
		case http.MethodPut:
			var body struct {
				BarkURL string `json:"bark_url"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
				return
			}
			if err := settingsStore.SetSetting("alert_bark_url", body.BarkURL); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"saved": true})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
	})
	protectedV1.HandleFunc("/v1/settings/alert/test", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		barkURL, ok := settingsStore.GetSetting("alert_bark_url")
		if !ok || barkURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请先保存 Bark URL"})
			return
		}
		sender := alert.NewBarkSender(settingsStore)
		if err := sender.Send(r.Context(), "Pulse", "测试推送，若收到则配置正常"); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "推送失败: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sent": true})
	})

	// 公开订阅端点，无需认证
	serverapi.RegisterSubAPI(mux, userStore, inboundStore)

	discourseCfg := auth.NewDiscourseConfig(cfg.DiscourseURL, cfg.DiscourseSSOSecret, cfg.DiscourseAdminUsers)
	panelHandler := panel.New(authManager, userStore, store, inboundStore, outboundStore, nodeAPI.Dial, applyOpts, discourseCfg, geoipDB)
	// Stripe 支付（懒初始化：路由无条件注册，每次请求时从 DB 动态读取密钥，回退到环境变量）
	{
		planStore := db.PlanStore()
		orderStore := db.OrderStore()
		panelHandler.SetPaymentStores(planStore, orderStore)
		webhookDeps := &payment.WebhookDeps{
			OrderStore:       orderStore,
			PlanStore:        planStore,
			UserStore:        userStore,
			Settings:         settingsStore,
			EnvSecretKey:     cfg.StripeSecretKey,
			EnvWebhookSecret: cfg.StripeWebhookSecret,
			AddUserToGroups: func(userID string, groupIDs []string) error {
				ugStore := db.UserGroupStore()
				for _, gid := range groupIDs {
					group, err := ugStore.GetUserGroup(gid)
					if err != nil {
						log.Printf("payment: get user group %s: %v", gid, err)
						continue
					}
					if err := ugStore.AddMember(gid, userID); err != nil {
						log.Printf("payment: add member %s to group %s: %v", userID, gid, err)
						continue
					}
					// 同步用户组的 inbound 给新用户
					if group.InboundIDs != "" {
						ibIDs := strings.Split(group.InboundIDs, ",")
						for i := range ibIDs {
							ibIDs[i] = strings.TrimSpace(ibIDs[i])
						}
						affected, err := panelHandler.SyncUserInbounds(userID, ibIDs)
						if err != nil {
							log.Printf("payment: sync inbounds for user %s group %s: %v", userID, gid, err)
							continue
						}
						panelHandler.ApplyNodes(affected)
					}
				}
				return nil
			},
			ApplyUserNodes: func(userID string) {
				accesses, err := userStore.ListUserInboundsByUser(userID)
				if err != nil {
					log.Printf("payment: list user inbounds %s: %v", userID, err)
					return
				}
				affectedNodeIDs := make(map[string]struct{})
				for _, acc := range accesses {
					affectedNodeIDs[acc.NodeID] = struct{}{}
				}
				nodeIDs := make([]string, 0, len(affectedNodeIDs))
				for id := range affectedNodeIDs {
					nodeIDs = append(nodeIDs, id)
				}
				panelHandler.ApplyNodes(nodeIDs)
			},
		}
		mux.HandleFunc("POST /webhook/stripe", webhookDeps.HandleWebhook)
		shopAPI := &payment.ShopAPI{
			PlanStore:    planStore,
			OrderStore:   orderStore,
			UserStore:    userStore,
			Deps:         webhookDeps,
			Settings:     settingsStore,
			EnvSecretKey: cfg.StripeSecretKey,
			AdminAuth:    authManager,
			BaseURL: func() string {
				if u, _ := settingsStore.GetSetting("shop_base_url"); u != "" {
					return strings.TrimRight(u, "/")
				}
				return fmt.Sprintf("http://localhost%s", cfg.ServerAddr)
			}(),
		}
		shopAPI.Register(mux)
		// 公开端点：查询支付成功后的订单信息（Stripe 重定向回来时 SPA 使用）。
		// sub_url 仅在付款后 15 分钟内可读取，过期后不再暴露敏感订阅令牌。
		mux.HandleFunc("GET /v1/shop/order-info", func(w http.ResponseWriter, r *http.Request) {
			sessionID := r.URL.Query().Get("session_id")
			if sessionID == "" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "session_id required"})
				return
			}
			order, err := orderStore.GetOrderByStripeSession(sessionID)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "order not found"})
				return
			}
			resp := map[string]any{"email": order.Email}
			// sub_url 仅在付款后 15 分钟内有效，防止 session_id 泄露导致订阅令牌被枚举
			const orderInfoTTL = 15 * time.Minute
			paidRecently := order.PaidAt != nil && time.Since(*order.PaidAt) < orderInfoTTL
			if order.UserID != "" && paidRecently {
				if user, uErr := userStore.GetUser(order.UserID); uErr == nil && user.SubToken != "" {
					base, _ := settingsStore.GetSetting("shop_base_url")
					base = strings.TrimRight(base, "/")
					if base == "" {
						scheme := "https"
						if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
							scheme = "http"
						}
						base = scheme + "://" + r.Host
					}
					resp["sub_url"] = base + "/sub/" + user.SubToken
					resp["portal_url"] = base + "/user/" + user.SubToken
				}
			}
			writeJSON(w, http.StatusOK, resp)
		})
		// 账单列表：返回全部订单，按创建时间倒序
		protectedV1.HandleFunc("GET /v1/orders", func(w http.ResponseWriter, r *http.Request) {
			list, err := orderStore.ListOrders()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"orders": list})
		})

		go func() {
			time.Sleep(30 * time.Second)
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for {
				if err := payment.SyncSubscriptions(schedulerCtx, settingsStore, cfg.StripeSecretKey, orderStore, userStore); err != nil {
					log.Printf("[jobs] sync-stripe error: %v", err)
				}
				select {
				case <-schedulerCtx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}

	panelHandler.RegisterPublicRoutes(mux)

	// Node enrollment 端点（POST /v1/node-enroll）：节点首次接入时通过一次性 token
	// 提交 CSR 换取由 Node CA 签发的客户端证书，用于后续 gRPC mTLS 长连接。
	// 不走 admin 鉴权 —— token 本身即凭据。
	nodeCA, err := cert.LoadOrCreateNodeCA(cfg.NodeCACertFile, cfg.NodeCAKeyFile)
	if err != nil {
		return fmt.Errorf("load/create node CA: %w", err)
	}
	serverapi.RegisterEnrollEndpoint(mux, nodeCA, db.EnrollTokenStore(), cfg.NodeGRPCURL)
	serverapi.RegisterNodeEnrollTokenEndpoint(protectedV1, store, db.EnrollTokenStore(), nil)

	protectedV1.HandleFunc("POST /v1/system/nodes/apply", func(w http.ResponseWriter, r *http.Request) {
		allNodes, err := store.List()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		nodeIDs := make([]string, 0, len(allNodes))
		for _, n := range allNodes {
			nodeIDs = append(nodeIDs, n.ID)
		}
		panelHandler.ApplyNodes(nodeIDs)
		writeJSON(w, http.StatusOK, map[string]any{"nodes": len(nodeIDs)})
	})

	// 公开端点：认证状态信息（Discourse 是否启用等）
	mux.HandleFunc("GET /v1/auth/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"discourse_enabled": discourseCfg.Enabled(),
		})
	})

	// React SPA: serve embedded frontend (catch-all after all specific routes)
	distFS, err := fs.Sub(web.PanelDistFS, "panel/dist")
	if err != nil {
		return fmt.Errorf("embed SPA: %w", err)
	}
	spaHandler, err := spa.New(distFS)
	if err != nil {
		return fmt.Errorf("init SPA: %w", err)
	}

	nodeAPI2 := serverapi.NewWithUsers(store, userStore, inboundStore, outboundStore, applyOpts, settingsStore)
	nodeAPI2.SetAccessLogStore(accessLogStore)
	nodeAPI2.SetAuditRuleStore(auditRuleStore)
	nodeAPI2.Register(protectedV1)
	// 改密端点（需要认证）
	serverapi.RegisterAuthCredentialsAPI(protectedV1, userStore, authManager.DeleteAllSessions)
	serverapi.RegisterUsersAPI(protectedV1, userStore, store, inboundStore, outboundStore, nodeAPI.Dial, applyOpts, geoipDB, db.PortalSessionStore())
	serverapi.RegisterSystemAPIWithInbounds(protectedV1, userStore, store, inboundStore, applyOpts)
	serverapi.RegisterInboundsAPI(protectedV1, inboundStore, userStore, store, outboundStore, nodeAPI.Dial, applyOpts, nodeAPI2.TriggerNodeSync)
	serverapi.RegisterOutboundsAPI(protectedV1, outboundStore)
	serverapi.RegisterRouteRulesAPI(protectedV1, routeRuleStore, store, userStore, inboundStore, outboundStore, nodeAPI.Dial, applyOpts)
	serverapi.RegisterPlansAPI(protectedV1, db.PlanStore())
	serverapi.RegisterToolsAPI(protectedV1)
	serverapi.RegisterCFDomainAPI(protectedV1, db.CFDomainStore())
	serverapi.RegisterUserGroupAPI(protectedV1, db.UserGroupStore(), userStore, inboundStore, panelHandler.ApplyNodes)
	serverapi.RegisterNodeDomainAPI(protectedV1, db.NodeDomainStore(), db.CFDomainStore(), store)
	serverapi.RegisterUpdateAPI(protectedV1, settingsStore)
	go serverapi.WarmUpdateCache()
	serverapi.RegisterGeoIPAPI(protectedV1, settingsStore, store, geoipDB)
	serverapi.RegisterIPSentinelAPI(protectedV1, db.IPSentinelStore(), nodeAPI, geoipDB, store)

	// 实例化 nodehub.Hub 并在后台启动 gRPC 监听（mTLS by NodeCA）。
	// 先通过 SetupSelfSync 构建 MultiPushHandler（hello → self-sync + usage_push），
	// 再启动 hub，最后回填 HubCaller（两步初始化，打破互相依赖）。
	selfSyncHandler, hubPushHandler := SetupSelfSync(sigCtx, SelfSyncDeps{
		UserStore:     userStore,
		InboundStore:  inboundStore,
		OutboundStore: outboundStore,
		NodeStore:     store,
		ApplyOpts:     applyOpts,
		UsagePushHandler: func(nodeID string, seq uint64, body []byte) error {
			var stats nodes.UsageStats
			if len(body) > 0 {
				if err := json.Unmarshal(body, &stats); err != nil {
					log.Printf("nodehub: usage_push decode from %s seq=%d: %v", nodeID, seq, err)
					return err
				}
			}
			return usageBuf.Append(nodeID, seq, stats)
		},
	})
	// 解析 NodeGRPCURL 中的 host 添加为服务器证书 SAN，确保节点连接时 TLS 验证通过。
	grpcSANs := []string{"localhost", "127.0.0.1"}
	if u, parseErr := url.Parse(cfg.NodeGRPCURL); parseErr == nil && u.Hostname() != "" {
		h := u.Hostname()
		if h != "localhost" && h != "127.0.0.1" {
			grpcSANs = append(grpcSANs, h)
		}
	}
	nhResult := startNodeHub(sigCtx, "pulse-grpc-server", grpcSANs, nodeCA, db.NodeStore(), hubPushHandler)
	nodeHub := nhResult.Hub
	serverapi.RegisterNodeHubMetrics(protectedV1, nodeHub)
	// 让 serverapi 的 clientFactory 优先用 hub 构造 Client。
	nodeAPI.SetNodeHub(nodeHub)
	nodeAPI2.SetNodeHub(nodeHub)
	// 回填 HubCaller 并注入 hub-aware client 工厂，使 self-sync 触发的 ApplyNode
	// 能通过已建立的 gRPC 流下发配置（而非尝试直连，节点已不再监听端口）。
	SetupSelfSyncHubCaller(selfSyncHandler, nodeHub)
	jobs.SetNodesHubClientFactory(func(nodeID string, _ jobs.HubCaller) *nodes.Client {
		return nodes.NewClientWithHub(nodeID, nodeHub)
	})
	// hub 注入完毕后再启动 scheduler 和 metrics hub，保证首次立即执行时所有依赖已就绪。
	nodeAPI2.StartMetricsHub(sigCtx)
	scheduler.Start(schedulerCtx)
	mux.Handle("/v1/system/nodehub/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/tools/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/system/info", authManager.Middleware(protectedV1))
	mux.Handle("/v1/system/db/stats", authManager.Middleware(protectedV1))
	mux.Handle("/v1/system/db/cleanup", authManager.Middleware(protectedV1))
	mux.Handle("/v1/system/logs/cleanup", authManager.Middleware(protectedV1))
	mux.Handle("/v1/system/nodes/apply", authManager.Middleware(protectedV1))
	mux.Handle("/v1/system/db/query", authManager.Middleware(protectedV1))
	mux.Handle("/v1/system/sync-usage", authManager.Middleware(protectedV1))
	mux.Handle("/v1/system/update/check", authManager.Middleware(protectedV1))
	mux.Handle("/v1/system/update/apply", authManager.Middleware(protectedV1))
	mux.Handle("/v1/settings/github-token", authManager.Middleware(protectedV1))
	mux.Handle("/v1/settings/cf-token", authManager.Middleware(protectedV1))
	mux.Handle("/v1/system/geoip/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/nodes/geoip", authManager.Middleware(protectedV1))
	mux.Handle("/v1/stats", authManager.Middleware(protectedV1))
	mux.Handle("/v1/stats/nodes/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/stats/users/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/audit/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/nodes", authManager.Middleware(protectedV1))
	mux.Handle("/v1/nodes/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/users", authManager.Middleware(protectedV1))
	mux.Handle("/v1/users/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/inbounds", authManager.Middleware(protectedV1))
	mux.Handle("/v1/inbounds/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/hosts", authManager.Middleware(protectedV1))
	mux.Handle("/v1/hosts/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/outbounds", authManager.Middleware(protectedV1))
	mux.Handle("/v1/outbounds/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/routerules", authManager.Middleware(protectedV1))
	mux.Handle("/v1/routerules/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/plans", authManager.Middleware(protectedV1))
	mux.Handle("/v1/plans/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/orders", authManager.Middleware(protectedV1))
	mux.Handle("/v1/orders/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/settings/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/announcements", authManager.Middleware(protectedV1))
	mux.Handle("/v1/announcements/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/tickets", authManager.Middleware(protectedV1))
	mux.Handle("/v1/tickets/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/cf-domains", authManager.Middleware(protectedV1))
	mux.Handle("/v1/cf-domains/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/user-groups", authManager.Middleware(protectedV1))
	mux.Handle("/v1/user-groups/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/node-domains", authManager.Middleware(protectedV1))
	mux.Handle("/v1/node-domains/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/ip-sentinel/", authManager.Middleware(protectedV1))

	mux.Handle("/v1/auth/credentials", authManager.Middleware(protectedV1))

	// Public portal API (no admin auth, uses sub_token)
	serverapi.RegisterPortalAPI(mux, userStore, store, inboundStore, outboundStore, settingsStore, db.PlanStore(), annStore, ticketStore, db.PortalSessionStore(), uploadsDir)

	// 节点自注册（公开，仅用于节点上报 BaseURL；mTLS 客户端证书已由 enrollment 流程取代）
	serverapi.RegisterNodeRegisterAPI(mux, store, nodeAPI2.EvictClient)

	// SPA catch-all: must be registered last
	mux.Handle("/", spaHandler)

	httpSrv := &http.Server{
		Handler:           accessLog(mux),
		ReadHeaderTimeout: 5 * time.Second,
		// WriteTimeout 不设：系统日志等 SSE 长连接会被强制断流
		IdleTimeout: 120 * time.Second,
	}

	// 单端口 cmux 模式：面板 HTTP + gRPC TLS 共用同一端口。
	// Cloudflare 需配置为 Flexible 模式（CF → 源站走 HTTP），节点直连走 TLS。
	// cmux.TLS() 只匹配 TLS ClientHello，CF 的 HTTP 连接不会触发，不会路由到 gRPC。
	lis, err := net.Listen("tcp", cfg.ServerAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.ServerAddr, err)
	}

	g, gctx := errgroup.WithContext(sigCtx)
	g.Go(func() error {
		<-gctx.Done()
		return lis.Close()
	})

	if nhResult.GRPCServer != nil {
		log.Printf("pulse-server panel+gRPC on %s (HTTP+TLS mux)", cfg.ServerAddr)
		m := cmux.New(lis)
		grpcL := m.Match(cmux.TLS())
		httpL := m.Match(cmux.Any())

		grpcSrv := nhResult.GRPCServer
		g.Go(func() error { return grpcSrv.Serve(grpcL) })
		g.Go(func() error { return httpSrv.Serve(httpL) })
		g.Go(func() error {
			<-gctx.Done()
			grpcSrv.Stop()
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpSrv.Shutdown(shutCtx)
			return nil
		})
		g.Go(func() error { return m.Serve() })
	} else {
		log.Printf("pulse-server panel on %s (HTTP, gRPC disabled)", cfg.ServerAddr)
		g.Go(func() error { return httpSrv.Serve(lis) })
		g.Go(func() error {
			<-gctx.Done()
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return httpSrv.Shutdown(shutCtx)
		})
	}

	err = g.Wait()
	if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

// accessLog 记录每次 HTTP 请求的方法、路径、状态码和耗时。
// 5xx 响应额外打印响应体，方便排查服务端错误。
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rw, r)
		elapsed := time.Since(start).Round(time.Millisecond)
		if rw.status >= 500 && rw.body.Len() > 0 {
			log.Printf("%s %s %d %s body=%s", r.Method, r.URL.Path, rw.status, elapsed, rw.body.String())
		} else {
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, rw.status, elapsed)
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer // 5xx 时缓存响应体
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if sw.status >= 500 {
		sw.body.Write(b) //nolint:errcheck
	}
	return sw.ResponseWriter.Write(b)
}

// Flush 转发 http.Flusher，保证 SSE 等流式端点可以正常推送数据。
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// adminStoreAdapter 将 users.Store 适配为 auth.AdminStore 接口。
type adminStoreAdapter struct{ s users.Store }

func (a *adminStoreAdapter) GetAdminUser() (string, string, string, bool) {
	u, err := a.s.GetAdminUser()
	if err != nil {
		return "", "", "", false
	}
	return u.ID, u.Username, u.Password, true
}
