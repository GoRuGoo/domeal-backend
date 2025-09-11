package controller

import (
	"context"
	"domeal/middleware"
	"domeal/model"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

// TODO 一部密結合なため時間があったら治す

// WebSocketメッセージの構造体
type RoleActionMessage struct {
	Action string `json:"action"` // "assign", "change", "get_state", "complete"
	UserID int64  `json:"user_id"`
	Role   string `json:"role,omitempty"` // "shopping", "cooking", "cleaning"
}

// ブロードキャスト用の状態メッセージ
type RoleStateMessage struct {
	Type    string                        `json:"type"` // "role_update"
	Roles   map[string][]model.RoleMember `json:"roles"`
	GroupID int64                         `json:"group_id"`
}

type RoleController struct {
	upgrader  websocket.Upgrader
	groupRepo model.GroupInterface
	redisRepo model.RoleRedisInterface
	flowRepo  model.FlowRedisInterface
	roleRepo  model.RoleInterface
	groupHubs map[int64]*Hub // group別にHubを持つ
}

func NewRoleController(groupRepo model.GroupInterface, redisRepo model.RoleRedisInterface, flowRepo model.FlowRedisInterface, roleRepo model.RoleInterface) *RoleController {
	return &RoleController{
		groupRepo: groupRepo,
		redisRepo: redisRepo,
		flowRepo:  flowRepo,
		roleRepo:  roleRepo,
		groupHubs: make(map[int64]*Hub),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

func (c *RoleController) getOrCreateGroupHub(groupID int64) *Hub {
	hub, exists := c.groupHubs[groupID]
	if !exists {
		hub = NewHub()
		c.groupHubs[groupID] = hub
		go hub.Run() // 新しいHubのgoroutineを開始
		slog.Info("Created new hub for group", "group_id", groupID)
	}
	return hub
}

func (c *RoleController) RoleDivisionController(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tmpUser, ok := middleware.GetUserFromContext(ctx)
	if !ok {
		slog.Error("ミドルウェアからユーザー情報を取得できませんでした｡Cookieなどを確認すべき｡")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	userID := int64(tmpUser.ID)

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

	conn, err := c.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Failed to upgrade connection", slog.String("error", err.Error()))
		http.Error(w, "Failed to upgrade connection", http.StatusInternalServerError)
		return
	}

	// groupID別のHubを取得または作成
	hub := c.getOrCreateGroupHub(groupID)

	client := &Client{
		hub:        hub,
		conn:       conn,
		send:       make(chan []byte, 256),
		groupID:    groupID,
		userID:     userID,
		userIcon:   tmpUser.PictureURL,
		controller: c,                    // RoleControllerへの参照を追加
		context:    context.Background(), // HTTPリクエストのcontextではなく、独立したcontextを使用
	}
	client.hub.register <- client

	go client.readExecution()
	go client.writeExecution()
}

type Client struct {
	hub        *Hub
	conn       *websocket.Conn
	send       chan []byte
	groupID    int64
	userID     int64
	userIcon   string
	context    context.Context
	controller *RoleController // RoleControllerへの参照を追加
}

type Hub struct {
	clients        map[*Client]bool
	broadcast      chan []byte
	register       chan *Client
	unregister     chan *Client
	completedUsers map[int64]bool // 完了通知を送ったユーザーを追跡
}

func NewHub() *Hub {
	return &Hub{
		clients:        make(map[*Client]bool),
		broadcast:      make(chan []byte),
		register:       make(chan *Client),
		unregister:     make(chan *Client),
		completedUsers: make(map[int64]bool),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}

func (c *Client) readExecution() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	// 接続が60秒間無通信なら切断する
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(appData string) error {
		// pongを受け取ったら60秒延長
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// 接続時に現在の役割状態をブロードキャスト
	c.broadcastCurrentState()

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Error("予期しないWebSocketのクローズエラーが発生した｡", "error", err.Error())
			}
			break
		}

		slog.Info("Received message from client",
			"message", string(message),
			"group_id", c.groupID,
			"user_id", c.userID)

		// メッセージを解析してRedis操作を実行
		var actionMsg RoleActionMessage
		if err := json.Unmarshal(message, &actionMsg); err != nil {
			slog.Error("Failed to parse message JSON", "error", err, "message", string(message))
			continue
		}

		switch actionMsg.Action {
		case "assign":
			if actionMsg.Role == "" {
				slog.Error("Role is required for assign action")
				continue
			}

			// タイムアウト付きのcontextを作成
			ctx, cancel := context.WithTimeout(c.context, 5*time.Second)
			defer cancel()

			// RedisでUpsert処理
			err = c.controller.redisRepo.UpsertUserRole(ctx, c.groupID, c.userID, actionMsg.Role, c.userIcon)
			if err != nil {
				slog.Error("Failed to assign or change role",
					"error", err,
					"group_id", c.groupID,
					"user_id", c.userID,
					"role", actionMsg.Role,
					"icon_url", c.userIcon,
				)
				// エラーレスポンスを返す
				c.hub.broadcast <- []byte(`{"type":"error","message":"Failed to assign role"}`)
				continue
			}

			slog.Info("Role upserted successfully",
				"group_id", c.groupID,
				"user_id", c.userID,
				"role", actionMsg.Role,
			)

			// Redisに変更があったので現在の状態をブロードキャスト
			c.broadcastCurrentState()

		case "get_state":
			slog.Info("Getting current state", "group_id", c.groupID)
			c.broadcastCurrentState()

		case "complete":
			slog.Info("User completed role assignment", "group_id", c.groupID, "user_id", c.userID)

			// 完了したユーザーをマーク
			c.hub.completedUsers[c.userID] = true

			// 全ユーザーが完了したかチェック
			if c.isAllUsersCompleted() {
				slog.Info("All users completed role assignment, persisting to DB", "group_id", c.groupID)

				//Redisに最新のフロー状態を保存
				lastMessageKey := fmt.Sprintf("last_message:group_flow:%d", c.groupID)
				c.controller.flowRepo.Set(c.context, lastMessageKey, "move_to_waiting_or_upload_receipt", 24*time.Hour)
				c.controller.flowRepo.Publish(c.context, fmt.Sprintf("group_flow:%d", c.groupID), "move_to_waiting_or_upload_receipt")
				c.persistRolesToDB()
			}

		default:
			slog.Error("Unknown action", "action", actionMsg.Action)
		}
	}
}

// 現在の役割状態を全クライアントにブロードキャスト
func (c *Client) broadcastCurrentState() {
	roles, err := c.controller.redisRepo.GetAllCurrentRoles(c.context, c.groupID)
	if err != nil {
		slog.Error("Failed to get current roles", "error", err, "group_id", c.groupID)
		return
	}

	stateMsg := RoleStateMessage{
		Type:    "role_update",
		Roles:   roles, // 既にmap[string][]model.RoleMember形式なのでそのまま使用
		GroupID: c.groupID,
	}

	stateBytes, err := json.Marshal(stateMsg)
	if err != nil {
		slog.Error("Failed to marshal state message", "error", err)
		return
	}

	slog.Info(string(stateBytes))

	// 同じグループの全クライアントにブロードキャスト
	c.hub.broadcast <- stateBytes
	slog.Info("Broadcasted role state to all clients", "group_id", c.groupID, "roles", roles)
}

func (c *Client) writeExecution() {
	pingTicker := time.NewTicker(30 * time.Second) // 30秒おきにPing送信
	defer func() {
		pingTicker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				// チャネルが閉じられたらWebSocketをクローズ
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			// クライアントにメッセージを送信
			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				slog.Error("Failed to get next writer", "error", err)
				return
			}

			if _, err := w.Write(message); err != nil {
				slog.Error("Failed to write message", "error", err)
				return
			}

			if err := w.Close(); err != nil {
				slog.Error("Failed to close writer", "error", err)
				return
			}

		case <-pingTicker.C:
			// 定期的にPingを送信
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				slog.Error("Failed to send ping", "error", err)
				return
			}
		}
	}
}

