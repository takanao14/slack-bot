package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"slack-bot/internal/bot"
	"slack-bot/internal/config"
	"slack-bot/internal/health"
	"syscall"
	"time"
)

// healthShutdownTimeout bounds the wait for in-flight probes at exit.
const healthShutdownTimeout = 5 * time.Second

// version is injected at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("Application failed", "error", err)
		os.Exit(1)
	}
	slog.Info("Application stopped successfully")
}

func run() error {
	// Create a context that is canceled on receiving an OS interrupt signal.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Load configuration from environment variables.
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := cfg.Logger
	logger.Info("Starting slack-bot", "version", version)

	// Start probing before the bot, whose auth.test retries can take tens of
	// seconds. A probe must not fail while startup is still healthy.
	if cfg.HealthAddr != "" {
		healthServer := health.New(cfg.HealthAddr, logger)
		if err := healthServer.Start(); err != nil {
			return err
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), healthShutdownTimeout)
			defer cancel()
			if err := healthServer.Shutdown(shutdownCtx); err != nil {
				logger.Error("Failed to shut down health endpoint", "error", err)
			}
		}()
	}

	// Initialize the bot.
	b, err := bot.New(ctx, cfg)
	if err != nil {
		// A signal can cancel auth retries.
		if errors.Is(err, context.Canceled) {
			logger.Info("Bot initialization canceled by signal")
			return nil
		}
		logger.Error("Failed to initialize bot", "error", err)
		return err
	}

	// Start the bot and wait for it to complete.
	if err := b.Run(ctx); err != nil {
		// Don't treat context cancellation as a fatal error.
		if errors.Is(err, context.Canceled) {
			logger.Info("Bot run canceled by signal")
		} else {
			logger.Error("Bot failed", "error", err)
			return err
		}
	}

	// Graceful shutdown.
	if err := b.Shutdown(); err != nil {
		logger.Error("Failed to shutdown gracefully", "error", err)
		return err
	}

	logger.Info("Bot shutdown complete")
	return nil
}
