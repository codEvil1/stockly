package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.DBMaxOpenConns != 25 || cfg.ShutdownTimeout != 10*time.Second ||
		cfg.OutboxInterval != 2*time.Second || cfg.LogLevel != slog.LevelInfo {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := load(env(map[string]string{
		"HTTP_ADDR":            ":9000",
		"DB_MAX_OPEN_CONNS":    "5",
		"OUTBOX_POLL_INTERVAL": "500ms",
		"LOG_LEVEL":            "DEBUG",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":9000" || cfg.DBMaxOpenConns != 5 || cfg.OutboxInterval != 500*time.Millisecond || cfg.LogLevel != slog.LevelDebug {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestLoadReportsAllErrors(t *testing.T) {
	_, err := load(env(map[string]string{
		"DB_MAX_OPEN_CONNS":    "zero",
		"OUTBOX_POLL_INTERVAL": "soon",
		"LOG_LEVEL":            "loud",
		"DB_DSN":               "root@tcp(db)/stockly",
	}))
	if err == nil {
		t.Fatal("expected validation errors")
	}
	for _, want := range []string{"DB_MAX_OPEN_CONNS", "OUTBOX_POLL_INTERVAL", "LOG_LEVEL", "parseTime=true"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s, got: %v", want, err)
		}
	}
}
