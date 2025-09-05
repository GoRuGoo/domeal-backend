package model

import (
	"context"
	"database/sql"
)

type RoomInterface interface {
	CreateRoom(tx *sql.Tx, room *Room) (int64, error)
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

type Room struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Menu         string `json:"menu"`
	MenuImageURL string `json:"menu_image_url"`
	CreatedBy    int64  `json:"created_by"`
}

func (repo *Repository) CreateRoom(tx *sql.Tx, room *Room) (int64, error) {
	query := `
		INSERT INTO
			groups (name, menu, menu_image_url, created_by, created_at)
		VALUES
			($1, $2, $3, $4, CURRENT_TIMESTAMP)
		RETURNING id
	`

	stmt, err := tx.Prepare(query)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var roomID int64
	err = stmt.QueryRow(
		room.Name,
		room.Menu,
		room.MenuImageURL,
		room.CreatedBy,
	).Scan(&roomID)

	if err != nil {
		return 0, err
	}

	return roomID, nil
}
