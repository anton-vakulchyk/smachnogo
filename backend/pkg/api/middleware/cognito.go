package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"go.uber.org/zap"

	"smachnogo/pkg/logging"
)

// CognitoAuth verifies Cognito ACCESS tokens in-Lambda (not an API GW
// authorizer — keeps local dev byte-identical and colocates the future
// entitlement logic). user_id = the `sub` claim ONLY: access tokens do not
// carry user-pool custom attributes, so we never depend on them.
type CognitoAuth struct {
	issuer   string
	clientID string
	cache    *jwk.Cache
	jwksURL  string
}

func NewCognitoAuth(ctx context.Context, region, poolID, clientID string) (*CognitoAuth, error) {
	issuer := fmt.Sprintf("https://cognito-idp.%s.amazonaws.com/%s", region, poolID)
	jwksURL := issuer + "/.well-known/jwks.json"

	cache := jwk.NewCache(ctx)
	if err := cache.Register(jwksURL, jwk.WithMinRefreshInterval(15*time.Minute)); err != nil {
		return nil, fmt.Errorf("register jwks: %w", err)
	}
	// Warm + validate at cold start: fail fast on misconfig.
	if _, err := cache.Refresh(ctx, jwksURL); err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}
	return &CognitoAuth{issuer: issuer, clientID: clientID, cache: cache, jwksURL: jwksURL}, nil
}

func (c *CognitoAuth) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if raw == "" {
				writeJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing bearer token")
				return
			}
			userID, err := c.verify(r.Context(), raw)
			if err != nil {
				logging.From(r.Context()).Info("token rejected", zap.Error(err))
				writeJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
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

func (c *CognitoAuth) verify(ctx context.Context, raw string) (string, error) {
	keySet, err := c.cache.Get(ctx, c.jwksURL)
	if err != nil {
		return "", fmt.Errorf("jwks: %w", err)
	}
	tok, err := jwt.Parse([]byte(raw),
		jwt.WithKeySet(keySet),
		jwt.WithValidate(true),
		jwt.WithIssuer(c.issuer),
		jwt.WithAcceptableSkew(30*time.Second),
	)
	if err != nil {
		return "", err
	}
	// Access-token shape: token_use=access and client_id must match the app
	// client (ID tokens carry aud instead — reject them; the contract is
	// access tokens only).
	if use, _ := tok.Get("token_use"); use != "access" {
		return "", fmt.Errorf("token_use %v is not access", use)
	}
	if cid, _ := tok.Get("client_id"); cid != c.clientID {
		return "", fmt.Errorf("client_id mismatch")
	}
	sub := tok.Subject()
	if sub == "" {
		return "", fmt.Errorf("empty sub")
	}
	return sub, nil
}
