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
	GetGroupMembersCount(groupID int64) (int, error)
	GetAllGroups() ([]*Group, error)
	GetGroupMemberIDs(groupID int64) ([]int64, error)
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

type GroupMember struct {
	UserID      int64  `json:"user_id"`
	DisplayName string `json:"display_name"`
	PictureURL  string `json:"picture_url"`
	IsOwner     bool   `json:"is_owner"`
}

type Group struct {
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	Menu         string         `json:"menu"`
	MenuImageURL string         `json:"menu_image_url"`
	CreatedBy    int64          `json:"created_by"`
	Members      []*GroupMember `json:"members"`
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

func (repo *Repository) GetAllGroups() ([]*Group, error) {
	query := `
		SELECT
			id, name, menu, menu_image_url, created_by
		FROM
			groups
		ORDER BY
			created_at DESC
	`

	stmt, err := repo.db.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []*Group
	for rows.Next() {
		var group Group
		var menuImageURL sql.NullString

		err := rows.Scan(
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

		// グループのメンバー情報を取得
		members, err := repo.getGroupMembers(group.ID)
		if err != nil {
			return nil, err
		}
		group.Members = members

		groups = append(groups, &group)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return groups, nil
}

func (repo *Repository) getGroupMembers(groupID int64) ([]*GroupMember, error) {
	query := `
		SELECT
			u.id, u.display_name, u.picture_url, gm.is_owner
		FROM
			users u
		INNER JOIN
			group_members gm ON u.id = gm.user_id
		WHERE
			gm.group_id = $1
		ORDER BY
			gm.is_owner DESC, u.display_name ASC
	`

	stmt, err := repo.db.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []*GroupMember
	for rows.Next() {
		var member GroupMember
		var pictureURL sql.NullString

		err := rows.Scan(
			&member.UserID,
			&member.DisplayName,
			&pictureURL,
			&member.IsOwner,
		)
		if err != nil {
			return nil, err
		}

		if pictureURL.Valid {
			member.PictureURL = pictureURL.String
		}

		members = append(members, &member)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return members, nil
}

func (repo *Repository) GetGroupMembersCount(groupID int64) (int, error) {
	query := `
	SELECT
		COUNT(*)
	FROM
		group_members
	WHERE
		group_id = $1`

	var count int
	err := repo.db.QueryRow(query, groupID).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// GetGroupMemberIDs は指定された groupID に所属するユーザーIDのスライスを返します
func (repo *Repository) GetGroupMemberIDs(groupID int64) ([]int64, error) {
	query := `
		SELECT
			user_id
		FROM
			group_members
		WHERE
			group_id = $1
		ORDER BY
			user_id ASC
	`

	stmt, err := repo.db.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var userIDs []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		userIDs = append(userIDs, userID)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return userIDs, nil
}
