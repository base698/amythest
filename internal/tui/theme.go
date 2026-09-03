package tui

import (
	"fmt"
	"os"
	"sort"

	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

// Theme is a named amy palette. Values are lipgloss colors — ANSI-256
// indexes ("212") or hex ("#ff5fa2"). Empty fields inherit the default
// theme, so a custom theme in cli.yaml only lists what it changes:
//
//	theme: omarchy            # or any built-in / custom name
//	themes:
//	  hotrod:
//	    accent: "#ff2800"
//	    due: "#ffaa00"
type Theme struct {
	HeaderFg string `yaml:"header_fg"` // top bar text
	HeaderBg string `yaml:"header_bg"`
	StatusFg string `yaml:"status_fg"` // bottom bar text
	StatusBg string `yaml:"status_bg"`
	Accent   string `yaml:"accent"` // cursor line, focused column border
	Title    string `yaml:"title"`  // group/column headers
	Dim      string `yaml:"dim"`    // secondary text, done tasks
	Due      string `yaml:"due"`    // due dates, [mine], pins
	Blocked  string `yaml:"blocked"`
	Link     string `yaml:"link"`
	Border   string `yaml:"border"` // unfocused column border
	SearchFg string `yaml:"search_fg"`
	SearchBg string `yaml:"search_bg"`
	DangerFg string `yaml:"danger_fg"`
	DangerBg string `yaml:"danger_bg"`
	P0       string `yaml:"p0"`
	P1       string `yaml:"p1"`
	P2       string `yaml:"p2"`
}

// defaultTheme is amy's stock look: terminal-native ANSI-256 colors that
// follow the terminal's own light/dark palette, amethyst accents.
var defaultTheme = Theme{
	HeaderFg: "15", HeaderBg: "54",
	StatusFg: "245", StatusBg: "236",
	Accent: "212", Title: "110", Dim: "242", Due: "179",
	Blocked: "203", Link: "111", Border: "240",
	SearchFg: "16", SearchBg: "179",
	DangerFg: "15", DangerBg: "124",
	P0: "203", P1: "215", P2: "110",
}

// omarchyTheme is the synthwave sunset: deep purple night, hot pink accent,
// sunset orange highlights.
var omarchyTheme = Theme{
	HeaderFg: "#ffd9ec", HeaderBg: "#2b1440",
	StatusFg: "#b39ac9", StatusBg: "#231136",
	Accent: "#ff5fa2", Title: "#c792ea", Dim: "#8574a1", Due: "#ffb454",
	Blocked: "#ff6d7f", Link: "#c9a7ff", Border: "#5e4a75",
	SearchFg: "#1a1026", SearchBg: "#ffb454",
	DangerFg: "#fff5fa", DangerBg: "#c81e5b",
	P0: "#ff6d7f", P1: "#ffb454", P2: "#c792ea",
}

var builtinThemes = map[string]Theme{
	"default": defaultTheme,
	"omarchy": omarchyTheme,
}

var (
	customThemes = map[string]Theme{}
	currentTheme = "default"
)

func init() { applyTheme(defaultTheme) }

// themeFile is the theming slice of cli.yaml.
type themeFile struct {
	Theme  string           `yaml:"theme"`
	Themes map[string]Theme `yaml:"themes"`
}

// LoadThemes reads theme:/themes: from cli.yaml, registers the custom
// palettes, and applies the selected theme (AMY_THEME overrides the yaml
// pick). Unknown names fall back to default with an error the caller can
// surface as a warning.
func LoadThemes(path string) error {
	var tf themeFile
	if path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			if err := yaml.Unmarshal(raw, &tf); err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
		}
	}
	for name, t := range tf.Themes {
		customThemes[name] = t
	}
	if env := os.Getenv("AMY_THEME"); env != "" {
		tf.Theme = env
	}
	if tf.Theme == "" {
		return nil
	}
	if !SetTheme(tf.Theme) {
		return fmt.Errorf("unknown theme %q (have: %v)", tf.Theme, ThemeNames())
	}
	return nil
}

