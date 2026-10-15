package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/go-sql-driver/mysql"

	"github.com/codEvil1/stockly/internal/orders/app"
	"github.com/codEvil1/stockly/internal/orders/infra/httpapi"
	mysqladapter "github.com/codEvil1/stockly/internal/orders/infra/mysql"
)

func main() {
	dsn := getenv("DB_DSN", "root:root@tcp(127.0.0.1:3306)/stockly?parseTime=true&loc=UTC")
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		slog.Error("opening database", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var pingErr error
	for i := 0; i < 10; i++ {
		if pingErr = db.PingContext(ctx); pingErr == nil {
			break
		}
		slog.Warn("database not ready, retrying", "attempt", i+1, "err", pingErr)
		select {
		case <-ctx.Done():
			os.Exit(1)
		case <-time.After(2 * time.Second):
		}
	}
	if pingErr != nil {
		slog.Error("pinging database", "err", pingErr)
		os.Exit(1)
	}

	repo := mysqladapter.NewRepository(db)
	svc := app.NewService(repo)

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer, middleware.Timeout(10*time.Second))
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	httpapi.DocsRoutes(r)
	httpapi.NewHandler(svc).Routes(r)

	srv := &http.Server{Addr: ":8080", Handler: r, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		slog.Info("api listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}