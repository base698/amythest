package expr

import (
	"testing"
	"time"
)

type testEnv struct {
	props map[string]any
	tags  []string
}

func (e *testEnv) Prop(name string) Value {
	if v, ok := e.props[name]; ok {
		return FromAny(v)
	}
	return NullV()
}
func (e *testEnv) File(name string) Value {
	switch name {
	case "name":
		return StrV("My Note")
	case "folder":
		return StrV("Food/Recipes")
	case "mtime":
		return DateV(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	}
	return NullV()
}
func (e *testEnv) FileCheck(method string, args []Value) (Value, error) {
	if method == "hasTag" {
		for _, t := range e.tags {
			if t == args[0].Text() {
				return BoolV(true), nil
			}
		}
		return BoolV(false), nil
	}
	return NullV(), nil
}
func (e *testEnv) Formula(name string) (Value, error) {
	return StrV("formula:" + name), nil
}

func eval(t *testing.T, src string) Value {
	t.Helper()
	n, err := Parse(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	env := &testEnv{
		props: map[string]any{"Weight": 185, "type": "recipe", "rating": 4.5,
			"tags": []any{"a", "b"}, "done": true},
		tags: []string{"recipe"},
	}
	v, err := Eval(n, env)
	if err != nil {
		t.Fatalf("eval %q: %v", src, err)
	}
	return v
}

func TestExpressions(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`1 + 2 * 3`, "7"},
		{`(1 + 2) * 3`, "9"},
		{`"a" + "b"`, "ab"},
		{`Weight > 100`, "true"},
		{`Weight > 100 && type == "recipe"`, "true"},
		{`type == "recipe" or type == "guide"`, "true"},
		{`!done`, "false"},
		{`if(Weight > 100, "heavy", "light")`, "heavy"},
		{`if(false, "a")`, ""},
		{`note.type.upper()`, "RECIPE"},
		{`file.name`, "My Note"},
		{`file.folder.startsWith("Food")`, "true"},
		{`file.hasTag("recipe")`, "true"},
		{`file.hasTag("nope")`, "false"},
		{`rating.toFixed(1)`, "4.5"},
		{`tags.count()`, "2"},
		{`tags.join("|")`, "a|b"},
		{`list(1, 2, 3).sum()`, "6"},
		{`list(1, 2, 3).mean()`, "2"},
		{`contains("Hello World", "world")`, "true"},
		{`"  x  ".trim()`, "x"},
		{`min(3, 1, 2)`, "1"},
		{`max(3, 1, 2)`, "3"},
		{`formula.doc_type`, "formula:doc_type"},
		{`missing_prop`, ""},
		{`missing_prop.isEmpty()`, "true"},
		{`file.mtime.format("YYYY-MM-DD")`, "2026-07-01"},
		{`file.mtime.year()`, "2026"},
		{`"abcdef".slice(1, 3)`, "bc"},
		{`10 % 3`, "1"},
		{`-Weight + 200`, "15"},
	}
	for _, c := range cases {
		if got := eval(t, c.src).Text(); got != c.want {
			t.Errorf("%s = %q, want %q", c.src, got, c.want)
		}
	}
}

func TestNumberRequiresOneArgument(t *testing.T) {
	n, err := Parse(`number()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Eval(n, &testEnv{}); err == nil {
		t.Fatal("number() with no arguments should error, not panic")
	}
	if got := eval(t, `number("42") + 1`).Text(); got != "43" {
		t.Errorf(`number("42") + 1 = %q, want "43"`, got)
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{`1 +`, `"unclosed`, `foo(`, `a = b`, `..`} {
		if _, err := Parse(src); err == nil {
			t.Errorf("expected parse error for %q", src)
		}
	}
	if _, err := Parse(`frobnicate(1)`); err != nil {
		t.Errorf("unknown function should parse (fails at eval): %v", err)
	}
}
