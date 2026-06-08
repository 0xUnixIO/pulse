package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"pulse/internal/store/postgres/sqlcgen"
	"pulse/internal/users"
)

type UserStore struct {
	db *pgxpool.Pool
}

// ─── User CRUD ────────────────────────────────────────────────────────────────

func (s *UserStore) UpsertUser(user users.User) (users.User, error) {
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now().UTC()
	}
	if user.Status == "" {
		user.Status = users.StatusActive
	}
	if user.DataLimitResetStrategy == "" {
		user.DataLimitResetStrategy = users.ResetStrategyNoReset
	}
	user.UsedBytes = user.UploadBytes + user.DownloadBytes

	err := sqlcgen.New(s.db).UpsertUser(context.Background(), sqlcgen.UpsertUserParams{
		ID:                     user.ID,
		Username:               user.Username,
		Status:                 string(user.Status),
		Note:                   user.Note,
		ExpireAt:               formatTimePtr(user.ExpireAt),
		DataLimitResetStrategy: string(user.DataLimitResetStrategy),
		TrafficLimitBytes:      user.TrafficLimit,
		UploadBytes:            user.UploadBytes,
		DownloadBytes:          user.DownloadBytes,
		UsedBytes:              user.UsedBytes,
		RawUploadBytes:         user.RawUploadBytes,
		RawDownloadBytes:       user.RawDownloadBytes,
		OnHoldExpireAt:         formatTimePtr(user.OnHoldExpireAt),
		LastTrafficResetAt:     formatTimePtr(user.LastTrafficResetAt),
		OnlineAt:               formatTimePtr(user.OnlineAt),
		Connections:            int64(user.Connections),
		Devices:                int64(user.Devices),
		CreatedAt:              user.CreatedAt.Format(time.RFC3339Nano),
		SubToken:               user.SubToken,
		StripeCustomerID:       user.StripeCustomerID,
		CurrentPlanID:          user.CurrentPlanID,
		Email:                  user.Email,
		Uuid:                   user.UUID,
		Secret:                 user.Secret,
		PlanTrafficLimitBytes:  user.PlanTrafficLimit,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return users.User{}, users.ErrUsernameTaken
		}
		return users.User{}, fmt.Errorf("upsert user: %w", err)
	}
	return user, nil
}

