// Package config loads amythest configuration from a YAML file with
// environment-variable and command-line flag overrides. Precedence:
// flags > env > yaml > defaults. Secrets are env-only, never YAML.
package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// Vault is the root of the Obsidian-style notes vault. May be a symlink.
	Vault string `yaml:"vault"`
	// Listen is the HTTP listen address. Loopback by default; the server is
	// meant to sit behind Tailscale Serve or another TLS-terminating proxy.
	Listen string `yaml:"listen"`
	// BaseURL is the public path prefix the site is served under, e.g. "/"
	// or "/notes". Used when generating absolute links.
	BaseURL string `yaml:"base_url"`
	// DataDir holds the SQLite indexes and render cache. Safe to delete;
	// everything in it is derived from the vault.
	DataDir string `yaml:"data_dir"`
	// SiteName is shown in the page title and header.
	SiteName string `yaml:"site_name"`
	// RescanInterval is how often the full vault rescan runs. The rescan is
	// the source of truth for index freshness; fsnotify only lowers latency.
	RescanInterval time.Duration `yaml:"rescan_interval"`
	// SharePlugins are executables run on share uploads (see internal/share).
	// Each is probed at startup; plugins that decline (e.g. missing API key)
	// stay inactive.
	SharePlugins []string `yaml:"share_plugins"`

	// --- service hardening (all opt-in; defaults preserve prior behavior) ---

	// LivenessPath and ReadinessPath are orchestrator probe endpoints.
	// /health stays as a readiness alias.
	LivenessPath  string `yaml:"liveness_path"`
	ReadinessPath string `yaml:"readiness_path"`

	// Structured logging: format text|json, level debug|info|warn|error,
	// output stdout|stderr, LogSource includes file:line.
	LogFormat string `yaml:"log_format"`
	LogLevel  string `yaml:"log_level"`
	LogOutput string `yaml:"log_output"`
	LogSource bool   `yaml:"log_source"`

	// RequestIDHeader is the inbound correlation-id header; generated when
	// absent and always echoed on responses.
	RequestIDHeader string `yaml:"request_id_header"`

	// Auth: off (default, today's behavior), static (the existing
	// AMYTHEST_MCP_TOKEN bearer check), or jwt (JWKS-validated bearer
	// tokens). Protect selects the surface: mcp|ui|all.
	AuthMode          string        `yaml:"auth_mode"`
	AuthJWKSURI       string        `yaml:"auth_jwks_uri"`
	AuthIssuer        string        `yaml:"auth_issuer"`
	AuthAudience      string        `yaml:"auth_audience"`
	AuthAlgs          string        `yaml:"auth_algs"`
	AuthLeeway        time.Duration `yaml:"auth_leeway"`
	AuthJWKSRefresh   time.Duration `yaml:"auth_jwks_refresh"`
	AuthProtect       string        `yaml:"auth_protect"`
	AuthRequiredScope string        `yaml:"auth_required_scope"`
}

func defaults() Config {
	return Config{
		Vault:           "",
		Listen:          "127.0.0.1:8639",
		BaseURL:         "/",
		DataDir:         "data",
		SiteName:        "Amythest",
		RescanInterval:  5 * time.Minute,
		LivenessPath:    "/probes/liveness",
		ReadinessPath:   "/probes/readiness",
		LogFormat:       "text",
		LogLevel:        "info",
		LogOutput:       "stdout",
		RequestIDHeader: "X-Request-Id",
		AuthMode:        "off",
		AuthAlgs:        "RS256,ES256",
		AuthLeeway:      60 * time.Second,
		AuthJWKSRefresh: 5 * time.Minute,
		AuthProtect:     "mcp",
	}
}

