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

	// レスポンスを作成
	response := IssueSignedReceiptResponse{
		UploadURL: presignRequest.URL,
		FileKey:   fileKey,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode response", "error", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	slog.Info("Signed URL issued successfully", "user_id", userID, "file_key", fileKey)
}
