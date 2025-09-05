package model

import (
	"context"
	"database/sql"
)

type GroupInterface interface {
	CreateGroup(tx *sql.Tx, group *Group) (int64, error)
	AddGroupMember(tx *sql.Tx, groupID, userID int64, isOwner bool) error
	GetGroup(groupID int64) (*Group, error)
	IsGroupMember(groupID, userID int64) (bool, error)
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

func (repo *Repository) GetGroup(groupID int64) (*Group, error) {
	query := `
		SELECT
			id, name, menu, menu_image_url, created_by
		FROM
			groups
		WHERE
			id = $1
	`

	stmt, err := repo.db.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	var group Group
	var menuImageURL sql.NullString
	err = stmt.QueryRow(groupID).Scan(
		&group.ID,
		&group.Name,
		&group.Menu,
		&menuImageURL,
		&group.CreatedBy,
	)

	if err != nil {
		return nil, err
	}

	if menuImageURL.Valid {
		group.MenuImageURL = menuImageURL.String
	}

	return &group, nil
}

func (repo *Repository) IsGroupMember(groupID, userID int64) (bool, error) {
	query := `
		SELECT COUNT(*)
		FROM
			group_members
		WHERE
			group_id = $1 AND user_id = $2
	`

	stmt, err := repo.db.Prepare(query)
	if err != nil {
		return false, err
	}
	defer stmt.Close()

	var count int
	err = stmt.QueryRow(groupID, userID).Scan(&count)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}
