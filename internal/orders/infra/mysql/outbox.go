package mysql

import (
	"context"
	"database/sql"
	"time"

	"github.com/codEvil1/stockly/internal/orders/app"
	"github.com/codEvil1/stockly/internal/orders/domain"
)

type OutboxStore struct {
	db  *sql.DB
	now func() time.Time
}

func NewOutboxStore(db *sql.DB) *OutboxStore {
	return &OutboxStore{db: db, now: func() time.Time { return time.Now().UTC() }}
}

var _ app.OutboxStore = (*OutboxStore)(nil)

func (s *OutboxStore) PublishPending(ctx context.Context, limit int, publish func(domain.Event) error) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx,
		`SELECT id, aggregate_id, type, payload, created_at
		   FROM outbox
		  WHERE published_at IS NULL
		  ORDER BY created_at, id
		  LIMIT ?
		    FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return 0, err
	}
	var pending []domain.Event
	for rows.Next() {
		var e domain.Event
		if err := rows.Scan(&e.ID, &e.AggregateID, &e.Type, &e.Payload, &e.OccurredAt); err != nil {
			_ = rows.Close()
			return 0, err
		}
		pending = append(pending, e)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()

	var published []domain.Event
	var publishErr error
	for _, e := range pending {
		if err := publish(e); err != nil {
			publishErr = err
			break
		}
		published = append(published, e)
	}

	if len(published) > 0 {
		args := make([]any, 0, len(published)+1)
		args = append(args, s.now())
		for _, e := range published {
			args = append(args, e.ID)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE outbox SET published_at = ? WHERE id IN (`+placeholders(len(published))+`)`, args...); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(published), publishErr
}
