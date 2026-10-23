package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLiveness(t *testing.T) {
	handler := NewRouter(Deps{Ping: func(context.Context) error { return nil }})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"stage":"P3"`) {
		t.Fatalf("unexpected response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestReadiness(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"ready", nil, http.StatusOK},
		{"database_down", errors.New("unreachable"), http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewRouter(Deps{Ping: func(context.Context) error { return tc.err }})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestUnknownRouteUsesErrorEnvelope(t *testing.T) {
	handler := NewRouter(Deps{Ping: func(context.Context) error { return nil }})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"NOT_FOUND"`) ||
		rec.Header().Get("X-Request-Id") == "" {
		t.Fatalf("unexpected response: %d %s", rec.Code, rec.Body.String())
	}
}
