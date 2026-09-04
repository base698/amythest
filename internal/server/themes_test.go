package server

import (
	"strings"
	"testing"

	"github.com/base698/amythest/internal/config"
)

func TestThemesCSSGeneration(t *testing.T) {
	css := themesCSS(map[string]config.WebTheme{
		"hotrod": {
			Light: map[string]string{"accent": "#d40000", "bg-alt": "#fff0f0"},
			Dark:  map[string]string{"accent": "#ff2800"},
		},
	})
	for _, want := range []string{
		`:root[data-palette="hotrod"] {`,
		"--accent: #d40000;",
		`:root[data-palette="hotrod"][data-theme="dark"] {`,
		"--accent: #ff2800;",
		"--panel: #fff0f0;", // kanban mirror of bg-alt
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("css missing %q:\n%s", want, css)
		}
	}
}

func TestThemesCSSSingleModeAppliesToBoth(t *testing.T) {
	css := themesCSS(map[string]config.WebTheme{
		"nightonly": {Dark: map[string]string{"bg": "#000011"}},
	})
	if strings.Count(css, "--bg: #000011;") != 2 {
		t.Fatalf("dark-only theme should cover both modes:\n%s", css)
	}
}

func TestThemesCSSRejectsInjection(t *testing.T) {
	css := themesCSS(map[string]config.WebTheme{
		"evil":     {Light: map[string]string{"accent": "red;} body{display:none", "bogus-token": "#fff"}},
		"bad name": {Light: map[string]string{"accent": "#fff"}},
	})
	for _, banned := range []string{"display:none", "bogus-token", "bad name"} {
		if strings.Contains(css, banned) {
			t.Fatalf("css contains %q:\n%s", banned, css)
		}
	}
}

func TestThemeNamesOrder(t *testing.T) {
	s := &Server{cfg: config.Config{Themes: map[string]config.WebTheme{
		"zebra":    {},
		"hotrod":   {},
		"Bad Name": {},
	}}}
	got := strings.Join(s.themeNames(), ",")
	if got != "default,omarchy,hotrod,zebra" {
		t.Fatalf("names = %s", got)
	}
}

func TestThemesCSSFontTokens(t *testing.T) {
	css := themesCSS(map[string]config.WebTheme{
		"typewriter": {Light: map[string]string{
			"font-body": `"Courier Prime", "Courier New", monospace`,
			"font-mono": `"Courier Prime", monospace`,
		}},
	})
	if !strings.Contains(css, `--font-body: "Courier Prime", "Courier New", monospace;`) {
		t.Fatalf("font token missing:\n%s", css)
	}
	// Still injection-proof: braces/semicolons in a font value are dropped.
	css = themesCSS(map[string]config.WebTheme{
		"evil": {Light: map[string]string{"font-body": `x;} body{display:none`}},
	})
	if strings.Contains(css, "display:none") {
		t.Fatalf("injection survived:\n%s", css)
	}
}
