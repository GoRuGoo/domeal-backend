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

	http.HandleFunc("/api/line-callback", userController.LineCallbackHandler)
	http.Handle(
		"/api/create-group",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(groupController.CreateGroupController)),
	)
	http.Handle(
		"/api/join-group",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(groupController.JoinGroupController)),
	)
}
