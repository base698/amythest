package expr

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Env supplies per-note context during evaluation.
type Env interface {
	// Prop returns a frontmatter property (note.<name> or bare identifier).
	Prop(name string) Value
	// File returns file metadata: name, path, folder, ext, tags, links,
	// ctime, mtime.
	File(name string) Value
	// FileCheck implements file.hasTag / hasLink / inFolder / hasProperty.
	FileCheck(method string, args []Value) (Value, error)
	// Formula evaluates a named formula (cycle-guarded by the caller).
	Formula(name string) (Value, error)
}

// Eval evaluates a parsed expression against an environment.
func Eval(n Node, env Env) (Value, error) {
	switch t := n.(type) {
	case *Lit:
		return t.V, nil
	case *Ident:
		if t.Name == "file" || t.Name == "note" || t.Name == "formula" {
			return NullV(), fmt.Errorf("%s needs a property (e.g. %s.name)", t.Name, t.Name)
		}
		return env.Prop(t.Name), nil
	case *Member:
		if id, ok := t.Recv.(*Ident); ok {
			switch id.Name {
			case "note":
				return env.Prop(t.Name), nil
			case "file":
				return env.File(t.Name), nil
			case "formula":
				return env.Formula(t.Name)
			}
		}
		// property access on a computed value: only .length on lists/strings
		v, err := Eval(t.Recv, env)
		if err != nil {
			return NullV(), err
		}
		if t.Name == "length" {
			switch v.Kind {
			case List:
				return NumV(float64(len(v.L))), nil
			case Str:
				return NumV(float64(len(v.S))), nil
			}
		}
		return NullV(), fmt.Errorf("unknown property .%s", t.Name)
	case *Call:
		return evalCall(t, env)
	case *MethodCall:
		return evalMethod(t, env)
	case *Unary:
		v, err := Eval(t.X, env)
		if err != nil {
			return NullV(), err
		}
		if t.Op == "!" {
			return BoolV(!v.Truthy()), nil
		}
		n, err := v.AsNumber()
		if err != nil {
			return NullV(), err
		}
		return NumV(-n), nil
	case *Binary:
		return evalBinary(t, env)
	}
	return NullV(), fmt.Errorf("unhandled node %T", n)
}

func evalBinary(b *Binary, env Env) (Value, error) {
	// short-circuit logic ops
	if b.Op == "&&" || b.Op == "||" {
		l, err := Eval(b.L, env)
		if err != nil {
			return NullV(), err
		}
		if b.Op == "&&" && !l.Truthy() {
			return BoolV(false), nil
		}
		if b.Op == "||" && l.Truthy() {
			return BoolV(true), nil
		}
		r, err := Eval(b.R, env)
		if err != nil {
			return NullV(), err
		}
		return BoolV(r.Truthy()), nil
	}

	l, err := Eval(b.L, env)
	if err != nil {
		return NullV(), err
	}
	r, err := Eval(b.R, env)
	if err != nil {
		return NullV(), err
	}
	switch b.Op {
	case "==":
		return BoolV(Equal(l, r)), nil
	case "!=":
		return BoolV(!Equal(l, r)), nil
	case "<":
		return BoolV(Compare(l, r) < 0), nil
	case "<=":
		return BoolV(Compare(l, r) <= 0), nil
	case ">":
		return BoolV(Compare(l, r) > 0), nil
	case ">=":
		return BoolV(Compare(l, r) >= 0), nil
	case "+":
		if l.Kind == Str || r.Kind == Str {
			return StrV(l.Text() + r.Text()), nil
		}
		return numOp(l, r, func(a, b float64) float64 { return a + b })
	case "-":
		return numOp(l, r, func(a, b float64) float64 { return a - b })
	case "*":
		return numOp(l, r, func(a, b float64) float64 { return a * b })
	case "/":
		return numOp(l, r, func(a, b float64) float64 { return a / b })
	case "%":
		return numOp(l, r, math.Mod)
	}
	return NullV(), fmt.Errorf("unknown operator %s", b.Op)
}

func numOp(l, r Value, f func(a, b float64) float64) (Value, error) {
	a, err := l.AsNumber()
	if err != nil {
		return NullV(), err
	}
	b, err := r.AsNumber()
	if err != nil {
		return NullV(), err
	}
	return NumV(f(a, b)), nil
}

