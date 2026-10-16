package mysql

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/codEvil1/stockly/internal/orders/domain"
)

const (
	mysqlErrDuplicateEntry = 1062
	idempotencyIndexName   = "uq_orders_idempotency_key"
)

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

var _ domain.Repository = (*Repository)(nil)

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (r *Repository) Insert(ctx context.Context, o *domain.Order, idempotencyKey string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var key any
	if idempotencyKey != "" {
		key = idempotencyKey
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO orders (id, customer_id, status, total_cents, created_at, idempotency_key)
		 VALUES (?,?,?,?,?,?)`,
		o.ID, o.CustomerID, o.Status, o.Total(), o.CreatedAt, key); err != nil {
		if isDuplicateIdempotencyKey(err) {
			return domain.ErrDuplicateKey
		}
		return err
	}
	for _, it := range o.Items {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO order_items (order_id, sku, quantity, price_cents) VALUES (?,?,?,?)`,
			o.ID, it.SKU, it.Quantity, it.PriceCents); err != nil {
			return err
		}
	}
	if err := insertEvents(ctx, tx, o.Events()); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) Update(ctx context.Context, o *domain.Order, from domain.Status) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`UPDATE orders SET status = ? WHERE id = ? AND status = ?`, o.Status, o.ID, from)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM orders WHERE id = ?`, o.ID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		return domain.ErrInvalidTransition
	}
	if err := insertEvents(ctx, tx, o.Events()); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) FindByID(ctx context.Context, id string) (*domain.Order, error) {
	return r.findOne(ctx, `id = ?`, id)
}

func (r *Repository) FindByIdempotencyKey(ctx context.Context, key string) (*domain.Order, error) {
	return r.findOne(ctx, `idempotency_key = ?`, key)
}

func (r *Repository) findOne(ctx context.Context, where string, arg any) (*domain.Order, error) {
	var id, customerID, status string
	var createdAt time.Time
	err := r.db.QueryRowContext(ctx,
		`SELECT id, customer_id, status, created_at FROM orders WHERE `+where, arg).
		Scan(&id, &customerID, &status, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	items, err := r.loadItems(ctx, []string{id})
	if err != nil {
		return nil, err
	}
	return domain.Rehydrate(id, customerID, domain.Status(status), items[id], createdAt), nil
}

func (r *Repository) List(ctx context.Context, f domain.ListFilter) ([]*domain.Order, error) {
	query := `SELECT id, customer_id, status, created_at FROM orders`
	var args []any
	if f.CustomerID != "" {
		query += ` WHERE customer_id = ?`
		args = append(args, f.CustomerID)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, f.Limit, f.Offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type header struct {
		id, customerID, status string
		createdAt              time.Time
	}
	var headers []header
	for rows.Next() {
		var h header
		if err := rows.Scan(&h.id, &h.customerID, &h.status, &h.createdAt); err != nil {
			return nil, err
		}
		headers = append(headers, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()

	orders := make([]*domain.Order, 0, len(headers))
	if len(headers) == 0 {
		return orders, nil
	}

	ids := make([]string, len(headers))
	for i, h := range headers {
		ids[i] = h.id
	}
	items, err := r.loadItems(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, h := range headers {
		orders = append(orders, domain.Rehydrate(h.id, h.customerID, domain.Status(h.status), items[h.id], h.createdAt))
	}
	return orders, nil
}

func (r *Repository) loadItems(ctx context.Context, orderIDs []string) (map[string][]domain.Item, error) {
	args := make([]any, len(orderIDs))
	for i, id := range orderIDs {
		args[i] = id
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT order_id, sku, quantity, price_cents FROM order_items
		 WHERE order_id IN (`+placeholders(len(orderIDs))+`) ORDER BY order_id, sku`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]domain.Item, len(orderIDs))
	for rows.Next() {
		var orderID string
		var it domain.Item
		if err := rows.Scan(&orderID, &it.SKU, &it.Quantity, &it.PriceCents); err != nil {
			return nil, err
		}
		out[orderID] = append(out[orderID], it)
	}
	return out, rows.Err()
}

func insertEvents(ctx context.Context, tx execer, events []domain.Event) error {
	for _, e := range events {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO outbox (id, aggregate_id, type, payload, created_at) VALUES (?,?,?,?,?)`,
			e.ID, e.AggregateID, e.Type, e.Payload, e.OccurredAt); err != nil {
			return err
		}
	}
	return nil
}

func isDuplicateIdempotencyKey(err error) bool {
	var me *mysqldriver.MySQLError
	return errors.As(err, &me) &&
		me.Number == mysqlErrDuplicateEntry &&
		strings.Contains(me.Message, idempotencyIndexName)
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
