package model

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{
		db: db,
	}
}

type UserInterface interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
	SaveUserInfo(tx *sql.Tx, userInfo map[string]interface{}) (int64, error)
	GetUserByLineID(lineID string) (*User, error)
	CreateSession(tx *sql.Tx, userID int64) (string, error)
	UpdateSessionIfExists(tx *sql.Tx, userID int64) (string, error)
	UpdateToken(tx *sql.Tx, userID int64, accessToken, refreshToken string) error
	SaveUserToken(tx *sql.Tx, userID int64, accessToken, refreshToken string) error
	GetUserBySessionToken(sessionToken string) (*User, error)
}

type User struct {
	ID     int64  `json:"id"`
	LineID string `json:"line_id"`
	Name   string `json:"name"`

	Picture string `json:"picture"`
	Email   string `json:"email"`
}

func (repo *Repository) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return repo.db.BeginTx(ctx, opts)
}

func (repo *Repository) SaveUserInfo(tx *sql.Tx, userInfo map[string]interface{}) (int64, error) {
	query := `
		INSERT INTO
			users (line_sub, display_name, picture_url, created_at, updated_at)
		VALUES
			($1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`

	stmt, err := tx.Prepare(query)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var userID int64
	err = stmt.QueryRow(
		userInfo["sub"],
		userInfo["name"],
		userInfo["picture"],
	).Scan(&userID)

	if err != nil {
		return 0, err
	}

	return userID, nil
}

func (repo *Repository) GetUserByLineID(lineID string) (*User, error) {
	query := `
		SELECT
			id,line_sub,display_name,picture_url
		FROM
			users
		WHERE
			line_sub = $1
	`

	stmt, err := repo.db.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	var user User
	var pictureURL sql.NullString
	err = stmt.QueryRow(lineID).Scan(
		&user.ID,
		&user.LineID,
		&user.Name,
		&pictureURL,
	)

	if err != nil {
		return nil, err
	}

	if pictureURL.Valid {
		user.Picture = pictureURL.String
	}

	return &user, nil
}

func (repo *Repository) CreateSession(tx *sql.Tx, userID int64) (string, error) {
	sessionID, err := generateSessionID(16)
	if err != nil {
		return "", err
	}

	query := `
		INSERT INTO
			sessions (user_id, session_token, created_at, last_used_at)
		VALUES
			($1, $2, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`

	stmt, err := tx.Prepare(query)
	if err != nil {
		return "", err
	}
	defer stmt.Close()

	_, err = stmt.Exec(userID, sessionID)
	if err != nil {
		return "", err
	}

	return sessionID, nil
}

func (repo *Repository) UpdateSessionIfExists(tx *sql.Tx, userID int64) (string, error) {
	sessionID, err := generateSessionID(16)
	if err != nil {
		return "", err
	}

	query := `
		UPDATE
			sessions
		SET
			session_token = $1, last_used_at = CURRENT_TIMESTAMP
		WHERE
			user_id = $2
	`

	stmt, err := tx.Prepare(query)
	if err != nil {
		return "", err
	}
	defer stmt.Close()

	result, err := stmt.Exec(sessionID, userID)
	if err != nil {
		return "", err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}

	if rowsAffected == 0 {
		return "", errors.New("セッションが存在しないのに更新はできません")
	}

	return sessionID, nil
}

func generateSessionID(n int) (string, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (repo *Repository) UpdateToken(tx *sql.Tx, userID int64, accessToken, refreshToken string) error {
	query := `
		UPDATE
			user_tokens
		SET
			access_token = $1,refresh_token=$2
		WHERE
			user_id = $3
	`

	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	result, err := stmt.Exec(accessToken, refreshToken, userID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return errors.New("セッションが存在しないのに更新はできません")
	}

	return nil
}

func (repo *Repository) SaveUserToken(tx *sql.Tx, userID int64, accessToken, refreshToken string) error {
	query := `
		INSERT INTO
			user_tokens (user_id, access_token, refresh_token, created_at, updated_at)
		VALUES
			($1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`

	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(userID, accessToken, refreshToken)
	if err != nil {
		return err
	}

	return nil
}

func (repo *Repository) GetUserBySessionToken(sessionToken string) (*User, error) {
	query := `
		SELECT
			u.id, u.line_sub, u.display_name, u.picture_url
		FROM
			users u
		INNER
			JOIN sessions s ON u.id = s.user_id
		WHERE
			s.session_token = $1 AND s.last_used_at > NOW() - INTERVAL '30 days'
	`

	stmt, err := repo.db.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	var user User
	var pictureURL sql.NullString
	err = stmt.QueryRow(sessionToken).Scan(
		&user.ID,
		&user.LineID,
		&user.Name,
		&pictureURL,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("session not found or expired")
		}
		return nil, err
	}

	if pictureURL.Valid {
		user.Picture = pictureURL.String
	}

	return &user, nil
}
