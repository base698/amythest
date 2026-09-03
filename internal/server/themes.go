package server

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/base698/amythest/internal/config"
)

// Web theming: a theme is a named set of the CSS custom properties the
// stylesheets are written against. "default" and "omarchy" are baked into
// the shipped CSS; custom themes come from amythest.yaml and are served as
// generated CSS on /gen/themes.css:
//
//	themes:
//	  hotrod:
//	    light: {accent: "#d40000", mark: "#ffe08a"}
//	    dark:  {accent: "#ff2800"}
//
// The active theme is chosen client-side ([data-palette] on <html>, saved
// in localStorage) so it follows the reader, not the server.

// builtinPalettes are the names always offered by the picker.
var builtinPalettes = []string{"default", "omarchy"}

// themeTokens is the allowlist of notes-site custom properties a theme may
// set, mirrored into the kanban UI's equivalent variables where one exists.
var themeTokens = map[string]string{ // token → kanban var ("" = none)
	"bg":          "--bg",
	"bg-alt":      "--panel",
	"fg":          "--text",
	"fg-muted":    "--muted",
	"fg-faint":    "",
	"accent":      "--accent",
	"accent-soft": "",
	"border":      "--border",
	"highlight":   "",
	"mark":        "",
}

// safeCSSValue keeps generated CSS injection-proof: color functions,
// hex, and keywords only — no braces, semicolons, url(), or escapes.
var safeCSSValue = regexp.MustCompile(`^[#a-zA-Z0-9(),.%/ -]+$`)

// themeNames lists the picker's options: built-ins, then customs sorted.
func (s *Server) themeNames() []string {
	names := append([]string{}, builtinPalettes...)
	var custom []string
	for name := range s.cfg.Themes {
		if name != "default" && name != "omarchy" && validThemeName(name) {
			custom = append(custom, name)
		}
	}
	sort.Strings(custom)
	return append(names, custom...)
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

func validThemeName(name string) bool { return validName.MatchString(name) }

// themesCSS renders every custom theme as attribute-scoped variable blocks.
// Light tokens go on the bare palette selector (so a dark-only theme still
// themes light mode via its dark block below and vice versa is handled by
// emitting whichever modes exist).
func themesCSS(themes map[string]config.WebTheme) string {
	var names []string
	for name := range themes {
		if validThemeName(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("/* generated from amythest.yaml themes: */\n")
	for _, name := range names {
		t := themes[name]
		light, dark := t.Light, t.Dark
		// A single-mode theme applies to both modes.
		if len(light) == 0 {
			light = dark
		}
		if len(dark) == 0 {
			dark = light
		}
		writeThemeBlock(&b, fmt.Sprintf(`:root[data-palette=%q]`, name), light)
		writeThemeBlock(&b, fmt.Sprintf(`:root[data-palette=%q][data-theme="dark"]`, name), dark)
	}
	return b.String()
}

func writeThemeBlock(b *strings.Builder, selector string, tokens map[string]string) {
	if len(tokens) == 0 {
		return
	}
	var keys []string
	for k := range tokens {
		if _, ok := themeTokens[k]; ok && safeCSSValue.MatchString(tokens[k]) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return
	}
	sort.Strings(keys)
	b.WriteString(selector + " {\n")
	for _, k := range keys {
		fmt.Fprintf(b, "  --%s: %s;\n", k, tokens[k])
		if kanbanVar := themeTokens[k]; kanbanVar != "" && kanbanVar != "--"+k {
			fmt.Fprintf(b, "  %s: %s;\n", kanbanVar, tokens[k])
		}
	}
	b.WriteString("}\n")
}

func (s *Server) handleThemesCSS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write([]byte(themesCSS(s.cfg.Themes)))
}
