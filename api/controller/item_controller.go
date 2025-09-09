package controller

import (
	"context"
	"domeal/middleware"
	"domeal/model"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

type ItemController struct {
	upgrader  websocket.Upgrader
	redisRepo model.ItemRedisInterface
	groupRepo model.GroupInterface
	groupHubs map[int64]*ItemHub
}

func NewItemController(redisRepo model.ItemRedisInterface, groupRepo model.GroupInterface) *ItemController {
	return &ItemController{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		redisRepo: redisRepo,
		groupRepo: groupRepo,
		groupHubs: make(map[int64]*ItemHub),
	}
}

func (c *ItemController) SelectItemController(w http.ResponseWriter, r *http.Request) {
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

	hub := c.getOrCreateItemHub(groupID)

	client := &ItemClient{
		hub:        hub,
		conn:       conn,
		send:       make(chan []byte, 256),
		groupID:    groupID,
		userID:     userID,
		userIcon:   tmpUser.PictureURL,
		controller: c,
		context:    context.Background(),
	}

	go client.readItemExecution()
	go client.writeItemExecution()
}

type ItemClient struct {
	hub        *ItemHub
	conn       *websocket.Conn
	send       chan []byte
	groupID    int64
	userID     int64
	userIcon   string
	context    context.Context
	controller *ItemController
}

type ItemHub struct {
	clients    map[*ItemClient]bool
	broadcast  chan []byte
	register   chan *ItemClient
	unregister chan *ItemClient
}

func NewItemHub() *ItemHub {
	return &ItemHub{
		clients:    make(map[*ItemClient]bool),
		broadcast:  make(chan []byte),
		register:   make(chan *ItemClient),
		unregister: make(chan *ItemClient),
	}
}

func (h *ItemHub) Run() {
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

func (c *ItemController) getOrCreateItemHub(groupID int64) *ItemHub {
	hub, exists := c.groupHubs[groupID]
	if !exists {
		hub = NewItemHub()
		c.groupHubs[groupID] = hub
		go hub.Run()
	}
	return hub
}

type ItemActionMessage struct {
	Action string `json:"action"`
	ItemID int64  `json:"item_id"`
}

func (c *ItemClient) readItemExecution() {
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

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Error("Unexpected close error", "error", err)
			}
			break
		}

		slog.Info("Received message from client",
			"message", string(message),
			"group_id", c.groupID,
			"user_id", c.userID)

		var itemActionMsg ItemActionMessage
		if err := json.Unmarshal(message, &itemActionMsg); err != nil {
			slog.Error("Failed to unmarshal message", "error", err, "message", string(message))
			continue
		}

		switch itemActionMsg.Action {
		case "choice":



	}

}
func (c *ItemClient) writeExecution() {
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
