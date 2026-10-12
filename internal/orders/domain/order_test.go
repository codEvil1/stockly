package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewOrder(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name          string
		items         []Item
		wantErr       error
		wantTotal     int64
	}{
		{"no items", nil, ErrNoItems, 0},
		{"zero quantity", []Item{{"A", 0, 100}}, ErrInvalidItem, 0},
		{"empty sku", []Item{{"", 1, 100}}, ErrInvalidItem, 0},
		{"valid", []Item{{"A", 2, 1000}, {"B", 1, 550}}, nil, 2550},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, err := NewOrder("id-1", "cust-1", c.items, now)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("expected error %v, got %v", c.wantErr, err)
			}
			if err == nil {
				if o.Total() != c.wantTotal {
					t.Errorf("expected total %d, got %d", c.wantTotal, o.Total())
				}
				if len(o.Events()) != 1 || o.Events()[0].Type != "OrderCreated" {
					t.Errorf("should emit an OrderCreated event")
				}
			}
		})
	}
}

func TestTransitions(t *testing.T) {
	o, _ := NewOrder("id", "cust", []Item{{"A", 1, 100}}, time.Now())
	if err := o.Complete(); err != nil {
		t.Fatal(err)
	}
	if err := o.Cancel(); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("should not cancel a completed order")
	}
}