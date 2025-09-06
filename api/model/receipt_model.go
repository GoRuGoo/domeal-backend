package model

import (
	"context"
	"database/sql"
)

type ReceiptInterface interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}
