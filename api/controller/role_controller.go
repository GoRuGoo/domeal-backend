package controller

import (
	"domeal/middleware"
	"domeal/model"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/websocket"
)

type RoleController struct {
	repo      model.RoleInterface
	groupRepo model.GroupInterface
	rdb       model.RoleRedisInterface
	upgrader  websocket.Upgrader
}

func NewRoleController(repo model.RoleInterface, groupRepo model.GroupInterface, rdb model.RoleRedisInterface) *RoleController {
	return &RoleController{
		repo:      repo,
		groupRepo: groupRepo,
		rdb:       rdb,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				// 開発環境では全てのオリジンを許可
				// 本番環境では適切なオリジンチェックを実装してください
				return true
			},
		},
	}
}

func (c *RoleController) RoleDivisionController(w http.ResponseWriter, r *http.Request) {
	// WebSocketアップグレード前に認証とグループメンバーシップをチェック
	ctx := r.Context()

	tmpUser, ok := middleware.GetUserFromContext(ctx)
	if !ok {
		slog.Error("ミドルウェアからユーザー情報を取得できませんでした｡Cookieなどを確認すべき｡")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	userID := int64(tmpUser.ID)

	// クエリパラメータからgroup_idを取得
	groupIDStr := r.URL.Query().Get("group_id")
	if groupIDStr == "" {
		slog.Error("group_id parameter is required")
		http.Error(w, "group_id parameter is required", http.StatusBadRequest)
		return
	}

	groupID, err := strconv.ParseInt(groupIDStr, 10, 64)
	if err != nil {
		slog.Error("Invalid group_id parameter", "group_id", groupIDStr, "error", err)
		http.Error(w, "Invalid group_id parameter", http.StatusBadRequest)
		return
	}

	// ユーザーがグループに所属しているかチェック
	isMember, err := c.groupRepo.IsGroupMember(groupID, userID)
	if err != nil {
		slog.Error("Failed to check group membership", "group_id", groupID, "user_id", userID, "error", err)
		http.Error(w, "Failed to verify group membership", http.StatusInternalServerError)
		return
	}

	if !isMember {
		slog.Error("User is not a member of the group", "group_id", groupID, "user_id", userID)
		http.Error(w, "User is not authorized to access this group", http.StatusForbidden)
		return
	}

	slog.Info("User successfully authenticated for group", "group_id", groupID, "user_id", userID)

	// すべての条件チェックが完了したら、WebSocketにアップグレード
	conn, err := c.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Failed to upgrade connection", slog.String("error", err.Error()))
		http.Error(w, "Failed to upgrade connection", http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	slog.Info("WebSocket connection established")

}
