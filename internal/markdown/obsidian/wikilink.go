// Package obsidian implements goldmark extensions for Obsidian-flavored
// markdown: wikilinks and embeds, callouts, #tags, ==highlights==, and
// inline [Key:: value] fields.
package obsidian

import (
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// Wikilink is an AST node for [[target#fragment|alias]] and the embed form
// ![[target]]. Resolution fields (Dest, Broken, AssetRel, EmbedHTML) are
// filled by a pre-render pass in the markdown package, not by the parser.
type Wikilink struct {
	ast.BaseInline
	Target   string
	Fragment string
	Alias    string
	Embed    bool

	Dest      string // resolved URL (href) for links
	Broken    bool
	AssetRel  string // resolved vault-relative path for asset embeds
	EmbedHTML []byte // pre-rendered HTML for note transclusions
}

var KindWikilink = ast.NewNodeKind("Wikilink")

func (n *Wikilink) Kind() ast.NodeKind { return KindWikilink }
func (n *Wikilink) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{
		"Target": n.Target, "Fragment": n.Fragment, "Alias": n.Alias,
	}, nil)
}

// Label returns the text to display for a (non-embed) wikilink.
func (n *Wikilink) Label() string {
	if n.Alias != "" {
		return n.Alias
	}
	l := n.Target
	if n.Fragment != "" {
		if l == "" {
			return n.Fragment
		}
		l += " > " + n.Fragment
	}
	// Display the basename for path-form links, like Obsidian.
	if i := strings.LastIndex(l, "/"); i >= 0 && n.Target != "" {
		l = l[i+1:]
	}
	return l
}

type wikilinkParser struct{}

// NewWikilinkParser parses [[...]] and ![[...]]. It must be registered with
// a priority above goldmark's standard link parser.
func NewWikilinkParser() parser.InlineParser { return &wikilinkParser{} }

func (p *wikilinkParser) Trigger() []byte { return []byte{'!', '['} }

func (p *wikilinkParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, seg := block.PeekLine()
	embed := false
	offset := 0
	if line[0] == '!' {
		if len(line) < 5 || line[1] != '[' || line[2] != '[' {
			return nil
		}
		embed = true
		offset = 3
	} else {
		if len(line) < 4 || line[1] != '[' {
			return nil
		}
		offset = 2
	}
	end := strings.Index(string(line[offset:]), "]]")
	if end < 0 {
		return nil
	}
	inner := string(line[offset : offset+end])
	if strings.ContainsAny(inner, "[]") {
		return nil
	}

	target := inner
	alias := ""
	if i := strings.Index(inner, "|"); i >= 0 {
		target, alias = inner[:i], inner[i+1:]
	}
	fragment := ""
	if i := strings.Index(target, "#"); i >= 0 {
		target, fragment = target[:i], target[i:][1:]
	}
	target = strings.TrimSpace(target)
	if target == "" && fragment == "" && alias == "" {
		return nil
	}

	block.Advance(offset + end + 2)
	_ = seg
	return &Wikilink{
		Target:   target,
		Fragment: strings.TrimSpace(fragment),
		Alias:    strings.TrimSpace(alias),
		Embed:    embed,
	}
}
