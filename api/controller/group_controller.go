package controller

import (
	"context"
	"database/sql"
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

type JoinGroupRequest struct {
	GroupID int64 `json:"group_id"`
}

type JoinGroupResponse struct {
	GroupID   int64  `json:"group_id"`
	GroupName string `json:"group_name"`
	UserID    int64  `json:"user_id"`
	Message   string `json:"message"`
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

func (c *GroupController) JoinGroupController(w http.ResponseWriter, r *http.Request) {
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
	var req JoinGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode request body", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// バリデーション
	if req.GroupID <= 0 {
		slog.Error("Valid group ID is required")
		http.Error(w, "Valid group ID is required", http.StatusBadRequest)
		return
	}

	// グループが存在するかチェック
	group, err := c.repo.GetGroup(req.GroupID)
	if err != nil {
		if err == sql.ErrNoRows {
			slog.Error("Group not found", "group_id", req.GroupID)
			http.Error(w, "Group not found", http.StatusNotFound)
			return
		}
		slog.Error("Failed to get group", "error", err)
		http.Error(w, "Failed to get group", http.StatusInternalServerError)
		return
	}

	// ユーザーが既にグループのメンバーかチェック
	isMember, err := c.repo.IsGroupMember(req.GroupID, userID)
	if err != nil {
		slog.Error("Failed to check group membership", "error", err)
		http.Error(w, "Failed to check group membership", http.StatusInternalServerError)
		return
	}

	if isMember {
		slog.Error("User is already a member of this group", "group_id", req.GroupID, "user_id", userID)
		http.Error(w, "You are already a member of this group", http.StatusConflict)
		return
	}

	// トランザクション開始
	tx, err := c.repo.BeginTx(context.Background(), nil)
	if err != nil {
		slog.Error("Failed to begin transaction", "error", err)
		http.Error(w, "Failed to begin transaction", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// ユーザーをグループメンバーとして追加（オーナーではない）
	err = c.repo.AddGroupMember(tx, req.GroupID, userID, false)
	if err != nil {
		slog.Error("Failed to add user to group", "error", err)
		http.Error(w, "Failed to join group", http.StatusInternalServerError)
		return
	}

	// トランザクションをコミット
	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		http.Error(w, "Failed to commit transaction", http.StatusInternalServerError)
		return
	}

	// レスポンスを作成
	response := JoinGroupResponse{
		GroupID:   req.GroupID,
		GroupName: group.Name,
		UserID:    userID,
		Message:   "Successfully joined the group",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode response", "error", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	slog.Info("User joined group successfully", "group_id", req.GroupID, "user_id", userID)
}
