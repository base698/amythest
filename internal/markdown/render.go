package markdown

import (
	"bytes"
	"fmt"
	"html/template"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"

	"github.com/base698/amythest/internal/markdown/obsidian"
	"github.com/base698/amythest/internal/tasks"
	"github.com/base698/amythest/internal/vault"
)

// Render renders a note to HTML with metadata extraction.
func (e *Engine) Render(v *vault.Vault, n *vault.Note) (*Result, error) {
	body, err := v.ReadBody(n)
	if err != nil {
		return nil, err
	}
	return e.RenderBody(v, n, body)
}

// RenderBody renders an already-read note body (lets the indexer share one
// read between rendering and task extraction).
func (e *Engine) RenderBody(v *vault.Vault, n *vault.Note, body []byte) (*Result, error) {
	visited := map[string]bool{n.Slug: true}
	return e.render(v, n, body, 0, visited)
}

func (e *Engine) render(v *vault.Vault, n *vault.Note, body []byte, depth int, visited map[string]bool) (*Result, error) {
	doc := e.parse(body)
	res := &Result{}
	e.resolve(v, n, doc, body, res, depth, visited)

	var buf bytes.Buffer
	if err := e.md.Renderer().Render(&buf, body, doc); err != nil {
		return nil, err
	}
	res.HTML = template.HTML(buf.String())
	res.Plain = plainText(doc, body)
	return res, nil
}

// resolve walks the AST filling in wikilink destinations, rendering
// transclusions, tagging task checkboxes with their source coordinates,
// and collecting TOC/links/tags.
func (e *Engine) resolve(v *vault.Vault, n *vault.Note, doc ast.Node, source []byte, res *Result, depth int, visited map[string]bool) {
	// Kanban board checkboxes stay read-only: those files belong to the
	// kanban store's lock/journal machinery, not the task toggle endpoint.
	editable := !strings.HasPrefix(n.Path, "kanban/")
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := node.(type) {
		case *ast.Heading:
			if depth == 0 {
				id := ""
				if v, ok := t.AttributeString("id"); ok {
					if b, ok := v.([]byte); ok {
						id = string(b)
					}
				}
				res.TOC = append(res.TOC, Heading{ID: id, Text: string(t.Text(source)), Level: t.Level})
			}
		case *obsidian.Tag:
			res.Tags = append(res.Tags, strings.ToLower(t.Name))
		case *obsidian.Wikilink:
			e.resolveWikilink(v, t, res, depth, visited)
		case *east.TaskCheckBox:
			if editable {
				if line, ok := checkboxLine(t, source); ok {
					t.SetAttributeString("slug", n.Slug)
					t.SetAttributeString("line", line)
					t.SetAttributeString("version", n.Hash)
					// The note's own tasks get the same inline actions as
					// /tasks rows; transcluded tasks keep just the toggle.
					if depth == 0 {
						if task, tok := tasks.ParseLine(sourceLine(source, line), n.Slug, n.Path, line, n.Hash); tok {
							if parent := t.Parent(); parent != nil {
								actions := ast.NewString([]byte(" " + tasks.RenderInlineActions(task)))
								actions.SetCode(true)
								parent.AppendChild(parent, actions)
							}
						}
					}
				}
			}
		}
		return ast.WalkContinue, nil
	})
}

// sourceLine returns the 1-based line from source, without its newline.
func sourceLine(source []byte, lineNo int) string {
	start := 0
	for current := 1; current < lineNo; current++ {
		i := bytes.IndexByte(source[start:], '\n')
		if i < 0 {
			return ""
		}
		start += i + 1
	}
	rest := source[start:]
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	return string(rest)
}

// checkboxLine locates a checkbox's 1-based body line via its parent
// block's first source segment.
func checkboxLine(cb *east.TaskCheckBox, source []byte) (int, bool) {
	parent := cb.Parent()
	if parent == nil || parent.Lines().Len() == 0 {
		return 0, false
	}
	start := parent.Lines().At(0).Start
	if start > len(source) {
		return 0, false
	}
	return bytes.Count(source[:start], []byte("\n")) + 1, true
}

func (e *Engine) resolveWikilink(v *vault.Vault, wl *obsidian.Wikilink, res *Result, depth int, visited map[string]bool) {
	if wl.Embed {
		e.resolveEmbed(v, wl, res, depth, visited)
		return
	}
	if wl.Target == "" { // [[#heading]] same-page link
		wl.Dest = "#" + HeadingID(wl.Fragment)
		return
	}
	n, ok := v.Resolve(wl.Target)
	if !ok {
		wl.Broken = true
		return
	}
	wl.Dest = e.slugURL(n.Slug)
	if wl.Fragment != "" {
		wl.Dest += "#" + HeadingID(wl.Fragment)
	}
	res.Links = append(res.Links, Link{Slug: n.Slug, Kind: "link"})
}

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".svg": true, ".avif": true, ".bmp": true}
var audioExts = map[string]bool{".mp3": true, ".m4a": true, ".wav": true, ".ogg": true, ".flac": true}
var videoExts = map[string]bool{".mp4": true, ".mov": true, ".webm": true, ".mkv": true}

