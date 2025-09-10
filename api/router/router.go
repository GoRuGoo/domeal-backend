package router

import (
	"database/sql"
	"domeal/controller"
	"domeal/middleware"
	"domeal/model"
	"log/slog"
	"net/http"
)

type Router struct {
	db *sql.DB
}

func NewRouter(db *sql.DB) *Router {
	return &Router{
		db: db,
	}
}

// CORSをrouter側で設定
func withCORS(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORSヘッダー設定
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")

		// プリフライトリクエストの場合は即終了
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		// 通常リクエストは次へ
		handler.ServeHTTP(w, r)
	})
}

func (r *Router) SetupRouter() http.Handler {
	repo := model.NewRepository(r.db)
	tmpRdb, err := model.InitRedis()
	itemRDB := model.NewRedisItemRepository(tmpRdb)
	rdb := model.NewRedisRepository(tmpRdb)
	if err != nil {
		slog.Error("Failed to connect to Redis", "error", err)
		return withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Failed to connect to Redis", http.StatusInternalServerError)
		}))
	}
	userController := controller.NewUserController(repo)
	groupController := controller.NewGroupController(repo)
	receiptController := controller.NewReceiptController(repo, repo, itemRDB)

	itemController := controller.NewItemController(itemRDB, repo)

	flowRDB := model.NewFlowRedisRepository(tmpRdb)
	flowController := controller.NewFlowController(flowRDB)

	// TODO: Redisの設定が必要
	// roleController := controller.NewRoleController(repo, redisRepo)

	mux := http.NewServeMux()

	// 認証不要エンドポイント
	mux.HandleFunc("/api/line-callback", userController.LineCallbackHandler)
	mux.HandleFunc("/api/check-login-status", userController.CheckLoginStatusHandler)

	// 認証が必要なエンドポイントは AuthMiddleware で個別にラップ
	mux.Handle("/api/create-group",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(groupController.CreateGroupController)),
	)
	mux.Handle("/api/join-group",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(groupController.JoinGroupController)),
	)
	mux.Handle("/api/groups",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(groupController.GetGroupsHandler)),
	)
	mux.Handle("/api/issue-signed-url",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(receiptController.IssueSignedS3URLHandler)),
	)
	mux.Handle("/api/confirm-upload-and-start-ocr",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(receiptController.ConfirmUploadAndStartOCRHandler)),
	)

	roleController := controller.NewRoleController(repo, rdb, repo)

	mux.Handle(
		"/ws/role-division",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(roleController.RoleDivisionController)),
	)

	mux.Handle(
		"/ws/select-item",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(itemController.SelectItemController)),
	)

	mux.Handle("/api/subscribe-flow",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(flowController.SubscribeFlowHandler)),
	)

	mux.Handle("/api/publish-flow",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(flowController.PublishFlowUpdate)),
	)

	// CORS設定で最外層をラップ
	return withCORS(mux)
}
