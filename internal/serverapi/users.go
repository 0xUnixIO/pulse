package serverapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"pulse/internal/geoip"
	"pulse/internal/idgen"
	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/outbounds"
	"pulse/internal/subscription"
	"pulse/internal/users"
)

type userAPI struct {
	users         users.Store
	nodes         nodes.Store
	inboundStore  inbounds.InboundStore
	outboundStore outbounds.Store
	base          *API
	applyOpts     jobs.ApplyOptions
	geoDB         *geoip.DB          // 可为 nil，nil 时跳过地理位置查询
	sessions      PortalSessionStore // 可为 nil，nil 时跳过 session 失效
}

type createUserRequest struct {
	ID                     string     `json:"id"`
	Username               string     `json:"username"`
	TrafficLimit           int64      `json:"traffic_limit_bytes"`
	ExpireAt               *time.Time `json:"expire_at,omitempty"`
	DataLimitResetStrategy string     `json:"data_limit_reset_strategy"`
	Note                   string     `json:"note,omitempty"`
	InboundIDs             []string   `json:"inbound_ids,omitempty"`
}

type updateUserRequest struct {
	Status                 string     `json:"status"`
	ExpireAt               *time.Time `json:"expire_at,omitempty"`
	DataLimitResetStrategy string     `json:"data_limit_reset_strategy"`
	TrafficLimit           int64      `json:"traffic_limit_bytes"`
	Note                   *string    `json:"note,omitempty"`
	OnHoldExpireAt         *time.Time `json:"on_hold_expire_at,omitempty"`
	ClearOnHoldExpireAt    bool       `json:"clear_on_hold_expire_at,omitempty"`
	LastTrafficResetAt     *time.Time `json:"last_traffic_reset_at,omitempty"`
	ClearLastTrafficReset  bool       `json:"clear_last_traffic_reset_at,omitempty"`
	InboundIDs             *[]string  `json:"inbound_ids,omitempty"`
	// Password 非 nil 时更新门户密码：空字符串清除密码，非空字符串设置新密码。
	Password *string `json:"password,omitempty"`
}

// createAccessRequest 添加用户到 inbound 的请求（只需指定 inbound ID）。
type createAccessRequest struct {
	ID        string `json:"id"`
	InboundID string `json:"inbound_id"`
	UUID      string `json:"uuid,omitempty"`   // 可留空自动生成
	Secret    string `json:"secret,omitempty"` // 可留空自动生成
}

func newUserAPI(usersStore users.Store, nodesStore nodes.Store, ibStore inbounds.InboundStore, outboundStore outbounds.Store, base *API, applyOpts jobs.ApplyOptions, geoDB *geoip.DB) *userAPI {
	return &userAPI{
		users:         usersStore,
		nodes:         nodesStore,
		inboundStore:  ibStore,
		outboundStore: outboundStore,
		base:          base,
		applyOpts:     applyOpts,
		geoDB:         geoDB,
	}
}

func (a *userAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/users", a.handleUsers)
	mux.HandleFunc("/v1/users/", a.handleUserRoutes)
}

