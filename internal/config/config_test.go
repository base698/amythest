package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExpandsHomeRelativeVaultAndDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{"-vault", "~/notes", "-data", "~/amythest-data"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "notes"); cfg.Vault != want {
		t.Errorf("Vault = %q, want %q", cfg.Vault, want)
	}
	if want := filepath.Join(home, "amythest-data"); cfg.DataDir != want {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, want)
	}
}

func TestLoadExpandsBareTildeVault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := Load([]string{"-vault", "~"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Vault != home {
		t.Errorf("Vault = %q, want %q", cfg.Vault, home)
	}
}

func TestLoadLeavesTildePrefixedNamesAlone(t *testing.T) {
	root := t.TempDir()
	vault := filepath.Join(root, "~backup")
	if err := os.Mkdir(vault, 0o755); err != nil {
		t.Fatal(err)
	}

	// "~backup" names a real directory; only "~" and "~/" mean the home dir.
	cfg, err := Load([]string{"-vault", vault})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Vault != vault {
		t.Errorf("Vault = %q, want %q", cfg.Vault, vault)
	}
}
