package controller

import (
	"context"
	"domeal/middleware"
	"domeal/model"
	"encoding/json"
	"log/slog"
	"net/http"
)

type GroupController struct {
	repo model.GroupInterface
}

func NewGroupController(repo model.GroupInterface) *GroupController {
	return &GroupController{
		repo: repo,
	}
}

type CreateGroupRequest struct {
	Name         string `json:"name"`
	Menu         string `json:"menu"`
	MenuImageURL string `json:"menu_image_url"`
}

type CreateGroupResponse struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Menu         string `json:"menu"`
	MenuImageURL string `json:"menu_image_url"`
}

func (c *GroupController) CreateGroupController(w http.ResponseWriter, r *http.Request) {
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
	var req CreateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode request body", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// バリデーション
	if req.Name == "" {
		slog.Error("Group name is required")
		http.Error(w, "Group name is required", http.StatusBadRequest)
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

	// Groupオブジェクトを作成
	group := &model.Group{
		Name:         req.Name,
		Menu:         req.Menu,
		MenuImageURL: req.MenuImageURL,
		CreatedBy:    userID,
	}

	// Groupを作成
	groupID, err := c.repo.CreateGroup(tx, group)
	if err != nil {
		slog.Error("Failed to create group", "error", err)
		http.Error(w, "Failed to create group", http.StatusInternalServerError)
		return
	}

	// グループ作成者をgroup_membersテーブルに追加（オーナーとして）
	err = c.repo.AddGroupMember(tx, groupID, userID, true)
	if err != nil {
		slog.Error("Failed to add group creator as group member", "error", err)
		http.Error(w, "Failed to add group creator as group member", http.StatusInternalServerError)
		return
	}

	// トランザクションをコミット
	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		http.Error(w, "Failed to commit transaction", http.StatusInternalServerError)
		return
	}

	// レスポンスを作成
	response := CreateGroupResponse{
		ID:           groupID,
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

	slog.Info("Group created successfully", "group_id", groupID, "user_id", userID)
}
