//go:build integration

package mysql_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"

	"github.com/codEvil1/stockly/internal/orders/domain"
	"github.com/codEvil1/stockly/internal/orders/infra/mysql"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		dsn = "root:root@tcp(127.0.0.1:3307)/stockly?parseTime=true&loc=UTC"
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("database not reachable (did you run `make up`?): %v", err)
	}
	return db
}

func newOrder(t *testing.T, customerID string, items ...domain.Item) *domain.Order {
	t.Helper()
	o, err := domain.NewOrder(uuid.NewString(), customerID, items, time.Now().UTC().Truncate(time.Microsecond))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestInsertAndFind(t *testing.T) {
	ctx := context.Background()
	repo := mysql.NewRepository(openDB(t))
	o := newOrder(t, "cust-"+uuid.NewString()[:8],
		domain.Item{SKU: "B", Quantity: 1, PriceCents: 550},
		domain.Item{SKU: "A", Quantity: 2, PriceCents: 1000})

	if err := repo.Insert(ctx, o, ""); err != nil {
		t.Fatal(err)
	}
	got, err := repo.FindByID(ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total() != 2550 || got.Status != domain.StatusCreated || len(got.Items) != 2 {
		t.Errorf("unexpected order: %+v", got)
	}
	if got.Items[0].SKU != "A" {
		t.Errorf("items must be ordered by sku, got %+v", got.Items)
	}
	if !got.CreatedAt.Equal(o.CreatedAt) {
		t.Errorf("created_at mismatch: %v vs %v", got.CreatedAt, o.CreatedAt)
	}

	if _, err := repo.FindByID(ctx, uuid.NewString()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestIdempotencyKeyIsUnique(t *testing.T) {
	ctx := context.Background()
	repo := mysql.NewRepository(openDB(t))
	key := "key-" + uuid.NewString()

	first := newOrder(t, "c1", domain.Item{SKU: "A", Quantity: 1, PriceCents: 1})
	if err := repo.Insert(ctx, first, key); err != nil {
		t.Fatal(err)
	}
	second := newOrder(t, "c1", domain.Item{SKU: "A", Quantity: 1, PriceCents: 1})
	if err := repo.Insert(ctx, second, key); !errors.Is(err, domain.ErrDuplicateKey) {
		t.Fatalf("expected ErrDuplicateKey, got %v", err)
	}
	if _, err := repo.FindByID(ctx, second.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the rejected order must not be persisted (transaction rollback)")
	}
	found, err := repo.FindByIdempotencyKey(ctx, key)
	if err != nil || found.ID != first.ID {
		t.Errorf("expected first order, got %+v err=%v", found, err)
	}
}

func TestUpdateIsConditionalOnPreviousStatus(t *testing.T) {
	ctx := context.Background()
	repo := mysql.NewRepository(openDB(t))
	o := newOrder(t, "c1", domain.Item{SKU: "A", Quantity: 1, PriceCents: 1})
	if err := repo.Insert(ctx, o, ""); err != nil {
		t.Fatal(err)
	}

	a, _ := repo.FindByID(ctx, o.ID)
	b, _ := repo.FindByID(ctx, o.ID)
	now := time.Now().UTC()
	if err := a.Complete(now); err != nil {
		t.Fatal(err)
	}
	if err := b.Cancel(now); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, a, domain.StatusCreated); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, b, domain.StatusCreated); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition for the loser, got %v", err)
	}
	final, _ := repo.FindByID(ctx, o.ID)
	if final.Status != domain.StatusCompleted {
		t.Errorf("expected COMPLETED, got %s", final.Status)
	}

	ghost := domain.Rehydrate(uuid.NewString(), "c", domain.StatusCompleted, nil, now)
	if err := repo.Update(ctx, ghost, domain.StatusCreated); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown order, got %v", err)
	}
}

func TestListFiltersAndPaginates(t *testing.T) {
	ctx := context.Background()
	repo := mysql.NewRepository(openDB(t))
	customer := "cust-" + uuid.NewString()[:8]

	base := time.Now().UTC().Truncate(time.Microsecond)
	var ids []string
	for i := 0; i < 3; i++ {
		o, err := domain.NewOrder(uuid.NewString(), customer,
			[]domain.Item{{SKU: "A", Quantity: 1, PriceCents: int64(i + 1)}}, base.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Insert(ctx, o, ""); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, o.ID)
	}

	page1, err := repo.List(ctx, domain.ListFilter{CustomerID: customer, Limit: 2, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	page2, err := repo.List(ctx, domain.ListFilter{CustomerID: customer, Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || len(page2) != 1 {
		t.Fatalf("expected pages of 2 and 1, got %d and %d", len(page1), len(page2))
	}
	if page1[0].ID != ids[2] || page1[1].ID != ids[1] || page2[0].ID != ids[0] {
		t.Errorf("unexpected order: %s %s %s", page1[0].ID, page1[1].ID, page2[0].ID)
	}
	if len(page1[0].Items) != 1 {
		t.Errorf("list must load items, got %+v", page1[0].Items)
	}

	none, err := repo.List(ctx, domain.ListFilter{CustomerID: "nobody-" + uuid.NewString(), Limit: 10})
	if err != nil || len(none) != 0 {
		t.Errorf("expected empty result, got %d err=%v", len(none), err)
	}
}

func TestOutboxRelayFlow(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	repo := mysql.NewRepository(db)
	store := mysql.NewOutboxStore(db)

	if _, err := db.ExecContext(ctx, `UPDATE outbox SET published_at = NOW(6) WHERE published_at IS NULL`); err != nil {
		t.Fatal(err)
	}

	o := newOrder(t, "c1", domain.Item{SKU: "A", Quantity: 1, PriceCents: 1})
	if err := repo.Insert(ctx, o, ""); err != nil {
		t.Fatal(err)
	}
	if err := o.Complete(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, o, domain.StatusCreated); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("broker down")
	n, err := store.PublishPending(ctx, 10, func(domain.Event) error { return boom })
	if !errors.Is(err, boom) || n != 0 {
		t.Fatalf("expected failure with 0 published, got n=%d err=%v", n, err)
	}

	var types []string
	n, err = store.PublishPending(ctx, 10, func(e domain.Event) error {
		types = append(types, e.Type)
		return nil
	})
	if err != nil || n != 2 {
		t.Fatalf("expected 2 published events, got n=%d err=%v", n, err)
	}
	if len(types) != 2 || types[0] != domain.EventOrderCreated || types[1] != domain.EventOrderCompleted {
		t.Errorf("unexpected events/order: %v", types)
	}

	n, err = store.PublishPending(ctx, 10, func(domain.Event) error { return nil })
	if err != nil || n != 0 {
		t.Errorf("outbox must be empty after publishing, got n=%d err=%v", n, err)
	}
}
