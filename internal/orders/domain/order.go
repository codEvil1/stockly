package domain

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

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
	maxCustomerID = 36
)

const (
	EventOrderCreated   = "OrderCreated"
	EventOrderCompleted = "OrderCompleted"
	EventOrderCanceled  = "OrderCanceled"
)

type ValidationError struct{ msg string }

func (e ValidationError) Error() string { return e.msg }

func NewValidationError(msg string) ValidationError { return ValidationError{msg: msg} }

var (
	ErrNoItems         = ValidationError{"order must have at least one item"}
	ErrTooManyItems    = ValidationError{"order has too many items"}
	ErrInvalidItem     = ValidationError{"item has empty sku, or invalid quantity or price"}
	ErrInvalidCustomer = ValidationError{"customer id is required (max 36 chars)"}
	ErrDuplicateSKU    = ValidationError{"duplicate sku in order"}
	ErrInvalidID       = ValidationError{"invalid order id"}

	ErrInvalidTransition   = errors.New("invalid status transition")
	ErrNotFound            = errors.New("order not found")
	ErrDuplicateKey        = errors.New("idempotency key already used")
	ErrIdempotencyMismatch = errors.New("idempotency key already used with different parameters")
)

type Item struct {
	SKU        string `json:"sku"`
	Quantity   int    `json:"quantity"`
	PriceCents int64  `json:"price_cents"`
}

type Event struct {
	ID          string
	AggregateID string
	Type        string
	Payload     []byte
	OccurredAt  time.Time
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
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrInvalidID
	}
	customerID = strings.TrimSpace(customerID)
	if customerID == "" || len(customerID) > maxCustomerID || utf8.RuneCountInString(customerID) > maxCustomerID {
		return nil, ErrInvalidCustomer
	}
	if len(items) == 0 {
		return nil, ErrNoItems
	}
	if len(items) > maxItems {
		return nil, ErrTooManyItems
	}
	seen := make(map[string]struct{}, len(items))
	normalized := make([]Item, len(items))
	for i, it := range items {
		sku := strings.TrimSpace(it.SKU)
		if sku == "" || len(sku) > maxSKULen || utf8.RuneCountInString(sku) > maxSKULen ||
			it.Quantity <= 0 || it.Quantity > maxQuantity ||
			it.PriceCents < 0 || it.PriceCents > maxPriceCents {
			return nil, ErrInvalidItem
		}
		skuKey := strings.ToUpper(sku)
		if _, dup := seen[skuKey]; dup {
			return nil, ErrDuplicateSKU
		}
		seen[skuKey] = struct{}{}
		normalized[i] = Item{SKU: sku, Quantity: it.Quantity, PriceCents: it.PriceCents}
	}

	o := &Order{
		ID:         id,
		CustomerID: customerID,
		Status:     StatusCreated,
		Items:      normalized,
		CreatedAt:  now,
	}
	if err := o.record(EventOrderCreated, now, map[string]any{
		"order_id":    o.ID,
		"customer_id": o.CustomerID,
		"total_cents": o.Total(),
		"items":       o.Items,
	}); err != nil {
		return nil, err
	}
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

func (o *Order) MatchesInput(customerID string, items []Item) bool {
	if o.CustomerID != customerID || len(o.Items) != len(items) {
		return false
	}
	bySKU := make(map[string]Item, len(o.Items))
	for _, it := range o.Items {
		bySKU[strings.ToUpper(it.SKU)] = it
	}
	for _, it := range items {
		cur, ok := bySKU[strings.ToUpper(it.SKU)]
		if !ok || cur.SKU != it.SKU || cur.Quantity != it.Quantity || cur.PriceCents != it.PriceCents {
			return false
		}
	}
	return true
}

func (o *Order) Complete(now time.Time) error {
	return o.transition(StatusCompleted, EventOrderCompleted, now)
}

func (o *Order) Cancel(now time.Time) error {
	return o.transition(StatusCanceled, EventOrderCanceled, now)
}

func (o *Order) Events() []Event { return o.events }

func (o *Order) ClearEvents() { o.events = nil }

func (o *Order) transition(to Status, eventType string, now time.Time) error {
	if o.Status != StatusCreated {
		return ErrInvalidTransition
	}
	o.Status = to
	return o.record(eventType, now, map[string]any{
		"order_id":    o.ID,
		"customer_id": o.CustomerID,
		"status":      to,
	})
}

func (o *Order) record(eventType string, now time.Time, data map[string]any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	o.events = append(o.events, Event{
		ID:          uuid.NewString(),
		AggregateID: o.ID,
		Type:        eventType,
		Payload:     payload,
		OccurredAt:  now,
	})
	return nil
}

type ListFilter struct {
	CustomerID string
	Limit      int
	Offset     int
}

type Repository interface {
	Insert(ctx context.Context, o *Order, idempotencyKey string) error
	Update(ctx context.Context, o *Order, from Status) error
	FindByID(ctx context.Context, id string) (*Order, error)
	FindByIdempotencyKey(ctx context.Context, key string) (*Order, error)
	List(ctx context.Context, f ListFilter) ([]*Order, error)
}
