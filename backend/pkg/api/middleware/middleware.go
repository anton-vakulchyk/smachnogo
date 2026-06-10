// Package middleware: auth (the Cognito seam), entitlement (the billing
// seam), request-id propagation, body caps. Handlers never parse auth —
// they read user_id from context only.
package middleware

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"smachnogo/pkg/logging"
)

type ctxKeyUserID struct{}
type ctxKeyRequestID struct{}

func UserID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyUserID{}).(string)
	return v
}

func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRequestID{}).(string)
	return v
}

// RequestIDMiddleware accepts an inbound X-Request-Id or generates one,
// reflects it on the response, and enriches the context logger so every
// log line in the request carries it.
func RequestIDMiddleware(base *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rid := r.Header.Get("X-Request-Id")
			if rid == "" || len(rid) > 64 {
				rid = uuid.NewString()
			}
			w.Header().Set("X-Request-Id", rid)
			ctx := context.WithValue(r.Context(), ctxKeyRequestID{}, rid)
			ctx = logging.Into(ctx, base.With(zap.String("request_id", rid)))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// StaticAuth is M1's auth mode: constant-time bearer compare → fixed dev
// user. M2 swaps this middleware for Cognito JWT verification; handlers and
// everything downstream are untouched (the auth seam).
func StaticAuth(token, userID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				writeJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or missing bearer token")
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyUserID{}, userID)
			if l := logging.From(ctx); l != nil {
				ctx = logging.Into(ctx, l.With(zap.String("user_id", userID)))
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Entitlement is the billing seam — allow-all until M7 implements the
// free-allowance/subscription check here (BEFORE quota consumption, so a
// paywalled request never strands an un-refundable counter increment).
func Entitlement() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

// MaxBody caps request bodies (64KB default) before any handler parses.
func MaxBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// writeJSONError is duplicated minimally here to avoid an import cycle with
// the handlers package; the canonical helper lives in pkg/api/handlers.
func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + msg + `"}}`))
}
