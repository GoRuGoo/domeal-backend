package model

import (
	"context"
	"database/sql"
)

type RoleInterface interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

type RoleRedisInterface interface {
	test() error
}
