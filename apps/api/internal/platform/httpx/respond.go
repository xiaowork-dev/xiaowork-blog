// Package httpx holds the API-wide HTTP conventions of API-DD §3: JSON envelopes, error codes,
// request IDs, safe logging, strict JSON decoding and client IP resolution.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// APIError is a business error with a stable machine-readable code (API-DD §3.3).
type APIError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
	// RetryAfter, when positive, is sent as the Retry-After header (429).
	RetryAfter time.Duration
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

func NewError(status int, code, message string) *APIError {
	return &APIError{Status: status, Code: code, Message: message}
}

// FieldErrors builds a 422 VALIDATION_FAILED error with details.fields (field -> [error_code]).
func FieldErrors(fields map[string][]string) *APIError {
	return &APIError{
		Status: http.StatusUnprocessableEntity, Code: "VALIDATION_FAILED", Message: "提交的内容未通过校验。",
		Details: map[string]any{"fields": fields},
	}
}

// Common errors. Messages are user-facing Chinese text; clients must branch on Code.
var (
	ErrInvalidRequest   = NewError(http.StatusBadRequest, "INVALID_REQUEST", "请求格式不正确。")
	ErrAuthRequired     = NewError(http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录。")
	ErrSessionExpired   = NewError(http.StatusUnauthorized, "SESSION_EXPIRED", "登录已过期，请重新登录。")
	ErrForbidden        = NewError(http.StatusForbidden, "FORBIDDEN", "没有权限执行此操作。")
	ErrEmailNotVerified = NewError(http.StatusForbidden, "EMAIL_NOT_VERIFIED", "请先完成邮箱验证。")
	ErrAccountDisabled  = NewError(http.StatusForbidden, "ACCOUNT_DISABLED", "该账号已被停用。")
	ErrCSRFInvalid      = NewError(http.StatusForbidden, "CSRF_INVALID", "安全校验失败，请刷新页面后重试。")
	ErrNotFound         = NewError(http.StatusNotFound, "NOT_FOUND", "请求的资源不存在。")
	ErrMethodNotAllowed = NewError(http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "不支持该请求方法。")
	ErrPayloadTooLarge  = NewError(http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "请求内容过大。")
	ErrUnsupportedType  = NewError(http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "请求必须是 JSON。")
	ErrInternal         = NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "服务器内部错误，请稍后重试。")
	ErrUnavailable      = NewError(http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "服务暂时不可用，请稍后重试。")
)

// RateLimited builds a 429 with Retry-After.
func RateLimited(retryAfter time.Duration) *APIError {
	return &APIError{Status: http.StatusTooManyRequests, Code: "RATE_LIMITED", Message: "操作过于频繁，请稍后再试。", RetryAfter: retryAfter}
}

// WriteJSON writes a raw JSON payload. API responses default to private, no-store (API-DD §3.4 item 6).
func WriteJSON(w http.ResponseWriter, status int, payload any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "private, no-store")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// WriteData writes {"data": data}.
func WriteData(w http.ResponseWriter, status int, data any) {
	WriteJSON(w, status, map[string]any{"data": data})
}

// WriteError converts any error into the API error envelope. Unknown errors become INTERNAL_ERROR or,
// for database connectivity failures, SERVICE_UNAVAILABLE; their text is logged, never returned.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		apiErr = ErrInternal
		if IsUnavailable(err) {
			apiErr = ErrUnavailable
		}
		LoggerFrom(r.Context()).Error("request failed", "error", err.Error(), "code", apiErr.Code)
	}
	if apiErr.RetryAfter > 0 {
		secs := int(apiErr.RetryAfter.Round(time.Second) / time.Second)
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	body := map[string]any{"code": apiErr.Code, "message": apiErr.Message}
	if len(apiErr.Details) > 0 {
		body["details"] = apiErr.Details
	}
	WriteJSON(w, apiErr.Status, map[string]any{"error": body, "request_id": RequestID(r.Context())})
}

// IsUnavailable reports whether err looks like the database (or another dependency) being unreachable
// rather than a query/constraint problem: anything that is not a PostgreSQL server error.
func IsUnavailable(err error) bool {
	var pgErr *pgconn.PgError
	return err != nil && !errors.As(err, &pgErr)
}

// LoggerFrom returns the request-scoped logger (with request_id) or the default logger.
func LoggerFrom(ctx interface{ Value(any) any }) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
