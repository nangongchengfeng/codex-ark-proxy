package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"proxy_doubao/internal/config"
	"proxy_doubao/internal/proxy"
)

func TestHealthHandler(t *testing.T) {
	srv := NewServer(config.Config{ListenAddr: ":8080"}, &proxy.Proxy{})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestResponsesRouteMethodNotAllowed(t *testing.T) {
	srv := NewServer(config.Config{}, &proxy.Proxy{})

	req := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}