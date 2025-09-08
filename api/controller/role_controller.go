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
		hub:     hub,
		conn:    conn,
		send:    make(chan []byte, 256),
		groupID: groupID,
		userID:  userID,
	}
	client.hub.register <- client

	go client.readExecution()
	go client.writeExecution()
}

type Client struct {
	hub     *Hub
	conn    *websocket.Conn
	send    chan []byte
	groupID int64
	userID  int64
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
			//このあとdefer呼ばれる
			break
		}

		slog.Info("Received message from client",
			"message", string(message),
			"group_id", c.groupID,
			"user_id", c.userID)

		c.hub.broadcast <- message
	}
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
