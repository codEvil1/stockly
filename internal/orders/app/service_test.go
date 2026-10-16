package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/codEvil1/stockly/internal/orders/domain"
)

type fakeRepo struct {
	mu       sync.Mutex
	orders   map[string]*domain.Order
	keys     map[string]string
	inserts  int
	failNext error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{orders: map[string]*domain.Order{}, keys: map[string]string{}}
}

func (f *fakeRepo) Insert(_ context.Context, o *domain.Order, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return err
	}
	if key != "" {
		if _, ok := f.keys[key]; ok {
			return domain.ErrDuplicateKey
		}
		f.keys[key] = o.ID
	}
	f.orders[o.ID] = domain.Rehydrate(o.ID, o.CustomerID, o.Status, o.Items, o.CreatedAt)
	f.inserts++
	return nil
}

func (f *fakeRepo) Update(_ context.Context, o *domain.Order, from domain.Status) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.orders[o.ID]
	if !ok {
		return domain.ErrNotFound
	}
	if cur.Status != from {
		return domain.ErrInvalidTransition
	}
	cur.Status = o.Status
	return nil
}

func (f *fakeRepo) FindByID(_ context.Context, id string) (*domain.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orders[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return domain.Rehydrate(o.ID, o.CustomerID, o.Status, o.Items, o.CreatedAt), nil
}

func (f *fakeRepo) FindByIdempotencyKey(ctx context.Context, key string) (*domain.Order, error) {
	f.mu.Lock()
	id, ok := f.keys[key]
	f.mu.Unlock()
	if !ok {
		return nil, domain.ErrNotFound
	}
	return f.FindByID(ctx, id)
}

func (f *fakeRepo) List(_ context.Context, fl domain.ListFilter) ([]*domain.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Order
	for _, o := range f.orders {
		if fl.CustomerID == "" || o.CustomerID == fl.CustomerID {
			out = append(out, o)
		}
	}
	return out, nil
}

var (
	fixedNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	validID  = "11111111-1111-4111-8111-111111111111"
	items    = []domain.Item{{SKU: "A", Quantity: 2, PriceCents: 1000}}
)

func newTestService(repo domain.Repository) *Service {
	return NewService(repo,
		WithClock(func() time.Time { return fixedNow }),
		WithIDGenerator(func() string { return validID }),
	)
}

func TestCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("persists a valid order", func(t *testing.T) {
		repo := newFakeRepo()
		o, created, err := newTestService(repo).Create(ctx, CreateInput{CustomerID: "c1", Items: items})
		if err != nil || !created {
			t.Fatalf("expected created order, got created=%v err=%v", created, err)
		}
		if o.ID != validID || !o.CreatedAt.Equal(fixedNow) || o.Total() != 2000 {
			t.Errorf("unexpected order: %+v", o)
		}
		if repo.inserts != 1 {
			t.Errorf("expected 1 insert, got %d", repo.inserts)
		}
	})

	t.Run("domain validation errors are returned and nothing is stored", func(t *testing.T) {
		repo := newFakeRepo()
		_, _, err := newTestService(repo).Create(ctx, CreateInput{CustomerID: "c1"})
		if !errors.Is(err, domain.ErrNoItems) {
			t.Fatalf("expected ErrNoItems, got %v", err)
		}
		if repo.inserts != 0 {
			t.Errorf("must not hit the repository on invalid input")
		}
	})

	t.Run("repository errors are propagated", func(t *testing.T) {
		repo := newFakeRepo()
		boom := errors.New("boom")
		repo.failNext = boom
		_, _, err := newTestService(repo).Create(ctx, CreateInput{CustomerID: "c1", Items: items})
		if !errors.Is(err, boom) {
			t.Fatalf("expected boom, got %v", err)
		}
	})

	t.Run("idempotency key replays the first order", func(t *testing.T) {
		repo := newFakeRepo()
		n := 0
		svc := NewService(repo, WithIDGenerator(func() string {
			n++
			return fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
		}))
		in := CreateInput{CustomerID: "c1", Items: items, IdempotencyKey: "key-1"}

		first, created, err := svc.Create(ctx, in)
		if err != nil || !created {
			t.Fatalf("first call: created=%v err=%v", created, err)
		}
		second, created, err := svc.Create(ctx, in)
		if err != nil || created {
			t.Fatalf("second call must be a replay: created=%v err=%v", created, err)
		}
		if first.ID != second.ID || repo.inserts != 1 {
			t.Errorf("replay must return the same order (%s vs %s), inserts=%d", first.ID, second.ID, repo.inserts)
		}
	})

	t.Run("race on the unique key returns the winner", func(t *testing.T) {
		repo := newFakeRepo()
		repo.keys["k"] = validID
		repo.orders[validID] = domain.Rehydrate(validID, "c1", domain.StatusCreated, items, fixedNow)
		racy := &racyRepo{fakeRepo: repo}
		o, created, err := newTestService(racy).Create(ctx, CreateInput{CustomerID: "c1", Items: items, IdempotencyKey: "k"})
		if err != nil || created || o.ID != validID {
			t.Fatalf("expected winner replay, got order=%+v created=%v err=%v", o, created, err)
		}
	})

	t.Run("idempotency key too long", func(t *testing.T) {
		long := make([]byte, maxIdemKeyLen+1)
		for i := range long {
			long[i] = 'k'
		}
		_, _, err := newTestService(newFakeRepo()).Create(ctx, CreateInput{CustomerID: "c1", Items: items, IdempotencyKey: string(long)})
		if !errors.Is(err, ErrInvalidIdempotencyKey) {
			t.Fatalf("expected ErrInvalidIdempotencyKey, got %v", err)
		}
	})
}

