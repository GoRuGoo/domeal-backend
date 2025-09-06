package controller

import (
	"context"
	"domeal/middleware"
	"domeal/model"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

type ReceiptController struct {
	repo model.ReceiptInterface
}

func NewReceiptController(repo model.ReceiptInterface) *ReceiptController {
	return &ReceiptController{
		repo: repo,
	}
}

type IssueSignedReceiptRequest struct {
	GroupId string `json:"group_id"`
}

type IssueSignedReceiptResponse struct {
	UploadURL string `json:"upload_url"`
	FileKey   string `json:"file_key"`
	ReceiptID int64  `json:"receipt_id"`
}

func (c *ReceiptController) IssueSignedS3URLHandler(w http.ResponseWriter, r *http.Request) {
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
	var req IssueSignedReceiptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode request body", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.GroupId == "" {
		slog.Error("Group ID is required")
		http.Error(w, "Group ID is required", http.StatusBadRequest)
		return
	}

	// GroupIDを文字列からint64に変換
	groupID, err := strconv.ParseInt(req.GroupId, 10, 64)
	if err != nil {
		slog.Error("Invalid Group ID format", "group_id", req.GroupId, "error", err)
		http.Error(w, "Invalid Group ID format", http.StatusBadRequest)
		return
	}

	// 保存するS3キーを生成（グループID/UUID.png 形式）
	fileKey := fmt.Sprintf("%s/%s.png", req.GroupId, uuid.New().String())

	// S3バケット名を環境変数から取得
	bucketName := os.Getenv("S3_BUCKET_NAME")
	if bucketName == "" {
		slog.Error("S3_BUCKET_NAME environment variable is not set")
		http.Error(w, "S3 configuration error", http.StatusInternalServerError)
		return
	}

	// ===== AWS認証情報の取得 =====
	awsAccessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	awsSecretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	awsRegion := os.Getenv("AWS_REGION")

	if awsAccessKey == "" || awsSecretKey == "" || awsRegion == "" {
		slog.Error("AWS credentials or region are missing. Please check environment variables.")
		http.Error(w, "AWS configuration error", http.StatusInternalServerError)
		return
	}

	// AWS Config を明示的に認証情報付きでロード
	cfg, err := config.LoadDefaultConfig(
		context.TODO(),
		config.WithRegion(awsRegion),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(awsAccessKey, awsSecretKey, "")),
	)
	if err != nil {
		slog.Error("Failed to load AWS config", "error", err)
		http.Error(w, "Failed to load AWS config", http.StatusInternalServerError)
		return
	}

	// S3クライアント作成
	s3Client := s3.NewFromConfig(cfg)

	// Presignクライアント作成
	presignClient := s3.NewPresignClient(s3Client)

	// 署名付きURLを生成（15分有効）
	presignRequest, err := presignClient.PresignPutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(fileKey),
		ContentType: aws.String("image/png"),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = 15 * time.Minute
	})
	if err != nil {
		slog.Error("Failed to generate presigned URL", "error", err)
		http.Error(w, "Failed to generate presigned URL", http.StatusInternalServerError)
		return
	}

	// トランザクション開始してreceiptテーブルに書き込み
	tx, err := c.repo.BeginTx(context.Background(), nil)
	if err != nil {
		slog.Error("Failed to begin transaction", "error", err)
		http.Error(w, "Failed to begin transaction", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// Receipt構造体を作成
	receipt := &model.Receipt{
		GroupID:    groupID,
		FileKey:    fileKey,
		OcrStatus:  "pending", // 署名付きURL発行時点では"pending"
		UploadedBy: userID,
		IsUploaded: false,
	}

	// Receiptをデータベースに保存
	receiptID, err := c.repo.CreateReceipt(tx, receipt)
	if err != nil {
		slog.Error("Failed to create receipt record", "error", err)
		http.Error(w, "Failed to create receipt record", http.StatusInternalServerError)
		return
	}

	// トランザクションをコミット
	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		http.Error(w, "Failed to commit transaction", http.StatusInternalServerError)
		return
	}

	slog.Info("Signed URL issued and receipt record created successfully",
		"user_id", userID,
		"group_id", groupID,
		"receipt_id", receiptID,
		"file_key", fileKey)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// レスポンスを作成
	response := IssueSignedReceiptResponse{
		UploadURL: presignRequest.URL,
		FileKey:   fileKey,
		ReceiptID: receiptID,
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode response", "error", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

type ConfirmUploadRequest struct {
	ReceiptID int64 `json:"receipt_id"`
}

type ConfirmUploadResponse struct {
	Message   string `json:"message"`
	ReceiptID int64  `json:"receipt_id"`
	Status    string `json:"status"`
}

func (c *ReceiptController) ConfirmUploadAndStartOCRHandler(w http.ResponseWriter, r *http.Request) {
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
	var req ConfirmUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode request body", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.ReceiptID <= 0 {
		slog.Error("Receipt ID is required and must be positive")
		http.Error(w, "Invalid Receipt ID", http.StatusBadRequest)
		return
	}

	// receiptIDからreceipt情報を取得
	receipt, err := c.repo.GetReceiptByID(req.ReceiptID)
	if err != nil {
		slog.Error("Failed to get receipt", "receipt_id", req.ReceiptID, "error", err)
		http.Error(w, "Receipt not found", http.StatusNotFound)
		return
	}

	// ユーザーがグループに所属しているかチェック
	isMember, err := c.repo.IsUserInGroup(receipt.GroupID, userID)
	if err != nil {
		slog.Error("Failed to check group membership", "group_id", receipt.GroupID, "user_id", userID, "error", err)
		http.Error(w, "Failed to verify group membership", http.StatusInternalServerError)
		return
	}

	if !isMember {
		slog.Error("User is not a member of the group", "group_id", receipt.GroupID, "user_id", userID)
		http.Error(w, "User is not authorized to update this receipt", http.StatusForbidden)
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

	// アップロードフラグを true に更新
	err = c.repo.UpdateReceiptUploadStatus(tx, req.ReceiptID, true)
	if err != nil {
		slog.Error("Failed to update receipt upload status", "receipt_id", req.ReceiptID, "error", err)
		http.Error(w, "Failed to update receipt status", http.StatusInternalServerError)
		return
	}

	// トランザクションをコミット
	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		http.Error(w, "Failed to commit transaction", http.StatusInternalServerError)
		return
	}

	// レスポンスを作成
	response := ConfirmUploadResponse{
		Message:   "Receipt upload confirmed successfully",
		ReceiptID: req.ReceiptID,
		Status:    "uploaded",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode response", "error", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	slog.Info("Receipt upload confirmed successfully",
		"user_id", userID,
		"receipt_id", req.ReceiptID,
		"group_id", receipt.GroupID)
}
