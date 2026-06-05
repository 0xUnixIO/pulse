package serverapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"pulse/internal/idgen"
	"pulse/internal/inbounds"
	"pulse/internal/usergroup"
	"pulse/internal/users"
)

type userGroupAPI struct {
	ugStore    usergroup.Store
	userStore  users.Store
	ibStore    inbounds.InboundStore
	applyNodes func(nodeIDs []string)
}

// RegisterUserGroupAPI 注册用户组管理相关 API 路由。
func RegisterUserGroupAPI(
	mux *http.ServeMux,
	ugStore usergroup.Store,
	userStore users.Store,
	ibStore inbounds.InboundStore,
	applyNodes func(nodeIDs []string),
) {
	a := &userGroupAPI{
		ugStore:    ugStore,
		userStore:  userStore,
		ibStore:    ibStore,
		applyNodes: applyNodes,
	}
	mux.HandleFunc("/v1/user-groups", a.handleUserGroups)
	mux.HandleFunc("/v1/user-groups/", a.handleUserGroupRoutes)
}

// handleUserGroups 处理 /v1/user-groups 的 GET 和 POST 请求。
func (a *userGroupAPI) handleUserGroups(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		groups, err := a.ugStore.ListUserGroups()
		if err != nil {
			internalError(w, r, err)
			return
		}
		if groups == nil {
			groups = []usergroup.UserGroup{}
		}
		type groupWithCount struct {
			usergroup.UserGroup
			MemberCount int `json:"member_count"`
		}
		result := make([]groupWithCount, len(groups))
		for i, g := range groups {
			members, _ := a.ugStore.ListGroupMembers(g.ID)
			result[i] = groupWithCount{UserGroup: g, MemberCount: len(members)}
		}
		writeJSON(w, http.StatusOK, map[string]any{"user_groups": result})

	case http.MethodPost:
		var req usergroup.UserGroup
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
			return
		}
		if req.ID == "" {
			req.ID = idgen.NextString()
		}
		g, err := a.ugStore.UpsertUserGroup(req)
		if err != nil {
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, g)

	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

// handleUserGroupRoutes 处理 /v1/user-groups/ 下的子路由。
func (a *userGroupAPI) handleUserGroupRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/user-groups/")
	if path == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "group id is required"})
		return
	}

	// 解析路径：{id} 或 {id}/members 或 {id}/members/{uid} 或 {id}/sync
	parts := strings.SplitN(path, "/", 3)
	groupID := parts[0]

	if len(parts) == 1 {
		// /v1/user-groups/{id}
		a.handleUserGroupByID(w, r, groupID)
		return
	}

	switch parts[1] {
	case "members":
		if len(parts) == 2 || parts[2] == "" {
			// /v1/user-groups/{id}/members
			a.handleGroupMembers(w, r, groupID)
		} else {
			// /v1/user-groups/{id}/members/{uid}
			a.handleGroupMemberByUID(w, r, groupID, parts[2])
		}
	case "sync":
		// /v1/user-groups/{id}/sync
		a.handleGroupSync(w, r, groupID)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
	}
}

