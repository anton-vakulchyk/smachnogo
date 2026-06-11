// Package api assembles the HTTP surface. One router serves both
// transports: algnhsa wraps it on Lambda, net/http serves it locally.
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"smachnogo/pkg/api/handlers"
	"smachnogo/pkg/api/middleware"
	"smachnogo/pkg/config"
	"smachnogo/pkg/store"
)

type Deps struct {
	Cfg           *config.Config
	Logger        *zap.Logger
	Scans         *handlers.Scans
	Meals         *handlers.Meals
	Users         *handlers.Users
	Subscriptions *handlers.Subscriptions // App Store receipt + webhook (M7.2)
	Store         *store.Store            // entitlement middleware's profile reads
	Cognito       *middleware.CognitoAuth // required when AUTH_MODE=cognito
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestIDMiddleware(d.Logger))
	r.Use(middleware.MaxBody(64 * 1024))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"sha":"` + d.Cfg.GitSHA + `"}`))
	})

	// App Store Server Notifications arrive from Apple, which won't carry our
	// bearer token — the route bypasses auth and trusts JWS verification only.
	if d.Subscriptions != nil {
		r.With(middleware.MaxBody(256*1024)).Post("/v1/webhooks/appstore", d.Subscriptions.Webhook)
	}

	r.Route("/v1", func(v1 chi.Router) {
		switch d.Cfg.AuthMode {
		case "static":
			v1.Use(middleware.StaticAuth(d.Cfg.StaticBearerToken, d.Cfg.StaticUserID))
		case "cognito":
			if d.Cognito == nil {
				panic("AUTH_MODE=cognito requires a constructed CognitoAuth")
			}
			v1.Use(d.Cognito.Middleware())
		default:
			panic("unsupported AUTH_MODE: " + d.Cfg.AuthMode)
		}

		// Entitlement (the billing seam) mounts only on routes that need the
		// profile: the paid path, tiered-cap paths, and the state endpoint.
		ent := middleware.Entitlement(d.Store, d.Cfg.EntitlementMode)

		v1.With(ent).Post("/scans", d.Scans.Create)
		v1.Post("/scans/{scanID}/uploaded", d.Scans.Uploaded)
		v1.Get("/scans/{scanID}", d.Scans.Get)
		v1.Post("/scans/{scanID}/confirm", d.Scans.Confirm)
		v1.With(ent).Post("/scans/{scanID}/refine", d.Scans.Refine)

		v1.Post("/meals", d.Meals.Create)
		v1.Get("/meals", d.Meals.List)
		v1.Get("/meals/recent", d.Meals.Recent)
		v1.With(ent).Post("/meals/estimate", d.Meals.Estimate)
		v1.Patch("/meals/{mealID}", d.Meals.Patch)
		v1.Delete("/meals/{mealID}", d.Meals.Delete)
		v1.Get("/summary", d.Meals.Summary)

		v1.With(ent).Get("/users/me", d.Users.Me)
		v1.Delete("/users/me", d.Users.DeleteMe)
		v1.Get("/export", d.Users.Export)

		if d.Subscriptions != nil {
			v1.Post("/subscriptions/receipt", d.Subscriptions.Receipt)
		}
	})

	return r
}
