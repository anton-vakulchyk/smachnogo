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
	"smachnogo/pkg/models"
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

// ProfileGetter is the slice of the store the entitlement middleware needs.
type ProfileGetter interface {
	GetProfile(ctx context.Context, userID string) (*models.Profile, error)
}

type ctxKeyEntitlement struct{}

// EntitlementInfo rides the context from the middleware to handlers, so the
// billing decision is read once per request. Authoritative *enforcement*
// stays in the conditional writes (ConsumeFreeScan) — this is the routing
// signal: which quota path, which caps, what /users/me reports.
type EntitlementInfo struct {
	Enforced   bool // false when ENTITLEMENT_MODE=off
	Subscribed bool // entitlement permits scanning beyond the free allowance
	Profile    *models.Profile
}

func EntitlementFrom(ctx context.Context) EntitlementInfo {
	v, ok := ctx.Value(ctxKeyEntitlement{}).(EntitlementInfo)
	if !ok {
		// Route not wrapped — treat as unenforced rather than failing open
		// on a paid path silently: scans/estimate routes are always wrapped.
		return EntitlementInfo{Enforced: false, Subscribed: true, Profile: &models.Profile{}}
	}
	return v
}

// Entitlement loads the billing profile into context for routes that need
// it (scan create, estimates, refine, /users/me). Mounted per-route — a
// profile read on every request would be waste. The 402 itself is issued by
// the scan-create handler AFTER validation, via the atomic ConsumeFreeScan
// (BEFORE daily-quota consumption, so a paywalled request never strands an
// un-refundable increment).
func Entitlement(profiles ProfileGetter, mode string) func(http.Handler) http.Handler {
	enforced := mode == "enforce"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The profile loads in EVERY mode: routes wrapped here serve
			// profile data beyond billing (limits, apple_linked) — "off"
			// only disables the billing gates, it must not blank the data.
			p, err := profiles.GetProfile(r.Context(), UserID(r.Context()))
			if err != nil {
				logging.From(r.Context()).Error("entitlement profile read", zap.Error(err))
				writeJSONError(w, http.StatusInternalServerError, "INTERNAL", "internal error")
				return
			}
			info := EntitlementInfo{
				Enforced:   enforced,
				Subscribed: !enforced || p.Ent().Subscribed(),
				Profile:    p,
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyEntitlement{}, info)))
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
