package router

import (
	"database/sql"
	"domeal/controller"
	"domeal/middleware"
	"domeal/model"
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

func (r *Router) SetupRouter() {
	repo := model.NewRepository(r.db)
	userController := controller.NewUserController(repo)
	groupController := controller.NewGroupController(repo)
	receiptController := controller.NewReceiptController(repo)

	http.HandleFunc("/api/line-callback", userController.LineCallbackHandler)
	http.Handle(
		"/api/create-group",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(groupController.CreateGroupController)),
	)
	http.Handle(
		"/api/join-group",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(groupController.JoinGroupController)),
	)
	http.Handle(
		"/api/issue-signed-receipt",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(receiptController.IssueSignedS3URLHandler)),
	)
	http.Handle(
		"/api/confirm-upload-and-start-ocr",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(receiptController.ConfirmUploadAndStartOCRHandler)),
	)
}
