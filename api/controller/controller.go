package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"domeal/model"

	"github.com/golang-jwt/jwt/v4"
)

type UserController struct {
	repo model.UserInterface
}

func NewUserController(repo model.UserInterface) *UserController {
	return &UserController{
		repo: repo,
	}
}

// LineCallbackHandler はLINEログインのコールバックを処理します
func (c *UserController) LineCallbackHandler(w http.ResponseWriter, r *http.Request) {
	// 認可コードの取得
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing code", http.StatusBadRequest)
		return
	}

	log.Println("Received code:", code)

	// エンドポイントへPOST
	tokenURL := "https://api.line.me/oauth2/v2.1/token"
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", "http://localhost:8080/api/line-callback")
	data.Set("client_id", os.Getenv("LINE_CLIENT_ID"))
	data.Set("client_secret", os.Getenv("LINE_CLIENT_SECRET"))

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

	// LINE IDからユーザーを検索
	lineID, ok := userInfo["sub"].(string)
	if !ok {
		http.Error(w, "Invalid LINE ID", http.StatusBadRequest)
		return
	}

	isSignUpComplete := false
	user, err := c.repo.GetUserByLineID(lineID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			isSignUpComplete = false
		} else {
			slog.Error("ユーザー登録しているかどうかの判定で論理的ではなくて技術的なエラーが発生した｡接続などを確認すべき｡", "error", err)
			http.Error(w, "ユーザ登録系でエラーが起きました", http.StatusInternalServerError)
			return
		}
	}

	var sessionID string

	if isSignUpComplete {
		slog.Info("ユーザーが登録済みなので更新のみ行います")
		//ユーザーがすでにこれまでにサービスを使っていたら更新のみ
		// トランザクション開始
		tx, err := c.repo.BeginTx(context.Background(), nil)
		if err != nil {
			slog.Error("トランザクションの開始に失敗した｡技術的な問題を確認すべき", "error", err)
			http.Error(w, "Failed to begin transaction", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()

		// セッション更新
		sessionID, err = c.repo.UpdateSessionIfExists(tx, user.ID)
		if err != nil {
			slog.Error("セッションの更新に失敗した｡技術的な問題を確認すべき", "error", err)
			http.Error(w, "Failed to update session", http.StatusInternalServerError)
			return
		}

		err = c.repo.UpdateToken(tx, user.ID, tokenResponse.AccessToken, tokenResponse.RefreshToken)
		if err != nil {
			slog.Error("トークンの更新に失敗した｡レコードの確認または技術的な問題を確認すべき｡", "error", err)
			http.Error(w, "Failed to update token", http.StatusInternalServerError)
			return
		}

		if err := tx.Commit(); err != nil {
			slog.Error("トランザクションのコミットに失敗した｡技術的な問題を確認すべき", "error", err)
			http.Error(w, "Failed to commit transaction", http.StatusInternalServerError)
			return
		}
	} else {
		// ユーザーが存在しない場合、新規登録
		slog.Info("ユーザーが存在しないため新規登録を行います")
		// トランザクション開始
		tx, err := c.repo.BeginTx(context.Background(), nil)
		if err != nil {
			http.Error(w, "Failed to begin transaction", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()

		// ユーザー情報をデータベースに保存
		userID, err := c.repo.SaveUserInfo(tx, userInfo)
		if err != nil {
			http.Error(w, "Failed to save user info", http.StatusInternalServerError)
			return
		}

		// トークン情報を保存
		err = c.repo.SaveUserToken(tx, userID, tokenResponse.AccessToken, tokenResponse.RefreshToken)
		if err != nil {
			http.Error(w, "Failed to save user token", http.StatusInternalServerError)
			return
		}

		// セッション作成
		sessionID, err = c.repo.CreateSession(tx, userID)
		if err != nil {
			http.Error(w, "Failed to create session", http.StatusInternalServerError)
			return
		}

		if err := tx.Commit(); err != nil {
			http.Error(w, "Failed to commit transaction", http.StatusInternalServerError)
			return
		}
	}

	// HTTP Only CookieにセッションIDをセット
	cookie := &http.Cookie{
		Name:     "session_id",
		Value:    sessionID,
		HttpOnly: true,
		Secure:   false, // 開発環境ではfalse、本番環境ではtrueに設定
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60, // 30日
	}
	http.SetCookie(w, cookie)

	http.Redirect(w, r, "https://www.google.com", http.StatusTemporaryRedirect)
}
