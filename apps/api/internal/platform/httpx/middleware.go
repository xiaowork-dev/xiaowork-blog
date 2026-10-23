package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"
)

type (
	requestIDKey struct{}
	loggerKey    struct{}
)

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// RequestID returns the request ID stored by the RequestContext middleware.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func newRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "req_" + hex.EncodeToString(b)
}

// RequestContext accepts a well-formed client X-Request-Id (API-DD §3.1) or generates one, echoes it,
// and attaches a logger carrying request_id to the context.
func RequestContext(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-Id")
			if !validRequestID.MatchString(id) {
				id = newRequestID()
			}
			w.Header().Set("X-Request-Id", id)
			ctx := context.WithValue(r.Context(), requestIDKey{}, id)
			ctx = context.WithValue(ctx, loggerKey{}, base.With("request_id", id))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// AccessLog logs one line per request. Only method, path (never the query string: it can carry
// one-time tokens), status and duration are recorded — no headers, cookies or bodies.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		LoggerFrom(r.Context()).Info("http request",
			"method", r.Method, "path", r.URL.Path, "status", rec.status, "duration_ms", time.Since(start).Milliseconds())
	})
}

// Recover turns panics into a JSON 500 and logs the stack (stack traces never reach the client).
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				LoggerFrom(r.Context()).Error("panic recovered", "panic", v, "stack", string(debug.Stack()))
				WriteError(w, r, ErrInternal)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// SecurityHeaders sets conservative headers for a JSON API.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// NotFound and MethodNotAllowed keep unknown routes inside the JSON error contract.
func NotFound(w http.ResponseWriter, r *http.Request) { WriteError(w, r, ErrNotFound) }

func MethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, ErrMethodNotAllowed)
}

// MaxJSONBody is the request size limit for JSON endpoints.
const MaxJSONBody = 64 << 10

// DecodeJSON strictly decodes a single JSON object: correct Content-Type, size limit, no unknown fields
// (so e.g. a "role" field in a public registration is rejected rather than silently ignored).
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return ErrUnsupportedType
	}
	dec := jsonDecoder(http.MaxBytesReader(w, r.Body, MaxJSONBody))
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return ErrPayloadTooLarge
		}
		return ErrInvalidRequest
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidRequest
	}
	return nil
}

func jsonDecoder(r io.Reader) *json.Decoder {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	return dec
}
