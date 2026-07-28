package obsidian

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// ---- ==highlight== ----

type Highlight struct {
	ast.BaseInline
	Content string
}

var KindHighlight = ast.NewNodeKind("Highlight")

func (n *Highlight) Kind() ast.NodeKind { return KindHighlight }
func (n *Highlight) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Text": n.Content}, nil)
}

type highlightParser struct{}

func NewHighlightParser() parser.InlineParser { return &highlightParser{} }

func (p *highlightParser) Trigger() []byte { return []byte{'='} }

func (p *highlightParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if len(line) < 5 || line[1] != '=' {
		return nil
	}
	end := strings.Index(string(line[2:]), "==")
	if end <= 0 {
		return nil
	}
	inner := string(line[2 : 2+end])
	block.Advance(end + 4)
	return &Highlight{Content: inner}
}

// ---- #tag ----

type Tag struct {
	ast.BaseInline
	Name string
}

var KindTag = ast.NewNodeKind("Tag")

func (n *Tag) Kind() ast.NodeKind { return KindTag }
func (n *Tag) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Name": n.Name}, nil)
}

var tagRe = regexp.MustCompile(`^#([\p{L}\p{N}_/-]+)`)

type tagParser struct{}

func NewTagParser() parser.InlineParser { return &tagParser{} }

func (p *tagParser) Trigger() []byte { return []byte{'#'} }

func (p *tagParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	// Only at start of line or after whitespace/punctuation that isn't a
	// word character — avoids matching URL anchors and headings (headings
	// never reach inline parsing with their leading #).
	if prev := block.PrecendingCharacter(); prev != ' ' && prev != '\t' && prev != '\n' &&
		prev != '(' && prev != 0 && prev > 0 {
		return nil
	}
	line, _ := block.PeekLine()
	m := tagRe.FindSubmatch(line)
	if m == nil {
		return nil
	}
	name := string(m[1])
	// Obsidian requires at least one non-numeric character.
	if strings.Trim(name, "0123456789") == "" {
		return nil
	}
	block.Advance(len(m[0]))
	return &Tag{Name: name}
}

// ---- [Key:: value] inline dataview fields ----

type InlineField struct {
	ast.BaseInline
	Key   string
	Value string
}

var KindInlineField = ast.NewNodeKind("InlineField")

func (n *InlineField) Kind() ast.NodeKind { return KindInlineField }
func (n *InlineField) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Key": n.Key, "Value": n.Value}, nil)
}

var inlineFieldRe = regexp.MustCompile(`^\[([^\[\]():]{1,64})::\s*([^\[\]]*?)\]`)

type inlineFieldParser struct{}

func NewInlineFieldParser() parser.InlineParser { return &inlineFieldParser{} }

func (p *inlineFieldParser) Trigger() []byte { return []byte{'['} }

func (p *inlineFieldParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, _ := block.PeekLine()
	m := inlineFieldRe.FindSubmatch(line)
	if m == nil {
		return nil
	}
	block.Advance(len(m[0]))
	return &InlineField{Key: strings.TrimSpace(string(m[1])), Value: strings.TrimSpace(string(m[2]))}
}
