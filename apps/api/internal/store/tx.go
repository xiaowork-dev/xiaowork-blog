package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// TxBeginner is satisfied by *pgxpool.Pool, *pgx.Conn and pgx.Tx (nested calls become savepoints).
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// ExecTx runs fn inside one transaction: it commits when fn returns nil and rolls back on any error or panic.
// Multi-table changes (draft save, publish, token consumption) must go through it so they are atomic (DD §2.1).
func ExecTx(ctx context.Context, db TxBeginner, fn func(*Queries) error) error {
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		return fn(New(tx))
	})
}
