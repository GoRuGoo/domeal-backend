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
	roomController := controller.NewRoomController(repo)

	http.HandleFunc("/api/line-callback", userController.LineCallbackHandler)
	http.Handle(
		"/api/create-room",
		middleware.AuthMiddleware(r.db)(http.HandlerFunc(roomController.CreateRoomController)),
	)
}