// handleUserGroupByID 处理单个用户组的 PUT 和 DELETE 请求。
func (a *userGroupAPI) handleUserGroupByID(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodPut:
		var req usergroup.UserGroup
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		req.ID = id
		if req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
			return
		}
		g, err := a.ugStore.UpsertUserGroup(req)
		if err != nil {
			internalError(w, r, err)
			return
		}
		// inbound 列表变更后自动同步给所有成员
		inboundIDs := parseCommaList(g.InboundIDs)
		memberIDs, _ := a.ugStore.ListGroupMembers(g.ID)
		affectedNodeIDs := make(map[string]struct{})
		for _, uid := range memberIDs {
			if nodeIDs, err := a.syncGroupForUser(g.ID, uid, inboundIDs); err == nil {
				for _, nid := range nodeIDs {
					affectedNodeIDs[nid] = struct{}{}
				}
			}
		}
		nodes := make([]string, 0, len(affectedNodeIDs))
		for nid := range affectedNodeIDs {
			nodes = append(nodes, nid)
		}
		go a.applyNodes(nodes)
		writeJSON(w, http.StatusOK, g)

	case http.MethodDelete:
		// 删除时先清除所有成员的组 inbound 记录，再删除成员关系，最后删除组
		memberIDs, err := a.ugStore.ListGroupMembers(id)
		if err != nil {
			internalError(w, r, err)
			return
		}
		for _, uid := range memberIDs {
			if err := a.userStore.DeleteGroupUserInbounds(uid, id); err != nil {
				internalError(w, r, err)
				return
			}
		}
		if err := a.userStore.DeleteAllInboundsForGroup(id); err != nil {
			internalError(w, r, err)
			return
		}
		if err := a.ugStore.DeleteUserGroup(id); err != nil {
			writeUserGroupError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})

	default:
		writeMethodNotAllowed(w, http.MethodPut+", "+http.MethodDelete)
	}
}

