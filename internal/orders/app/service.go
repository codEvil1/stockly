package app

import (
	"context"
	"time"

	"github.com/codEvil1/stockly/internal/orders/domain"
	"github.com/google/uuid"
)

type Service struct {
	repo domain.Repository
}

func NewService(repo domain.Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, customerID string, items []domain.Item) (*domain.Order, error) {
	o, err := domain.NewOrder(uuid.NewString(), customerID, items, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := s.repo.Insert(ctx, o); err != nil {
		return nil, err
	}
	return o, nil
}	

func (s *Service) Get(ctx context.Context, id string) (*domain.Order, error) {
	return s.repo.FindByID(ctx, id)
}