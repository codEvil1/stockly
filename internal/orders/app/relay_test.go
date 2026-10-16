package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/codEvil1/stockly/internal/orders/domain"
)

type fakeStore struct {
	pending []domain.Event
	calls   int
}

func (s *fakeStore) PublishPending(_ context.Context, limit int, publish func(domain.Event) error) (int, error) {
	s.calls++
	n := 0
	for len(s.pending) > 0 && n < limit {
		if err := publish(s.pending[0]); err != nil {
			return n, err
		}
		s.pending = s.pending[1:]
		n++
	}
	return n, nil
}

type recordingPublisher struct {
	got     []string
	failOn  string
	failErr error
}

func (p *recordingPublisher) Publish(_ context.Context, e domain.Event) error {
	if e.ID == p.failOn {
		return p.failErr
	}
	p.got = append(p.got, e.ID)
	return nil
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func events(ids ...string) []domain.Event {
	out := make([]domain.Event, len(ids))
	for i, id := range ids {
		out[i] = domain.Event{ID: id}
	}
	return out
}

func TestRelayDrainsAllBatches(t *testing.T) {
	store := &fakeStore{pending: events("1", "2", "3", "4", "5")}
	pub := &recordingPublisher{}
	r := NewRelay(store, pub, time.Hour, 2, quietLogger())

	r.drain(context.Background())

	if len(pub.got) != 5 || len(store.pending) != 0 {
		t.Fatalf("expected all 5 events published, got %v (pending %d)", pub.got, len(store.pending))
	}
	if store.calls != 3 {
		t.Errorf("expected 3 batches, got %d", store.calls)
	}
}

func TestRelayStopsOnPublishErrorAndKeepsRemainder(t *testing.T) {
	store := &fakeStore{pending: events("1", "2", "3")}
	pub := &recordingPublisher{failOn: "2", failErr: errors.New("broker down")}
	r := NewRelay(store, pub, time.Hour, 10, quietLogger())

	r.drain(context.Background())

	if len(pub.got) != 1 || pub.got[0] != "1" {
		t.Errorf("only event 1 should have been published, got %v", pub.got)
	}
	if len(store.pending) != 2 {
		t.Errorf("events 2 and 3 must remain pending, got %d", len(store.pending))
	}
}

func TestRelayRunStopsOnContextCancel(t *testing.T) {
	store := &fakeStore{}
	r := NewRelay(store, &recordingPublisher{}, 10*time.Millisecond, 10, quietLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	time.Sleep(35 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
	if store.calls < 2 {
		t.Errorf("expected the relay to poll repeatedly, got %d calls", store.calls)
	}
}
