package router

import (
	"domeal/controller"
	"net/http"
)

func SetupRouter() {
	http.HandleFunc("/ws/line-callback", controller.WSHandler)
	http.HandleFunc("/api/line-callback", controller.LineCallbackHandler)
}
