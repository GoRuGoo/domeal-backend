package controller

import (
	"context"
	"domeal/middleware"
	"domeal/model"
	"log/slog"
	"net/http"
	"strconv"

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
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
}

func NewItemHub() *ItemHub {
	return &ItemHub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
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

func (c *ItemClient) readItemExecution() {

}

func (c *ItemClient) writeItemExecution() {
}
