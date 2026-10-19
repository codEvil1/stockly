package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr        string
	DBDSN           string
	DBMaxOpenConns  int
	DBMaxIdleConns  int
	DBConnMaxLife   time.Duration
	DBConnectTries  int
	ShutdownTimeout time.Duration
	OutboxInterval  time.Duration
	OutboxBatchSize int
	LogLevel        slog.Level
}

func Load() (Config, error) {
	return load(os.Getenv)
}

func load(get func(string) string) (Config, error) {
	var errs []error
	str := func(key, def string) string {
		if v := get(key); v != "" {
			return v
		}
		return def
	}
	integer := func(key string, def int) int {
		v := get(key)
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			errs = append(errs, fmt.Errorf("%s must be a positive integer, got %q", key, v))
			return def
		}
		return n
	}
	duration := func(key string, def time.Duration) time.Duration {
		v := get(key)
		if v == "" {
			return def
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("%s must be a positive duration (e.g. 5s), got %q", key, v))
			return def
		}
		return d
	}

	cfg := Config{
		HTTPAddr:        str("HTTP_ADDR", ":8080"),
		DBDSN:           str("DB_DSN", "root:root@tcp(127.0.0.1:3307)/stockly?parseTime=true&loc=UTC"),
		DBMaxOpenConns:  integer("DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns:  integer("DB_MAX_IDLE_CONNS", 25),
		DBConnMaxLife:   duration("DB_CONN_MAX_LIFETIME", 5*time.Minute),
		DBConnectTries:  integer("DB_CONNECT_RETRIES", 10),
		ShutdownTimeout: duration("SHUTDOWN_TIMEOUT", 10*time.Second),
		OutboxInterval:  duration("OUTBOX_POLL_INTERVAL", 2*time.Second),
		OutboxBatchSize: integer("OUTBOX_BATCH_SIZE", 100),
	}

	switch strings.ToLower(str("LOG_LEVEL", "info")) {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "info":
		cfg.LogLevel = slog.LevelInfo
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		errs = append(errs, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error, got %q", get("LOG_LEVEL")))
	}

	if !strings.Contains(cfg.DBDSN, "parseTime=true") {
		errs = append(errs, errors.New("DB_DSN must include parseTime=true"))
	}

	return cfg, errors.Join(errs...)
}