func evalCall(c *Call, env Env) (Value, error) {
	// if() lazily evaluates branches
	if c.Fn == "if" {
		if len(c.Args) < 2 || len(c.Args) > 3 {
			return NullV(), fmt.Errorf("if() takes 2 or 3 arguments")
		}
		cond, err := Eval(c.Args[0], env)
		if err != nil {
			return NullV(), err
		}
		if cond.Truthy() {
			return Eval(c.Args[1], env)
		}
		if len(c.Args) == 3 {
			return Eval(c.Args[2], env)
		}
		return NullV(), nil
	}

	args := make([]Value, len(c.Args))
	for i, a := range c.Args {
		v, err := Eval(a, env)
		if err != nil {
			return NullV(), err
		}
		args[i] = v
	}

	switch c.Fn {
	case "date":
		if len(args) != 1 {
			return NullV(), fmt.Errorf("date() takes 1 argument")
		}
		t, err := args[0].AsDate()
		if err != nil {
			return NullV(), err
		}
		return DateV(t), nil
	case "now":
		return DateV(time.Now()), nil
	case "today":
		y, m, d := time.Now().Date()
		return DateV(time.Date(y, m, d, 0, 0, 0, 0, time.Local)), nil
	case "number":
		n, err := args[0].AsNumber()
		if err != nil {
			return NullV(), err
		}
		return NumV(n), nil
	case "min", "max":
		if len(args) == 0 {
			return NullV(), nil
		}
		best := args[0]
		for _, a := range args[1:] {
			cmp := Compare(a, best)
			if (cmp < 0 && c.Fn == "min") || (cmp > 0 && c.Fn == "max") {
				best = a
			}
		}
		return best, nil
	case "list":
		return ListV(args), nil
	case "contains":
		if len(args) != 2 {
			return NullV(), fmt.Errorf("contains() takes 2 arguments")
		}
		return containsValue(args[0], args[1]), nil
	case "link", "image", "icon":
		if len(args) == 0 {
			return NullV(), nil
		}
		return args[0], nil
	case "escapeHTML", "html":
		if len(args) == 0 {
			return NullV(), nil
		}
		return StrV(args[0].Text()), nil
	case "duration":
		if len(args) == 0 {
			return NullV(), nil
		}
		return args[0], nil
	}
	return NullV(), fmt.Errorf("unsupported function %s()", c.Fn)
}

func containsValue(hay, needle Value) Value {
	switch hay.Kind {
	case List:
		for _, e := range hay.L {
			if Equal(e, needle) {
				return BoolV(true)
			}
		}
		return BoolV(false)
	default:
		return BoolV(strings.Contains(strings.ToLower(hay.Text()), strings.ToLower(needle.Text())))
	}
}

func evalMethod(m *MethodCall, env Env) (Value, error) {
	// file.hasTag(...) style checks route to the environment.
	if id, ok := m.Recv.(*Ident); ok && id.Name == "file" {
		args := make([]Value, len(m.Args))
		for i, a := range m.Args {
			v, err := Eval(a, env)
			if err != nil {
				return NullV(), err
			}
			args[i] = v
		}
		return env.FileCheck(m.Name, args)
	}

	recv, err := Eval(m.Recv, env)
	if err != nil {
		return NullV(), err
	}
	args := make([]Value, len(m.Args))
	for i, a := range m.Args {
		v, err := Eval(a, env)
		if err != nil {
			return NullV(), err
		}
		args[i] = v
	}
	return callMethod(recv, m.Name, args)
}

