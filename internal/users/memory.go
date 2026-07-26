package users

import (
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu       sync.RWMutex
	users    map[string]User
	inbounds map[string]UserInbound
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		users:    make(map[string]User),
		inbounds: make(map[string]UserInbound),
	}
}

// ─── User CRUD ────────────────────────────────────────────────────────────────

func (s *MemoryStore) UpsertUser(user User) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now().UTC()
	}
	if user.Status == "" {
		user.Status = StatusActive
	}
	if user.DataLimitResetStrategy == "" {
		user.DataLimitResetStrategy = ResetStrategyNoReset
	}
	user.UsedBytes = user.UploadBytes + user.DownloadBytes
	s.users[user.ID] = user
	return user, nil
}

func (s *MemoryStore) ResetTrafficForValidity(userID string, now time.Time, validityCostDays int) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return User{}, ErrUserNotFound
	}
	if user.Status != StatusActive && user.Status != StatusLimited {
		return User{}, ErrTrafficResetNotAllowed
	}
	if user.UploadBytes+user.DownloadBytes <= 0 || user.ExpireAt == nil ||
		!user.ExpireAt.After(now.AddDate(0, 0, validityCostDays)) {
		return User{}, ErrTrafficResetNotAllowed
	}
	expireAt := user.ExpireAt.AddDate(0, 0, -validityCostDays)
	user.UploadBytes = 0
	user.DownloadBytes = 0
	user.UsedBytes = 0
	user.RawUploadBytes = 0
	user.RawDownloadBytes = 0
	user.LastTrafficResetAt = &now
	user.ExpireAt = &expireAt
	s.users[userID] = user
	return user, nil
}

func (s *MemoryStore) GetUser(id string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[id]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return user, nil
}

func (s *MemoryStore) GetUserBySubToken(token string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.SubToken == token {
			return u, nil
		}
	}
	return User{}, ErrUserNotFound
}

func (s *MemoryStore) GetUserByStripeCustomerID(customerID string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.StripeCustomerID == customerID {
			return u, nil
		}
	}
	return User{}, ErrUserNotFound
}

func (s *MemoryStore) ListUsers() ([]User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortUsers(usersMapToSlice(s.users)), nil
}

func (s *MemoryStore) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[id]; !ok {
		return ErrUserNotFound
	}
	delete(s.users, id)
	return nil
}

func (s *MemoryStore) SetCredentials(userID, uuid, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return ErrUserNotFound
	}
	u.UUID = uuid
	u.Secret = secret
	s.users[userID] = u
	return nil
}

func (s *MemoryStore) SetPassword(userID, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return ErrUserNotFound
	}
	u.Password = hash
	s.users[userID] = u
	return nil
}

func (s *MemoryStore) GetPasswordBySubToken(subToken string) (string, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.SubToken == subToken {
			return u.ID, u.Password, nil
		}
	}
	return "", "", ErrUserNotFound
}

func (s *MemoryStore) GetPasswordByUsername(username string) (string, string, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.Username == username {
			return u.ID, u.Password, u.SubToken, nil
		}
	}
	return "", "", "", ErrUserNotFound
}

func (s *MemoryStore) GetAdminUser() (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.IsAdmin {
			return u, nil
		}
	}
	return User{}, ErrUserNotFound
}

func (s *MemoryStore) SetIsAdmin(userID string, isAdmin bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return ErrUserNotFound
	}
	u.IsAdmin = isAdmin
	s.users[userID] = u
	return nil
}

func (s *MemoryStore) GetUsersByIDs(ids []string) (map[string]User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]User, len(ids))
	for _, id := range ids {
		if u, ok := s.users[id]; ok {
			out[id] = u
		}
	}
	return out, nil
}

// ─── UserInbound CRUD ─────────────────────────────────────────────────────────

func (s *MemoryStore) UpsertUserInbound(acc UserInbound) (UserInbound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if acc.CreatedAt.IsZero() {
		acc.CreatedAt = time.Now().UTC()
	}
	s.inbounds[acc.ID] = acc
	return acc, nil
}

func (s *MemoryStore) GetUserInbound(id string) (UserInbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	acc, ok := s.inbounds[id]
	if !ok {
		return UserInbound{}, ErrUserInboundNotFound
	}
	return acc, nil
}

func (s *MemoryStore) ListUserInboundsByUser(userID string) ([]UserInbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UserInbound, 0)
	for _, acc := range s.inbounds {
		if acc.UserID == userID {
			out = append(out, acc)
		}
	}
	return sortInbounds(out), nil
}

// ListActiveUserInboundsByUser 内存 store 无节点禁用状态，直接返回全部。
func (s *MemoryStore) ListActiveUserInboundsByUser(userID string) ([]UserInbound, error) {
	return s.ListUserInboundsByUser(userID)
}

