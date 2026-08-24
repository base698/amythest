// Package apiclient is an HTTP client for a running amythest server. It is
// UI-agnostic: the amy TUI sits on top of it, and scripts can reuse it.
//
// Configuration precedence matches the server: flags > env > yaml > defaults.
// Secrets (KANBAN_USERNAME / KANBAN_PASSWORD) are env-only — read from the
// process environment or the shell-style env file shared with the server and
// the kanban.py skill client, never from yaml.
package apiclient

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultEndpoint = "http://127.0.0.1:8639"

type Config struct {
	// Endpoint is the base URL of the amythest server, including any
	// public prefix (e.g. https://host/notes). No trailing slash.
	Endpoint string
	// Timeout applies per HTTP request.
	Timeout time.Duration
	// SessionFile caches the login session. Shared with kanban.py.
	SessionFile string
	// EnvFile is a shell-style KEY=VALUE file consulted for credentials
	// when KANBAN_USERNAME/KANBAN_PASSWORD are not in the environment.
	EnvFile string
	// File is the yaml config path actually read ("" when none existed);
	// the sources loader shares it.
	File string
}

type fileConfig struct {
	Endpoint string `yaml:"endpoint"`
}

// LoadConfig resolves configuration from args, environment, and the optional
// yaml file at ~/.config/amythest/cli.yaml (or -config).
func LoadConfig(args []string) (Config, error) {
	fs := flag.NewFlagSet("amy", flag.ContinueOnError)
	endpoint := fs.String("endpoint", "", "amythest server base URL (e.g. https://host/notes)")
	configPath := fs.String("config", "", "path to cli.yaml (default ~/.config/amythest/cli.yaml)")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	cfg := Config{
		Endpoint:    defaultEndpoint,
		Timeout:     30 * time.Second,
		SessionFile: envOr("KANBAN_SESSION_FILE", filepath.Join(homeDir(), ".cache", "amythest-kanban", "session.json")),
		EnvFile:     envOr("KANBAN_ENV_FILE", filepath.Join(homeDir(), ".config", "amythest", "env")),
	}

	path := expandHome(*configPath)
	if path == "" {
		path = filepath.Join(homeDir(), ".config", "amythest", "cli.yaml")
	}
	if raw, err := os.ReadFile(path); err == nil {
		var fc fileConfig
		if err := yaml.Unmarshal(raw, &fc); err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", path, err)
		}
		if fc.Endpoint != "" {
			cfg.Endpoint = fc.Endpoint
		}
		cfg.File = path
	} else if *configPath != "" {
		// An explicitly named config file must exist.
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	if v := os.Getenv("AMYTHEST_ENDPOINT"); v != "" {
		cfg.Endpoint = v
	}
	if *endpoint != "" {
		cfg.Endpoint = *endpoint
	}
	cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/")
	cfg.SessionFile = expandHome(cfg.SessionFile)
	cfg.EnvFile = expandHome(cfg.EnvFile)
	return cfg, nil
}

// KanbanBase is the kanban mount under the endpoint. It doubles as the
// session-cache "base" key so amy and kanban.py invalidate each other's
// cached sessions exactly when they point at different servers.
func (c Config) KanbanBase() string {
	return c.Endpoint + "/kanban"
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

func expandHome(path string) string {
	if path == "~" {
		return homeDir()
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir(), path[2:])
	}
	return path
}
