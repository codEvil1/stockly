package domain

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusCreated   Status = "CREATED"
	StatusCompleted Status = "COMPLETED"
	StatusCanceled  Status = "CANCELED"
)

const (
	maxItems      = 200
	maxSKULen     = 50
	maxQuantity   = 1_000_000
	maxPriceCents = 10_000_000_000
)

var (
	ErrNoItems           = errors.New("order must have at least one item")
	ErrInvalidItem       = errors.New("item has empty sku, or invalid quantity or price")
	ErrInvalidTransition = errors.New("invalid status transition")
	ErrNotFound          = errors.New("order not found")
	ErrInvalidCustomer   = errors.New("customer id is required (max 36 chars)")
	ErrDuplicateSKU      = errors.New("duplicate sku in order")
)

type Item struct {
	SKU        string `json:"sku"`
	Quantity   int    `json:"quantity"`
	PriceCents int64  `json:"price_cents"`
}

type Event struct {
	ID      string
	Type    string
	Payload []byte
}

type Order struct {
	ID         string
	CustomerID string
	Status     Status
	Items      []Item
	CreatedAt  time.Time
	events     []Event
}

func NewOrder(id, customerID string, items []Item, now time.Time) (*Order, error) {
	if id == "" || customerID == "" || len(customerID) > 36 {
		return nil, ErrInvalidCustomer
	}
	if len(items) == 0 || len(items) > maxItems {
		return nil, ErrNoItems
	}
	seen := make(map[string]struct{}, len(items))
	for _, it := range items {
		if it.SKU == "" || len(it.SKU) > maxSKULen ||
			it.Quantity <= 0 || it.Quantity > maxQuantity ||
			it.PriceCents < 0 || it.PriceCents > maxPriceCents {
			return nil, ErrInvalidItem
		}
		if _, dup := seen[it.SKU]; dup {
			return nil, ErrDuplicateSKU
		}
		seen[it.SKU] = struct{}{}
	}
	items = slices.Clone(items)
	
	o := &Order{ID: id, CustomerID: customerID, Status: StatusCreated, Items: items, CreatedAt: now}

	payload, err := json.Marshal(map[string]any{
		"order_id":    o.ID,
		"customer_id": o.CustomerID,
		"total_cents": o.Total(),
		"items":       o.Items,
	})
	if err != nil {
		return nil, err
	}
	o.events = append(o.events, Event{ID: uuid.NewString(), Type: "OrderCreated", Payload: payload})
	return o, nil
}

func Rehydrate(id, customerID string, status Status, items []Item, createdAt time.Time) *Order {
	return &Order{ID: id, CustomerID: customerID, Status: status, Items: items, CreatedAt: createdAt}
}

func (o *Order) Total() int64 {
	var total int64
	for _, it := range o.Items {
		total += int64(it.Quantity) * it.PriceCents
	}
	return total
}

func (o *Order) Complete() error {
	if o.Status != StatusCreated {
		return ErrInvalidTransition
	}
	o.Status = StatusCompleted
	return nil
}

func (o *Order) Cancel() error {
	if o.Status != StatusCreated {
		return ErrInvalidTransition
	}
	o.Status = StatusCanceled
	return nil
}

func (o *Order) Events() []Event { return o.events }

type Repository interface {
	Insert(ctx context.Context, o *Order) error
	FindByID(ctx context.Context, id string) (*Order, error)
}