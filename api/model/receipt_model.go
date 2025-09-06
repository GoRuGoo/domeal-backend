package model

import (
	"context"
	"database/sql"
	"time"
)

type ReceiptInterface interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
	CreateReceipt(tx *sql.Tx, receipt *Receipt) (int64, error)
	GetReceiptByID(receiptID int64) (*Receipt, error)
	UpdateReceiptUploadStatus(tx *sql.Tx, receiptID int64, isUploaded bool) error
	IsUserInGroup(groupID, userID int64) (bool, error)
	GetReceiptObjectKeyByGroupID(groupID int64) (string, error)
}

type Receipt struct {
	ID         int64     `json:"id"`
	GroupID    int64     `json:"group_id"`
	FileKey    string    `json:"file_key"`
	OcrStatus  string    `json:"ocr_status"` // "pending", "uploaded", "processed"
	IsUploaded bool      `json:"is_uploaded"`
	UploadedBy int64     `json:"uploaded_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (repo *Repository) CreateReceipt(tx *sql.Tx, receipt *Receipt) (int64, error) {
	query := `
		INSERT INTO
			receipts (group_id, file_key, ocr_status, uploaded_by,is_uploaded, created_at, updated_at)
		VALUES
			($1, $2, $3, $4,$5, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING
			id
	`

	stmt, err := tx.Prepare(query)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var receiptID int64
	err = stmt.QueryRow(
		receipt.GroupID,
		receipt.FileKey,
		receipt.OcrStatus,
		receipt.UploadedBy,
		receipt.IsUploaded,
	).Scan(&receiptID)

	if err != nil {
		return 0, err
	}

	return receiptID, nil
}

func (repo *Repository) GetReceiptByID(receiptID int64) (*Receipt, error) {
	query := `
		SELECT
			id, group_id, file_key, ocr_status, is_uploaded, uploaded_by, created_at, updated_at
		FROM
			receipts
		WHERE
			id = $1
	`

	stmt, err := repo.db.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	var receipt Receipt
	err = stmt.QueryRow(receiptID).Scan(
		&receipt.ID,
		&receipt.GroupID,
		&receipt.FileKey,
		&receipt.OcrStatus,
		&receipt.IsUploaded,
		&receipt.UploadedBy,
		&receipt.CreatedAt,
		&receipt.UpdatedAt,
	)

	if err != nil {
		return nil, err
	}

	return &receipt, nil
}

func (repo *Repository) UpdateReceiptUploadStatus(tx *sql.Tx, receiptID int64, isUploaded bool) error {
	query := `
		UPDATE
			receipts
		SET
			is_uploaded = $1, updated_at = CURRENT_TIMESTAMP
		WHERE
			id = $2
	`

	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(isUploaded, receiptID)
	return err
}

func (repo *Repository) IsUserInGroup(groupID, userID int64) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1
				FROM group_members
			WHERE
				group_id = $1 AND user_id = $2
		)
	`

	stmt, err := repo.db.Prepare(query)
	if err != nil {
		return false, err
	}
	defer stmt.Close()

	var exists bool
	err = stmt.QueryRow(groupID, userID).Scan(&exists)
	if err != nil {
		return false, err
	}

	return exists, nil
}

func (repo *Repository) GetReceiptObjectKeyByGroupID(groupID int64) (string, error) {
	query := `
		SELECT
			file_key
		FROM
			receipts
		WHERE
			group_id=$1
		ORDER BY
			created_at DESC
		LIMIT 1
	`

	stmt, err := repo.db.Prepare(query)
	if err != nil {
		return "", err
	}
	defer stmt.Close()

	var fileKey string
	err = stmt.QueryRow(groupID).Scan(&fileKey)
	if err != nil {
		return "", err
	}

	return fileKey, nil
}
