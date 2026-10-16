package httpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestOperationalEndpoints(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	healthy := true
	srv := New(":0", logger,
		func(context.Context) error {
			if healthy {
				return nil
			}
			return errors.New("db down")
		},
		func(r chi.Router) {
			r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
		})

	status := func(path string) int {
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code
	}

	if got := status("/healthz"); got != http.StatusOK {
		t.Errorf("/healthz: got %d", got)
	}
	if got := status("/readyz"); got != http.StatusOK {
		t.Errorf("/readyz healthy: got %d", got)
	}
	healthy = false
	if got := status("/readyz"); got != http.StatusServiceUnavailable {
		t.Errorf("/readyz unhealthy: got %d", got)
	}
	if got := status("/healthz"); got != http.StatusOK {
		t.Errorf("/healthz must stay up when dependencies fail, got %d", got)
	}
	if got := status("/boom"); got != http.StatusInternalServerError {
		t.Errorf("panics must be recovered as 500, got %d", got)
	}
}
