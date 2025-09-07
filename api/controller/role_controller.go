package controller

import (
	"domeal/middleware"
	"domeal/model"
	"log/slog"
	"net/http"
	"strconv"
	"sync"

	"github.com/gorilla/websocket"
)

// RoleController はWebSocketを管理するためのコントローラー
type RoleController struct {
	upgrader  websocket.Upgrader
	groupRepo model.GroupInterface
}

// コンストラクタ
func NewRoleController(groupRepo model.GroupInterface) *RoleController {
	return &RoleController{
		groupRepo: groupRepo,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				// 開発環境では全てのオリジンを許可
				// 本番環境では適切なオリジンチェックを実装してください
				return true
			},
		},
	}
}

// groupConnections: グループごとの接続管理
var (
	groupConnections = make(map[int64][]*websocket.Conn)
	mu               sync.Mutex
)

// RoleDivisionController はWebSocket接続を受け付けるハンドラ
func (c *RoleController) RoleDivisionController(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// ユーザー情報をミドルウェアから取得
	tmpUser, ok := middleware.GetUserFromContext(ctx)
	if !ok {
		slog.Error("ミドルウェアからユーザー情報を取得できませんでした｡Cookieなどを確認すべき｡")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	userID := int64(tmpUser.ID)

	// クエリパラメータから group_id を取得
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

	slog.Info("User successfully authenticated for group",
		"group_id", groupID,
		"user_id", userID)

	// WebSocketにアップグレード
	conn, err := c.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Failed to upgrade connection", slog.String("error", err.Error()))
		http.Error(w, "Failed to upgrade connection", http.StatusInternalServerError)
		return
	}

	// 接続を groupConnections に登録
	mu.Lock()
	groupConnections[groupID] = append(groupConnections[groupID], conn)
	slog.Info("Connection added",
		"group_id", groupID,
		"user_id", userID,
		"total_connections", len(groupConnections[groupID]))
	mu.Unlock()

	// deferを利用して確実に切断処理を行う
	defer func() {
		conn.Close()

		mu.Lock()
		conns := groupConnections[groupID]
		for i, c := range conns {
			if c == conn {
				groupConnections[groupID] = append(conns[:i], conns[i+1:]...)
				break
			}
		}
		slog.Info("Connection removed",
			"group_id", groupID,
			"user_id", userID,
			"remaining_connections", len(groupConnections[groupID]))
		mu.Unlock()
	}()

	// メッセージの受信とブロードキャスト
	c.handleMessages(conn, groupID, userID)
}

// handleMessages はWebSocketからのメッセージを受信して同じグループ内にブロードキャストする
func (c *RoleController) handleMessages(conn *websocket.Conn, groupID, userID int64) {
	for {
		// メッセージを受信
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure) {
				slog.Error("WebSocket unexpected close error",
					"group_id", groupID,
					"user_id", userID,
					"error", err)
			} else {
				slog.Info("WebSocket connection closed normally",
					"group_id", groupID,
					"user_id", userID)
			}
			break
		}

		slog.Info("Received WebSocket message",
			"group_id", groupID,
			"user_id", userID,
			"message", string(message))

		// 同じグループの全接続にメッセージを送信
		mu.Lock()
		for _, c := range groupConnections[groupID] {
			if err := c.WriteMessage(websocket.TextMessage, message); err != nil {
				slog.Error("Failed to send message to client",
					"group_id", groupID,
					"user_id", userID,
					"error", err)
			}
		}
		mu.Unlock()
	}
}
