package obsidian

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Callout is a blockquote of the form `> [!type]± Title`. Unknown types
// render with a generic style rather than failing — the vault uses
// non-standard types like [!decision].
type Callout struct {
	ast.BaseBlock
	CalloutType string
	Title    string
	Foldable bool
	Folded   bool
}

var KindCallout = ast.NewNodeKind("Callout")

func (n *Callout) Kind() ast.NodeKind { return KindCallout }
func (n *Callout) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Type": n.CalloutType, "Title": n.Title}, nil)
}

var calloutRe = regexp.MustCompile(`^\[!([\w-]+)\]([+-]?)\s*(.*)$`)

// calloutTransformer rewrites matching Blockquote nodes into Callout nodes.
type calloutTransformer struct{}

func NewCalloutTransformer() parser.ASTTransformer { return &calloutTransformer{} }

func (t *calloutTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	source := reader.Source()
	var quotes []*ast.Blockquote
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if bq, ok := n.(*ast.Blockquote); ok && entering {
			quotes = append(quotes, bq)
		}
		return ast.WalkContinue, nil
	})
	// Innermost first so nested callouts transform correctly.
	for i := len(quotes) - 1; i >= 0; i-- {
		transformQuote(quotes[i], source)
	}
}

func transformQuote(bq *ast.Blockquote, source []byte) {
	para, ok := bq.FirstChild().(*ast.Paragraph)
	if !ok || para.Lines().Len() == 0 {
		return
	}
	firstLine := para.Lines().At(0)
	m := calloutRe.FindSubmatch(firstLine.Value(source))
	if m == nil {
		return
	}

	c := &Callout{
		CalloutType: strings.ToLower(string(m[1])),
		Title:    strings.TrimSpace(string(m[3])),
		Foldable: len(m[2]) > 0,
		Folded:   string(m[2]) == "-",
	}

	// Drop inline nodes that lie entirely within the marker line.
	lineEnd := firstLine.Stop
	for child := para.FirstChild(); child != nil; {
		next := child.NextSibling()
		if inlineWithin(child, lineEnd) {
			para.RemoveChild(para, child)
		}
		child = next
	}
	trimLeadingBreak(para, source)

	// Move all blockquote children into the callout.
	for child := bq.FirstChild(); child != nil; {
		next := child.NextSibling()
		bq.RemoveChild(bq, child)
		if child == ast.Node(para) && para.ChildCount() == 0 {
			child = next
			continue
		}
		c.AppendChild(c, child)
		child = next
	}
	parent := bq.Parent()
	parent.ReplaceChild(parent, bq, c)
}

// inlineWithin reports whether every text segment of n ends at or before stop.
func inlineWithin(n ast.Node, stop int) bool {
	switch t := n.(type) {
	case *ast.Text:
		return t.Segment.Stop <= stop
	default:
		if n.ChildCount() == 0 {
			// Non-text leaf (e.g. an auto-link) — keep it unless we can
			// prove it's on the marker line; be conservative.
			return false
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if !inlineWithin(c, stop) {
				return false
			}
		}
		return true
	}
}

// trimLeadingBreak removes a leading empty text/linebreak left after
// stripping the marker line.
func trimLeadingBreak(para *ast.Paragraph, source []byte) {
	for {
		first, ok := para.FirstChild().(*ast.Text)
		if !ok {
			return
		}
		if len(strings.TrimSpace(string(first.Segment.Value(source)))) == 0 {
			para.RemoveChild(para, first)
			continue
		}
		return
	}
}

// calloutIcons maps callout types to their icon class; unknown types fall
// back to "note" styling via CSS.
var CalloutAliases = map[string]string{
	"abstract": "abstract", "summary": "abstract", "tldr": "abstract",
	"info": "info", "todo": "todo",
	"tip": "tip", "hint": "tip", "important": "tip",
	"success": "success", "check": "success", "done": "success",
	"question": "question", "help": "question", "faq": "question",
	"warning": "warning", "caution": "warning", "attention": "warning",
	"failure": "failure", "fail": "failure", "missing": "failure",
	"danger": "danger", "error": "danger",
	"bug": "bug", "example": "example", "quote": "quote", "cite": "quote",
	"note": "note",
}

// Ensure this file's imports stay used if the alias table shrinks.
var _ = util.Prioritized
