package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/codEvil1/stockly/internal/orders/domain"
)

type OutboxStore interface {
	PublishPending(ctx context.Context, limit int, publish func(domain.Event) error) (int, error)
}

type Publisher interface {
	Publish(ctx context.Context, e domain.Event) error
}

type Relay struct {
	store     OutboxStore
	publisher Publisher
	interval  time.Duration
	batchSize int
	logger    *slog.Logger
}

func NewRelay(store OutboxStore, publisher Publisher, interval time.Duration, batchSize int, logger *slog.Logger) *Relay {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Relay{store: store, publisher: publisher, interval: interval, batchSize: batchSize, logger: logger}
}

func (r *Relay) Run(ctx context.Context) {
	r.logger.Info("outbox relay started", "interval", r.interval, "batch_size", r.batchSize)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		r.drain(ctx)
		select {
		case <-ctx.Done():
			r.logger.Info("outbox relay stopped")
			return
		case <-ticker.C:
		}
	}
}

func (r *Relay) drain(ctx context.Context) {
	for ctx.Err() == nil {
		n, err := r.store.PublishPending(ctx, r.batchSize, func(e domain.Event) error {
			return r.publisher.Publish(ctx, e)
		})
		if err != nil {
			r.logger.Error("outbox publish failed", "published", n, "err", err)
			return
		}
		if n < r.batchSize {
			return
		}
	}
}
