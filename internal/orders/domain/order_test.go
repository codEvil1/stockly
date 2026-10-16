package domain

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNewOrder(t *testing.T) {
	now := time.Now()
	manyItems := make([]Item, maxItems+1)
	for i := range manyItems {
		manyItems[i] = Item{SKU: fmt.Sprintf("SKU-%d", i), Quantity: 1, PriceCents: 1}
	}

	cases := []struct {
		name       string
		id         string
		customerID string
		items      []Item
		wantErr    error
		wantTotal  int64
	}{
		{"no items", "id-1", "cust-1", nil, ErrNoItems, 0},
		{"too many items", "id-1", "cust-1", manyItems, ErrTooManyItems, 0},
		{"zero quantity", "id-1", "cust-1", []Item{{"A", 0, 100}}, ErrInvalidItem, 0},
		{"negative price", "id-1", "cust-1", []Item{{"A", 1, -1}}, ErrInvalidItem, 0},
		{"price above limit", "id-1", "cust-1", []Item{{"A", 1, maxPriceCents + 1}}, ErrInvalidItem, 0},
		{"empty sku", "id-1", "cust-1", []Item{{"", 1, 100}}, ErrInvalidItem, 0},
		{"sku too long", "id-1", "cust-1", []Item{{strings.Repeat("a", maxSKULen+1), 1, 100}}, ErrInvalidItem, 0},
		{"duplicate sku", "id-1", "cust-1", []Item{{"A", 1, 100}, {"A", 2, 100}}, ErrDuplicateSKU, 0},
		{"empty customer", "id-1", "", []Item{{"A", 1, 100}}, ErrInvalidCustomer, 0},
		{"customer too long", "id-1", strings.Repeat("c", maxCustomerID+1), []Item{{"A", 1, 100}}, ErrInvalidCustomer, 0},
		{"empty id", "", "cust-1", []Item{{"A", 1, 100}}, ErrInvalidCustomer, 0},
		{"free item is allowed", "id-1", "cust-1", []Item{{"A", 1, 0}}, nil, 0},
		{"valid", "id-1", "cust-1", []Item{{"A", 2, 1000}, {"B", 1, 550}}, nil, 2550},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, err := NewOrder(c.id, c.customerID, c.items, now)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("expected error %v, got %v", c.wantErr, err)
			}
			if err != nil {
				var ve ValidationError
				if !errors.As(err, &ve) {
					t.Errorf("error %v should be a ValidationError", err)
				}
				return
			}
			if o.Total() != c.wantTotal {
				t.Errorf("expected total %d, got %d", c.wantTotal, o.Total())
			}
			if o.Status != StatusCreated {
				t.Errorf("expected status %s, got %s", StatusCreated, o.Status)
			}
			ev := o.Events()
			if len(ev) != 1 || ev[0].Type != EventOrderCreated || ev[0].AggregateID != o.ID {
				t.Errorf("should emit one OrderCreated event for the aggregate, got %+v", ev)
			}
		})
	}
}

func TestNewOrderDoesNotAliasInputItems(t *testing.T) {
	items := []Item{{"A", 1, 100}}
	o, err := NewOrder("id", "cust", items, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	items[0].Quantity = 99
	if o.Items[0].Quantity != 1 {
		t.Errorf("order items must not share memory with the caller's slice")
	}
}

func TestTransitions(t *testing.T) {
	now := time.Now()
	newOrder := func(t *testing.T) *Order {
		t.Helper()
		o, err := NewOrder("id", "cust", []Item{{"A", 1, 100}}, now)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}

	t.Run("complete then cancel is rejected", func(t *testing.T) {
		o := newOrder(t)
		if err := o.Complete(now); err != nil {
			t.Fatal(err)
		}
		if err := o.Cancel(now); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("should not cancel a completed order, got %v", err)
		}
		if o.Status != StatusCompleted {
			t.Errorf("status must stay COMPLETED, got %s", o.Status)
		}
	})

	t.Run("cancel then complete is rejected", func(t *testing.T) {
		o := newOrder(t)
		if err := o.Cancel(now); err != nil {
			t.Fatal(err)
		}
		if err := o.Complete(now); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("should not complete a canceled order, got %v", err)
		}
	})

	t.Run("transition emits an event", func(t *testing.T) {
		o := newOrder(t)
		if err := o.Complete(now); err != nil {
			t.Fatal(err)
		}
		ev := o.Events()
		if len(ev) != 2 || ev[1].Type != EventOrderCompleted {
			t.Errorf("expected OrderCreated + OrderCompleted, got %+v", ev)
		}
	})

	t.Run("rejected transition emits nothing", func(t *testing.T) {
		o := newOrder(t)
		_ = o.Cancel(now)
		before := len(o.Events())
		_ = o.Complete(now)
		if len(o.Events()) != before {
			t.Errorf("a rejected transition must not emit events")
		}
	})
}

func TestRehydrateHasNoPendingEvents(t *testing.T) {
	o := Rehydrate("id", "cust", StatusCompleted, []Item{{"A", 1, 100}}, time.Now())
	if len(o.Events()) != 0 {
		t.Errorf("rehydrated orders must not carry events")
	}
}
