// Package authjwt validates bearer JWTs against a JWKS/OIDC issuer. It wraps
// golang-jwt + keyfunc behind a small interface so the libraries can be
// swapped without touching the server.
//
// Validation rules: signature via JWKS key matched by kid; exp/nbf/iat with
// configurable leeway; iss/aud enforced when configured; algorithms outside
// the allowlist (and "none", always) rejected. JWKS is fetched at startup
// (fail fast), cached, refreshed periodically and on unknown kid
// (rate-limited by the library); transient refresh failures serve from cache.
package authjwt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

type Config struct {
	JWKSURI       string
	Issuer        string        // enforced when non-empty
	Audience      string        // enforced when non-empty
	Algs          []string      // allowlist; empty = RS256,ES256
	Leeway        time.Duration // clock-skew tolerance
	Refresh       time.Duration // JWKS refresh interval
	RequiredScope string        // optional scope gate
}

// ErrForbidden distinguishes "valid token, insufficient scope" (403) from
// invalid-token errors (401).
var ErrForbidden = errors.New("insufficient scope")

type Validator struct {
	cfg    Config
	keys   keyfunc.Keyfunc
	parser *jwt.Parser
}

// New builds a validator, fetching the JWKS immediately — an unreachable or
// empty JWKS is a startup error (fail closed).
func New(ctx context.Context, cfg Config) (*Validator, error) {
	if cfg.JWKSURI == "" {
		return nil, errors.New("jwt auth requires a JWKS URI")
	}
	algs := cfg.Algs
	if len(algs) == 0 {
		algs = []string{"RS256", "ES256"}
	}
	for _, alg := range algs {
		lower := strings.ToLower(strings.TrimSpace(alg))
		if lower == "none" {
			return nil, errors.New(`"none" is not an acceptable signing algorithm`)
		}
	}
	refresh := cfg.Refresh
	if refresh <= 0 {
		refresh = 5 * time.Minute
	}
	failFirst := false // fail fast: an unreachable JWKS is a startup error
	keys, err := keyfunc.NewDefaultOverrideCtx(ctx, []string{cfg.JWKSURI}, keyfunc.Override{
		RefreshInterval:           refresh,
		NoErrorReturnFirstHTTPReq: &failFirst,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS %s: %w", cfg.JWKSURI, err)
	}

	opts := []jwt.ParserOption{
		jwt.WithValidMethods(algs),
		jwt.WithLeeway(cfg.Leeway),
		jwt.WithExpirationRequired(),
	}
	if cfg.Issuer != "" {
		opts = append(opts, jwt.WithIssuer(cfg.Issuer))
	}
	if cfg.Audience != "" {
		opts = append(opts, jwt.WithAudience(cfg.Audience))
	}
	return &Validator{cfg: cfg, keys: keys, parser: jwt.NewParser(opts...)}, nil
}

// Validate checks one bearer token. A nil return means authenticated (and
// authorized when a scope is required). ErrForbidden means the token was
// valid but lacks the required scope.
func (v *Validator) Validate(token string) error {
	parsed, err := v.parser.Parse(token, v.keys.Keyfunc)
	if err != nil {
		return err
	}
	if v.cfg.RequiredScope == "" {
		return nil
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return ErrForbidden
	}
	if scopeString, ok := claims["scope"].(string); ok {
		for _, scope := range strings.Fields(scopeString) {
			if scope == v.cfg.RequiredScope {
				return nil
			}
		}
	}
	// Also accept array-form scp claims (some issuers).
	if list, ok := claims["scp"].([]any); ok {
		for _, item := range list {
			if s, ok := item.(string); ok && s == v.cfg.RequiredScope {
				return nil
			}
		}
	}
	return ErrForbidden
}
