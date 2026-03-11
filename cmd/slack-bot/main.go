package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"slack-bot/internal/bot"
	"slack-bot/internal/config"
	"syscall"
)

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
		// Use a default logger since the configured one isn't available.
		return err
	}
	logger := cfg.Logger

	// Initialize the bot.
	b, err := bot.New(cfg)
	if err != nil {
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