func (s *UserStore) GetUser(id string) (users.User, error) {
	row, err := sqlcgen.New(s.db).GetUserByID(context.Background(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return users.User{}, users.ErrUserNotFound
	}
	if err != nil {
		return users.User{}, fmt.Errorf("get user: %w", err)
	}
	return toUser(row)
}

func (s *UserStore) GetUserBySubToken(token string) (users.User, error) {
	row, err := sqlcgen.New(s.db).GetUserBySubToken(context.Background(), token)
	if errors.Is(err, pgx.ErrNoRows) {
		return users.User{}, users.ErrUserNotFound
	}
	if err != nil {
		return users.User{}, fmt.Errorf("get user by sub token: %w", err)
	}
	return toUser(row)
}

func (s *UserStore) GetUserByStripeCustomerID(customerID string) (users.User, error) {
	row, err := sqlcgen.New(s.db).GetUserByStripeCustomerID(context.Background(), customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return users.User{}, users.ErrUserNotFound
	}
	if err != nil {
		return users.User{}, fmt.Errorf("get user by stripe customer id: %w", err)
	}
	return toUser(row)
}

func (s *UserStore) ListUsers() ([]users.User, error) {
	rows, err := sqlcgen.New(s.db).ListUsers(context.Background())
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return toUsers(rows)
}

func (s *UserStore) DeleteUser(id string) error {
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("delete user begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := sqlcgen.New(tx)
	result, err := q.DeleteUserByID(ctx, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if result.RowsAffected() == 0 {
		return users.ErrUserNotFound
	}
	if err := q.DeleteUserInboundsByUserID(ctx, id); err != nil {
		return fmt.Errorf("delete user inbounds: %w", err)
	}
	if err := q.DeleteSubAccessLogsByUserID(ctx, id); err != nil {
		return fmt.Errorf("delete sub access logs: %w", err)
	}
	if err := q.DeleteUserNodeDailyUsageByUserID(ctx, id); err != nil {
		return fmt.Errorf("delete user node usage: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *UserStore) SetCredentials(userID, uuid, secret string) error {
	err := sqlcgen.New(s.db).SetUserCredentials(context.Background(), sqlcgen.SetUserCredentialsParams{
		ID:     userID,
		Uuid:   uuid,
		Secret: secret,
	})
	if err != nil {
		return fmt.Errorf("set user credentials: %w", err)
	}
	return nil
}

func (s *UserStore) GetUsersByIDs(ids []string) (map[string]users.User, error) {
	if len(ids) == 0 {
		return map[string]users.User{}, nil
	}
	rows, err := sqlcgen.New(s.db).GetUsersByIDs(context.Background(), ids)
	if err != nil {
		return nil, fmt.Errorf("get users by ids: %w", err)
	}
	out := make(map[string]users.User, len(rows))
	for _, row := range rows {
		u, err := toUser(row)
		if err != nil {
			return nil, err
		}
		out[u.ID] = u
	}
	return out, nil
}

// ─── UserInbound CRUD ─────────────────────────────────────────────────────────

func (s *UserStore) UpsertUserInbound(acc users.UserInbound) (users.UserInbound, error) {
	if acc.CreatedAt.IsZero() {
		acc.CreatedAt = time.Now().UTC()
	}
	err := sqlcgen.New(s.db).UpsertUserInbound(context.Background(), sqlcgen.UpsertUserInboundParams{
		ID:        acc.ID,
		UserID:    acc.UserID,
		InboundID: acc.InboundID,
		NodeID:    acc.NodeID,
		Uuid:      acc.UUID,
		Secret:    acc.Secret,
		CreatedAt: acc.CreatedAt.Format(time.RFC3339Nano),
		GroupID:   acc.GroupID,
	})
	if err != nil {
		return users.UserInbound{}, fmt.Errorf("upsert user inbound: %w", err)
	}
	return acc, nil
}

func (s *UserStore) GetUserInbound(id string) (users.UserInbound, error) {
	row, err := sqlcgen.New(s.db).GetUserInboundByID(context.Background(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return users.UserInbound{}, users.ErrUserInboundNotFound
	}
	if err != nil {
		return users.UserInbound{}, fmt.Errorf("get user inbound: %w", err)
	}
	return toUserInbound(row)
}

func (s *UserStore) ListUserInboundsByUser(userID string) ([]users.UserInbound, error) {
	rows, err := sqlcgen.New(s.db).ListUserInboundsByUser(context.Background(), userID)
	if err != nil {
		return nil, fmt.Errorf("list user inbounds by user: %w", err)
	}
	return toUserInbounds(rows)
}

func (s *UserStore) ListActiveUserInboundsByUser(userID string) ([]users.UserInbound, error) {
	rows, err := sqlcgen.New(s.db).ListActiveUserInboundsByUser(context.Background(), userID)
	if err != nil {
		return nil, fmt.Errorf("list active user inbounds by user: %w", err)
	}
	return toUserInbounds(rows)
}

func (s *UserStore) ListUserInboundsByNode(nodeID string) ([]users.UserInbound, error) {
	rows, err := sqlcgen.New(s.db).ListUserInboundsByNode(context.Background(), nodeID)
	if err != nil {
		return nil, fmt.Errorf("list user inbounds by node: %w", err)
	}
	return toUserInbounds(rows)
}

func (s *UserStore) ListUserInboundsByInbound(inboundID string) ([]users.UserInbound, error) {
	rows, err := sqlcgen.New(s.db).ListUserInboundsByInbound(context.Background(), inboundID)
	if err != nil {
		return nil, fmt.Errorf("list user inbounds by inbound: %w", err)
	}
	return toUserInbounds(rows)
}

func (s *UserStore) CountUsersByInbound() (map[string]int, error) {
	rows, err := sqlcgen.New(s.db).CountUsersByInbound(context.Background())
	if err != nil {
		return nil, fmt.Errorf("count users by inbound: %w", err)
	}
	result := make(map[string]int, len(rows))
	for _, r := range rows {
		result[r.InboundID] = int(r.Count)
	}
	return result, nil
}

func (s *UserStore) DeleteUserInbound(id string) error {
	result, err := sqlcgen.New(s.db).DeleteUserInboundByID(context.Background(), id)
	if err != nil {
		return fmt.Errorf("delete user inbound: %w", err)
	}
	if result.RowsAffected() == 0 {
		return users.ErrUserInboundNotFound
	}
	return nil
}

func (s *UserStore) DeleteUserInboundsByUser(userID string) error {
	if err := sqlcgen.New(s.db).DeleteUserInboundsByUserID(context.Background(), userID); err != nil {
		return fmt.Errorf("delete user inbounds by user: %w", err)
	}
	return nil
}

func (s *UserStore) UpdateUserInboundsNode(inboundID, newNodeID string) error {
	err := sqlcgen.New(s.db).UpdateUserInboundsNodeID(context.Background(), sqlcgen.UpdateUserInboundsNodeIDParams{
		NodeID:    newNodeID,
		InboundID: inboundID,
	})
	if err != nil {
		return fmt.Errorf("update user_inbounds node: %w", err)
	}
	return nil
}

// ─── 订阅访问日志 ─────────────────────────────────────────────────────────────

func (s *UserStore) LogSubAccess(userID, ip, userAgent string) error {
	q := sqlcgen.New(s.db)
	ctx := context.Background()
	if err := q.InsertSubAccessLog(ctx, sqlcgen.InsertSubAccessLogParams{
		UserID:     userID,
		Ip:         ip,
		UserAgent:  userAgent,
		AccessedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return fmt.Errorf("log sub access: %w", err)
	}
	// 每次写入后截断，每个用户最多保留 50 条
	_ = q.TrimSubAccessLogsByUser(ctx, sqlcgen.TrimSubAccessLogsByUserParams{
		UserID: userID,
		Limit:  50,
	})
	return nil
}

func (s *UserStore) ListSubAccessLogs(userID string, limit int) ([]users.SubAccessLog, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := sqlcgen.New(s.db).ListSubAccessLogs(context.Background(), sqlcgen.ListSubAccessLogsParams{
		UserID: userID,
		Limit:  int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list sub access logs: %w", err)
	}
	out := make([]users.SubAccessLog, 0, len(rows))
	for _, r := range rows {
		l := users.SubAccessLog{
			ID:        r.ID,
			UserID:    r.UserID,
			IP:        r.Ip,
			UserAgent: r.UserAgent,
		}
		if r.AccessedAt != "" {
			t, err := time.Parse(time.RFC3339, r.AccessedAt)
			if err == nil {
				l.AccessedAt = t
			}
		}
		out = append(out, l)
	}
	return out, nil
}

// ─── 用户节点流量统计 ──────────────────────────────────────────────────────────

func (s *UserStore) AddUserNodeTraffic(userID, nodeID, date string, upload, download int64) error {
	err := sqlcgen.New(s.db).UpsertUserNodeTraffic(context.Background(), sqlcgen.UpsertUserNodeTrafficParams{
		UserID:        userID,
		NodeID:        nodeID,
		Date:          date,
		UploadBytes:   upload,
		DownloadBytes: download,
	})
	if err != nil {
		return fmt.Errorf("add user node traffic: %w", err)
	}
	return nil
}

func (s *UserStore) ListUserDailyUsage(userID string, days int) ([]users.UserDailyUsage, error) {
	if days <= 0 {
		days = 7
	}
	since := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := sqlcgen.New(s.db).ListUserDailyUsage(context.Background(), sqlcgen.ListUserDailyUsageParams{
		UserID: userID,
		Date:   since,
	})
	if err != nil {
		return nil, fmt.Errorf("list user daily usage: %w", err)
	}
	out := make([]users.UserDailyUsage, 0, len(rows))
	for _, r := range rows {
		out = append(out, users.UserDailyUsage{
			Date:          r.Date,
			UploadBytes:   r.UploadBytes,
			DownloadBytes: r.DownloadBytes,
		})
	}
	return out, nil
}

func (s *UserStore) ClearUserNodeDailyUsage(userID string) error {
	return sqlcgen.New(s.db).DeleteUserNodeDailyUsageByUserID(context.Background(), userID)
}

func (s *UserStore) ListUserNodeUsage(userID string) ([]users.UserNodeUsage, error) {
	rows, err := sqlcgen.New(s.db).ListUserNodeUsage(context.Background(), userID)
	if err != nil {
		return nil, fmt.Errorf("list user node usage: %w", err)
	}
	out := make([]users.UserNodeUsage, 0, len(rows))
	for _, r := range rows {
		out = append(out, users.UserNodeUsage{
			NodeID:        r.NodeID,
			UploadBytes:   r.UploadBytes,
			DownloadBytes: r.DownloadBytes,
		})
	}
	return out, nil
}

// ListTodayUserStats 返回今日有流量的用户统计（跨节点合并），按总流量降序。
func (s *UserStore) ListTodayUserStats(limit int) ([]users.TodayUserStat, error) {
	today := time.Now().UTC().Format("2006-01-02")
	rows, err := sqlcgen.New(s.db).ListTodayUserStats(context.Background(), sqlcgen.ListTodayUserStatsParams{
		Date:  today,
		Limit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list today user stats: %w", err)
	}
	out := make([]users.TodayUserStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, users.TodayUserStat{
			Username:      r.Username,
			UploadBytes:   r.UploadBytes,
			DownloadBytes: r.DownloadBytes,
			TotalBytes:    r.UploadBytes + r.DownloadBytes,
		})
	}
	return out, nil
}

// ListTodayNodeUserStats 返回今日指定节点有流量的用户统计，按总流量降序。
func (s *UserStore) ListTodayNodeUserStats(nodeID string, limit int) ([]users.TodayUserStat, error) {
	today := time.Now().UTC().Format("2006-01-02")
	rows, err := sqlcgen.New(s.db).ListTodayNodeUserStats(context.Background(), sqlcgen.ListTodayNodeUserStatsParams{
		Date:   today,
		NodeID: nodeID,
		Limit:  int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list today node user stats: %w", err)
	}
	out := make([]users.TodayUserStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, users.TodayUserStat{
			Username:      r.Username,
			UploadBytes:   r.UploadBytes,
			DownloadBytes: r.DownloadBytes,
			TotalBytes:    r.UploadBytes + r.DownloadBytes,
		})
	}
	return out, nil
}

// ListTodayUserNodeStats 返回今日指定用户在各节点的流量分布。
func (s *UserStore) ListTodayUserNodeStats(username string) ([]users.TodayNodeStat, error) {
	today := time.Now().UTC().Format("2006-01-02")
	rows, err := sqlcgen.New(s.db).ListTodayUserNodeStats(context.Background(), sqlcgen.ListTodayUserNodeStatsParams{
		Date:     today,
		Username: username,
	})
	if err != nil {
		return nil, fmt.Errorf("list today user node stats: %w", err)
	}
	out := make([]users.TodayNodeStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, users.TodayNodeStat{
			NodeID:        r.NodeID,
			NodeName:      r.NodeName,
			UploadBytes:   r.UploadBytes,
			DownloadBytes: r.DownloadBytes,
			TotalBytes:    r.UploadBytes + r.DownloadBytes,
		})
	}
	return out, nil
}

// ─── 类型转换辅助 ─────────────────────────────────────────────────────────────

func toUser(r sqlcgen.User) (users.User, error) {
	u := users.User{
		ID:                     r.ID,
		Username:               r.Username,
		Status:                 r.Status,
		Note:                   r.Note,
		DataLimitResetStrategy: r.DataLimitResetStrategy,
		TrafficLimit:           r.TrafficLimitBytes,
		PlanTrafficLimit:       r.PlanTrafficLimitBytes,
		UploadBytes:            r.UploadBytes,
		DownloadBytes:          r.DownloadBytes,
		UsedBytes:              r.UsedBytes,
		RawUploadBytes:         r.RawUploadBytes,
		RawDownloadBytes:       r.RawDownloadBytes,
		Connections:            int(r.Connections),
		Devices:                int(r.Devices),
		SubToken:               r.SubToken,
		StripeCustomerID:       r.StripeCustomerID,
		CurrentPlanID:          r.CurrentPlanID,
		Email:                  r.Email,
		UUID:                   r.Uuid,
		Secret:                 r.Secret,
	}
	if u.UsedBytes == 0 {
		u.UsedBytes = u.UploadBytes + u.DownloadBytes
	}
	if u.Status == "" {
		u.Status = users.StatusActive
	}
	if u.DataLimitResetStrategy == "" {
		u.DataLimitResetStrategy = users.ResetStrategyNoReset
	}
	if r.CreatedAt != "" {
		t, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
		if err != nil {
			return users.User{}, fmt.Errorf("parse user created_at: %w", err)
		}
		u.CreatedAt = t
	}
	if r.ExpireAt != nil && *r.ExpireAt != "" {
		t, err := time.Parse(time.RFC3339Nano, *r.ExpireAt)
		if err != nil {
			return users.User{}, fmt.Errorf("parse user expire_at: %w", err)
		}
		u.ExpireAt = &t
	}
	if r.OnHoldExpireAt != nil && *r.OnHoldExpireAt != "" {
		t, err := time.Parse(time.RFC3339Nano, *r.OnHoldExpireAt)
		if err != nil {
			return users.User{}, fmt.Errorf("parse user on_hold_expire_at: %w", err)
		}
		u.OnHoldExpireAt = &t
	}
	if r.LastTrafficResetAt != nil && *r.LastTrafficResetAt != "" {
		t, err := time.Parse(time.RFC3339Nano, *r.LastTrafficResetAt)
		if err != nil {
			return users.User{}, fmt.Errorf("parse user last_traffic_reset_at: %w", err)
		}
		u.LastTrafficResetAt = &t
	}
	if r.OnlineAt != nil && *r.OnlineAt != "" {
		t, err := time.Parse(time.RFC3339Nano, *r.OnlineAt)
		if err != nil {
			return users.User{}, fmt.Errorf("parse user online_at: %w", err)
		}
		u.OnlineAt = &t
	}
	return u, nil
}

func toUsers(rows []sqlcgen.User) ([]users.User, error) {
	out := make([]users.User, 0, len(rows))
	for _, r := range rows {
		u, err := toUser(r)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

func toUserInbound(r sqlcgen.UserInbound) (users.UserInbound, error) {
	acc := users.UserInbound{
		ID:        r.ID,
		UserID:    r.UserID,
		InboundID: r.InboundID,
		NodeID:    r.NodeID,
		UUID:      r.Uuid,
		Secret:    r.Secret,
		GroupID:   r.GroupID,
	}
	if r.CreatedAt != "" {
		t, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
		if err != nil {
			return users.UserInbound{}, fmt.Errorf("parse inbound created_at: %w", err)
		}
		acc.CreatedAt = t
	}
	return acc, nil
}

func toUserInbounds(rows []sqlcgen.UserInbound) ([]users.UserInbound, error) {
	out := make([]users.UserInbound, 0, len(rows))
	for _, r := range rows {
		acc, err := toUserInbound(r)
		if err != nil {
			return nil, err
		}
		out = append(out, acc)
	}
	return out, nil
}

func formatTimePtr(value *time.Time) *string {
	if value == nil || value.IsZero() {
		return nil
	}
	s := value.Format(time.RFC3339Nano)
	return &s
}

// ─── Host 排除 ────────────────────────────────────────────────────────────────

func (s *UserStore) ListHostExclusionsByUser(userID string) ([]string, error) {
	rows, err := sqlcgen.New(s.db).ListHostExclusionsByUser(context.Background(), userID)
	if err != nil {
		return nil, fmt.Errorf("list host exclusions: %w", err)
	}
	return rows, nil
}

func (s *UserStore) SetHostExclusion(userID, hostID string) error {
	return sqlcgen.New(s.db).SetHostExclusion(context.Background(), sqlcgen.SetHostExclusionParams{
		UserID: userID,
		HostID: hostID,
	})
}

func (s *UserStore) ClearHostExclusion(userID, hostID string) error {
	return sqlcgen.New(s.db).ClearHostExclusion(context.Background(), sqlcgen.ClearHostExclusionParams{
		UserID: userID,
		HostID: hostID,
	})
}

// ─── 用户组 inbound 相关 ──────────────────────────────────────────────────────

func (s *UserStore) ListDirectUserInboundsByUser(userID string) ([]users.UserInbound, error) {
	rows, err := sqlcgen.New(s.db).ListDirectUserInboundsByUser(context.Background(), userID)
	if err != nil {
		return nil, fmt.Errorf("list direct user inbounds by user: %w", err)
	}
	return toUserInbounds(rows)
}

func (s *UserStore) DeleteGroupUserInbounds(userID, groupID string) error {
	err := sqlcgen.New(s.db).DeleteGroupUserInbounds(context.Background(), sqlcgen.DeleteGroupUserInboundsParams{
		UserID:  userID,
		GroupID: groupID,
	})
	if err != nil {
		return fmt.Errorf("delete group user inbounds: %w", err)
	}
	return nil
}

func (s *UserStore) DeleteAllInboundsForGroup(groupID string) error {
	if err := sqlcgen.New(s.db).DeleteAllInboundsForGroup(context.Background(), groupID); err != nil {
		return fmt.Errorf("delete all inbounds for group: %w", err)
	}
	return nil
}

func (s *UserStore) UpsertGroupUserInbound(acc users.UserInbound) (users.UserInbound, error) {
	if acc.CreatedAt.IsZero() {
		acc.CreatedAt = time.Now().UTC()
	}
	err := sqlcgen.New(s.db).UpsertGroupUserInbound(context.Background(), sqlcgen.UpsertGroupUserInboundParams{
		ID:        acc.ID,
		UserID:    acc.UserID,
		InboundID: acc.InboundID,
		NodeID:    acc.NodeID,
		Uuid:      acc.UUID,
		Secret:    acc.Secret,
		GroupID:   acc.GroupID,
		CreatedAt: acc.CreatedAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return users.UserInbound{}, fmt.Errorf("upsert group user inbound: %w", err)
	}
	return acc, nil
}

func (s *UserStore) ListUserGroupsByUser(userID string) ([]string, error) {
	rows, err := sqlcgen.New(s.db).ListUserGroupsByUser(context.Background(), userID)
	if err != nil {
		return nil, fmt.Errorf("list user groups by user: %w", err)
	}
	return rows, nil
}

func (s *UserStore) SetPassword(userID, hash string) error {
	tag, err := s.db.Exec(context.Background(),
		`UPDATE users SET password = $1 WHERE id = $2`, hash, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return users.ErrUserNotFound
	}
	return nil
}

func (s *UserStore) GetPasswordBySubToken(subToken string) (string, string, error) {
	var userID, hash string
	err := s.db.QueryRow(context.Background(),
		`SELECT id, password FROM users WHERE sub_token = $1`, subToken,
	).Scan(&userID, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", users.ErrUserNotFound
	}
	return userID, hash, err
}

func (s *UserStore) GetPasswordByUsername(username string) (string, string, string, error) {
	var userID, hash, subToken string
	err := s.db.QueryRow(context.Background(),
		`SELECT id, password, sub_token FROM users WHERE username = $1`, username,
	).Scan(&userID, &hash, &subToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", users.ErrUserNotFound
	}
	return userID, hash, subToken, err
}

// GetAdminUser 返回第一个 is_admin=true 的用户。
func (s *UserStore) GetAdminUser() (users.User, error) {
	var id, username, password string
	err := s.db.QueryRow(context.Background(),
		`SELECT id, username, password FROM users WHERE is_admin = TRUE LIMIT 1`,
	).Scan(&id, &username, &password)
	if errors.Is(err, pgx.ErrNoRows) {
		return users.User{}, users.ErrUserNotFound
	}
	if err != nil {
		return users.User{}, err
	}
	return users.User{ID: id, Username: username, Password: password, IsAdmin: true}, nil
}

// SetIsAdmin 设置指定用户的管理员标记。
func (s *UserStore) SetIsAdmin(userID string, isAdmin bool) error {
	tag, err := s.db.Exec(context.Background(),
		`UPDATE users SET is_admin = $1 WHERE id = $2`, isAdmin, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return users.ErrUserNotFound
	}
	return nil
}

// UpdateUsername 只更新用户名，不影响其他字段。
func (s *UserStore) UpdateUsername(userID, username string) error {
	tag, err := s.db.Exec(context.Background(),
		`UPDATE users SET username = $1 WHERE id = $2`, username, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return users.ErrUserNotFound
	}
	return nil
}