func callMethod(recv Value, name string, args []Value) (Value, error) {
	arg := func(i int) Value {
		if i < len(args) {
			return args[i]
		}
		return NullV()
	}

	switch name {
	// generic
	case "isEmpty":
		return BoolV(!recv.Truthy()), nil
	case "toString":
		return StrV(recv.Text()), nil
	case "contains", "includes":
		return containsValue(recv, arg(0)), nil
	}

	switch recv.Kind {
	case Str:
		s := recv.S
		switch name {
		case "lower":
			return StrV(strings.ToLower(s)), nil
		case "upper":
			return StrV(strings.ToUpper(s)), nil
		case "trim":
			return StrV(strings.TrimSpace(s)), nil
		case "startsWith":
			return BoolV(strings.HasPrefix(s, arg(0).Text())), nil
		case "endsWith":
			return BoolV(strings.HasSuffix(s, arg(0).Text())), nil
		case "replace":
			return StrV(strings.ReplaceAll(s, arg(0).Text(), arg(1).Text())), nil
		case "slice":
			start, _ := arg(0).AsNumber()
			end := float64(len(s))
			if len(args) > 1 {
				end, _ = arg(1).AsNumber()
			}
			si, ei := clampIndex(int(start), len(s)), clampIndex(int(end), len(s))
			if si > ei {
				si = ei
			}
			return StrV(s[si:ei]), nil
		case "split":
			parts := strings.Split(s, arg(0).Text())
			l := make([]Value, len(parts))
			for i, p := range parts {
				l[i] = StrV(p)
			}
			return ListV(l), nil
		case "repeat":
			n, _ := arg(0).AsNumber()
			if n < 0 || n > 1000 {
				n = 0
			}
			return StrV(strings.Repeat(s, int(n))), nil
		case "reverse":
			r := []rune(s)
			for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
				r[i], r[j] = r[j], r[i]
			}
			return StrV(string(r)), nil
		}
	case Num:
		n := recv.N
		switch name {
		case "toFixed":
			d, _ := arg(0).AsNumber()
			return StrV(fmt.Sprintf("%.*f", int(d), n)), nil
		case "round":
			return NumV(math.Round(n)), nil
		case "floor":
			return NumV(math.Floor(n)), nil
		case "ceil":
			return NumV(math.Ceil(n)), nil
		case "abs":
			return NumV(math.Abs(n)), nil
		}
	case Date:
		t := recv.T
		switch name {
		case "format":
			return StrV(t.Format(goDateLayout(arg(0).Text()))), nil
		case "year":
			return NumV(float64(t.Year())), nil
		case "month":
			return NumV(float64(t.Month())), nil
		case "day":
			return NumV(float64(t.Day())), nil
		case "time":
			return StrV(t.Format("15:04")), nil
		case "date":
			return StrV(t.Format("2006-01-02")), nil
		case "relative":
			return StrV(relativeTime(t)), nil
		}
	case List:
		l := recv.L
		switch name {
		case "count", "length":
			return NumV(float64(len(l))), nil
		case "join":
			sep := ", "
			if len(args) > 0 {
				sep = arg(0).Text()
			}
			parts := make([]string, len(l))
			for i, e := range l {
				parts[i] = e.Text()
			}
			return StrV(strings.Join(parts, sep)), nil
		case "sum", "mean", "min", "max":
			var nums []float64
			for _, e := range l {
				if n, err := e.AsNumber(); err == nil {
					nums = append(nums, n)
				}
			}
			if len(nums) == 0 {
				return NullV(), nil
			}
			agg := nums[0]
			sum := 0.0
			for _, n := range nums {
				sum += n
				if (name == "min" && n < agg) || (name == "max" && n > agg) {
					agg = n
				}
			}
			switch name {
			case "sum":
				return NumV(sum), nil
			case "mean":
				return NumV(sum / float64(len(nums))), nil
			}
			return NumV(agg), nil
		case "unique":
			var out []Value
			for _, e := range l {
				dup := false
				for _, o := range out {
					if Equal(e, o) {
						dup = true
						break
					}
				}
				if !dup {
					out = append(out, e)
				}
			}
			return ListV(out), nil
		case "first":
			if len(l) == 0 {
				return NullV(), nil
			}
			return l[0], nil
		case "last":
			if len(l) == 0 {
				return NullV(), nil
			}
			return l[len(l)-1], nil
		}
	}
	return NullV(), fmt.Errorf("unsupported method .%s() on %s", name, kindName(recv.Kind))
}

func clampIndex(i, n int) int {
	if i < 0 {
		i += n
	}
	if i < 0 {
		return 0
	}
	if i > n {
		return n
	}
	return i
}

func kindName(k Kind) string {
	return [...]string{"null", "string", "number", "boolean", "list", "date"}[k]
}

// goDateLayout converts moment-style tokens (YYYY-MM-DD) to Go layouts.
func goDateLayout(f string) string {
	r := strings.NewReplacer(
		"YYYY", "2006", "YY", "06",
		"MMMM", "January", "MMM", "Jan", "MM", "01",
		"DD", "02", "D", "2",
		"HH", "15", "mm", "04", "ss", "05",
	)
	return r.Replace(f)
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	past := d >= 0
	if !past {
		d = -d
	}
	var s string
	switch {
	case d < time.Minute:
		s = "moments"
	case d < time.Hour:
		s = fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 24*time.Hour:
		s = fmt.Sprintf("%d hours", int(d.Hours()))
	case d < 30*24*time.Hour:
		s = fmt.Sprintf("%d days", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		s = fmt.Sprintf("%d months", int(d.Hours()/(24*30)))
	default:
		s = fmt.Sprintf("%d years", int(d.Hours()/(24*365)))
	}
	if past {
		return s + " ago"
	}
	return "in " + s
}
