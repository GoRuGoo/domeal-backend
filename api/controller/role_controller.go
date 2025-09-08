package controller

import (
	"domeal/middleware"
	"domeal/model"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/websocket"
)

// TODO 一部密結合なため時間があったら治す

// WebSocketメッセージの構造体
type RoleActionMessage struct {
	Action string `json:"action"` // "assign", "change", "get_state"
	UserID int64  `json:"user_id"`
	Role   string `json:"role,omitempty"` // "shopping", "cooking", "cleaning"
}

// ブロードキャスト用の状態メッセージ
type RoleStateMessage struct {
	Type    string              `json:"type"` // "role_update"
	Roles   map[string][]string `json:"roles"`
	GroupID int64               `json:"group_id"`
}

type RoleController struct {
	upgrader  websocket.Upgrader
	groupRepo model.GroupInterface
	redisRepo model.RoleRedisInterface
	groupHubs map[int64]*Hub // group別にHubを持つ
}

func NewRoleController(groupRepo model.GroupInterface, redisRepo model.RoleRedisInterface) *RoleController {
	return &RoleController{
		groupRepo: groupRepo,
		redisRepo: redisRepo,
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
		controller: c, // RoleControllerへの参照を追加
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
	controller *RoleController // RoleControllerへの参照を追加
}

type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
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

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Error("予期しないWebSocketのクローズエラーが発生した｡正常ではないのでコネクションまわりの設定などを確認すべき｡", "error", err.Error())
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

		// Redisに操作を書き込み
		switch actionMsg.Action {
		case "assign":
			if actionMsg.Role == "" {
				slog.Error("Role is required for assign action")
				continue
			}
			err = c.controller.redisRepo.AssignRole(c.groupID, actionMsg.UserID, actionMsg.Role)
			if err != nil {
				slog.Error("Failed to assign role", "error", err, "group_id", c.groupID, "user_id", actionMsg.UserID, "role", actionMsg.Role)
				continue
			}
			slog.Info("Role assigned successfully", "group_id", c.groupID, "user_id", actionMsg.UserID, "role", actionMsg.Role)

		case "change":
			if actionMsg.Role == "" {
				slog.Error("Role is required for change action")
				continue
			}
			err = c.controller.redisRepo.ChangeRole(c.groupID, actionMsg.UserID, actionMsg.Role)
			if err != nil {
				slog.Error("Failed to change role", "error", err, "group_id", c.groupID, "user_id", actionMsg.UserID, "role", actionMsg.Role)
				continue
			}
			slog.Info("Role changed successfully", "group_id", c.groupID, "user_id", actionMsg.UserID, "role", actionMsg.Role)

		case "get_state":
			// 状態取得のみ、Redis書き込みなし
			slog.Info("Getting current state", "group_id", c.groupID)

		default:
			slog.Error("Unknown action", "action", actionMsg.Action)
			continue
		}

		// 現在の状態を取得してブロードキャスト
		c.broadcastCurrentState()
	}
}

// 現在の役割状態を全クライアントにブロードキャスト
func (c *Client) broadcastCurrentState() {
	// GetAllCurrentRolesメソッドを使用する（実装がない場合は別の方法を考える）
	roles, err := c.controller.redisRepo.GetAllCurrentRoles(c.groupID)
	if err != nil {
		slog.Error("Failed to get current roles", "error", err, "group_id", c.groupID)
		return
	}

	// map[int64]string を map[string][]string に変換
	rolesByName := make(map[string][]string)
	for userID, role := range roles {
		if rolesByName[role] == nil {
			rolesByName[role] = make([]string, 0)
		}
		rolesByName[role] = append(rolesByName[role], strconv.FormatInt(userID, 10))
	}

	stateMsg := RoleStateMessage{
		Type:    "role_update",
		Roles:   rolesByName,
		GroupID: c.groupID,
	}

	stateBytes, err := json.Marshal(stateMsg)
	if err != nil {
		slog.Error("Failed to marshal state message", "error", err)
		return
	}

	// 同じグループの全クライアントにブロードキャスト
	c.hub.broadcast <- stateBytes
	slog.Info("Broadcasted role state to all clients", "group_id", c.groupID, "roles", rolesByName)
}

func (c *Client) writeExecution() {
	defer func() {
		c.conn.Close()
	}()

	for {
		message, ok := <-c.send
		if !ok {
			c.conn.WriteMessage(websocket.CloseMessage, []byte{})
			return
		}

		w, err := c.conn.NextWriter(websocket.TextMessage)
		if err != nil {
			slog.Error("Failed to get next writer", "error", err)
			return
		}

		w.Write(message)

		if err := w.Close(); err != nil {
			slog.Error("Failed to close writer", "error", err)
			return
		}
	}
}
