package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/article"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/auth"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/comment"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/media"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/httpx"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/site"
)

type PingFunc func(context.Context) error

// Deps are the collaborators of the HTTP API. Auth and Media may be nil (health endpoints only).
type Deps struct {
	Ping     PingFunc
	Auth     *auth.Service
	Media    *media.Service
	Articles *article.Service
	Site     *site.Service
	Comments *comment.Service
	Logger   *slog.Logger
}

// NewRouter wires health checks, /media file delivery and the /api/v1 tree (API-DD §2, §3).
func NewRouter(d Deps) *chi.Mux {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	r := chi.NewRouter()
	r.Use(httpx.RequestContext(logger))
	r.Use(httpx.Recover)
	r.Use(httpx.SecurityHeaders)
	// Per-request I/O deadlines instead of server-wide ones; upload/download handlers extend them.
	r.Use(httpx.Deadlines(30 * time.Second))
	r.NotFound(httpx.NotFound)
	r.MethodNotAllowed(httpx.MethodNotAllowed)

	r.Get("/health/live", func(w http.ResponseWriter, req *http.Request) {
		writeHealth(w, http.StatusOK, map[string]any{"status": "ok", "stage": "P6"})
	})
	r.Get("/health/ready", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer cancel()
		if err := d.Ping(ctx); err != nil {
			writeHealth(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable"})
			return
		}
		writeHealth(w, http.StatusOK, map[string]any{"status": "ready", "stage": "P6"})
	})

	if d.Auth == nil {
		return r
	}
	if d.Media != nil {
		r.Group(func(files chi.Router) {
			files.Use(httpx.AccessLog, d.Auth.Authenticate)
			d.Media.FileRoutes(files)
		})
	}
	r.Route("/api/v1", func(api chi.Router) {
		api.Use(httpx.AccessLog)
		api.Use(d.Auth.Authenticate)
		api.Use(d.Auth.CSRF)
		// Multipart uploads run without the 15 s handler timeout; they bound their own processing.
		if d.Media != nil {
			d.Media.AvatarRoute(api)
		}
		api.Group(func(json chi.Router) {
			json.Use(middleware.Timeout(15 * time.Second))
			d.Auth.Routes(json)
			if d.Articles != nil {
				d.Articles.PublicRoutes(json)
			}
			if d.Site != nil {
				d.Site.PublicRoutes(json)
			}
			if d.Comments != nil {
				d.Comments.Routes(json)
			}
		})
		// Every /admin path is guarded before routing, so non-admins get 401/403 and never learn which
		// admin endpoints exist. Further admin features arrive in P4–P7.
		api.Route("/admin", func(admin chi.Router) {
			admin.Use(d.Auth.RequireAdmin)
			if d.Media != nil {
				d.Media.AdminRoutes(admin)
			}
			if d.Articles != nil {
				admin.Group(func(json chi.Router) {
					json.Use(middleware.Timeout(15 * time.Second))
					d.Articles.AdminRoutes(json)
				})
			}
			if d.Comments != nil {
				admin.Group(func(json chi.Router) {
					json.Use(middleware.Timeout(15 * time.Second))
					d.Comments.AdminRoutes(json)
				})
			}
			admin.HandleFunc("/*", httpx.NotFound)
		})
	})
	return r
}

func writeHealth(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, status, payload)
}
