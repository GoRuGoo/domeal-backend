package controller

import (
	"context"
	"database/sql"
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
	"github.com/sashabaranov/go-openai"
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

	// OCRに送るためにオブジェクトキーを取得
	objectKey, err := c.repo.GetReceiptObjectKeyByGroupID(receipt.GroupID)
	if err != nil {
		slog.Error("Failed to get receipt object key", "group_id", receipt.GroupID, "error", err)
		http.Error(w, "Failed to get receipt object key", http.StatusInternalServerError)
		return
	}

	imageURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", os.Getenv("S3_BUCKET_NAME"), os.Getenv("AWS_REGION"), objectKey)

	// ChatGPT APIでOCR処理を実行
	ocrResult, err := c.performOCRWithChatGPT(imageURL)
	if err != nil {
		slog.Error("Failed to perform OCR", "image_url", imageURL, "error", err)
		// OCRエラーでもレスポンスは返す（アップロードは成功）
	} else {
		// OCR結果をパースしてデータベースに保存
		if err := c.saveOCRResultToDB(tx, req.ReceiptID, receipt.GroupID, ocrResult); err != nil {
			slog.Error("Failed to save OCR result to database", "receipt_id", req.ReceiptID, "error", err)
			// データベース保存エラーでもトランザクションは継続
		} else {
			slog.Info("OCR completed and saved successfully",
				"receipt_id", req.ReceiptID,
				"group_id", receipt.GroupID)
		}
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

// performOCRWithChatGPT はChatGPT APIを使用してOCR処理を行います
func (c *ReceiptController) performOCRWithChatGPT(imageURL string) (string, error) {
	openaiAPIKey := os.Getenv("OPENAI_API_KEY")
	if openaiAPIKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY environment variable is not set")
	}

	client := openai.NewClient(openaiAPIKey)

	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: "gpt-5-nano",
			Messages: []openai.ChatCompletionMessage{
				{
					Role: openai.ChatMessageRoleUser,
					MultiContent: []openai.ChatMessagePart{
						{
							Type: openai.ChatMessagePartTypeText,
							Text: `この画像はレシートです。以下のJSON形式で商品情報を抽出してください：
								{
								"date": "日付",
								"total": 合計金額(数値),
								"items": [
									{
									"name": "商品名（レシートに記載されている正確な文字列）",
									"predict_name": "商品の予想される正式名称（略語や途切れた名前を補完した推測）",
									"price": 価格(数値),
									"quantity": 数量(数値、記載がない場合は1)
									}
								]
								}
								数値は必ず数字のみで回答してください。predict_nameは商品名が途切れている場合や略語の場合に正式名称を推測して入力してください。それ以外の場合(商品名が途切れることなく適切に確認できる)は､predict_item_nameにいれる必要はないです｡JSONの形式を厳密に守ってください。`,
						},
						{
							Type: openai.ChatMessagePartTypeImageURL,
							ImageURL: &openai.ChatMessageImageURL{
								URL: imageURL,
							},
						},
					},
				},
			},
		},
	)

	if err != nil {
		return "", fmt.Errorf("failed to call OpenAI API: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no response from OpenAI API")
	}

	return resp.Choices[0].Message.Content, nil
}

type OCRReceiptData struct {
	Items []OCRPurchaseItem `json:"items"`
}

type OCRPurchaseItem struct {
	Name        string  `json:"name"`
	PredictName string  `json:"predict_name"`
	Price       float64 `json:"price"`
	Quantity    int     `json:"quantity"`
}

// OCRReceiptData はOCR結果の構造体
func (c *ReceiptController) saveOCRResultToDB(tx *sql.Tx, receiptID, groupID int64, ocrResult string) error {
	// OCR結果をJSONとしてパース
	var receiptData OCRReceiptData
	if err := json.Unmarshal([]byte(ocrResult), &receiptData); err != nil {
		slog.Error("Failed to parse OCR result as JSON", "error", err, "ocr_result", ocrResult)
		return fmt.Errorf("failed to parse OCR result: %w", err)
	}

	// 購入商品をpurchase_itemsテーブルに保存
	var purchaseItems []model.PurchaseItem
	for _, item := range receiptData.Items {
		purchaseItems = append(purchaseItems, model.PurchaseItem{
			ReceiptID:       receiptID,
			GroupID:         groupID,
			ItemName:        item.Name,
			PredictItemName: item.PredictName,
			Price:           item.Price,
			Quantity:        item.Quantity,
		})
	}

	if err := c.repo.SavePurchaseItems(tx, receiptID, groupID, purchaseItems); err != nil {
		return fmt.Errorf("failed to save purchase items: %w", err)
	}

	// OCRステータスを "completed" に更新
	if err := c.repo.UpdateReceiptOCRStatus(tx, receiptID, "completed"); err != nil {
		return fmt.Errorf("failed to update OCR status: %w", err)
	}

	slog.Info("OCR result saved to database successfully",
		"receipt_id", receiptID,
		"group_id", groupID,
		"items_count", len(purchaseItems))

	return nil
}
