package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/httpapi"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		logger.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	address := os.Getenv("HTTP_ADDR")
	if address == "" {
		address = ":8080"
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		logger.Error("could not create DB pool", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	pingCtx, cancelPing := context.WithTimeout(ctx, 5*time.Second)
	err = db.Ping(pingCtx)
	cancelPing()
	if err != nil {
		logger.Error("database not ready", "error", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              address,
		Handler:           httpapi.NewRouter(db.Ping),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.ListenAndServe() }()
	logger.Info("P0 API listening", "addr", address)

	select {
	case <-ctx.Done():
		logger.Info("shutdown requested")
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failure", "error", err)
			os.Exit(1)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
