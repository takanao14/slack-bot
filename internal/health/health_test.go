package health

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHealthzReturnsOK(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
}

func TestUnknownPathReturnsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
}

func TestPostIsRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))

	if rec.Code == http.StatusOK {
		t.Fatal("expected a non-200 status for POST")
	}
}

func TestStartServesAndShutdownStops(t *testing.T) {
	s := New("127.0.0.1:0", testLogger())
	if err := s.Start(); err != nil {
		t.Fatalf("expected Start to succeed, got %v", err)
	}

	resp, err := http.Get("http://" + s.Addr() + "/healthz")
	if err != nil {
		t.Fatalf("expected probe to succeed, got %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("expected Shutdown to succeed, got %v", err)
	}

	if _, err := http.Get("http://" + s.Addr() + "/healthz"); err == nil {
		t.Fatal("expected probe to fail after shutdown")
	}
}

func TestStartReturnsErrorWhenPortIsTaken(t *testing.T) {
	first := New("127.0.0.1:0", testLogger())
	if err := first.Start(); err != nil {
		t.Fatalf("expected first Start to succeed, got %v", err)
	}
	t.Cleanup(func() { _ = first.Shutdown(context.Background()) })

	second := New(first.Addr(), testLogger())
	if err := second.Start(); err == nil {
		t.Fatal("expected Start to fail on a taken port")
		_ = second.Shutdown(context.Background())
	}
}
