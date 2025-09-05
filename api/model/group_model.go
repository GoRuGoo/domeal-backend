package model

import (
	"context"
	"database/sql"
)

type GroupInterface interface {
	CreateGroup(tx *sql.Tx, group *Group) (int64, error)
	AddGroupMember(tx *sql.Tx, groupID, userID int64, isOwner bool) error
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

type Group struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Menu         string `json:"menu"`
	MenuImageURL string `json:"menu_image_url"`
	CreatedBy    int64  `json:"created_by"`
}

func (repo *Repository) CreateGroup(tx *sql.Tx, group *Group) (int64, error) {
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

	var groupID int64
	err = stmt.QueryRow(
		group.Name,
		group.Menu,
		group.MenuImageURL,
		group.CreatedBy,
	).Scan(&groupID)

	if err != nil {
		return 0, err
	}

	return groupID, nil
}

func (repo *Repository) AddGroupMember(tx *sql.Tx, groupID, userID int64, isOwner bool) error {
	query := `
		INSERT INTO group_members (group_id, user_id, is_owner, joined_at)
		VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
	`

	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(groupID, userID, isOwner)
	if err != nil {
		return err
	}

	return nil
}
