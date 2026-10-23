package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/auth"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/httpx"
)

type PingFunc func(context.Context) error

// Deps are the collaborators of the HTTP API. Auth may be nil (health endpoints only).
type Deps struct {
	Ping   PingFunc
	Auth   *auth.Service
	Logger *slog.Logger
}

// NewRouter wires health checks and the /api/v1 tree (API-DD §3).
func NewRouter(d Deps) *chi.Mux {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	r := chi.NewRouter()
	r.Use(httpx.RequestContext(logger))
	r.Use(httpx.Recover)
	r.Use(httpx.SecurityHeaders)
	r.Use(middleware.Timeout(15 * time.Second))
	r.NotFound(httpx.NotFound)
	r.MethodNotAllowed(httpx.MethodNotAllowed)

	r.Get("/health/live", func(w http.ResponseWriter, req *http.Request) {
		writeHealth(w, http.StatusOK, map[string]any{"status": "ok", "stage": "P2"})
	})
	r.Get("/health/ready", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer cancel()
		if err := d.Ping(ctx); err != nil {
			writeHealth(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable"})
			return
		}
		writeHealth(w, http.StatusOK, map[string]any{"status": "ready", "stage": "P2"})
	})

	if d.Auth != nil {
		r.Route("/api/v1", func(api chi.Router) {
			api.Use(httpx.AccessLog)
			api.Use(d.Auth.Authenticate)
			api.Use(d.Auth.CSRF)
			d.Auth.Routes(api)
			// Every /admin path is guarded before routing, so non-admins get 401/403 and never learn which
			// admin endpoints exist. Admin features themselves arrive in P4–P7.
			api.Route("/admin", func(admin chi.Router) {
				admin.Use(d.Auth.RequireAdmin)
				admin.HandleFunc("/*", httpx.NotFound)
			})
		})
	}
	return r
}

func writeHealth(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, status, payload)
}