func (e *Engine) resolveEmbed(v *vault.Vault, wl *obsidian.Wikilink, res *Result, depth int, visited map[string]bool) {
	ext := strings.ToLower(path.Ext(wl.Target))
	if ext != "" && ext != ".md" {
		if rel, ok := v.ResolveAsset(wl.Target); ok {
			wl.AssetRel = rel
			return
		}
		wl.Broken = true
		return
	}

	n, ok := v.Resolve(wl.Target)
	if !ok {
		wl.Broken = true
		return
	}
	if depth >= maxEmbedDepth || visited[n.Slug] {
		wl.EmbedHTML = []byte(fmt.Sprintf(
			`<blockquote class="callout" data-callout="warning"><div class="callout-title">Transclusion limit: %s</div></blockquote>`,
			template.HTMLEscapeString(n.Title)))
		return
	}
	body, err := v.ReadBody(n)
	if err != nil {
		wl.Broken = true
		return
	}
	visited[n.Slug] = true
	sub, err := e.render(v, n, body, depth+1, visited)
	delete(visited, n.Slug)
	if err != nil {
		wl.Broken = true
		return
	}
	res.Links = append(res.Links, Link{Slug: n.Slug, Kind: "embed"})
	res.Links = append(res.Links, sub.Links...)

	var b bytes.Buffer
	b.WriteString(`<blockquote class="transclude">`)
	fmt.Fprintf(&b, `<a class="transclude-src internal" href="%s">%s</a>`,
		e.slugURL(n.Slug), template.HTMLEscapeString(n.Title))
	b.WriteString(string(sub.HTML))
	b.WriteString(`</blockquote>`)
	wl.EmbedHTML = b.Bytes()
}

func (e *Engine) slugURL(slug string) string {
	u := url.URL{Path: e.base + slug}
	return u.EscapedPath()
}

func (e *Engine) assetURL(rel string) string {
	u := url.URL{Path: e.base + "assets/" + rel}
	return u.EscapedPath()
}

// ---- node renderer ----

type nodeRenderer struct {
	engine *Engine
}

func (r *nodeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(obsidian.KindWikilink, r.renderWikilink)
	reg.Register(obsidian.KindHighlight, r.renderHighlight)
	reg.Register(obsidian.KindTag, r.renderTag)
	reg.Register(obsidian.KindInlineField, r.renderInlineField)
	reg.Register(obsidian.KindCallout, r.renderCallout)
	reg.Register(ast.KindFencedCodeBlock, r.renderFencedCode)
	reg.Register(east.KindTaskCheckBox, r.renderTaskCheckbox)
}

// renderTaskCheckbox emits an interactive checkbox carrying its vault
// coordinates when the resolve pass marked it editable; otherwise the
// standard disabled checkbox.
func (r *nodeRenderer) renderTaskCheckbox(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	cb := node.(*east.TaskCheckBox)
	checked := ""
	if cb.IsChecked {
		checked = " checked"
	}
	slug, sok := cb.AttributeString("slug")
	line, lok := cb.AttributeString("line")
	version, vok := cb.AttributeString("version")
	if sok && lok && vok {
		fmt.Fprintf(w, `<input type="checkbox" class="task-toggle" data-slug="%s" data-line="%d" data-version="%s"%s> `,
			template.HTMLEscapeString(fmt.Sprint(slug)), line, template.HTMLEscapeString(fmt.Sprint(version)), checked)
	} else {
		fmt.Fprintf(w, `<input type="checkbox" disabled%s> `, checked)
	}
	return ast.WalkContinue, nil
}

func (r *nodeRenderer) renderWikilink(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	wl := node.(*obsidian.Wikilink)
	var buf bytes.Buffer
	switch {
	case wl.Embed && wl.EmbedHTML != nil:
		buf.Write(wl.EmbedHTML)
	case wl.Embed && wl.AssetRel != "":
		r.renderAssetEmbed(&buf, wl)
	case wl.Broken && wl.Embed:
		fmt.Fprintf(&buf, `<span class="broken-embed">![[%s]]</span>`, template.HTMLEscapeString(wl.Target))
	case wl.Broken:
		fmt.Fprintf(&buf, `<a class="internal broken">%s</a>`, template.HTMLEscapeString(wl.Label()))
	default:
		fmt.Fprintf(&buf, `<a class="internal" href="%s">%s</a>`,
			wl.Dest, template.HTMLEscapeString(wl.Label()))
	}
	_, _ = w.Write(buf.Bytes())
	return ast.WalkSkipChildren, nil
}

