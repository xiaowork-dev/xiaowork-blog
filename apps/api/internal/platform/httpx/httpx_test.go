package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestClientIPOnlyTrustsConfiguredProxies(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.16.0.0/12")}
	cases := []struct {
		remote, xff, want string
	}{
		{"203.0.113.9:1234", "1.2.3.4", "203.0.113.9"},               // untrusted peer: header ignored
		{"172.18.0.5:5555", "198.51.100.7", "198.51.100.7"},          // via trusted proxy
		{"172.18.0.5:5555", "6.6.6.6, 198.51.100.7", "198.51.100.7"}, // spoofed left entry ignored
		{"172.18.0.5:5555", "198.51.100.7, 172.18.0.9", "198.51.100.7"},
		{"172.18.0.5:5555", "", "172.18.0.5"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := ClientIP(r, trusted).String(); got != c.want {
			t.Errorf("remote=%s xff=%q: got %s want %s", c.remote, c.xff, got, c.want)
		}
	}
}

func TestErrorEnvelopeAndRequestID(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := RequestContext(logger)(Recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rate":
			WriteError(w, r, RateLimited(90*time.Second))
		case "/db":
			WriteError(w, r, errors.New("dial tcp: connection refused"))
		default:
			panic("boom")
		}
	})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rate", nil)
	req.Header.Set("X-Request-Id", "client-req-12345")
	h.ServeHTTP(rec, req)
	var body struct {
		Error     struct{ Code, Message string } `json:"error"`
		RequestID string                         `json:"request_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 429 || body.Error.Code != "RATE_LIMITED" || rec.Header().Get("Retry-After") != "90" ||
		body.RequestID != "client-req-12345" || rec.Header().Get("X-Request-Id") != "client-req-12345" {
		t.Fatalf("429 envelope: %d %s %+v", rec.Code, rec.Body.String(), rec.Header())
	}

	// Malformed client IDs are replaced.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/db", nil)
	req.Header.Set("X-Request-Id", "bad id\nwith newline")
	h.ServeHTTP(rec, req)
	if rec.Code != 503 || !strings.HasPrefix(rec.Header().Get("X-Request-Id"), "req_") || strings.Contains(rec.Body.String(), "connection refused") {
		t.Fatalf("dependency failure: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "boom") || !strings.Contains(logs.String(), "panic recovered") {
		t.Fatalf("panic handling: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDecodeJSONIsStrict(t *testing.T) {
	type payload struct {
		Email string `json:"email"`
	}
	cases := map[string]struct {
		contentType, body string
		want              *APIError
	}{
		"ok":            {"application/json", `{"email":"a@b.c"}`, nil},
		"wrong type":    {"text/plain", `{"email":"a@b.c"}`, ErrUnsupportedType},
		"unknown field": {"application/json", `{"email":"a@b.c","role":"ADMIN"}`, ErrInvalidRequest},
		"two objects":   {"application/json", `{"email":"a"}{"email":"b"}`, ErrInvalidRequest},
		"too large":     {"application/json", `{"email":"` + strings.Repeat("a", MaxJSONBody) + `"}`, ErrPayloadTooLarge},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(c.body)).WithContext(context.Background())
			r.Header.Set("Content-Type", c.contentType)
			var p payload
			err := DecodeJSON(httptest.NewRecorder(), r, &p)
			if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
		})
	}
}