func (a *userAPI) handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		all, err := a.users.ListUsers()
		if err != nil {
			internalError(w, r, err)
			return
		}
		items, total := filterUsersByQuery(all, r)
		writeJSON(w, http.StatusOK, map[string]any{"users": items, "total": total})
	case http.MethodPost:
		var req createUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		req.Username = strings.TrimSpace(req.Username)
		req.Note = strings.TrimSpace(req.Note)
		if req.Username == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "username is required"})
			return
		}
		if strings.TrimSpace(req.ID) == "" {
			req.ID = idgen.NextString()
		}
		if req.DataLimitResetStrategy == "" {
			req.DataLimitResetStrategy = users.ResetStrategyNoReset
		}
		user, err := a.users.UpsertUser(users.User{
			ID:                     req.ID,
			Username:               req.Username,
			Status:                 users.StatusActive,
			Note:                   req.Note,
			ExpireAt:               req.ExpireAt,
			DataLimitResetStrategy: req.DataLimitResetStrategy,
			TrafficLimit:           req.TrafficLimit,
			SubToken:               randomToken(16),
			UUID:                   randomUUID(),
			Secret:                 randomToken(16),
		})
		if err != nil {
			if errors.Is(err, users.ErrUsernameTaken) {
				writeJSON(w, http.StatusConflict, map[string]any{"error": "username already exists"})
				return
			}
			internalError(w, r, err)
			return
		}
		// Auto-associate with selected inbounds
		if len(req.InboundIDs) > 0 {
			affected, syncErr := a.syncUserInbounds(user.ID, req.InboundIDs)
			if syncErr != nil {
				// 回滚：删除刚创建的用户，保证原子性
				_ = a.users.DeleteUser(user.ID)
				internalError(w, r, syncErr)
				return
			}
			a.applyNodes(affected)
		}
		writeJSON(w, http.StatusOK, user)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (a *userAPI) handleUserRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/users/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "user id is required"})
		return
	}

	userID := parts[0]
	switch len(parts) {
	case 1:
		a.handleUser(w, r, userID)
	case 2:
		switch parts[1] {
		case "inbounds":
			a.handleUserInbounds(w, r, userID)
		case "reset-traffic":
			a.handleResetTraffic(w, r, userID)
		case "sub-logs":
			a.handleSubLogs(w, r, userID)
		case "node-usage":
			a.handleNodeUsage(w, r, userID)
		case "credentials":
			a.handleUserCredentials(w, r, userID)
		case "regenerate-sub-token":
			a.handleRegenerateSubToken(w, r, userID)
		default:
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
		}
	case 3:
		if parts[1] == "inbounds" {
			a.handleUserInbound(w, r, userID, parts[2])
		} else {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
		}
	case 4:
		if parts[1] == "inbounds" {
			ibID := parts[2]
			switch parts[3] {
			case "apply":
				a.handleAccessApply(w, r, userID, ibID)
			case "subscription":
				a.handleAccessSubscription(w, r, userID, ibID)
			default:
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
			}
		} else {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
		}
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
	}
}