func (s *MemoryStore) ListUserInboundsByNode(nodeID string) ([]UserInbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UserInbound, 0)
	for _, acc := range s.inbounds {
		if acc.NodeID == nodeID {
			out = append(out, acc)
		}
	}
	return sortInbounds(out), nil
}

func (s *MemoryStore) ListUserInboundsByInbound(inboundID string) ([]UserInbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UserInbound, 0)
	for _, acc := range s.inbounds {
		if acc.InboundID == inboundID {
			out = append(out, acc)
		}
	}
	return sortInbounds(out), nil
}

func (s *MemoryStore) CountUsersByInbound() (map[string]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]int)
	for _, ib := range s.inbounds {
		result[ib.InboundID]++
	}
	return result, nil
}

func (s *MemoryStore) DeleteUserInbound(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.inbounds[id]; !ok {
		return ErrUserInboundNotFound
	}
	delete(s.inbounds, id)
	return nil
}

func (s *MemoryStore) DeleteUserInboundsByUser(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, acc := range s.inbounds {
		if acc.UserID == userID {
			delete(s.inbounds, id)
		}
	}
	return nil
}

func (s *MemoryStore) UpdateUserInboundsNode(inboundID, newNodeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, acc := range s.inbounds {
		if acc.InboundID == inboundID {
			acc.NodeID = newNodeID
			s.inbounds[id] = acc
		}
	}
	return nil
}

// ─── 订阅访问日志（内存 stub，仅测试用） ──────────────────────────────────────

func (s *MemoryStore) LogSubAccess(userID, ip, userAgent string) error {
	return nil
}

func (s *MemoryStore) ListSubAccessLogs(userID string, limit int) ([]SubAccessLog, error) {
	return nil, nil
}

func (s *MemoryStore) AddUserNodeTraffic(userID, nodeID, date string, upload, download int64) error {
	return nil
}

func (s *MemoryStore) ClearUserNodeDailyUsage(userID string) error {
	return nil
}

func (s *MemoryStore) ListUserNodeUsage(userID string) ([]UserNodeUsage, error) {
	return nil, nil
}

func (s *MemoryStore) ListUserDailyUsage(userID string, days int) ([]UserDailyUsage, error) {
	return nil, nil
}

func (s *MemoryStore) ListTodayUserStats(limit int) ([]TodayUserStat, error) {
	return nil, nil
}

func (s *MemoryStore) ListTodayNodeUserStats(nodeID string, limit int) ([]TodayUserStat, error) {
	return nil, nil
}

func (s *MemoryStore) ListTodayUserNodeStats(username string) ([]TodayNodeStat, error) {
	return nil, nil
}

func (s *MemoryStore) ListHostExclusionsByUser(userID string) ([]string, error) { return nil, nil }
func (s *MemoryStore) SetHostExclusion(userID, hostID string) error             { return nil }
func (s *MemoryStore) ClearHostExclusion(userID, hostID string) error           { return nil }

// ─── 用户组 inbound 相关（内存 stub，仅测试用） ───────────────────────────────

func (s *MemoryStore) ListDirectUserInboundsByUser(userID string) ([]UserInbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UserInbound, 0)
	for _, acc := range s.inbounds {
		if acc.UserID == userID && acc.GroupID == "" {
			out = append(out, acc)
		}
	}
	return sortInbounds(out), nil
}

func (s *MemoryStore) DeleteGroupUserInbounds(userID, groupID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, acc := range s.inbounds {
		if acc.UserID == userID && acc.GroupID == groupID {
			delete(s.inbounds, id)
		}
	}
	return nil
}

func (s *MemoryStore) DeleteAllInboundsForGroup(groupID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, acc := range s.inbounds {
		if acc.GroupID == groupID {
			delete(s.inbounds, id)
		}
	}
	return nil
}

func (s *MemoryStore) UpsertGroupUserInbound(acc UserInbound) (UserInbound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if acc.CreatedAt.IsZero() {
		acc.CreatedAt = time.Now().UTC()
	}
	s.inbounds[acc.ID] = acc
	return acc, nil
}

func (s *MemoryStore) ListUserGroupsByUser(userID string) ([]string, error) {
	return []string{}, nil
}

func (s *MemoryStore) UpdateUsername(userID, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return ErrUserNotFound
	}
	u.Username = username
	s.users[userID] = u
	return nil
}

// ─── 辅助函数 ─────────────────────────────────────────────────────────────────

func usersMapToSlice(items map[string]User) []User {
	out := make([]User, 0, len(items))
	for _, user := range items {
		out = append(out, user)
	}
	return out
}

func sortUsers(out []User) []User {
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func sortInbounds(out []UserInbound) []UserInbound {
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}
