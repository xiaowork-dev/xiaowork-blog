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
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/auth"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/config"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/httpapi"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/mail"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	cfg, err := config.Load()
	if err != nil {
		// Validation messages name variables, never their values.
		logger.Error("invalid configuration", "error", err.Error())
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
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

	authService := auth.NewService(db, cfg, mail.NewSMTP(cfg.SMTP), auth.NewPasswordHasher(auth.DefaultArgon2, 4), logger)
	go authService.RunJanitor(ctx, time.Hour)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewRouter(httpapi.Deps{Ping: db.Ping, Auth: authService, Logger: logger}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}

	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.ListenAndServe() }()
	logger.Info("API listening", "config", cfg.String())

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
	authService.Wait() // let queued e-mails finish
}
