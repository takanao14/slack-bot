// Package httpserver serves the endpoints that observe the bot: /healthz for
// container probes and /metrics for scraping. Socket Mode needs no listener of
// its own, so this is the only one the bot opens, and it stays optional for
// deployments that neither probe nor scrape.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"slack-bot/internal/metrics"
)

// readHeaderTimeout bounds slow-loris probes against the listener.
const readHeaderTimeout = 5 * time.Second

// Server serves liveness probes and the metrics exposition.
type Server struct {
	http     *http.Server
	listener net.Listener
	logger   *slog.Logger
}

// New builds an observability server for addr.
func New(addr string, logger *slog.Logger) *Server {
	return &Server{
		http: &http.Server{
			Addr:              addr,
			Handler:           newMux(),
			ReadHeaderTimeout: readHeaderTimeout,
		},
		logger: logger,
	}
}

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("GET /metrics", metrics.Handler())
	return mux
}

// Start binds the listener before serving so that a port conflict is reported
// here instead of disappearing into the serving goroutine.
func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.http.Addr, err)
	}
	s.listener = listener

	s.logger.Info("HTTP endpoints listening", slog.String("addr", listener.Addr().String()))

	go func() {
		if err := s.http.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("HTTP endpoints stopped", slog.Any("error", err))
		}
	}()
	return nil
}

// Addr reports the bound address, which differs from the configured one when
// the port was left to the kernel.
func (s *Server) Addr() string {
	if s.listener == nil {
		return s.http.Addr
	}
	return s.listener.Addr().String()
}

// Shutdown stops serving and waits for in-flight probes.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