// ThemeNames lists selectable themes: built-ins first, customs after,
// each group sorted.
func ThemeNames() []string {
	names := []string{"default", "omarchy"}
	var custom []string
	for name := range customThemes {
		if _, isBuiltin := builtinThemes[name]; !isBuiltin {
			custom = append(custom, name)
		}
	}
	sort.Strings(custom)
	return append(names, custom...)
}

// CurrentTheme is the active theme's name.
func CurrentTheme() string { return currentTheme }

// SetTheme looks up a theme by name and applies it; false when unknown.
// Customs may shadow a built-in name to restyle it.
func SetTheme(name string) bool {
	t, ok := customThemes[name]
	if !ok {
		if t, ok = builtinThemes[name]; !ok {
			return false
		}
	}
	currentTheme = name
	applyTheme(t)
	return true
}

// resolve fills a theme's empty fields from the default palette.
func resolve(t Theme) Theme {
	fill := func(v *string, d string) {
		if *v == "" {
			*v = d
		}
	}
	d := defaultTheme
	fill(&t.HeaderFg, d.HeaderFg)
	fill(&t.HeaderBg, d.HeaderBg)
	fill(&t.StatusFg, d.StatusFg)
	fill(&t.StatusBg, d.StatusBg)
	fill(&t.Accent, d.Accent)
	fill(&t.Title, d.Title)
	fill(&t.Dim, d.Dim)
	fill(&t.Due, d.Due)
	fill(&t.Blocked, d.Blocked)
	fill(&t.Link, d.Link)
	fill(&t.Border, d.Border)
	fill(&t.SearchFg, d.SearchFg)
	fill(&t.SearchBg, d.SearchBg)
	fill(&t.DangerFg, d.DangerFg)
	fill(&t.DangerBg, d.DangerBg)
	fill(&t.P0, d.P0)
	fill(&t.P1, d.P1)
	fill(&t.P2, d.P2)
	return t
}

// applyTheme rebuilds every package style from the palette. Views read the
// style vars at render time, so the whole UI recolors on the next frame.
func applyTheme(t Theme) {
	t = resolve(t)
	c := func(v string) lipgloss.Color { return lipgloss.Color(v) }

	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(c(t.HeaderFg)).Background(c(t.HeaderBg))
	statusStyle = lipgloss.NewStyle().Foreground(c(t.StatusFg)).Background(c(t.StatusBg))

	groupHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(c(t.Title))
	cursorStyle = lipgloss.NewStyle().Bold(true).Foreground(c(t.Accent))
	dimStyle = lipgloss.NewStyle().Foreground(c(t.Dim))
	doneStyle = lipgloss.NewStyle().Strikethrough(true).Foreground(c(t.Dim))
	dueStyle = lipgloss.NewStyle().Foreground(c(t.Due))
	blockedStyle = lipgloss.NewStyle().Foreground(c(t.Blocked))

	searchHitStyle = lipgloss.NewStyle().Bold(true).Foreground(c(t.SearchFg)).Background(c(t.SearchBg))
	linkStyle = lipgloss.NewStyle().Underline(true).Foreground(c(t.Link))
	dangerStyle = lipgloss.NewStyle().Bold(true).Foreground(c(t.DangerFg)).Background(c(t.DangerBg))

	columnStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(c(t.Border)).
		Padding(0, 1)
	columnFocusStyle = columnStyle.BorderForeground(c(t.Accent))
	columnTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(c(t.Title))

	priorityStyles = map[string]lipgloss.Style{
		"p0": lipgloss.NewStyle().Bold(true).Foreground(c(t.P0)),
		"p1": lipgloss.NewStyle().Foreground(c(t.P1)),
		"p2": lipgloss.NewStyle().Foreground(c(t.P2)),
		"p3": dimStyle,
	}
}