func (a *userAPI) handleUser(w http.ResponseWriter, r *http.Request, userID string) {
	switch r.Method {
	case http.MethodGet:
		user, err := a.users.GetUser(userID)
		if err != nil {
			writeUserError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, user)
	case http.MethodPut:
		a.handleUpdateUser(w, r, userID)
	case http.MethodDelete:
		// 删除前先收集该用户所在的节点，删除后重新下发配置以踢出用户
		accesses, _ := a.users.ListUserInboundsByUser(userID)
		affectedNodeIDs := make(map[string]struct{})
		for _, acc := range accesses {
			affectedNodeIDs[acc.NodeID] = struct{}{}
		}
		if err := a.users.DeleteUser(userID); err != nil {
			writeUserError(w, err)
			return
		}
		nodeIDs := make([]string, 0, len(affectedNodeIDs))
		for id := range affectedNodeIDs {
			nodeIDs = append(nodeIDs, id)
		}
		a.applyNodes(nodeIDs)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

func (a *userAPI) handleUpdateUser(w http.ResponseWriter, r *http.Request, userID string) {
	user, err := a.users.GetUser(userID)
	if err != nil {
		writeUserError(w, err)
		return
	}

	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}

	if req.Status != "" {
		if !validStatus(req.Status) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid status"})
			return
		}
		user.Status = req.Status
	}
	if req.ExpireAt != nil {
		user.ExpireAt = req.ExpireAt
	}
	if req.DataLimitResetStrategy != "" {
		if !validResetStrategy(req.DataLimitResetStrategy) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid data_limit_reset_strategy"})
			return
		}
		user.DataLimitResetStrategy = req.DataLimitResetStrategy
	}
	// 在字段赋值前记录变更，避免赋值后比较恒为 false
	proxyFieldChanged := req.Status != "" ||
		req.ExpireAt != nil ||
		(req.TrafficLimit >= 0 && req.TrafficLimit != user.TrafficLimit) ||
		req.DataLimitResetStrategy != ""

	if req.TrafficLimit >= 0 && req.TrafficLimit != user.TrafficLimit {
		user.TrafficLimit = req.TrafficLimit
	}
	if req.Note != nil {
		user.Note = strings.TrimSpace(*req.Note)
	}
	if req.OnHoldExpireAt != nil {
		user.OnHoldExpireAt = req.OnHoldExpireAt
	}
	if req.ClearOnHoldExpireAt {
		user.OnHoldExpireAt = nil
	}
	if req.LastTrafficResetAt != nil {
		user.LastTrafficResetAt = req.LastTrafficResetAt
	}
	if req.ClearLastTrafficReset {
		user.LastTrafficResetAt = nil
	}

	updated, err := a.users.UpsertUser(user)
	if err != nil {
		internalError(w, r, err)
		return
	}

	// 门户密码更新（UpsertUser 成功后再改密码，避免主更新失败但密码已改）
	if req.Password != nil {
		var hash string
		if *req.Password != "" {
			if len(*req.Password) > 72 {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "password too long"})
				return
			}
			h, hErr := bcrypt.GenerateFromPassword([]byte(*req.Password), 12)
			if hErr != nil {
				internalError(w, r, hErr)
				return
			}
			hash = string(h)
		}
		if err := a.users.SetPassword(userID, hash); err != nil {
			internalError(w, r, err)
			return
		}
		// 密码变更（包括清除）时使所有门户 session 失效
		if a.sessions != nil {
			_ = a.sessions.DeleteByUserID(userID)
		}
	}
	// Sync inbound associations if provided
	if req.InboundIDs != nil {
		affected, syncErr := a.syncUserInbounds(userID, *req.InboundIDs)
		if syncErr != nil {
			internalError(w, r, syncErr)
			return
		}
		a.applyNodes(affected)
	} else if proxyFieldChanged {
		// 仅改了状态/流量/到期时间，需推送到已关联的节点
		if err := a.triggerUserApply(userID); err != nil {
			log.Printf("handleUpdateUser: trigger apply %s: %v", userID, err)
		}
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleUserInbounds 处理用户的节点访问凭据列表（GET / POST）。
func (a *userAPI) handleUserInbounds(w http.ResponseWriter, r *http.Request, userID string) {
	switch r.Method {
	case http.MethodGet:
		accesses, err := a.users.ListUserInboundsByUser(userID)
		if err != nil {
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"inbounds": accesses, "total": len(accesses)})
	case http.MethodPost:
		if _, err := a.users.GetUser(userID); err != nil {
			writeUserError(w, err)
			return
		}
		var req createAccessRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.InboundID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "inbound_id is required"})
			return
		}
		ib, err := a.inboundStore.GetInbound(req.InboundID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "inbound not found"})
			return
		}
		if strings.TrimSpace(req.ID) == "" {
			req.ID = idgen.NextString()
		}
		if req.UUID == "" {
			req.UUID = randomUUID()
		}
		if req.Secret == "" {
			req.Secret = randomToken(12)
		}
		acc, err := a.users.UpsertUserInbound(users.UserInbound{
			ID:        req.ID,
			UserID:    userID,
			InboundID: req.InboundID,
			NodeID:    ib.NodeID,
			UUID:      req.UUID,
			Secret:    req.Secret,
		})
		if err != nil {
			internalError(w, r, err)
			return
		}
		a.applyNodes([]string{ib.NodeID})
		writeJSON(w, http.StatusOK, acc)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (a *userAPI) handleUserInbound(w http.ResponseWriter, r *http.Request, userID, ibID string) {
	switch r.Method {
	case http.MethodGet:
		acc, err := a.users.GetUserInbound(ibID)
		if err != nil {
			writeUserInboundError(w, err)
			return
		}
		if acc.UserID != userID {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "user inbound not found"})
			return
		}
		writeJSON(w, http.StatusOK, acc)
	case http.MethodDelete:
		acc, err := a.users.GetUserInbound(ibID)
		if err != nil {
			writeUserInboundError(w, err)
			return
		}
		if acc.UserID != userID {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "user inbound not found"})
			return
		}
		if err := a.users.DeleteUserInbound(ibID); err != nil {
			writeUserInboundError(w, err)
			return
		}
		a.applyNodes([]string{acc.NodeID})
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodDelete)
	}
}