// 全ユーザーが完了したかチェック
func (c *Client) isAllUsersCompleted() bool {
	// グループの全メンバー数を取得
	membersCount, err := c.controller.groupRepo.GetGroupMembersCount(c.groupID)
	if err != nil {
		slog.Error("Failed to get group members", "error", err, "group_id", c.groupID)
		return false
	}

	// 完了したユーザー数と全メンバー数を比較
	completedCount := len(c.hub.completedUsers)

	slog.Info("Checking completion status",
		"group_id", c.groupID,
		"completed", completedCount,
		"total", membersCount)

	return completedCount >= membersCount
}

// RedisからDBに役割分担を永続化
func (c *Client) persistRolesToDB() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Redisから現在の役割分担を取得
	roles, err := c.controller.redisRepo.GetAllCurrentRoles(ctx, c.groupID)
	if err != nil {
		slog.Error("Failed to get roles from Redis", "error", err, "group_id", c.groupID)
		return
	}

	// DBに永続化
	for roleName, members := range roles {
		for _, member := range members {
			err := c.controller.roleRepo.CreateRole(ctx, c.groupID, member.UserID, roleName)
			if err != nil {
				slog.Error("Failed to persist role to DB",
					"error", err,
					"group_id", c.groupID,
					"user_id", member.UserID,
					"role", roleName)
				continue
			}
			slog.Info("Role persisted to DB",
				"group_id", c.groupID,
				"user_id", member.UserID,
				"role", roleName)
		}
	}

	// 完了状態をリセット（次回の役割分担のため）
	c.hub.completedUsers = make(map[int64]bool)

	// 永続化完了をクライアントに通知
	completionMsg := map[string]interface{}{
		"type":     "roles_persisted",
		"group_id": c.groupID,
		"message":  "役割分担がデータベースに保存されました",
	}

	msgBytes, err := json.Marshal(completionMsg)
	if err != nil {
		slog.Error("Failed to marshal completion message", "error", err)
		return
	}

	c.hub.broadcast <- msgBytes
	slog.Info("Notified all clients about role persistence", "group_id", c.groupID)
}
