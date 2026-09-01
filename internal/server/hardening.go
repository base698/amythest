package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/base698/amythest/internal/authjwt"
	"github.com/base698/amythest/internal/config"
)

// buildAuthGuard returns the middleware for the configured auth mode, or nil
// for off. jwt mode fails fast on bad config or unreachable JWKS.
func (s *Server) buildAuthGuard(cfg config.Config) (func(http.Handler) http.Handler, error) {
	switch strings.ToLower(cfg.AuthMode) {
	case "", "off":
		return nil, nil
	case "static":
		token := os.Getenv("AMYTHEST_MCP_TOKEN")
		if token == "" {
			return nil, errors.New("auth mode static requires AMYTHEST_MCP_TOKEN")
		}
		validate := func(bearer string) error {
			if bearer != token {
				return errors.New("invalid token")
			}
			return nil
		}
		return s.guardFor(cfg, validate), nil
	case "jwt":
		var algs []string
		for _, alg := range strings.Split(cfg.AuthAlgs, ",") {
			if alg = strings.TrimSpace(alg); alg != "" {
				algs = append(algs, alg)
			}
		}
		validator, err := authjwt.New(s.baseCtx, authjwt.Config{
			JWKSURI:       cfg.AuthJWKSURI,
			Issuer:        cfg.AuthIssuer,
			Audience:      cfg.AuthAudience,
			Algs:          algs,
			Leeway:        cfg.AuthLeeway,
			Refresh:       cfg.AuthJWKSRefresh,
			RequiredScope: cfg.AuthRequiredScope,
		})
		if err != nil {
			return nil, fmt.Errorf("auth mode jwt: %w", err)
		}
		return s.guardFor(cfg, validator.Validate), nil
	default:
		return nil, fmt.Errorf("unknown auth mode %q (off|static|jwt)", cfg.AuthMode)
	}
}

// guardFor wraps a handler so requests to the protected surface must carry a
// bearer token that validate accepts. Probes are always exempt.
func (s *Server) guardFor(cfg config.Config, validate func(string) error) func(http.Handler) http.Handler {
	protect := strings.ToLower(cfg.AuthProtect)
	if protect == "" {
		protect = "mcp"
	}
	protects := func(path string) bool {
		if s.isProbePath(path) {
			return false
		}
		isMCP := path == "/mcp" || strings.HasPrefix(path, "/mcp/")
		switch protect {
		case "mcp":
			return isMCP
		case "ui":
			return !isMCP
		case "all":
			return true
		default:
			return isMCP
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !protects(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			auth := r.Header.Get("Authorization")
			bearer, ok := strings.CutPrefix(auth, "Bearer ")
			if !ok || bearer == "" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="amythest"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if err := validate(bearer); err != nil {
				if errors.Is(err, authjwt.ErrForbidden) {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
				// Generic reason only; never leak validation detail.
				w.Header().Set("WWW-Authenticate", `Bearer realm="amythest", error="invalid_token"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
