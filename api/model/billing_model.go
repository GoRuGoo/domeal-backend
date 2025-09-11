package model

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

type UserBilling struct {
	ID          int64     `json:"id"`
	GroupID     int64     `json:"group_id"`
	ReceiptID   int64     `json:"receipt_id"`
	UserID      int64     `json:"user_id"`
	TotalAmount float64   `json:"total_amount"`
	Status      string    `json:"status"`
	Address     *int64    `json:"address,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type BillingInterface interface {
	StoreUserBilling(ctx context.Context, tx *sql.Tx, groupID, receiptID, userID, recipient int64, totalAmount float64) error
	StoreUserBillings(ctx context.Context, tx *sql.Tx, groupID, receiptID, recipient int64, userBills map[int64]float64) error
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// 個別の請求データを保存する
func (repo *Repository) StoreUserBilling(ctx context.Context, tx *sql.Tx, groupID, receiptID, userID, recipient int64, totalAmount float64) error {
	query := `
		INSERT INTO
			user_bills (group_id, receipt_id, user_id, recipient, total_amount, status, created_at, updated_at)
		VALUES
			($1, $2, $3, $4, $5, $6, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`

	_, err := tx.ExecContext(ctx, query, groupID, receiptID, userID, recipient, totalAmount, "pending")
	if err != nil {
		slog.Error("Failed to store user billing",
			"error", err,
			"group_id", groupID,
			"receipt_id", receiptID,
			"user_id", userID,
			"total_amount", totalAmount,
			"recipient", recipient,
		)
		return fmt.Errorf("failed to store user billing: %w", err)
	}

	slog.Info("Successfully stored user billing",
		"group_id", groupID,
		"receipt_id", receiptID,
		"user_id", userID,
		"total_amount", totalAmount,
		"recipient", recipient,
	)

	return nil
}

// 複数ユーザー分の請求を一括保存
func (repo *Repository) StoreUserBillings(ctx context.Context, tx *sql.Tx, groupID, receiptID, recipient int64, userBills map[int64]float64) error {
	for userID, totalAmount := range userBills {
		if err := repo.StoreUserBilling(ctx, tx, groupID, receiptID, userID, recipient, totalAmount); err != nil {
			return fmt.Errorf("failed to store billing for user %d: %w", userID, err)
		}
	}

	slog.Info("Successfully stored all user billings",
		"group_id", groupID,
		"receipt_id", receiptID,
		"user_count", len(userBills),
	)

	return nil
}