type racyRepo struct {
	*fakeRepo
	calls int
}

func (r *racyRepo) FindByIdempotencyKey(ctx context.Context, key string) (*domain.Order, error) {
	r.calls++
	if r.calls == 1 {
		return nil, domain.ErrNotFound
	}
	return r.fakeRepo.FindByIdempotencyKey(ctx, key)
}

func TestGet(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := newTestService(repo)

	if _, err := svc.Get(ctx, "not-a-uuid"); !errors.Is(err, domain.ErrInvalidID) {
		t.Errorf("expected ErrInvalidID, got %v", err)
	}
	if _, err := svc.Get(ctx, validID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if _, _, err := svc.Create(ctx, CreateInput{CustomerID: "c1", Items: items}); err != nil {
		t.Fatal(err)
	}
	if o, err := svc.Get(ctx, validID); err != nil || o.ID != validID {
		t.Errorf("expected to find the order, got %+v err=%v", o, err)
	}
}

func TestTransitions(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := newTestService(repo)
	if _, _, err := svc.Create(ctx, CreateInput{CustomerID: "c1", Items: items}); err != nil {
		t.Fatal(err)
	}

	o, err := svc.Complete(ctx, validID)
	if err != nil || o.Status != domain.StatusCompleted {
		t.Fatalf("complete: %+v err=%v", o, err)
	}
	if stored, _ := repo.FindByID(ctx, validID); stored.Status != domain.StatusCompleted {
		t.Errorf("status must be persisted, got %s", stored.Status)
	}
	if _, err := svc.Cancel(ctx, validID); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Errorf("cancel after complete must fail, got %v", err)
	}
	if _, err := svc.Complete(ctx, "22222222-2222-4222-8222-222222222222"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown order must be not found, got %v", err)
	}
}

func TestListNormalizesPagination(t *testing.T) {
	spy := &listSpy{fakeRepo: newFakeRepo()}
	svc := newTestService(spy)
	cases := []struct {
		in           ListInput
		wantLim, off int
	}{
		{ListInput{}, DefaultListLimit, 0},
		{ListInput{Limit: 5, Offset: 10}, 5, 10},
		{ListInput{Limit: 10_000, Offset: -3}, MaxListLimit, 0},
	}
	for _, c := range cases {
		if _, err := svc.List(context.Background(), c.in); err != nil {
			t.Fatal(err)
		}
		if spy.got.Limit != c.wantLim || spy.got.Offset != c.off {
			t.Errorf("input %+v: got limit=%d offset=%d, want %d/%d", c.in, spy.got.Limit, spy.got.Offset, c.wantLim, c.off)
		}
	}
}

type listSpy struct {
	*fakeRepo
	got domain.ListFilter
}

func (l *listSpy) List(ctx context.Context, f domain.ListFilter) ([]*domain.Order, error) {
	l.got = f
	return l.fakeRepo.List(ctx, f)
}