func (r *nodeRenderer) renderAssetEmbed(buf *bytes.Buffer, wl *obsidian.Wikilink) {
	src := r.engine.assetURL(wl.AssetRel)
	ext := strings.ToLower(path.Ext(wl.AssetRel))
	switch {
	case imageExts[ext]:
		attrs := ""
		if wl.Alias != "" {
			if width, err := strconv.Atoi(wl.Alias); err == nil {
				attrs = fmt.Sprintf(` width="%d"`, width)
			}
		}
		fmt.Fprintf(buf, `<img src="%s" alt="%s" loading="lazy"%s>`,
			src, template.HTMLEscapeString(path.Base(wl.AssetRel)), attrs)
	case ext == ".pdf":
		fmt.Fprintf(buf, `<iframe class="pdf-embed" src="%s"></iframe>`, src)
	case audioExts[ext]:
		fmt.Fprintf(buf, `<audio controls src="%s"></audio>`, src)
	case videoExts[ext]:
		fmt.Fprintf(buf, `<video controls src="%s"></video>`, src)
	default:
		fmt.Fprintf(buf, `<a href="%s">%s</a>`, src, template.HTMLEscapeString(path.Base(wl.AssetRel)))
	}
}

func (r *nodeRenderer) renderHighlight(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		h := node.(*obsidian.Highlight)
		_, _ = w.WriteString("<mark>")
		template.HTMLEscape(w, []byte(h.Content))
		_, _ = w.WriteString("</mark>")
	}
	return ast.WalkSkipChildren, nil
}

func (r *nodeRenderer) renderTag(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		t := node.(*obsidian.Tag)
		fmt.Fprintf(w, `<a class="tag-link" href="%stags/%s">#%s</a>`,
			r.engine.base, url.PathEscape(strings.ToLower(t.Name)), template.HTMLEscapeString(t.Name))
	}
	return ast.WalkSkipChildren, nil
}

func (r *nodeRenderer) renderInlineField(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		f := node.(*obsidian.InlineField)
		fmt.Fprintf(w, `<span class="inline-field"><span class="inline-field-key">%s</span><span class="inline-field-value">%s</span></span>`,
			template.HTMLEscapeString(f.Key), template.HTMLEscapeString(f.Value))
	}
	return ast.WalkSkipChildren, nil
}

func (r *nodeRenderer) renderCallout(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	c := node.(*obsidian.Callout)
	kind := obsidian.CalloutAliases[c.CalloutType]
	if kind == "" {
		kind = "note"
	}
	title := c.Title
	if title == "" {
		title = strings.ToUpper(c.CalloutType[:1]) + c.CalloutType[1:]
	}
	if entering {
		if c.Foldable {
			open := ""
			if !c.Folded {
				open = " open"
			}
			fmt.Fprintf(w, `<details class="callout" data-callout="%s"%s><summary class="callout-title"><span class="callout-icon"></span><span class="callout-title-inner">%s</span></summary><div class="callout-content">`,
				kind, open, template.HTMLEscapeString(title))
		} else {
			fmt.Fprintf(w, `<blockquote class="callout" data-callout="%s"><div class="callout-title"><span class="callout-icon"></span><span class="callout-title-inner">%s</span></div><div class="callout-content">`,
				kind, template.HTMLEscapeString(title))
		}
	} else {
		if c.Foldable {
			_, _ = w.WriteString("</div></details>")
		} else {
			_, _ = w.WriteString("</div></blockquote>")
		}
	}
	return ast.WalkContinue, nil
}

func (r *nodeRenderer) renderFencedCode(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	cb := node.(*ast.FencedCodeBlock)
	lang := string(cb.Language(source))
	var code bytes.Buffer
	for i := 0; i < cb.Lines().Len(); i++ {
		line := cb.Lines().At(i)
		code.Write(line.Value(source))
	}
	var buf bytes.Buffer
	highlightCode(&buf, lang, code.String())
	_, _ = w.Write(buf.Bytes())
	return ast.WalkSkipChildren, nil
}

// plainText extracts readable text for search indexing.
func plainText(doc ast.Node, source []byte) string {
	var b strings.Builder
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := node.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(source))
			if t.SoftLineBreak() || t.HardLineBreak() {
				b.WriteByte('\n')
			}
		case *ast.Heading, *ast.Paragraph, *ast.ListItem:
			b.WriteByte('\n')
		case *obsidian.Wikilink:
			if !t.Embed {
				b.WriteString(t.Label())
				b.WriteByte(' ')
			}
			return ast.WalkSkipChildren, nil
		case *obsidian.Highlight:
			b.WriteString(t.Content)
		case *east.TaskCheckBox:
			// no text
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}
