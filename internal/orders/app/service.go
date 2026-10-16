package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/codEvil1/stockly/internal/orders/domain"
)

const (
	DefaultListLimit = 20
	MaxListLimit     = 100
	maxIdemKeyLen    = 64
)

var ErrInvalidIdempotencyKey = domain.NewValidationError("idempotency key must have at most 64 chars")

type Service struct {
	repo  domain.Repository
	now   func() time.Time
	newID func() string
}

type Option func(*Service)

func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

func WithIDGenerator(f func() string) Option { return func(s *Service) { s.newID = f } }

func NewService(repo domain.Repository, opts ...Option) *Service {
	s := &Service{
		repo:  repo,
		now:   func() time.Time { return time.Now().UTC() },
		newID: uuid.NewString,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

type CreateInput struct {
	CustomerID     string
	Items          []domain.Item
	IdempotencyKey string
}

func (s *Service) Create(ctx context.Context, in CreateInput) (*domain.Order, bool, error) {
	if len(in.IdempotencyKey) > maxIdemKeyLen {
		return nil, false, ErrInvalidIdempotencyKey
	}

	if in.IdempotencyKey != "" {
		existing, err := s.repo.FindByIdempotencyKey(ctx, in.IdempotencyKey)
		switch {
		case err == nil:
			return existing, false, nil
		case !errors.Is(err, domain.ErrNotFound):
			return nil, false, err
		}
	}

	o, err := domain.NewOrder(s.newID(), in.CustomerID, in.Items, s.now())
	if err != nil {
		return nil, false, err
	}

	if err := s.repo.Insert(ctx, o, in.IdempotencyKey); err != nil {
		if errors.Is(err, domain.ErrDuplicateKey) {
			existing, ferr := s.repo.FindByIdempotencyKey(ctx, in.IdempotencyKey)
			if ferr != nil {
				return nil, false, ferr
			}
			return existing, false, nil
		}
		return nil, false, err
	}
	return o, true, nil
}

func (s *Service) Get(ctx context.Context, id string) (*domain.Order, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, domain.ErrInvalidID
	}
	return s.repo.FindByID(ctx, id)
}

type ListInput struct {
	CustomerID string
	Limit      int
	Offset     int
}

func (s *Service) List(ctx context.Context, in ListInput) ([]*domain.Order, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	offset := max(in.Offset, 0)
	return s.repo.List(ctx, domain.ListFilter{CustomerID: in.CustomerID, Limit: limit, Offset: offset})
}

func (s *Service) Complete(ctx context.Context, id string) (*domain.Order, error) {
	return s.transition(ctx, id, (*domain.Order).Complete)
}

func (s *Service) Cancel(ctx context.Context, id string) (*domain.Order, error) {
	return s.transition(ctx, id, (*domain.Order).Cancel)
}

func (s *Service) transition(ctx context.Context, id string, apply func(*domain.Order, time.Time) error) (*domain.Order, error) {
	o, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	from := o.Status
	if err := apply(o, s.now()); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, o, from); err != nil {
		return nil, err
	}
	return o, nil
}
