package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v4"

	"github.com/gorilla/websocket"
)

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	lineClientID     = os.Getenv("LINE_CLIENT_ID")
	lineClientSecret = os.Getenv("LINE_CLIENT_SECRET")
	redirectURI      = "http://localhost:8080/api/line-callback"
)

// WebSocketハンドラ
func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	defer conn.Close()

	for {
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			log.Println("Read error:", err)
			break
		}
		log.Printf("Received: %s", msg)
		conn.WriteMessage(mt, []byte("Echo: "+string(msg)))
	}
}

// API: Helloテスト
func apiHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Hello from Go API!")
}

// LINEコールバックハンドラ
func lineCallbackHandler(w http.ResponseWriter, r *http.Request) {
	// ========================
	// 1. 認可コードを受け取る
	// ========================
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" {
		http.Error(w, "Missing code", http.StatusBadRequest)
		return
	}

	log.Println("Received code:", code)
	log.Println("Received state:", state)

	// ========================
	// 2. LINEのトークンエンドポイントへPOST
	// ========================
	tokenURL := "https://api.line.me/oauth2/v2.1/token"
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("client_id", lineClientID)
	data.Set("client_secret", lineClientSecret)

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to send request to LINE", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// レスポンス読み込み
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Failed to read response from LINE", http.StatusInternalServerError)
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Println("LINE token endpoint error:", string(body))
		http.Error(w, "LINE token request failed", resp.StatusCode)
		return
	}

	// JSONパース
	var tokenResponse struct {
		AccessToken  string `json:"access_token"`
		ExpiresIn    int    `json:"expires_in"`
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		http.Error(w, "Failed to parse token JSON", http.StatusInternalServerError)
		return
	}

	log.Println("Access Token:", tokenResponse.AccessToken)
	log.Println("ID Token:", tokenResponse.IDToken)

	// ========================
	// 3. JWT(id_token)をデコード
	// ========================
	claims := jwt.MapClaims{}
	parser := jwt.NewParser()

	// 署名検証は省略してデコードのみ
	_, _, err = parser.ParseUnverified(tokenResponse.IDToken, claims)
	if err != nil {
		http.Error(w, "Failed to parse id_token", http.StatusInternalServerError)
		return
	}

	// ユーザー情報を取得
	userInfo := map[string]interface{}{
		"sub":     claims["sub"],
		"name":    claims["name"],
		"picture": claims["picture"],
		"email":   claims["email"], // scopeにemailが含まれていれば取得可能
	}

	log.Println("User Info (from id_token):", userInfo)

	// ========================
	// 4. まとめて返却
	// ========================
	result := map[string]interface{}{
		"token": tokenResponse,
		"user":  userInfo,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func main() {
	http.HandleFunc("/api/hello", apiHandler)
	http.HandleFunc("/ws/echo", wsHandler)
	http.HandleFunc("/api/line-callback", lineCallbackHandler)

	log.Println("Starting server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
