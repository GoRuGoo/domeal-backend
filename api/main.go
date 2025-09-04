package main

import (
	"domeal/model"
	"domeal/router"
	"log"
	"log/slog"
	"net/http"
	"os"
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
	router.SetupRouter()

	log.Println("Starting server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
