package controller

import (
	"domeal/model"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
)

type FlowController struct {
	rdb model.FlowRedisInterface
}

func NewFlowController(rdb model.FlowRedisInterface) *FlowController {
	return &FlowController{
		rdb: rdb,
	}
}

func (c *FlowController) SubscribeFlowHandler(w http.ResponseWriter, r *http.Request) {
	groupIDStr := r.URL.Query().Get("group_id")
	if groupIDStr == "" {
		slog.Error("group_id parameter is required")
		http.Error(w, "group_id parameter is required", http.StatusBadRequest)
		return
	}

	groupID, err := strconv.ParseInt(groupIDStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid group_id", http.StatusBadRequest)
		return
	}

	// SSE用のヘッダー設定（レスポンスを書き始める前に設定）
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Cache-Control")

	// 即座にヘッダーを送信
	w.WriteHeader(http.StatusOK)

	channelName := fmt.Sprintf("group_flow:%d", groupID)
	slog.Info("Subscribing to channel", "channel", channelName)

	pubsub := c.rdb.Subscribe(r.Context(), channelName)
	defer pubsub.Close()

	notify := r.Context().Done()
	ch := pubsub.Channel()

	for {
		select {
		case <-notify:
			slog.Info("Client disconnected from flow synchronization", "channel", channelName)
			return
		case msg := <-ch:
			if msg == nil {
				continue
			}
			slog.Info("Received message on channel", "channel", channelName, "message", msg.Payload)
			fmt.Fprintf(w, "data: %s\n\n", msg.Payload)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			slog.Info("Sent message to client", "channel", channelName, "message", msg.Payload)
		}
	}
}

func (c *FlowController) PublishFlowUpdate(w http.ResponseWriter, r *http.Request) {
	groupIDStr := r.URL.Query().Get("group_id")
	if groupIDStr == "" {
		http.Error(w, "group_id is required", http.StatusBadRequest)
		return
	}

	groupID, err := strconv.ParseInt(groupIDStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid group_id", http.StatusBadRequest)
		return
	}

	message := r.URL.Query().Get("msg")
	if message == "" {
		http.Error(w, "msg is required", http.StatusBadRequest)
		return
	}

	channelName := fmt.Sprintf("group_flow:%d", groupID)
	slog.Info("Publishing message to channel", "channel", channelName, "message", message)

	err = c.rdb.Publish(r.Context(), channelName, message)
	if err != nil {
		slog.Error("Failed to publish message", "error", err, "channel", channelName)
		http.Error(w, "failed to publish message", http.StatusInternalServerError)
		return
	}

	slog.Info("Successfully published message", "channel", channelName, "message", message)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"published"}`))
}
