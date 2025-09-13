package router

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/GoRuGoo/domeal-backend/api/controller"
	"github.com/GoRuGoo/domeal-backend/api/middleware"
	"github.com/GoRuGoo/domeal-backend/api/model"
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
	flowRDB := model.NewFlowRedisRepository(tmpRdb)
	userController := controller.NewUserController(repo)
	groupController := controller.NewGroupController(repo, flowRDB)
	receiptController := controller.NewReceiptController(repo, repo, flowRDB, itemRDB)

	itemController := controller.NewItemController(itemRDB, repo, repo, repo)

	flowController := controller.NewFlowController(flowRDB)

	billingController := controller.NewBillingController(repo)

	// TODO: Redisの設定が必要
	// roleController := controller.NewRoleController(repo, redisRepo)

	mux := http.NewServeMux()

	// 認証不要エンドポイント
	mux.HandleFunc("/rest/line-callback", userController.LineCallbackHandler)
	mux.HandleFunc("/rest/check-login-status", userController.CheckLoginStatusHandler)

	// 認証が必要なエンドポイントは AuthMiddleware で個別にラップ
	mux.Handle("/rest/create-group",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(groupController.CreateGroupController)),
	)
	mux.Handle("/rest/join-group",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(groupController.JoinGroupController)),
	)
	mux.Handle("/rest/groups",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(groupController.GetGroupsHandler)),
	)
	mux.Handle("/rest/issue-signed-url",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(receiptController.IssueSignedS3URLHandler)),
	)
	mux.Handle("/rest/confirm-upload-and-start-ocr",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(receiptController.ConfirmUploadAndStartOCRHandler)),
	)

	mux.Handle("/rest/get-user-bill",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(billingController.GetUserBill)),
	)

	mux.Handle("/rest/complete-bill",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(billingController.CompleteBill)),
	)

	roleController := controller.NewRoleController(repo, rdb, flowRDB, repo)

	mux.Handle(
		"/ws/role-division",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(roleController.RoleDivisionController)),
	)

	mux.Handle(
		"/ws/select-item",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(itemController.SelectItemController)),
	)

	mux.Handle("/rest/subscribe-flow",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(flowController.SubscribeFlowHandler)),
	)

	mux.Handle("/rest/publish-flow",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(flowController.PublishFlowUpdate)),
	)

	// CORS設定で最外層をラップ
	return withCORS(mux)
}
