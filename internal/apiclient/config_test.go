package apiclient

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigPrecedenceFlagBeatsEnvBeatsYaml(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "cli.yaml")
	if err := os.WriteFile(yamlPath, []byte("endpoint: http://yaml.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AMYTHEST_ENDPOINT", "")
	cfg, err := LoadConfig([]string{"-config", yamlPath})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != "http://yaml.example" {
		t.Fatalf("yaml endpoint = %q", cfg.Endpoint)
	}

	t.Setenv("AMYTHEST_ENDPOINT", "http://env.example/")
	cfg, err = LoadConfig([]string{"-config", yamlPath})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != "http://env.example" { // trailing slash trimmed
		t.Fatalf("env endpoint = %q", cfg.Endpoint)
	}

	cfg, err = LoadConfig([]string{"-config", yamlPath, "-endpoint", "http://flag.example"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != "http://flag.example" {
		t.Fatalf("flag endpoint = %q", cfg.Endpoint)
	}
	if cfg.KanbanBase() != "http://flag.example/kanban" {
		t.Fatalf("kanban base = %q", cfg.KanbanBase())
	}
}

func TestLoadConfigMissingExplicitFileFails(t *testing.T) {
	if _, err := LoadConfig([]string{"-config", filepath.Join(t.TempDir(), "nope.yaml")}); err == nil {
		t.Fatal("expected error for missing explicit config file")
	}
}

func TestLoadConfigDefaultsWithoutFile(t *testing.T) {
	t.Setenv("AMYTHEST_ENDPOINT", "")
	t.Setenv("KANBAN_SESSION_FILE", filepath.Join(t.TempDir(), "s.json"))
	t.Setenv("KANBAN_ENV_FILE", "")
	t.Setenv("HOME", t.TempDir()) // no ~/.config/amythest/cli.yaml
	cfg, err := LoadConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != defaultEndpoint {
		t.Fatalf("default endpoint = %q", cfg.Endpoint)
	}
}
