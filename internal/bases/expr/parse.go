package expr

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ---- lexer ----

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tNum
	tStr
	tOp    // + - * / % == != < <= > >= ! && ||
	tPunct // ( ) , .
)

type token struct {
	kind tokKind
	text string
	pos  int
}

func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '"' || c == '\'':
			quote := c
			j := i + 1
			var sb strings.Builder
			for j < len(src) && src[j] != quote {
				if src[j] == '\\' && j+1 < len(src) {
					j++
				}
				sb.WriteByte(src[j])
				j++
			}
			if j >= len(src) {
				return nil, fmt.Errorf("unterminated string at %d", i)
			}
			toks = append(toks, token{tStr, sb.String(), i})
			i = j + 1
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && (src[j] >= '0' && src[j] <= '9' || src[j] == '.') {
				j++
			}
			toks = append(toks, token{tNum, src[i:j], i})
			i = j
		case isIdentStart(rune(c)):
			j := i
			for j < len(src) && isIdentPart(rune(src[j])) {
				j++
			}
			toks = append(toks, token{tIdent, src[i:j], i})
			i = j
		case strings.ContainsRune("()+-*/%,.", rune(c)):
			toks = append(toks, token{map[bool]tokKind{true: tPunct, false: tOp}[strings.ContainsRune("(),.", rune(c))], string(c), i})
			i++
		case c == '=' || c == '!' || c == '<' || c == '>':
			if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, token{tOp, src[i : i+2], i})
				i += 2
			} else if c == '=' {
				return nil, fmt.Errorf("single '=' at %d (use ==)", i)
			} else {
				toks = append(toks, token{tOp, string(c), i})
				i++
			}
		case c == '&' && i+1 < len(src) && src[i+1] == '&':
			toks = append(toks, token{tOp, "&&", i})
			i += 2
		case c == '|' && i+1 < len(src) && src[i+1] == '|':
			toks = append(toks, token{tOp, "||", i})
			i += 2
		default:
			return nil, fmt.Errorf("unexpected character %q at %d", c, i)
		}
	}
	toks = append(toks, token{tEOF, "", len(src)})
	return toks, nil
}

func isIdentStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_'
}
func isIdentPart(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

// ---- AST ----

type Node interface{}

type Lit struct{ V Value }
type Ident struct{ Name string }
type Member struct {
	Recv Node
	Name string
}
type Call struct {
	Fn   string
	Args []Node
}
type MethodCall struct {
	Recv Node
	Name string
	Args []Node
}
type Unary struct {
	Op string
	X  Node
}
type Binary struct {
	Op   string
	L, R Node
}

// ---- Pratt parser ----

type parser struct {
	toks []token
	pos  int
}

// Parse compiles an expression to an AST.
func Parse(src string) (Node, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("unexpected %q", p.peek().text)
	}
	return n, nil
}

func (p *parser) peek() token  { return p.toks[p.pos] }
func (p *parser) next() token  { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) accept(text string) bool {
	if p.peek().text == text && p.peek().kind != tStr {
		p.pos++
		return true
	}
	return false
}

var binaryPrec = map[string]int{
	"||": 1, "or": 1,
	"&&": 2, "and": 2,
	"==": 3, "!=": 3, "<": 3, "<=": 3, ">": 3, ">=": 3,
	"+": 4, "-": 4,
	"*": 5, "/": 5, "%": 5,
}

func (p *parser) parseExpr(minPrec int) (Node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		op := t.text
		if t.kind == tIdent && (op == "and" || op == "or") {
			// keyword operators
		} else if t.kind != tOp {
			break
		}
		prec, ok := binaryPrec[op]
		if !ok || prec < minPrec {
			break
		}
		p.next()
		right, err := p.parseExpr(prec + 1)
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: normalizeOp(op), L: left, R: right}
	}
	return left, nil
}

func normalizeOp(op string) string {
	switch op {
	case "and":
		return "&&"
	case "or":
		return "||"
	}
	return op
}

func (p *parser) parseUnary() (Node, error) {
	t := p.peek()
	if t.text == "!" || (t.kind == tIdent && t.text == "not") {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Unary{Op: "!", X: x}, nil
	}
	if t.text == "-" && t.kind == tOp {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Unary{Op: "-", X: x}, nil
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() (Node, error) {
	n, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		if p.accept(".") {
			name := p.next()
			if name.kind != tIdent {
				return nil, fmt.Errorf("expected name after '.', got %q", name.text)
			}
			if p.accept("(") {
				args, err := p.parseArgs()
				if err != nil {
					return nil, err
				}
				n = &MethodCall{Recv: n, Name: name.text, Args: args}
			} else {
				n = &Member{Recv: n, Name: name.text}
			}
			continue
		}
		break
	}
	return n, nil
}

func (p *parser) parsePrimary() (Node, error) {
	t := p.next()
	switch t.kind {
	case tNum:
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return nil, err
		}
		return &Lit{V: NumV(f)}, nil
	case tStr:
		return &Lit{V: StrV(t.text)}, nil
	case tIdent:
		switch t.text {
		case "true":
			return &Lit{V: BoolV(true)}, nil
		case "false":
			return &Lit{V: BoolV(false)}, nil
		case "null":
			return &Lit{V: NullV()}, nil
		}
		if p.accept("(") {
			args, err := p.parseArgs()
			if err != nil {
				return nil, err
			}
			return &Call{Fn: t.text, Args: args}, nil
		}
		return &Ident{Name: t.text}, nil
	case tPunct:
		if t.text == "(" {
			n, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			if !p.accept(")") {
				return nil, fmt.Errorf("missing ')'")
			}
			return n, nil
		}
	}
	return nil, fmt.Errorf("unexpected %q", t.text)
}

func (p *parser) parseArgs() ([]Node, error) {
	var args []Node
	if p.accept(")") {
		return args, nil
	}
	for {
		a, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		if p.accept(",") {
			continue
		}
		if p.accept(")") {
			return args, nil
		}
		return nil, fmt.Errorf("expected ',' or ')', got %q", p.peek().text)
	}
}
