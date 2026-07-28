// Package expr implements the Obsidian Bases expression language: a small
// dynamically-typed language over note metadata (note.*, file.*, formula.*)
// with functions and methods, evaluated by tree-walking. Errors are values
// surfaced per-cell in rendered views, never render failures.
package expr

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type Kind int

const (
	Null Kind = iota
	Str
	Num
	Bool
	List
	Date
)

type Value struct {
	Kind Kind
	S    string
	N    float64
	B    bool
	L    []Value
	T    time.Time
}

func NullV() Value            { return Value{Kind: Null} }
func StrV(s string) Value     { return Value{Kind: Str, S: s} }
func NumV(n float64) Value    { return Value{Kind: Num, N: n} }
func BoolV(b bool) Value      { return Value{Kind: Bool, B: b} }
func ListV(l []Value) Value   { return Value{Kind: List, L: l} }
func DateV(t time.Time) Value { return Value{Kind: Date, T: t} }

// FromAny converts decoded YAML/JSON frontmatter values.
func FromAny(v any) Value {
	switch t := v.(type) {
	case nil:
		return NullV()
	case string:
		return StrV(t)
	case bool:
		return BoolV(t)
	case int:
		return NumV(float64(t))
	case int64:
		return NumV(float64(t))
	case float64:
		return NumV(t)
	case []any:
		l := make([]Value, 0, len(t))
		for _, e := range t {
			l = append(l, FromAny(e))
		}
		return ListV(l)
	case map[string]any:
		// objects flatten to their string form; rare in frontmatter
		return StrV(fmt.Sprint(t))
	case time.Time:
		return DateV(t)
	default:
		return StrV(fmt.Sprint(t))
	}
}

func (v Value) Truthy() bool {
	switch v.Kind {
	case Null:
		return false
	case Bool:
		return v.B
	case Num:
		return v.N != 0
	case Str:
		return v.S != ""
	case List:
		return len(v.L) > 0
	case Date:
		return !v.T.IsZero()
	}
	return false
}

// Text renders a value for display.
func (v Value) Text() string {
	switch v.Kind {
	case Null:
		return ""
	case Str:
		return v.S
	case Num:
		if v.N == math.Trunc(v.N) && math.Abs(v.N) < 1e15 {
			return strconv.FormatInt(int64(v.N), 10)
		}
		return strconv.FormatFloat(v.N, 'f', -1, 64)
	case Bool:
		if v.B {
			return "true"
		}
		return "false"
	case List:
		parts := make([]string, len(v.L))
		for i, e := range v.L {
			parts[i] = e.Text()
		}
		return strings.Join(parts, ", ")
	case Date:
		if v.T.Hour() == 0 && v.T.Minute() == 0 && v.T.Second() == 0 {
			return v.T.Format("2006-01-02")
		}
		return v.T.Format("2006-01-02 15:04")
	}
	return ""
}

// Compare orders two values for sorting; mixed kinds order by kind.
func Compare(a, b Value) int {
	if a.Kind == Null && b.Kind == Null {
		return 0
	}
	if a.Kind == Null {
		return 1 // nulls last
	}
	if b.Kind == Null {
		return -1
	}
	if a.Kind == Num && b.Kind == Num {
		switch {
		case a.N < b.N:
			return -1
		case a.N > b.N:
			return 1
		}
		return 0
	}
	if a.Kind == Date && b.Kind == Date {
		switch {
		case a.T.Before(b.T):
			return -1
		case a.T.After(b.T):
			return 1
		}
		return 0
	}
	if a.Kind == Bool && b.Kind == Bool {
		if a.B == b.B {
			return 0
		}
		if !a.B {
			return -1
		}
		return 1
	}
	return strings.Compare(a.Text(), b.Text())
}

// Equal is looser than Compare: numbers compare to numeric strings etc.
func Equal(a, b Value) bool {
	if a.Kind == b.Kind {
		return Compare(a, b) == 0
	}
	if (a.Kind == Num && b.Kind == Str) || (a.Kind == Str && b.Kind == Num) {
		an, aerr := a.AsNumber()
		bn, berr := b.AsNumber()
		return aerr == nil && berr == nil && an == bn
	}
	return a.Text() == b.Text()
}

func (v Value) AsNumber() (float64, error) {
	switch v.Kind {
	case Num:
		return v.N, nil
	case Str:
		return strconv.ParseFloat(strings.TrimSpace(v.S), 64)
	case Bool:
		if v.B {
			return 1, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("not a number: %s", v.Text())
}

var dateFormats = []string{"2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02"}

func (v Value) AsDate() (time.Time, error) {
	switch v.Kind {
	case Date:
		return v.T, nil
	case Str:
		for _, f := range dateFormats {
			if t, err := time.Parse(f, strings.TrimSpace(v.S)); err == nil {
				return t, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("not a date: %s", v.Text())
}
