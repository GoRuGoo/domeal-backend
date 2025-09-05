package controller

import (
	"domeal/middleware"
	"domeal/model"
	"net/http"
)

type RoomController struct {
	repo model.RoomInterface
}

func NewRoomController(repo model.RoomInterface) *RoomController {
	return &RoomController{
		repo: repo,
	}
}

func (c *RoomController) CreateRoomController(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetUserFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Room created successfully", "user": ` + user.DisplayName + `}`))
}
