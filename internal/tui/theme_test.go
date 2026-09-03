package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resetThemes(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		customThemes = map[string]Theme{}
		SetTheme("default")
	})
}

func TestSetThemeSwitchesStyles(t *testing.T) {
	resetThemes(t)
	before := cursorStyle.GetForeground()
	if !SetTheme("omarchy") {
		t.Fatal("omarchy should exist")
	}
	if CurrentTheme() != "omarchy" {
		t.Fatalf("current = %q", CurrentTheme())
	}
	if after := cursorStyle.GetForeground(); after == before {
		t.Fatal("accent style unchanged after theme switch")
	}
	if SetTheme("nope") {
		t.Fatal("unknown theme accepted")
	}
	if CurrentTheme() != "omarchy" {
		t.Fatal("failed switch must not change the current theme")
	}
}

func TestLoadThemesFromYAML(t *testing.T) {
	resetThemes(t)
	path := filepath.Join(t.TempDir(), "cli.yaml")
	yaml := `
endpoint: http://localhost:1
theme: hotrod
themes:
  hotrod:
    accent: "#ff2800"
    due: "#ffaa00"
`
	os.WriteFile(path, []byte(yaml), 0o644)
	if err := LoadThemes(path); err != nil {
		t.Fatal(err)
	}
	if CurrentTheme() != "hotrod" {
		t.Fatalf("current = %q", CurrentTheme())
	}
	// Unset fields inherit the default palette.
	got := resolve(customThemes["hotrod"])
	if got.Accent != "#ff2800" || got.Title != defaultTheme.Title {
		t.Fatalf("resolved = %+v", got)
	}
	names := ThemeNames()
	if names[0] != "default" || names[1] != "omarchy" || names[len(names)-1] != "hotrod" {
		t.Fatalf("names = %v", names)
	}
}

func TestLoadThemesUnknownNameErrors(t *testing.T) {
	resetThemes(t)
	path := filepath.Join(t.TempDir(), "cli.yaml")
	os.WriteFile(path, []byte("theme: nonexistent\n"), 0o644)
	err := LoadThemes(path)
	if err == nil || !strings.Contains(err.Error(), "unknown theme") {
		t.Fatalf("err = %v", err)
	}
	if CurrentTheme() != "default" {
		t.Fatalf("current = %q", CurrentTheme())
	}
}

func TestLoadThemesEnvOverride(t *testing.T) {
	resetThemes(t)
	t.Setenv("AMY_THEME", "omarchy")
	if err := LoadThemes(""); err != nil {
		t.Fatal(err)
	}
	if CurrentTheme() != "omarchy" {
		t.Fatalf("current = %q", CurrentTheme())
	}
}

func TestThemePickerPreviewAndRestore(t *testing.T) {
	resetThemes(t)
	v := newThemePickerView()
	if v.names[v.cursor] != "default" {
		t.Fatalf("picker should start on the current theme, got %q", v.names[v.cursor])
	}
	v.Update(keyMsg("j")) // preview omarchy live
	if CurrentTheme() != "omarchy" {
		t.Fatalf("moving should preview, current = %q", CurrentTheme())
	}
	_, cmd := v.Update(keyMsg("esc"))
	if CurrentTheme() != "default" {
		t.Fatalf("esc should restore, current = %q", CurrentTheme())
	}
	if cmd == nil {
		t.Fatal("esc should pop the picker")
	}
	// Enter keeps the selection.
	v2 := newThemePickerView()
	v2.Update(keyMsg("j"))
	_, cmd = v2.Update(keyMsg("enter"))
	if CurrentTheme() != "omarchy" || cmd == nil {
		t.Fatalf("enter should keep the theme, current = %q", CurrentTheme())
	}
	out := v2.View(80, 24)
	if !strings.Contains(out, "omarchy") || !strings.Contains(out, "(current)") {
		t.Fatalf("picker view:\n%s", out)
	}
}
