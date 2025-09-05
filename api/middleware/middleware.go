package middleware

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// contextにユーザー情報を格納するためのキー
type contextKey string

const userContextKey contextKey = "user"

// User 構造体
type User struct {
	ID          int
	DisplayName string
	LineSub     string
}

// 認証ミドルウェア
func AuthMiddleware(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Cookieからsession_idを取得
			cookie, err := r.Cookie("session_id")
			if err != nil {
				http.Error(w, "Unauthorized: missing session", http.StatusUnauthorized)
				return
			}

			sessionToken := cookie.Value

			slog.Info("Authenticating user with session token", "session_token", sessionToken)

			// セッションをDBから確認
			var user User
			var lastUsedAt time.Time
			err = db.QueryRow(`
                SELECT
					u.id, u.display_name, u.line_sub, s.last_used_at
                FROM
					sessions s
                JOIN
					users u ON s.user_id = u.id
                WHERE
					s.session_token = $1
            `, sessionToken).Scan(&user.ID, &user.DisplayName, &user.LineSub, &lastUsedAt)

			if err == sql.ErrNoRows {
				http.Error(w, "Unauthorized: invalid session", http.StatusUnauthorized)
				return
			} else if err != nil {
				fmt.Println("DB error:", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			// 最終利用日時を更新（任意）
			_, err = db.Exec(`
                UPDATE
					sessions
                SET
					last_used_at = NOW()
                WHERE
					session_token = $1
            `, sessionToken)
			if err != nil {
				fmt.Println("Failed to update last_used_at:", err)
			}

			// ユーザー情報をcontextに保存
			ctx := context.WithValue(r.Context(), userContextKey, &user)

			slog.Info("User authenticated", "user_id", user.ID)

			// 次のハンドラーへ
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// contextからUserを取り出すヘルパー
func GetUserFromContext(ctx context.Context) (*User, bool) {
	user, ok := ctx.Value(userContextKey).(*User)
	return user, ok
}