// handleUserCredentials 管理用户全局凭证（UUID / Secret）。
// GET 返回当前凭证；PUT 更新凭证（留空则重新随机生成），随后触发全节点配置下发。
// handleRegenerateSubToken 重新生成用户的 sub_token，使老的订阅链接失效。
// 仅接受 POST，token 由服务端用 crypto/rand 生成。
func (a *userAPI) handleRegenerateSubToken(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	user, err := a.users.GetUser(userID)
	if err != nil {
		writeUserError(w, err)
		return
	}
	user.SubToken = randomToken(16)
	updated, err := a.users.UpsertUser(user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sub_token": updated.SubToken})
}

func (a *userAPI) handleUserCredentials(w http.ResponseWriter, r *http.Request, userID string) {
	user, err := a.users.GetUser(userID)
	if err != nil {
		writeUserError(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"uuid":   user.UUID,
			"secret": user.Secret,
		})
	case http.MethodPut:
		var req struct {
			UUID   string `json:"uuid"`
			Secret string `json:"secret"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		if req.UUID == "" {
			req.UUID = randomUUID()
		} else if !isValidUUID(req.UUID) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid uuid format"})
			return
		}
		if req.Secret == "" {
			req.Secret = randomToken(16)
		}
		if err := a.users.SetCredentials(userID, req.UUID, req.Secret); err != nil {
			internalError(w, r, err)
			return
		}
		// 重新下发所有相关节点配置
		if err := a.triggerUserApply(userID); err != nil {
			log.Printf("warn: re-apply after credentials change user %s: %v", userID, err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"uuid": req.UUID, "secret": req.Secret})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
	}
}

// handleAccessApply 将该节点的完整配置重新下发。
func (a *userAPI) handleAccessApply(w http.ResponseWriter, r *http.Request, userID, ibID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	acc, err := a.users.GetUserInbound(ibID)
	if err != nil {
		writeUserInboundError(w, err)
		return
	}
	if acc.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "user inbound not found"})
		return
	}

	nodeInbounds, err := a.inboundStore.ListInboundsByNode(acc.NodeID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	allAccesses, err := a.users.ListUserInboundsByNode(acc.NodeID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	userIDs := collectUserIDs(allAccesses)
	userMap, err := a.users.GetUsersByIDs(userIDs)
	if err != nil {
		internalError(w, r, err)
		return
	}

	client, err := a.base.clientFor(acc.NodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	accNode, _ := a.base.store.Get(acc.NodeID)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	status, config, err := jobs.ApplyNodeUsers(ctx, client, nodeInbounds, allAccesses, userMap, a.inboundStore, a.outboundStore, a.applyOpts, accNode)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access":       acc,
		"users_count":  len(allAccesses),
		"active_users": len(filterEnabledAccesses(allAccesses, userMap)),
		"node_status":  status,
		"node_config":  json.RawMessage(config),
	})
}

// handleAccessSubscription 返回该节点访问凭据对应的所有订阅链接。
func (a *userAPI) handleAccessSubscription(w http.ResponseWriter, r *http.Request, userID, ibID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	acc, err := a.users.GetUserInbound(ibID)
	if err != nil {
		writeUserInboundError(w, err)
		return
	}
	if acc.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "user inbound not found"})
		return
	}
	user, err := a.users.GetUser(userID)
	if err != nil {
		writeUserError(w, err)
		return
	}

	// 按 InboundID 获取对应的 inbound 列表；旧版记录（InboundID 为空）回退到节点级别
	var ibList []inbounds.Inbound
	if acc.InboundID != "" {
		ib, err := a.inboundStore.GetInbound(acc.InboundID)
		if err != nil {
			internalError(w, r, err)
			return
		}
		ibList = []inbounds.Inbound{ib}
	} else {
		ibList, err = a.inboundStore.ListInboundsByNode(acc.NodeID)
		if err != nil {
			internalError(w, r, err)
			return
		}
	}

	type linkItem struct {
		Protocol string `json:"protocol"`
		Remark   string `json:"remark"`
		Link     string `json:"link"`
	}

	links := make([]linkItem, 0)
	for _, ib := range ibList {
		hosts, err := a.inboundStore.ListHostsByInbound(ib.ID)
		if err != nil {
			continue
		}
		for _, h := range hosts {
			link := subscription.Link(ib, h, acc, user)
			remark := h.Remark
			if remark == "" {
				remark = h.Address
			}
			links = append(links, linkItem{
				Protocol: ib.Protocol,
				Remark:   remark,
				Link:     link,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"links": links})
}

func writeUserError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, users.ErrUserNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func writeUserInboundError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, users.ErrUserInboundNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isValidUUID(s string) bool { return uuidRe.MatchString(s) }

func randomUUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("pulse-%d", time.Now().UnixNano())
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

func randomToken(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("pulse-secret-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", buf)
}

func supportedProtocol(value string) bool {
	switch value {
	case "vless", "trojan", "shadowsocks", "anytls", "hy2":
		return true
	default:
		return false
	}
}

func validStatus(value string) bool {
	switch value {
	case users.StatusActive, users.StatusDisabled, users.StatusOnHold:
		return true
	default:
		return false
	}
}

func validResetStrategy(value string) bool {
	switch value {
	case users.ResetStrategyNoReset, users.ResetStrategyDay, users.ResetStrategyWeek,
		users.ResetStrategyMonth, users.ResetStrategyYear:
		return true
	default:
		return false
	}
}

// filterUsersByQuery 根据 URL 查询参数过滤用户列表。
func filterUsersByQuery(items []users.User, r *http.Request) ([]users.User, int) {
	q := r.URL.Query()
	search := strings.ToLower(strings.TrimSpace(q.Get("search")))
	statusFilter := strings.ToLower(strings.TrimSpace(q.Get("status")))

	out := make([]users.User, 0, len(items))
	for _, u := range items {
		if statusFilter != "" && u.EffectiveStatus() != statusFilter {
			continue
		}
		if search != "" {
			if !strings.Contains(strings.ToLower(u.Username), search) {
				continue
			}
		}
		out = append(out, u)
	}
	return out, len(out)
}

func filterEnabledAccesses(accesses []users.UserInbound, userMap map[string]users.User) []users.UserInbound {
	out := make([]users.UserInbound, 0, len(accesses))
	for _, acc := range accesses {
		u, ok := userMap[acc.UserID]
		if ok && u.EffectiveEnabled() {
			out = append(out, acc)
		}
	}
	return out
}

// collectUserIDs 从 accesses 中提取去重后的 UserID（serverapi 内部使用）。
func collectUserIDs(accesses []users.UserInbound) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, acc := range accesses {
		if _, ok := seen[acc.UserID]; !ok {
			seen[acc.UserID] = struct{}{}
			out = append(out, acc.UserID)
		}
	}
	return out
}

// syncUserInbounds reconciles a user's inbound assignments. Returns affected node IDs.
func (a *userAPI) syncUserInbounds(userID string, selectedIDs []string) ([]string, error) {
	wantedInbounds := make(map[string]inbounds.Inbound)
	for _, ibID := range selectedIDs {
		ib, err := a.inboundStore.GetInbound(ibID)
		if err != nil {
			continue
		}
		wantedInbounds[ibID] = ib
	}

	existing, err := a.users.ListUserInboundsByUser(userID)
	if err != nil {
		return nil, err
	}
	existingByInbound := make(map[string]users.UserInbound, len(existing))
	for _, acc := range existing {
		existingByInbound[acc.InboundID] = acc
	}

	changedNodeIDs := make(map[string]struct{})

	for ibID, ib := range wantedInbounds {
		if _, ok := existingByInbound[ibID]; !ok {
			secret := randomToken(12)
			if ib.Protocol == "shadowsocks" && strings.HasPrefix(ib.Method, "2022-") {
				secret = ssPassword(ib.Method)
			}
			acc := users.UserInbound{
				ID:        idgen.NextString(),
				UserID:    userID,
				InboundID: ibID,
				NodeID:    ib.NodeID,
				UUID:      randomUUID(),
				Secret:    secret,
			}
			if _, err := a.users.UpsertUserInbound(acc); err != nil {
				return nil, err
			}
			changedNodeIDs[ib.NodeID] = struct{}{}
		}
	}

	for ibID, acc := range existingByInbound {
		if _, wanted := wantedInbounds[ibID]; !wanted {
			if err := a.users.DeleteUserInbound(acc.ID); err != nil {
				return nil, err
			}
			changedNodeIDs[acc.NodeID] = struct{}{}
		}
	}

	affected := make([]string, 0, len(changedNodeIDs))
	for id := range changedNodeIDs {
		affected = append(affected, id)
	}
	return affected, nil
}

// triggerUserApply 找出用户关联的所有节点并异步重新下发配置，用于凭证变更后生效。
// 实际下发是异步的（applyNodes 内部 go func + context.Background），调用方无法通过 ctx 控制；
// 这里不接收 ctx 以避免误导。
func (a *userAPI) triggerUserApply(userID string) error {
	inbounds, err := a.users.ListUserInboundsByUser(userID)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{})
	var nodeIDs []string
	for _, ib := range inbounds {
		if _, dup := seen[ib.NodeID]; !dup {
			seen[ib.NodeID] = struct{}{}
			nodeIDs = append(nodeIDs, ib.NodeID)
		}
	}
	a.applyNodes(nodeIDs)
	return nil
}

// applyNodes pushes config to affected nodes after inbound association changes.
func (a *userAPI) applyNodes(nodeIDs []string) {
	for _, nodeID := range nodeIDs {
		go func(id string) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := jobs.ApplyNode(ctx, id, a.nodes, a.users, a.inboundStore, a.outboundStore, a.base.Dial, a.applyOpts); err != nil {
				log.Printf("applyNodes: %s: %v", id, err)
			}
		}(nodeID)
	}
}

func ssPassword(method string) string {
	size := 32
	if method == "2022-blake3-aes-128-gcm" {
		size = 16
	}
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

// handleResetTraffic 重置用户流量。
func (a *userAPI) handleResetTraffic(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	user, err := a.users.GetUser(userID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "user not found"})
		return
	}
	now := time.Now().UTC()
	user.UploadBytes = 0
	user.DownloadBytes = 0
	user.UsedBytes = 0
	user.RawUploadBytes = 0
	user.RawDownloadBytes = 0
	user.LastTrafficResetAt = &now
	if _, err := a.users.UpsertUser(user); err != nil {
		internalError(w, r, fmt.Errorf("failed to reset traffic: %w", err))
		return
	}
	if err := a.users.ClearUserNodeDailyUsage(userID); err != nil {
		internalError(w, r, fmt.Errorf("failed to clear node daily usage: %w", err))
		return
	}
	accesses, err := a.users.ListUserInboundsByUser(userID)
	if err != nil {
		log.Printf("handleResetTraffic: list user inbounds %s: %v", userID, err)
	}
	affectedNodeIDs := make(map[string]struct{})
	for _, acc := range accesses {
		affectedNodeIDs[acc.NodeID] = struct{}{}
	}
	nodeIDs := make([]string, 0, len(affectedNodeIDs))
	for id := range affectedNodeIDs {
		nodeIDs = append(nodeIDs, id)
	}
	a.applyNodes(nodeIDs)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSubLogs 返回用户的订阅访问日志。
func (a *userAPI) handleSubLogs(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	logs, err := a.users.ListSubAccessLogs(userID, limit)
	if err != nil {
		internalError(w, r, fmt.Errorf("failed to list sub logs: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs})
}

func (a *userAPI) handleNodeUsage(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	rawUsage, err := a.users.ListUserNodeUsage(userID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	nodeList, _ := a.nodes.List()
	nodeNames := make(map[string]string, len(nodeList))
	for _, n := range nodeList {
		nodeNames[n.ID] = n.Name
	}
	type nodeUsageItem struct {
		NodeID        string `json:"node_id"`
		NodeName      string `json:"node_name"`
		UploadBytes   int64  `json:"upload_bytes"`
		DownloadBytes int64  `json:"download_bytes"`
		TotalBytes    int64  `json:"total_bytes"`
	}
	items := make([]nodeUsageItem, 0, len(rawUsage))
	for _, u := range rawUsage {
		t := u.UploadBytes + u.DownloadBytes
		if t == 0 {
			continue
		}
		name := nodeNames[u.NodeID]
		if name == "" {
			name = u.NodeID
		}
		items = append(items, nodeUsageItem{
			NodeID:        u.NodeID,
			NodeName:      name,
			UploadBytes:   u.UploadBytes,
			DownloadBytes: u.DownloadBytes,
			TotalBytes:    t,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": items})
}