// handleGroupMembers 处理 /v1/user-groups/{id}/members 的 GET 和 POST 请求。
func (a *userGroupAPI) handleGroupMembers(w http.ResponseWriter, r *http.Request, groupID string) {
	// 先确认组存在
	if _, err := a.ugStore.GetUserGroup(groupID); err != nil {
		writeUserGroupError(w, err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		memberIDs, err := a.ugStore.ListGroupMembers(groupID)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if len(memberIDs) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"members": []any{}})
			return
		}
		userMap, err := a.userStore.GetUsersByIDs(memberIDs)
		if err != nil {
			internalError(w, r, err)
			return
		}
		type memberInfo struct {
			UserID   string `json:"user_id"`
			Username string `json:"username"`
		}
		members := make([]memberInfo, 0, len(memberIDs))
		for _, uid := range memberIDs {
			u, ok := userMap[uid]
			if !ok {
				// 用户已删除，清理孤儿记录
				_ = a.ugStore.RemoveMember(groupID, uid)
				continue
			}
			members = append(members, memberInfo{UserID: uid, Username: u.Username})
		}
		writeJSON(w, http.StatusOK, map[string]any{"members": members})

	case http.MethodPost:
		var req struct {
			UserID string `json:"user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.UserID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "user_id is required"})
			return
		}
		if err := a.ugStore.AddMember(groupID, req.UserID); err != nil {
			internalError(w, r, err)
			return
		}
		// 加入成员后立即同步该成员的 inbound 记录并下发节点配置
		g, err := a.ugStore.GetUserGroup(groupID)
		if err == nil {
			inboundIDs := parseCommaList(g.InboundIDs)
			if len(inboundIDs) > 0 {
				if nodeIDs, err := a.syncGroupForUser(groupID, req.UserID, inboundIDs); err == nil {
					a.applyNodes(nodeIDs)
				}
			}
		}
		writeJSON(w, http.StatusCreated, map[string]any{"added": true})

	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

// handleGroupMemberByUID 处理 /v1/user-groups/{id}/members/{uid} 的 DELETE 请求。
func (a *userGroupAPI) handleGroupMemberByUID(w http.ResponseWriter, r *http.Request, groupID, userID string) {
	if r.Method != http.MethodDelete {
		writeMethodNotAllowed(w, http.MethodDelete)
		return
	}
	// 删除前先收集该用户在此组的节点 ID，用于删除后触发下发
	var affectedNodes []string
	if accesses, err := a.userStore.ListUserInboundsByUser(userID); err != nil {
		log.Printf("handleGroupMemberByUID: list inbounds user=%s: %v (node push may be incomplete)", userID, err)
	} else {
		seen := make(map[string]struct{})
		for _, acc := range accesses {
			if acc.GroupID == groupID {
				if _, dup := seen[acc.NodeID]; !dup {
					seen[acc.NodeID] = struct{}{}
					affectedNodes = append(affectedNodes, acc.NodeID)
				}
			}
		}
	}
	if err := a.ugStore.RemoveMember(groupID, userID); err != nil {
		internalError(w, r, err)
		return
	}
	// 清除该用户在此组的 inbound 记录
	if err := a.userStore.DeleteGroupUserInbounds(userID, groupID); err != nil {
		internalError(w, r, err)
		return
	}
	a.applyNodes(affectedNodes)
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

// handleGroupSync 同步用户组所有成员的 inbound 记录。
func (a *userGroupAPI) handleGroupSync(w http.ResponseWriter, r *http.Request, groupID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	g, err := a.ugStore.GetUserGroup(groupID)
	if err != nil {
		writeUserGroupError(w, err)
		return
	}
	inboundIDs := parseCommaList(g.InboundIDs)
	memberIDs, err := a.ugStore.ListGroupMembers(groupID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	affectedNodeIDs := make(map[string]struct{})
	for _, uid := range memberIDs {
		nodeIDs, err := a.syncGroupForUser(groupID, uid, inboundIDs)
		if err != nil {
			internalError(w, r, err)
			return
		}
		for _, nid := range nodeIDs {
			affectedNodeIDs[nid] = struct{}{}
		}
	}
	nodes := make([]string, 0, len(affectedNodeIDs))
	for nid := range affectedNodeIDs {
		nodes = append(nodes, nid)
	}
	a.applyNodes(nodes)
	writeJSON(w, http.StatusOK, map[string]any{"synced_members": len(memberIDs), "affected_nodes": nodes})
}

// syncGroupForUser 同步指定用户在指定组的 inbound 记录：
// 先删除旧的组记录，再按 inboundIDs 重建，返回受影响的 nodeIDs。
// 若用户已通过其他途径持有相同 inbound，复用其凭据保持密码不变。
func (a *userGroupAPI) syncGroupForUser(groupID, userID string, inboundIDs []string) ([]string, error) {
	// 先获取现有 user_inbounds，用于复用凭据（保证切换来源时密码不变）
	existing, _ := a.userStore.ListUserInboundsByUser(userID)
	credsByInbound := make(map[string]users.UserInbound, len(existing))
	for _, acc := range existing {
		if _, ok := credsByInbound[acc.InboundID]; !ok {
			credsByInbound[acc.InboundID] = acc // 优先保留先找到的（直接分配排在前）
		}
	}

	// 删除旧的组记录
	if err := a.userStore.DeleteGroupUserInbounds(userID, groupID); err != nil {
		return nil, err
	}
	affectedNodes := make(map[string]struct{})
	for _, ibID := range inboundIDs {
		ib, err := a.ibStore.GetInbound(ibID)
		if err != nil {
			continue // inbound 不存在则跳过
		}
		uuid := randomUUID()
		secret := generateGroupSecret(ib.Protocol, ib.Method)
		if prev, ok := credsByInbound[ibID]; ok {
			uuid = prev.UUID
			secret = prev.Secret
		}
		acc := users.UserInbound{
			ID:        idgen.NextString(),
			UserID:    userID,
			InboundID: ibID,
			NodeID:    ib.NodeID,
			UUID:      uuid,
			Secret:    secret,
			GroupID:   groupID,
			CreatedAt: time.Now().UTC(),
		}
		if _, err := a.userStore.UpsertGroupUserInbound(acc); err != nil {
			return nil, err
		}
		affectedNodes[ib.NodeID] = struct{}{}
	}
	out := make([]string, 0, len(affectedNodes))
	for nid := range affectedNodes {
		out = append(out, nid)
	}
	return out, nil
}

// generateGroupSecret 根据协议和方法生成适当的密钥。
func generateGroupSecret(protocol, method string) string {
	if protocol == "shadowsocks" && strings.HasPrefix(method, "2022-") {
		return ssPassword(method)
	}
	return randomToken(12)
}

// parseCommaList 解析逗号分隔的字符串列表，过滤空元素。
func parseCommaList(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// writeUserGroupError 根据错误类型返回对应 HTTP 状态码。
func writeUserGroupError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, usergroup.ErrUserGroupNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}
