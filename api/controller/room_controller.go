package controller

import (
	"context"
	"domeal/middleware"
	"domeal/model"
	"encoding/json"
	"log/slog"
	"net/http"
)

type RoomController struct {
	repo model.RoomInterface
}

func NewRoomController(repo model.RoomInterface) *RoomController {
	return &RoomController{
		repo: repo,
	}
}

type CreateRoomRequest struct {
	Name         string `json:"name"`
	Menu         string `json:"menu"`
	MenuImageURL string `json:"menu_image_url"`
}

type CreateRoomResponse struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Menu         string `json:"menu"`
	MenuImageURL string `json:"menu_image_url"`
}

func (c *RoomController) CreateRoomController(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		slog.Error("Invalid method", "method", r.Method)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// ミドルウェアで設定されたユーザーIDを取得
	tmpUser, ok := middleware.GetUserFromContext(r.Context())
	if !ok {
		slog.Error("ミドルウェアからユーザー情報を取得できませんでした｡Cookieなどを確認すべき｡")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	userID := int64(tmpUser.ID)

	// リクエストボディをパース
	var req CreateRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode request body", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// バリデーション
	if req.Name == "" {
		slog.Error("Room name is required")
		http.Error(w, "Room name is required", http.StatusBadRequest)
		return
	}

	if req.Menu == "" {
		slog.Error("Menu is required")
		http.Error(w, "Menu is required", http.StatusBadRequest)
		return
	}

	// TODO:  料理の画像は一旦ダミーをつかう
	req.MenuImageURL = "https://www.foodiesfeed.com/wp-content/uploads/2023/06/burger-with-melted-cheese.jpg.webp"

	// トランザクション開始
	tx, err := c.repo.BeginTx(context.Background(), nil)
	if err != nil {
		slog.Error("Failed to begin transaction", "error", err)
		http.Error(w, "Failed to begin transaction", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// Roomオブジェクトを作成
	room := &model.Room{
		Name:         req.Name,
		Menu:         req.Menu,
		MenuImageURL: req.MenuImageURL,
		CreatedBy:    userID,
	}

	// Roomを作成
	roomID, err := c.repo.CreateRoom(tx, room)
	if err != nil {
		slog.Error("Failed to create room", "error", err)
		http.Error(w, "Failed to create room", http.StatusInternalServerError)
		return
	}

	// トランザクションをコミット
	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		http.Error(w, "Failed to commit transaction", http.StatusInternalServerError)
		return
	}

	// レスポンスを作成
	response := CreateRoomResponse{
		ID:           roomID,
		Name:         req.Name,
		Menu:         req.Menu,
		MenuImageURL: req.MenuImageURL,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode response", "error", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	slog.Info("Room created successfully", "room_id", roomID, "user_id", userID)
}
