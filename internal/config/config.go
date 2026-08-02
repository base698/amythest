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
}

func defaults() Config {
	return Config{
		Vault:          "",
		Listen:         "127.0.0.1:8639",
		BaseURL:        "/",
		DataDir:        "data",
		SiteName:       "Amythest",
		RescanInterval: 5 * time.Minute,
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
