package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type Options struct {
	DSN          string
	MaxOpenConns int
	MaxIdleConns int
	ConnMaxLife  time.Duration
	ConnectTries int
	RetryDelay   time.Duration
}

func Open(ctx context.Context, opts Options, logger *slog.Logger) (*sql.DB, error) {
	if opts.RetryDelay <= 0 {
		opts.RetryDelay = 2 * time.Second
	}
	if opts.ConnectTries <= 0 {
		opts.ConnectTries = 1
	}

	db, err := sql.Open("mysql", opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	db.SetMaxOpenConns(opts.MaxOpenConns)
	db.SetMaxIdleConns(opts.MaxIdleConns)
	db.SetConnMaxLifetime(opts.ConnMaxLife)

	var pingErr error
	for attempt := 1; attempt <= opts.ConnectTries; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		pingErr = db.PingContext(pingCtx)
		cancel()
		if pingErr == nil {
			return db, nil
		}
		logger.Warn("database not ready, retrying", "attempt", attempt, "of", opts.ConnectTries, "err", pingErr)

		if attempt == opts.ConnectTries {
			break
		}
		select {
		case <-ctx.Done():
			_ = db.Close()
			return nil, ctx.Err()
		case <-time.After(opts.RetryDelay):
		}
	}
	_ = db.Close()
	return nil, fmt.Errorf("pinging database: %w", pingErr)
}
