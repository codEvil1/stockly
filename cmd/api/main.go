package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/go-chi/chi/v5"

	"github.com/codEvil1/stockly/internal/orders/app"
	"github.com/codEvil1/stockly/internal/orders/infra/eventlog"
	"github.com/codEvil1/stockly/internal/orders/infra/httpapi"
	mysqladapter "github.com/codEvil1/stockly/internal/orders/infra/mysql"
	"github.com/codEvil1/stockly/internal/platform/config"
	"github.com/codEvil1/stockly/internal/platform/database"
	"github.com/codEvil1/stockly/internal/platform/httpserver"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, database.Options{
		DSN:          cfg.DBDSN,
		MaxOpenConns: cfg.DBMaxOpenConns,
		MaxIdleConns: cfg.DBMaxIdleConns,
		ConnMaxLife:  cfg.DBConnMaxLife,
		ConnectTries: cfg.DBConnectTries,
	}, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	repo := mysqladapter.NewRepository(db)
	svc := app.NewService(repo)
	handler := httpapi.NewHandler(svc, logger)

	srv := httpserver.New(cfg.HTTPAddr, logger, db.PingContext, func(r chi.Router) {
		httpapi.DocsRoutes(r)
		handler.Routes(r)
	})

	relay := app.NewRelay(
		mysqladapter.NewOutboxStore(db),
		eventlog.NewPublisher(logger),
		cfg.OutboxInterval, cfg.OutboxBatchSize, logger,
	)

	relayCtx, stopRelay := context.WithCancel(context.Background())
	defer stopRelay()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		relay.Run(relayCtx)
	}()

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("api listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down...")
	case err := <-serverErr:
		stop()
		stopRelay()
		wg.Wait()
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	stopRelay()
	wg.Wait()
	return shutdownErr
}
