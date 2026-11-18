package markdown

import (
	"fmt"
	"html/template"
	"net/url"
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
)

var canvasIDPattern = regexp.MustCompile(`^[a-z2-7]{12}$`)

// canvasID reports the canvas a link points at: this site's /canvas/<id>,
// relative or on any host (the public origin is not known here).
func (e *Engine) canvasID(dest string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(dest))
	if err != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	rest, ok := strings.CutPrefix(u.Path, e.base+"canvas/")
	if !ok {
		return "", false
	}
	rest = strings.TrimSuffix(rest, "/")
	if !canvasIDPattern.MatchString(rest) {
		return "", false
	}
	return rest, true
}

func (e *Engine) canvasEmbedHTML(id, label string) string {
	page := e.base + "canvas/" + id
	img := e.base + "api/canvas/" + id + "/drawing.png"
	if label == "" || strings.HasPrefix(label, "http") || strings.Contains(label, "/canvas/") {
		label = "Canvas"
	}
	l := template.HTMLEscapeString(label)
	return fmt.Sprintf(`<a class="canvas-embed" data-no-spa href="%s"><img src="%s" alt="%s" loading="lazy"><span class="canvas-embed-label">%s · open canvas</span></a>`,
		template.HTMLEscapeString(page), template.HTMLEscapeString(img), l, l)
}

// linkText is the visible text of a link node.
func linkText(n ast.Node, source []byte) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(source))
		case *ast.String:
			b.Write(t.Value)
		default:
			b.WriteString(linkText(c, source))
		}
	}
	return strings.TrimSpace(b.String())
}

// alone reports whether n is the only thing in its paragraph, ignoring
// whitespace — a link on a line of its own, which reads as "show this".
func alone(n ast.Node, source []byte) bool {
	p := n.Parent()
	if p == nil || p.Kind() != ast.KindParagraph {
		return false
	}
	for c := p.FirstChild(); c != nil; c = c.NextSibling() {
		if c == n {
			continue
		}
		if t, ok := c.(*ast.Text); ok && strings.TrimSpace(string(t.Segment.Value(source))) == "" {
			continue
		}
		return false
	}
	return true
}

// canvasEmbeds turns canvas links into live drawings: an image whose source
// is a canvas link, or a canvas link on a line of its own. A canvas link
// inside a sentence stays a link. The image is fetched live, so a note shows
// the canvas as it is now, not when the note was rendered.
func (e *Engine) canvasEmbeds(doc ast.Node, source []byte) {
	type swap struct {
		node ast.Node
		html string
	}
	var swaps []swap
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := node.(type) {
		case *ast.Image:
			if id, ok := e.canvasID(string(t.Destination)); ok {
				swaps = append(swaps, swap{t, e.canvasEmbedHTML(id, linkText(t, source))})
				return ast.WalkSkipChildren, nil
			}
		case *ast.Link:
			if id, ok := e.canvasID(string(t.Destination)); ok && alone(t, source) {
				swaps = append(swaps, swap{t, e.canvasEmbedHTML(id, linkText(t, source))})
				return ast.WalkSkipChildren, nil
			}
		case *ast.AutoLink:
			if id, ok := e.canvasID(string(t.URL(source))); ok && alone(t, source) {
				swaps = append(swaps, swap{t, e.canvasEmbedHTML(id, "")})
				return ast.WalkSkipChildren, nil
			}
		}
		return ast.WalkContinue, nil
	})
	for _, s := range swaps {
		raw := ast.NewString([]byte(s.html))
		raw.SetCode(true)
		s.node.Parent().ReplaceChild(s.node.Parent(), s.node, raw)
	}
}
