package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultPostgresMaxConns = 10

func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return NewPoolWithMaxConns(ctx, dsn, defaultPostgresMaxConns)
}

func NewPoolWithMaxConns(ctx context.Context, dsn string, maxConns int) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if tracer := newTxnSQLDebugTracerFromEnv(); tracer != nil {
		cfg.ConnConfig.Tracer = tracer
	}
	if maxConns <= 0 {
		maxConns = defaultPostgresMaxConns
	}
	cfg.MaxConns = int32(maxConns)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
