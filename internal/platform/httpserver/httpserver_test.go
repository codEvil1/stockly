package httpserver

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestOperationalEndpoints(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
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

	do := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	if rec := do("/healthz"); rec.Code != http.StatusOK || rec.Header().Get("X-Request-Id") == "" {
		t.Errorf("/healthz: got status=%d req_id=%q", rec.Code, rec.Header().Get("X-Request-Id"))
	}
	if rec := do("/readyz"); rec.Code != http.StatusOK {
		t.Errorf("/readyz healthy: got %d", rec.Code)
	}
	healthy = false
	if rec := do("/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz unhealthy: got %d", rec.Code)
	}
	if rec := do("/healthz"); rec.Code != http.StatusOK {
		t.Errorf("/healthz must stay up when dependencies fail, got %d", rec.Code)
	}
	logs.Reset()
	if rec := do("/boom"); rec.Code != http.StatusInternalServerError {
		t.Errorf("panics must be recovered as 500, got %d", rec.Code)
	}
	out := logs.String()
	if !strings.Contains(out, `"msg":"panic recovered"`) || !strings.Contains(out, `"status":500`) {
		t.Errorf("expected structured panic and 500 http request logs, got: %s", out)
	}
}
