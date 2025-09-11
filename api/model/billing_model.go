package model

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
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

type UserBillSummary struct {
	ID               int64  `json:"id"`                 // user_billsテーブルのid
	TotalAmount      int64  `json:"total_amount"`       // 小数点切り上げ後の金額
	PaypalMeUsername string `json:"paypal_me_username"` // 受取人のPayPal Me username
}

type BillingInterface interface {
	StoreUserBilling(ctx context.Context, tx *sql.Tx, groupID, receiptID, userID, recipient int64, totalAmount float64) error
	StoreUserBillings(ctx context.Context, tx *sql.Tx, groupID, receiptID, recipient int64, userBills map[int64]float64) error
	GetUserBillByUserID(ctx context.Context, userID int64) ([]UserBillSummary, error)
	UpdateBillStatus(ctx context.Context, billID int64, status string) error
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

// GetUserBillByUserID はユーザーIDでpendingステータスの請求書を取得し、小数点を切り上げして返す
func (repo *Repository) GetUserBillByUserID(ctx context.Context, userID int64) ([]UserBillSummary, error) {
	query := `
		SELECT
			ub.id, u.paypal_me_username, ub.total_amount
		FROM
			user_bills ub
		JOIN
			users u ON ub.recipient = u.id
		WHERE
			ub.user_id = $1
		AND
			ub.status = 'pending'
		ORDER BY
			ub.created_at DESC
	`

	rows, err := repo.db.QueryContext(ctx, query, userID)
	if err != nil {
		slog.Error("Failed to query user bills",
			"error", err,
			"user_id", userID)
		return nil, fmt.Errorf("failed to query user bills: %w", err)
	}
	defer rows.Close()

	var bills []UserBillSummary
	for rows.Next() {
		var id int64
		var paypalMeUsername string
		var totalAmount float64

		if err := rows.Scan(&id, &paypalMeUsername, &totalAmount); err != nil {
			slog.Error("Failed to scan user bill row",
				"error", err,
				"user_id", userID)
			return nil, fmt.Errorf("failed to scan user bill row: %w", err)
		}

		// 小数点以下は切り上げる
		roundedAmount := int64(math.Ceil(totalAmount))

		bills = append(bills, UserBillSummary{
			ID:               id,
			TotalAmount:      roundedAmount,
			PaypalMeUsername: paypalMeUsername,
		})
	}

	if err := rows.Err(); err != nil {
		slog.Error("Error iterating user bill rows",
			"error", err,
			"user_id", userID)
		return nil, fmt.Errorf("error iterating user bill rows: %w", err)
	}

	slog.Info("Successfully retrieved user bills",
		"user_id", userID,
		"bill_count", len(bills))

	return bills, nil
}

func (repo *Repository) UpdateBillStatus(ctx context.Context, billID int64, status string) error {
	query := `
		UPDATE user_bills
		SET status = $2, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`

	result, err := repo.db.ExecContext(ctx, query, billID, status)
	if err != nil {
		slog.Error("Failed to update bill status",
			"error", err,
			"bill_id", billID,
			"status", status)
		return fmt.Errorf("failed to update bill status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		slog.Error("Failed to get rows affected",
			"error", err,
			"bill_id", billID)
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		slog.Warn("No bill found with the given ID",
			"bill_id", billID)
		return fmt.Errorf("no bill found with ID %d", billID)
	}

	slog.Info("Successfully updated bill status",
		"bill_id", billID,
		"status", status,
		"rows_affected", rowsAffected)

	return nil
}
