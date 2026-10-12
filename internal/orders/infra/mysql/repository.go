package mysql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/codEvil1/stockly/internal/orders/domain"
)

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

func (r *Repository) Insert(ctx context.Context, o *domain.Order) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO orders (id, customer_id, status, total_cents, created_at) VALUES (?,?,?,?,?)`,
		o.ID, o.CustomerID, o.Status, o.Total(), o.CreatedAt); err != nil {
		return err
	}
	for _, it := range o.Items {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO order_items (order_id, sku, quantity, price_cents) VALUES (?,?,?,?)`,
			o.ID, it.SKU, it.Quantity, it.PriceCents); err != nil {
			return err
		}
	}
	for _, e := range o.Events() {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO outbox (id, aggregate_id, type, payload, created_at) VALUES (?,?,?,?,?)`,
			e.ID, o.ID, e.Type, e.Payload, time.Now().UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repository) FindByID(ctx context.Context, id string) (*domain.Order, error) {
	var customerID, status string
	var createdAt time.Time
	err := r.db.QueryRowContext(ctx,
		`SELECT customer_id, status, created_at FROM orders WHERE id = ?`, id).
		Scan(&customerID, &status, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT sku, quantity, price_cents FROM order_items WHERE order_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []domain.Item
	for rows.Next() {
		var it domain.Item
		if err := rows.Scan(&it.SKU, &it.Quantity, &it.PriceCents); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return domain.Rehydrate(id, customerID, domain.Status(status), items, createdAt), nil
}