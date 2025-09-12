package main

import (
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/GoRuGoo/domeal-backend/api/model"
	"github.com/GoRuGoo/domeal-backend/api/router"
)

func main() {
	opts := &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelDebug,
	}

	// JSONハンドラを作成し、デフォルトのロガーに設定
	handler := slog.NewJSONHandler(os.Stdout, opts)
	slog.SetDefault(slog.New(handler))

	conn, err := model.InitDB()
	if err != nil {
		log.Fatal(err)
		panic(err)
	}
	if conn == nil {
		log.Fatal("Failed to connect to database")
	}
	defer conn.Close()

	router := router.NewRouter(conn)

	port := fmt.Sprintf(":%s", os.Getenv("PORT"))
	slog.Info("Starting server on ", "port", port)
	http.ListenAndServe(port, router.SetupRouter())
}