// Load parses flags from args (not including the program name) and returns
// the merged configuration.
func Load(args []string) (Config, error) {
	cfg := defaults()

	fs := flag.NewFlagSet("amythest", flag.ContinueOnError)
	cfgPath := fs.String("config", envOr("AMYTHEST_CONFIG", ""), "path to amythest.yaml")
	vault := fs.String("vault", "", "vault root directory")
	listen := fs.String("listen", "", "listen address")
	baseURL := fs.String("base-url", "", "public base URL path prefix")
	dataDir := fs.String("data", "", "data directory for indexes")
	livenessPath := fs.String("liveness-path", "", "liveness probe path")
	readinessPath := fs.String("readiness-path", "", "readiness probe path")
	logFormat := fs.String("log-format", "", "log format: text|json")
	logLevel := fs.String("log-level", "", "log level: debug|info|warn|error")
	logOutput := fs.String("log-output", "", "log output: stdout|stderr")
	logSource := fs.Bool("log-source", false, "include source file:line in logs")
	requestIDHeader := fs.String("request-id-header", "", "correlation id header name")
	authMode := fs.String("auth-mode", "", "auth mode: off|static|jwt")
	authJWKS := fs.String("auth-jwks-uri", "", "JWKS document URL (jwt mode)")
	authIssuer := fs.String("auth-issuer", "", "expected iss claim")
	authAudience := fs.String("auth-audience", "", "expected aud claim")
	authAlgs := fs.String("auth-algs", "", "allowed signing algorithms, comma-separated")
	authLeeway := fs.Duration("auth-leeway", 0, "clock-skew tolerance for exp/nbf")
	authRefresh := fs.Duration("auth-jwks-refresh", 0, "JWKS cache refresh interval")
	authProtect := fs.String("auth-protect", "", "surface to protect: mcp|ui|all")
	authScope := fs.String("auth-required-scope", "", "required scope claim value")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}

	if *cfgPath != "" {
		raw, err := os.ReadFile(*cfgPath)
		if err != nil {
			return cfg, fmt.Errorf("read config: %w", err)
		}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return cfg, fmt.Errorf("parse config %s: %w", *cfgPath, err)
		}
	}

	applyEnv(&cfg)
	if *vault != "" {
		cfg.Vault = *vault
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *baseURL != "" {
		cfg.BaseURL = *baseURL
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	setIf := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	setIf(&cfg.LivenessPath, *livenessPath)
	setIf(&cfg.ReadinessPath, *readinessPath)
	setIf(&cfg.LogFormat, *logFormat)
	setIf(&cfg.LogLevel, *logLevel)
	setIf(&cfg.LogOutput, *logOutput)
	if *logSource {
		cfg.LogSource = true
	}
	setIf(&cfg.RequestIDHeader, *requestIDHeader)
	setIf(&cfg.AuthMode, *authMode)
	setIf(&cfg.AuthJWKSURI, *authJWKS)
	setIf(&cfg.AuthIssuer, *authIssuer)
	setIf(&cfg.AuthAudience, *authAudience)
	setIf(&cfg.AuthAlgs, *authAlgs)
	if *authLeeway != 0 {
		cfg.AuthLeeway = *authLeeway
	}
	if *authRefresh != 0 {
		cfg.AuthJWKSRefresh = *authRefresh
	}
	setIf(&cfg.AuthProtect, *authProtect)
	setIf(&cfg.AuthRequiredScope, *authScope)

	if cfg.Vault == "" {
		return cfg, fmt.Errorf("no vault configured: pass -vault, set AMYTHEST_VAULT, or set vault: in %s", *cfgPath)
	}
	vaultPath, err := expandHome(cfg.Vault)
	if err != nil {
		return cfg, err
	}
	cfg.Vault = vaultPath
	if cfg.DataDir, err = expandHome(cfg.DataDir); err != nil {
		return cfg, err
	}
	abs, err := filepath.Abs(cfg.Vault)
	if err != nil {
		return cfg, err
	}
	cfg.Vault = abs
	if st, err := os.Stat(cfg.Vault); err != nil || !st.IsDir() {
		return cfg, fmt.Errorf("vault %s is not a directory", cfg.Vault)
	}
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("AMYTHEST_VAULT"); v != "" {
		cfg.Vault = v
	}
	if v := os.Getenv("AMYTHEST_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := os.Getenv("AMYTHEST_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("AMYTHEST_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	envStr := func(dst *string, key string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	envStr(&cfg.LivenessPath, "AMYTHEST_LIVENESS_PATH")
	envStr(&cfg.ReadinessPath, "AMYTHEST_READINESS_PATH")
	envStr(&cfg.LogFormat, "AMYTHEST_LOG_FORMAT")
	envStr(&cfg.LogLevel, "AMYTHEST_LOG_LEVEL")
	envStr(&cfg.LogOutput, "AMYTHEST_LOG_OUTPUT")
	if v := os.Getenv("AMYTHEST_LOG_SOURCE"); v == "true" || v == "1" {
		cfg.LogSource = true
	}
	envStr(&cfg.RequestIDHeader, "AMYTHEST_REQUEST_ID_HEADER")
	envStr(&cfg.AuthMode, "AMYTHEST_AUTH_MODE")
	envStr(&cfg.AuthJWKSURI, "AMYTHEST_AUTH_JWKS_URI")
	envStr(&cfg.AuthIssuer, "AMYTHEST_AUTH_ISSUER")
	envStr(&cfg.AuthAudience, "AMYTHEST_AUTH_AUDIENCE")
	envStr(&cfg.AuthAlgs, "AMYTHEST_AUTH_ALGS")
	if v := os.Getenv("AMYTHEST_AUTH_LEEWAY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.AuthLeeway = d
		}
	}
	if v := os.Getenv("AMYTHEST_AUTH_JWKS_REFRESH"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.AuthJWKSRefresh = d
		}
	}
	envStr(&cfg.AuthProtect, "AMYTHEST_AUTH_PROTECT")
	envStr(&cfg.AuthRequiredScope, "AMYTHEST_AUTH_REQUIRED_SCOPE")
}

// expandHome resolves a leading "~" or "~/" to the user's home directory so
// YAML configs can ship portable paths like "vault: ~/notes".
func expandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand %s: %w", p, err)
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
