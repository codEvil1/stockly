package eventlog

import (
	"context"
	"log/slog"

	"github.com/codEvil1/stockly/internal/orders/app"
	"github.com/codEvil1/stockly/internal/orders/domain"
)

type Publisher struct{ logger *slog.Logger }

func NewPublisher(logger *slog.Logger) *Publisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Publisher{logger: logger}
}

var _ app.Publisher = (*Publisher)(nil)

func (p *Publisher) Publish(ctx context.Context, e domain.Event) error {
	p.logger.InfoContext(ctx, "domain event published",
		"event_id", e.ID,
		"type", e.Type,
		"aggregate_id", e.AggregateID,
		"occurred_at", e.OccurredAt,
		"payload", string(e.Payload),
	)
	return nil
}
