// Package bases implements Obsidian-style .base views: YAML definitions
// with an expression language over note metadata, rendered server-side as
// table/list/cards/board views.
package bases

import (
	"fmt"
	"path"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/base698/amythest/internal/bases/expr"
)

// Base is a parsed .base file (or inline ```base block).
type Base struct {
	// Source selects what a row is: "notes" (default) — one row per note;
	// "tasks" — one row per indexed checkbox task; "items" — one row per
	// list line carrying [Key:: value] inline fields. Task and item rows
	// expose their data as note.* properties and inherit file.* from the
	// note they live in.
	Source     string               `yaml:"source"`
	Filters    *FilterNode          `yaml:"filters"`
	Formulas   map[string]string    `yaml:"formulas"`
	Properties map[string]PropMeta  `yaml:"properties"`
	Views      []View               `yaml:"views"`

	compiledFormulas map[string]expr.Node
}

type PropMeta struct {
	DisplayName string `yaml:"displayName"`
}

type View struct {
	Type       string         `yaml:"type"` // table|list|cards|gallery|board
	Name       string         `yaml:"name"`
	Filters    *FilterNode    `yaml:"filters"`
	Order      []string       `yaml:"order"`
	Sort       []SortSpec     `yaml:"sort"`
	GroupBy    *SortSpec      `yaml:"groupBy"`
	Limit      int            `yaml:"limit"`
	Sample     int            `yaml:"sample"` // pick N random rows after filtering
	ColumnSize map[string]int `yaml:"columnSize"`
	Image      string         `yaml:"image"`
	CardSize   int            `yaml:"cardSize"`
	BoardProp  string         `yaml:"boardProperty"`
}

type SortSpec struct {
	Property  string `yaml:"property"`
	Direction string `yaml:"direction"` // ASC|DESC
}

// FilterNode is a recursive and/or/not tree whose leaves are expressions.
type FilterNode struct {
	And  []*FilterNode
	Or   []*FilterNode
	Not  *FilterNode
	Expr string // leaf
}

func (f *FilterNode) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		f.Expr = node.Value
		return nil
	case yaml.MappingNode:
		var m struct {
			And []*FilterNode `yaml:"and"`
			Or  []*FilterNode `yaml:"or"`
			Not *FilterNode   `yaml:"not"`
		}
		if err := node.Decode(&m); err != nil {
			return err
		}
		f.And, f.Or, f.Not = m.And, m.Or, m.Not
		return nil
	}
	return fmt.Errorf("invalid filter node")
}

// ParseBase parses base YAML.
func ParseBase(src []byte) (*Base, error) {
	var b Base
	if err := yaml.Unmarshal(src, &b); err != nil {
		return nil, err
	}
	b.compiledFormulas = map[string]expr.Node{}
	for name, s := range b.Formulas {
		n, err := expr.Parse(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("formula %s: %w", name, err)
		}
		b.compiledFormulas[name] = n
	}
	if len(b.Views) == 0 {
		b.Views = []View{{Type: "table", Name: "All"}}
	}
	return &b, nil
}

// Row is one note's metadata as seen by the expression engine.
type Row struct {
	Slug        string
	Path        string
	Title       string
	Frontmatter map[string]any
	Tags        []string
	Links       []string
	MTime       time.Time
	CTime       time.Time
}

// rowEnv adapts a Row to expr.Env with formula support.
type rowEnv struct {
	row      *Row
	base     *Base
	inflight map[string]bool
	cache    map[string]expr.Value
}

func newRowEnv(row *Row, base *Base) *rowEnv {
	return &rowEnv{row: row, base: base, inflight: map[string]bool{}, cache: map[string]expr.Value{}}
}

func (e *rowEnv) Prop(name string) expr.Value {
	if v, ok := e.row.Frontmatter[name]; ok {
		return expr.FromAny(v)
	}
	// case-insensitive fallback: the vault mixes Type/type, Status/status
	for k, v := range e.row.Frontmatter {
		if strings.EqualFold(k, name) {
			return expr.FromAny(v)
		}
	}
	if name == "title" {
		return expr.StrV(e.row.Title)
	}
	return expr.NullV()
}

