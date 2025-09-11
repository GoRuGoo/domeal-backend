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
	upgrader    websocket.Upgrader
	redisRepo   model.ItemRedisInterface
	groupRepo   model.GroupInterface
	receiptRepo model.ReceiptInterface
	billingRepo model.BillingInterface
	groupHubs   map[int64]*ItemHub
}

func NewItemController(redisRepo model.ItemRedisInterface, groupRepo model.GroupInterface, receiptRepo model.ReceiptInterface, billingRepo model.BillingInterface) *ItemController {
	return &ItemController{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		redisRepo:   redisRepo,
		groupRepo:   groupRepo,
		receiptRepo: receiptRepo,
		billingRepo: billingRepo,
		groupHubs:   make(map[int64]*ItemHub),
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

	client.hub.register <- client

	go client.readItemExecution()
	go client.writeExecution()
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
	clients        map[*ItemClient]bool
	broadcast      chan []byte
	register       chan *ItemClient
	unregister     chan *ItemClient
	completedUsers map[int64]bool // 完了通知を送ったユーザーを追跡
}

func NewItemHub() *ItemHub {
	return &ItemHub{
		clients:        make(map[*ItemClient]bool),
		broadcast:      make(chan []byte),
		register:       make(chan *ItemClient),
		unregister:     make(chan *ItemClient),
		completedUsers: make(map[int64]bool),
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
	Action    string `json:"action"` // "choose", "remove", "get_items", "complete"
	ItemID    int64  `json:"item_id"`
	ReceiptID int64  `json:"receipt_id"`
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

	// 接続時に現在のアイテム選択状況をブロードキャスト
	c.broadcastCurrentItemSelections()

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
		case "choose":
			if itemActionMsg.ReceiptID == 0 {
				slog.Error("ReceiptID is required for choose action")
				continue
			}

			ctx, cancel := context.WithTimeout(c.context, 5*time.Second)
			defer cancel()

			err = c.controller.redisRepo.ChoiseItemByReceiptIDAndUserData(
				ctx, c.groupID, itemActionMsg.ReceiptID, c.userID, itemActionMsg.ItemID, c.userIcon)
			if err != nil {
				slog.Error("Failed to choose item",
					"error", err,
					"group_id", c.groupID,
					"receipt_id", itemActionMsg.ReceiptID,
					"user_id", c.userID)
				continue
			}

			slog.Info("Item chosen successfully",
				"group_id", c.groupID,
				"receipt_id", itemActionMsg.ReceiptID,
				"user_id", c.userID)

			c.broadcastCurrentItemSelections()

		case "remove":
			if itemActionMsg.ReceiptID == 0 {
				slog.Error("ReceiptID is required for remove action")
				continue
			}

			ctx, cancel := context.WithTimeout(c.context, 5*time.Second)
			defer cancel()

			err = c.controller.redisRepo.RemoveItemChoiceByReceiptIDAndUserData(
				ctx, c.groupID, itemActionMsg.ReceiptID, c.userID, itemActionMsg.ItemID, c.userIcon)
			if err != nil {
				slog.Error("Failed to remove item choice",
					"error", err,
					"group_id", c.groupID,
					"receipt_id", itemActionMsg.ReceiptID,
					"user_id", c.userID)
				continue
			}

			slog.Info("Item choice removed successfully",
				"group_id", c.groupID,
				"receipt_id", itemActionMsg.ReceiptID,
				"user_id", c.userID)

			c.broadcastCurrentItemSelections()

		case "get_items":
			// アイテム一覧をブロードキャスト（必要に応じて実装）
			slog.Info("Get items request received", "group_id", c.groupID)
			c.broadcastCurrentItemSelections()

		case "complete":
			slog.Info("User completed item selection", "group_id", c.groupID, "user_id", c.userID)

			// 完了したユーザーをマーク
			c.hub.completedUsers[c.userID] = true

			// 全ユーザーが完了したかチェック
			if c.isAllUsersCompleted() {
				slog.Info("All users completed item selection, persisting to DB", "group_id", c.groupID)
				c.calculateUserBillingAndStoreToDB()
				c.persistItemSelectionsToNotification()
			}

		default:
			slog.Error("Unknown action", "action", itemActionMsg.Action)
		}
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

type ItemSelectionMessage struct {
	Type    string                `json:"type"`     // "item_selection_update"
	GroupID int64                 `json:"group_id"` // グループID
	Items   []model.ItemWithUsers `json:"items"`    // 商品情報と選択ユーザー情報
}

func (c *ItemClient) broadcastCurrentItemSelections() {
	// Redis から商品情報と選択情報を一緒に取得
	items, err := c.controller.redisRepo.GetAllItemSelections(c.context, c.groupID)
	if err != nil {
		slog.Error("Failed to get current item selections", "error", err, "group_id", c.groupID)
		return
	}

	msg := ItemSelectionMessage{
		Type:    "item_selection_update",
		GroupID: c.groupID,
		Items:   items,
	}

	stateBytes, err := json.Marshal(msg)
	if err != nil {
		slog.Error("Failed to marshal item selection message", "error", err)
		return
	}

	slog.Info(string(stateBytes))

	// Hub にブロードキャスト
	c.hub.broadcast <- stateBytes
	slog.Info("Broadcasted current item selections to all clients", "group_id", c.groupID)
}

// 全ユーザーが完了したかチェック
func (c *ItemClient) isAllUsersCompleted() bool {
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

// アイテム選択完了を通知
func (c *ItemClient) persistItemSelectionsToNotification() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Redisから現在のアイテム選択情報を取得
	items, err := c.controller.redisRepo.GetAllItemSelections(ctx, c.groupID)
	if err != nil {
		slog.Error("Failed to get item selections from Redis", "error", err, "group_id", c.groupID)
		return
	}

	slog.Info("Item selections retrieved for notification",
		"group_id", c.groupID,
		"items_count", len(items))

	// 完了状態をリセット（次回のアイテム選択のため）
	c.hub.completedUsers = make(map[int64]bool)

	// 完了通知をクライアントに送信
	completionMsg := map[string]interface{}{
		"type":     "items_selection_completed",
		"group_id": c.groupID,
		"message":  "アイテム選択が完了しました",
		"items":    items,
	}

	msgBytes, err := json.Marshal(completionMsg)
	if err != nil {
		slog.Error("Failed to marshal completion message", "error", err)
		return
	}

	c.hub.broadcast <- msgBytes
	slog.Info("Notified all clients about item selection completion", "group_id", c.groupID)
}

func (c *ItemClient) calculateUserBillingAndStoreToDB() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	items, err := c.controller.redisRepo.GetAllItemSelections(ctx, c.groupID)
	if err != nil {
		slog.Error("Failed to get item selections from Redis", "error", err, "group_id", c.groupID)
		return
	}

	groupMemberIDs, err := c.controller.groupRepo.GetGroupMemberIDs(c.groupID)
	if err != nil {
		slog.Error("Failed to get group member IDs", "error", err, "group_id", c.groupID)
		return
	}

	userCount := len(groupMemberIDs)

	//すべてのユーザーが負担する請求の合計金額
	allUsersBilling := 0.0

	// ここにユーザーの請求データを保保存する
	userBillingMap := make(map[int64]float64)

	for _, item := range items {
		// 選択しているユーザーがいなければそれはみんなで割り勘するものなのですべてのユーザーが負担するようにする
		if len(item.SelectedUsers) == 0 {
			allUsersBilling += (float64(item.Quantity) * float64(item.Price)) / float64(userCount)
			continue
		}

		for _, user := range item.SelectedUsers {
			userID, err := strconv.ParseInt(user["user_id"], 10, 64)
			if err != nil {
				slog.Error("Failed to parse user_id", "error", err, "user_id_str", user["user_id"])
				continue
			}

			userBillingMap[userID] += (item.Price * float64(item.Quantity)) / float64(len(item.SelectedUsers))
		}
	}

	// すでに上記の処理で個別の請求は加算されたので、ここでは全ユーザーで割り勘する分だけを加算する
	for _, groupMemberID := range groupMemberIDs {
		userBillingMap[groupMemberID] += allUsersBilling
	}

	receiptID, uploaderID, err := c.controller.receiptRepo.GetReceiptIDAndUploaderIDByGroupID(c.groupID)
	if err != nil {
		slog.Info("Failed to get receiptID and uploaderID", "error", err, "group_id", c.groupID)
		return
	}

	tx, err := c.controller.billingRepo.BeginTx(context.Background(), nil)
	if err != nil {
		slog.Error("Failed to begin transaction", "error", err, "group_id", c.groupID)
		return
	}

	err = c.controller.billingRepo.StoreUserBillings(ctx, tx, c.groupID, receiptID, uploaderID, userBillingMap)
	if err != nil {
		tx.Rollback()
		slog.Error("Failed to store user billings", "error", err, "group_id", c.groupID)
		return
	}

	tx.Commit()
	slog.Info("User billings stored successfully", "group_id", c.groupID)
}
