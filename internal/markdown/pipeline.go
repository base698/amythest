// Package markdown turns vault notes into HTML. It assembles goldmark with
// the obsidian extensions, resolves wikilinks against the vault snapshot,
// renders transclusions with depth/cycle guards, and extracts the TOC,
// outgoing links, tags, and plain text used by the index.
package markdown

import (
	"bytes"
	"encoding/base64"
	"html/template"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/base698/amythest/internal/markdown/obsidian"
)

const maxEmbedDepth = 3

type Engine struct {
	base string // URL prefix, always with trailing slash
	md   goldmark.Markdown
}

type Heading struct {
	ID    string
	Text  string
	Level int
}

type Link struct {
	Slug string
	Kind string // "link" | "embed"
}

type Result struct {
	HTML  template.HTML
	TOC   []Heading
	Links []Link
	Tags  []string
	Plain string
}

func New(baseURL string) *Engine {
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	e := &Engine{base: baseURL}
	e.md = goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.Footnote, extension.Typographer),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(
				util.Prioritized(obsidian.NewCalloutTransformer(), 500),
			),
			parser.WithInlineParsers(
				util.Prioritized(obsidian.NewWikilinkParser(), 150),
				util.Prioritized(obsidian.NewInlineFieldParser(), 160),
				util.Prioritized(obsidian.NewHighlightParser(), 170),
				util.Prioritized(obsidian.NewTagParser(), 998),
			),
		),
		goldmark.WithRendererOptions(
			html.WithUnsafe(), // vault content is trusted
			renderer.WithNodeRenderers(
				util.Prioritized(&nodeRenderer{engine: e}, 100),
			),
		),
	)
	return e
}

// HeadingID approximates goldmark's auto heading id generation so wikilink
// #fragments land on the right anchor.
func HeadingID(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '-':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// ---- code block highlighting ----

var chromaFormatter = chromahtml.New(chromahtml.WithClasses(true))

func highlightCode(w *bytes.Buffer, lang string, code string) {
	switch lang {
	case "mermaid":
		w.WriteString(`<pre class="mermaid">`)
		template.HTMLEscape(w, []byte(code))
		w.WriteString("</pre>\n")
		return
	case "tasks":
		// Live task query: substituted with current results at serve time,
		// so cached page HTML never goes stale.
		w.WriteString(`<div class="tasks-block" data-tasks-query="`)
		w.WriteString(base64.StdEncoding.EncodeToString([]byte(code)))
		w.WriteString(`"></div>` + "\n")
		return
	case "dataview", "dataviewjs":
		// Legacy dataview blocks: preserved read-only, collapsed.
		w.WriteString(`<details class="legacy-dataview"><summary>Legacy dataview query</summary><pre><code>`)
		template.HTMLEscape(w, []byte(code))
		w.WriteString("</code></pre></details>\n")
		return
	case "base":
		// Inline Obsidian-Bases view: substituted at serve time.
		w.WriteString(`<div class="base-block" data-base-src="`)
		w.WriteString(base64.StdEncoding.EncodeToString([]byte(code)))
		w.WriteString(`"></div>` + "\n")
		return
	}
	var lexer chroma.Lexer
	if lang != "" {
		lexer = lexers.Get(lang)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, code)
	if err == nil {
		w.WriteString(`<div class="codeblock" data-lang="` + template.HTMLEscapeString(lang) + `">`)
		if err = chromaFormatter.Format(w, styles.Fallback, it); err == nil {
			w.WriteString("</div>\n")
			return
		}
	}
	w.WriteString("<pre><code>")
	template.HTMLEscape(w, []byte(code))
	w.WriteString("</code></pre>\n")
}

// ChromaCSS returns stylesheet text for the given chroma style, with every
// selector prefixed (e.g. to scope a dark style under [data-theme="dark"]).
func ChromaCSS(styleName, selectorPrefix string) string {
	style := styles.Get(styleName)
	if style == nil {
		style = styles.Fallback
	}
	var buf bytes.Buffer
	_ = chromaFormatter.WriteCSS(&buf, style)
	css := buf.String()
	if selectorPrefix == "" {
		return css
	}
	var out strings.Builder
	for line := range strings.Lines(css) {
		if strings.HasPrefix(line, ".chroma") || strings.HasPrefix(line, "/*") == false && strings.HasPrefix(line, ".") {
			out.WriteString(selectorPrefix + " " + line)
		} else {
			out.WriteString(line)
		}
	}
	return out.String()
}

// parse produces the AST for a body.
func (e *Engine) parse(body []byte) ast.Node {
	return e.md.Parser().Parse(text.NewReader(body))
}