func (e *rowEnv) File(name string) expr.Value {
	switch name {
	case "name":
		base := path.Base(e.row.Path)
		return expr.StrV(strings.TrimSuffix(base, path.Ext(base)))
	case "path":
		return expr.StrV(e.row.Path)
	case "folder":
		d := path.Dir(e.row.Path)
		if d == "." {
			d = ""
		}
		return expr.StrV(d)
	case "ext":
		return expr.StrV(strings.TrimPrefix(path.Ext(e.row.Path), "."))
	case "tags":
		l := make([]expr.Value, len(e.row.Tags))
		for i, t := range e.row.Tags {
			l[i] = expr.StrV(t)
		}
		return expr.ListV(l)
	case "links":
		l := make([]expr.Value, len(e.row.Links))
		for i, t := range e.row.Links {
			l[i] = expr.StrV(t)
		}
		return expr.ListV(l)
	case "mtime", "modified":
		return expr.DateV(e.row.MTime)
	case "ctime", "created":
		return expr.DateV(e.row.CTime)
	}
	return expr.NullV()
}

func (e *rowEnv) FileCheck(method string, args []expr.Value) (expr.Value, error) {
	arg := ""
	if len(args) > 0 {
		arg = args[0].Text()
	}
	switch method {
	case "hasTag":
		for _, t := range e.row.Tags {
			for _, a := range args {
				want := strings.TrimPrefix(strings.ToLower(a.Text()), "#")
				if t == want || strings.HasPrefix(t, want+"/") {
					return expr.BoolV(true), nil
				}
			}
		}
		return expr.BoolV(false), nil
	case "hasLink":
		for _, l := range e.row.Links {
			if strings.EqualFold(l, arg) || strings.EqualFold(path.Base(l), arg) {
				return expr.BoolV(true), nil
			}
		}
		return expr.BoolV(false), nil
	case "inFolder":
		return expr.BoolV(e.row.Path == arg || strings.HasPrefix(e.row.Path, strings.TrimSuffix(arg, "/")+"/")), nil
	case "hasProperty":
		_, ok := e.row.Frontmatter[arg]
		if !ok {
			for k := range e.row.Frontmatter {
				if strings.EqualFold(k, arg) {
					ok = true
					break
				}
			}
		}
		return expr.BoolV(ok), nil
	}
	return expr.NullV(), fmt.Errorf("unsupported file.%s()", method)
}

func (e *rowEnv) Formula(name string) (expr.Value, error) {
	if v, ok := e.cache[name]; ok {
		return v, nil
	}
	node, ok := e.base.compiledFormulas[name]
	if !ok {
		return expr.NullV(), fmt.Errorf("unknown formula %q", name)
	}
	if e.inflight[name] {
		return expr.NullV(), fmt.Errorf("formula cycle at %q", name)
	}
	e.inflight[name] = true
	v, err := expr.Eval(node, e)
	delete(e.inflight, name)
	if err != nil {
		return expr.NullV(), err
	}
	e.cache[name] = v
	return v, nil
}

// matches evaluates a filter tree for a row.
func matches(f *FilterNode, env *rowEnv) (bool, error) {
	if f == nil {
		return true, nil
	}
	if f.Expr != "" {
		node, err := expr.Parse(f.Expr)
		if err != nil {
			return false, fmt.Errorf("%q: %w", f.Expr, err)
		}
		v, err := expr.Eval(node, env)
		if err != nil {
			return false, fmt.Errorf("%q: %w", f.Expr, err)
		}
		return v.Truthy(), nil
	}
	if f.Not != nil {
		ok, err := matches(f.Not, env)
		return !ok, err
	}
	if len(f.And) > 0 {
		for _, c := range f.And {
			ok, err := matches(c, env)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	}
	if len(f.Or) > 0 {
		for _, c := range f.Or {
			ok, err := matches(c, env)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	return true, nil
}
