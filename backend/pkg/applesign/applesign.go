// Package applesign verifies Sign in with Apple identity tokens server-side
// (native-app flow: the app gets an RS256 JWT from ASAuthorization and
// POSTs it to us — no Cognito federation, no Apple Service ID needed; just
// Apple's public JWKS).
package applesign

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

const (
	issuer  = "https://appleid.apple.com"
	jwksURL = "https://appleid.apple.com/auth/keys"
)

// Identity is what a verified token asserts.
type Identity struct {
	Sub   string // Apple's stable per-team user id
	Nonce string // nonce claim when the client requested one
}

type Verifier interface {
	Verify(ctx context.Context, identityToken string) (Identity, error)
}

// JWKSVerifier does full verification: signature against Apple's JWKS,
// issuer, audience (our bundle id), expiry.
type JWKSVerifier struct {
	cache    *jwk.Cache
	audience string
}

// NewJWKSVerifier registers Apple's JWKS for cached fetching. No warm fetch:
// linking is a rare path and must not add a cold-start network dependency.
func NewJWKSVerifier(ctx context.Context, audience string) (*JWKSVerifier, error) {
	cache := jwk.NewCache(ctx)
	if err := cache.Register(jwksURL, jwk.WithMinRefreshInterval(15*time.Minute)); err != nil {
		return nil, fmt.Errorf("register apple jwks: %w", err)
	}
	return &JWKSVerifier{cache: cache, audience: audience}, nil
}

func (v *JWKSVerifier) Verify(ctx context.Context, raw string) (Identity, error) {
	keySet, err := v.cache.Get(ctx, jwksURL)
	if err != nil {
		return Identity{}, fmt.Errorf("apple jwks: %w", err)
	}
	tok, err := jwt.Parse([]byte(raw),
		jwt.WithKeySet(keySet),
		jwt.WithValidate(true),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(v.audience),
		jwt.WithAcceptableSkew(30*time.Second),
	)
	if err != nil {
		return Identity{}, err
	}
	return identityFrom(tok)
}

// Insecure decodes without signature/claim validation — Sign-in-with-Apple
// can't run against dev infrastructure from simulators/test scripts, so dev
// accepts crafted tokens the same way APPSTORE_VERIFY_MODE=insecure_dev
// does. Config refuses this mode in prod.
type Insecure struct{}

func (Insecure) Verify(_ context.Context, raw string) (Identity, error) {
	tok, err := jwt.Parse([]byte(raw), jwt.WithVerify(false), jwt.WithValidate(false))
	if err != nil {
		return Identity{}, err
	}
	return identityFrom(tok)
}

func identityFrom(tok jwt.Token) (Identity, error) {
	id := Identity{Sub: tok.Subject()}
	if id.Sub == "" {
		return Identity{}, fmt.Errorf("apple token has no sub")
	}
	if n, ok := tok.Get("nonce"); ok {
		if s, ok := n.(string); ok {
			id.Nonce = s
		}
	}
	return id, nil
}

// NonceMatches reports whether the token's nonce claim equals the
// SHA256-hex of the client's raw nonce (the standard SIWA pattern: the app
// passes the hash to Apple, keeps the raw value, and sends us the raw).
func NonceMatches(tokenNonce, rawNonce string) bool {
	if tokenNonce == "" {
		return rawNonce == "" // token carries no nonce — nothing to check
	}
	sum := sha256.Sum256([]byte(rawNonce))
	return tokenNonce == hex.EncodeToString(sum[:])
}
