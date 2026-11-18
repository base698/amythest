package canvas

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Summary is what an agent reads instead of the raw scene: the shapes and the
// links between them, free notes, frames, and where hand-drawn ink and images
// sit. Anything without a structural meaning (a scribble, a circled word) is
// placed "near" the closest shape so the agent knows where to look in the PNG.
type Summary struct {
	Shapes []Shape `json:"shapes"`
	Links  []Link  `json:"links"`
	Notes  []Note  `json:"notes"`
	Groups []Group `json:"groups"`
	Ink    []Ink   `json:"ink"`
	Images []Image `json:"images"`
}

type Shape struct {
	ID    string  `json:"id"`
	Kind  string  `json:"kind"`
	Label string  `json:"label,omitempty"`
	Color string  `json:"color,omitempty"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
}

type Link struct {
	ID    string `json:"id"`
	From  string `json:"from,omitempty"` // shape ID; empty when the end is loose
	To    string `json:"to,omitempty"`
	Label string `json:"label,omitempty"`
	Style string `json:"style"` // "->", "<-", "<->" or "--"
	Loose bool   `json:"loose,omitempty"`
}

type Note struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Near string `json:"near,omitempty"`
}

type Group struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

type Ink struct {
	Near    string `json:"near,omitempty"`
	Strokes int    `json:"strokes"`
}

type Image struct {
	ID   string `json:"id"`
	MIME string `json:"mime,omitempty"`
	Near string `json:"near,omitempty"`
}

type rawElement struct {
	ID           string      `json:"id"`
	Type         string      `json:"type"`
	X            float64     `json:"x"`
	Y            float64     `json:"y"`
	Width        float64     `json:"width"`
	Height       float64     `json:"height"`
	IsDeleted    bool        `json:"isDeleted"`
	Text         string      `json:"text"`
	OriginalText string      `json:"originalText"`
	ContainerID  *string     `json:"containerId"`
	FrameID      *string     `json:"frameId"`
	Name         *string     `json:"name"`
	StrokeColor  string      `json:"strokeColor"`
	BgColor      string      `json:"backgroundColor"`
	Points       [][]float64 `json:"points"`
	StartBinding *binding    `json:"startBinding"`
	EndBinding   *binding    `json:"endBinding"`
	StartArrow   *string     `json:"startArrowhead"`
	EndArrow     *string     `json:"endArrowhead"`
	FileID       *string     `json:"fileId"`
}

type binding struct {
	ElementID string `json:"elementId"`
}

type rawScene struct {
	Elements []rawElement `json:"elements"`
	Files    map[string]struct {
		MIMEType string `json:"mimeType"`
	} `json:"files"`
}

type box struct{ x, y, w, h float64 }

func (b box) contains(o box) bool {
	return o.x >= b.x && o.y >= b.y && o.x+o.w <= b.x+b.w && o.y+o.h <= b.y+b.h && (o.w*o.h) < (b.w*b.h)
}

// dist is the distance from a point to the box (0 inside it).
func (b box) dist(px, py float64) float64 {
	dx := math.Max(math.Max(b.x-px, 0), px-(b.x+b.w))
	dy := math.Max(math.Max(b.y-py, 0), py-(b.y+b.h))
	return math.Hypot(dx, dy)
}

func norm(e rawElement) box {
	b := box{e.X, e.Y, e.Width, e.Height}
	if b.w < 0 {
		b.x, b.w = b.x+b.w, -b.w
	}
	if b.h < 0 {
		b.y, b.h = b.y+b.h, -b.h
	}
	return b
}

var shapeKinds = map[string]bool{"rectangle": true, "ellipse": true, "diamond": true}

func elementText(e rawElement) string {
	t := e.OriginalText
	if t == "" {
		t = e.Text
	}
	return strings.Join(strings.Fields(t), " ")
}

// Summarize reads an Excalidraw scene.
func Summarize(scene []byte) (Summary, error) {
	var raw rawScene
	if err := json.Unmarshal(scene, &raw); err != nil {
		return Summary{}, err
	}
	live := make([]rawElement, 0, len(raw.Elements))
	byID := map[string]rawElement{}
	for _, e := range raw.Elements {
		if e.IsDeleted {
			continue
		}
		live = append(live, e)
		byID[e.ID] = e
	}

	sum := Summary{Shapes: []Shape{}, Links: []Link{}, Notes: []Note{}, Groups: []Group{}, Ink: []Ink{}, Images: []Image{}}
	boxes := map[string]box{}
	shapeIdx := map[string]int{}
	for _, e := range live {
		if !shapeKinds[e.Type] {
			continue
		}
		b := norm(e)
		boxes[e.ID] = b
		shapeIdx[e.ID] = len(sum.Shapes)
		color := ""
		if e.BgColor != "" && e.BgColor != "transparent" {
			color = e.BgColor
		}
		sum.Shapes = append(sum.Shapes, Shape{ID: e.ID, Kind: e.Type, Color: color, X: b.x, Y: b.y, W: b.w, H: b.h})
	}

	// nearest finds the shape closest to a point, within limit.
	nearest := func(px, py, limit float64) string {
		best, bestD, bestArea := "", limit, math.Inf(1)
		for id, b := range boxes {
			d := b.dist(px, py)
			area := b.w * b.h
			// Inside several (a box in a frame): the smallest one is meant.
			if d < bestD || (d == bestD && d <= limit && area < bestArea) {
				best, bestD, bestArea = id, d, area
			}
		}
		return best
	}

	// Labels: text bound to a shape, then loose text sitting inside one.
	linkLabels := map[string]string{}
	var loose []rawElement
	for _, e := range live {
		if e.Type != "text" {
			continue
		}
		if e.ContainerID != nil && *e.ContainerID != "" {
			if i, ok := shapeIdx[*e.ContainerID]; ok {
				sum.Shapes[i].Label = elementText(e)
				continue
			}
			if c, ok := byID[*e.ContainerID]; ok && (c.Type == "arrow" || c.Type == "line") {
				linkLabels[c.ID] = elementText(e)
				continue
			}
		}
		loose = append(loose, e)
	}
	for _, e := range loose {
		b := norm(e)
		cx, cy := b.x+b.w/2, b.y+b.h/2
		if id := nearest(cx, cy, 0); id != "" {
			if i := shapeIdx[id]; sum.Shapes[i].Label == "" {
				sum.Shapes[i].Label = elementText(e)
				continue
			}
		}
		sum.Notes = append(sum.Notes, Note{ID: e.ID, Text: elementText(e), Near: nearest(cx, cy, 200)})
	}

	// Links.
	strays := map[string]int{}
	for _, e := range live {
		if e.Type != "arrow" && e.Type != "line" {
			continue
		}
		l := Link{ID: e.ID, Label: linkLabels[e.ID], Style: "--"}
		if e.Type == "arrow" {
			switch {
			case e.StartArrow != nil && e.EndArrow != nil:
				l.Style = "<->"
			case e.StartArrow != nil:
				l.Style = "<-"
			case e.EndArrow != nil:
				l.Style = "->"
			}
		}
		end := func(b *binding, pt []float64) (string, bool) {
			if b != nil {
				if _, ok := boxes[b.ElementID]; ok {
					return b.ElementID, false
				}
			}
			if len(pt) < 2 {
				return "", false
			}
			id := nearest(e.X+pt[0], e.Y+pt[1], 30)
			return id, id != ""
		}
		var first, last []float64
		if len(e.Points) > 0 {
			first, last = e.Points[0], e.Points[len(e.Points)-1]
		}
		var lf, lt bool
		l.From, lf = end(e.StartBinding, first)
		l.To, lt = end(e.EndBinding, last)
		l.Loose = lf || lt
		if l.Loose && l.From == l.To && l.Label == "" {
			// Both guessed ends land on the same shape: a tick or a slip of
			// the pen, not a connection. Report it as a mark there.
			strays[l.From]++
			continue
		}
		if l.From == "" && l.To == "" && e.Type == "line" && l.Label == "" {
			// A bare line is decoration, not a connection.
			continue
		}
		sum.Links = append(sum.Links, l)
	}

	// Frames, then shapes drawn around other shapes.
	frames := map[string]*Group{}
	var frameOrder []string
	for _, e := range live {
		if e.Type == "frame" || e.Type == "magicframe" {
			name := "Frame"
			if e.Name != nil && *e.Name != "" {
				name = *e.Name
			}
			frames[e.ID] = &Group{ID: e.ID, Name: name, Members: []string{}}
			frameOrder = append(frameOrder, e.ID)
		}
	}
	for _, e := range live {
		if e.FrameID == nil {
			continue
		}
		if g, ok := frames[*e.FrameID]; ok {
			if _, isShape := shapeIdx[e.ID]; isShape {
				g.Members = append(g.Members, e.ID)
			}
		}
	}
	for _, id := range frameOrder {
		sum.Groups = append(sum.Groups, *frames[id])
	}
	for _, outer := range sum.Shapes {
		if outer.Kind != "rectangle" {
			continue
		}
		ob := boxes[outer.ID]
		var members []string
		for _, inner := range sum.Shapes {
			if inner.ID != outer.ID && ob.contains(boxes[inner.ID]) {
				members = append(members, inner.ID)
			}
		}
		if len(members) > 0 {
			name := outer.Label
			if name == "" {
				name = "box"
			}
			sum.Groups = append(sum.Groups, Group{ID: outer.ID, Name: name, Members: members})
		}
	}

	// Ink and images, placed by their middle.
	ink := strays
	for _, e := range live {
		b := norm(e)
		cx, cy := b.x+b.w/2, b.y+b.h/2
		switch e.Type {
		case "freedraw":
			ink[nearest(cx, cy, 150)]++
		case "image":
			img := Image{ID: e.ID, Near: nearest(cx, cy, 150)}
			if e.FileID != nil {
				img.MIME = raw.Files[*e.FileID].MIMEType
			}
			sum.Images = append(sum.Images, img)
		}
	}
	for near, n := range ink {
		sum.Ink = append(sum.Ink, Ink{Near: near, Strokes: n})
	}
	sort.Slice(sum.Ink, func(i, j int) bool { return sum.Ink[i].Near < sum.Ink[j].Near })

	// Reading order: top to bottom, then left to right.
	sort.SliceStable(sum.Shapes, func(i, j int) bool {
		a, b := sum.Shapes[i], sum.Shapes[j]
		if math.Abs(a.Y-b.Y) > 20 {
			return a.Y < b.Y
		}
		return a.X < b.X
	})
	return sum, nil
}

// names gives every shape a readable, unique handle: its label, or its kind,
// with a short ID suffix when two would read the same.
func (s Summary) names() map[string]string {
	base := map[string]string{}
	count := map[string]int{}
	for _, sh := range s.Shapes {
		n := sh.Label
		if r := []rune(n); len(r) > 60 {
			n = string(r[:57]) + "..."
		}
		base[sh.ID] = n
		count[n]++
	}
	out := map[string]string{}
	for _, sh := range s.Shapes {
		n := base[sh.ID]
		switch {
		case n == "":
			out[sh.ID] = fmt.Sprintf("unlabeled %s #%s", sh.Kind, shortID(sh.ID))
			continue
		case count[n] > 1:
			n = fmt.Sprintf("%s#%s", n, shortID(sh.ID))
		}
		out[sh.ID] = fmt.Sprintf("%q", n)
	}
	return out
}

func shortID(id string) string {
	if len(id) > 4 {
		return id[:4]
	}
	return id
}

func (s Summary) name(names map[string]string, id string) string {
	if id == "" {
		return "(nothing)"
	}
	if n, ok := names[id]; ok {
		return n
	}
	return "(gone)"
}

// Text renders the summary for an agent to read.
func (s Summary) Text() string {
	var b strings.Builder
	names := s.names()
	if len(s.Shapes)+len(s.Links)+len(s.Notes)+len(s.Ink)+len(s.Images) == 0 {
		return "The canvas is empty.\n"
	}
	if len(s.Shapes) > 0 {
		fmt.Fprintf(&b, "Shapes (%d):\n", len(s.Shapes))
		for _, sh := range s.Shapes {
			extra := ""
			if sh.Label != "" && sh.Kind != "rectangle" {
				extra = " (" + sh.Kind + ")"
			}
			if sh.Color != "" {
				extra += " fill " + sh.Color
			}
			fmt.Fprintf(&b, "  %s%s\n", names[sh.ID], extra)
		}
	}
	if len(s.Links) > 0 {
		fmt.Fprintf(&b, "Connections (%d):\n", len(s.Links))
		for _, l := range s.Links {
			fmt.Fprintf(&b, "  %s\n", s.linkText(names, l))
		}
	}
	if len(s.Groups) > 0 {
		b.WriteString("Groups:\n")
		for _, g := range s.Groups {
			var m []string
			for _, id := range g.Members {
				m = append(m, s.name(names, id))
			}
			fmt.Fprintf(&b, "  %q contains %s\n", g.Name, strings.Join(m, ", "))
		}
	}
	if len(s.Notes) > 0 {
		b.WriteString("Notes:\n")
		for _, n := range s.Notes {
			fmt.Fprintf(&b, "  %q%s\n", n.Text, s.nearText(names, n.Near))
		}
	}
	if len(s.Ink) > 0 {
		b.WriteString("Hand-drawn marks (look at the PNG):\n")
		for _, i := range s.Ink {
			fmt.Fprintf(&b, "  %d stroke(s)%s\n", i.Strokes, s.nearText(names, i.Near))
		}
	}
	if len(s.Images) > 0 {
		b.WriteString("Images:\n")
		for _, i := range s.Images {
			kind := i.MIME
			if kind == "" {
				kind = "image"
			}
			fmt.Fprintf(&b, "  %s%s\n", kind, s.nearText(names, i.Near))
		}
	}
	return b.String()
}

func (s Summary) nearText(names map[string]string, id string) string {
	if id == "" {
		return " (on its own)"
	}
	return " near " + s.name(names, id)
}

func (s Summary) linkText(names map[string]string, l Link) string {
	t := s.name(names, l.From) + " " + l.Style + " " + s.name(names, l.To)
	if l.Label != "" {
		t += fmt.Sprintf(" [%s]", l.Label)
	}
	if l.Loose {
		t += " (ends not attached; guessed from position)"
	}
	return t
}

// Diff describes what changed from prev to s, one line per change.
func (s Summary) Diff(prev Summary) []string {
	var out []string
	names, oldNames := s.names(), prev.names()
	oldShapes := map[string]Shape{}
	for _, sh := range prev.Shapes {
		oldShapes[sh.ID] = sh
	}
	for _, sh := range s.Shapes {
		old, ok := oldShapes[sh.ID]
		switch {
		case !ok:
			out = append(out, "added shape "+names[sh.ID])
		case old.Label != sh.Label:
			out = append(out, fmt.Sprintf("renamed %s to %s", oldNames[sh.ID], names[sh.ID]))
		case math.Abs(old.X-sh.X) > 40 || math.Abs(old.Y-sh.Y) > 40:
			out = append(out, "moved "+names[sh.ID])
		}
		delete(oldShapes, sh.ID)
	}
	for _, sh := range prev.Shapes {
		if _, gone := oldShapes[sh.ID]; gone {
			out = append(out, "removed shape "+oldNames[sh.ID])
		}
	}

	oldLinks := map[string]Link{}
	for _, l := range prev.Links {
		oldLinks[l.ID] = l
	}
	for _, l := range s.Links {
		old, ok := oldLinks[l.ID]
		switch {
		case !ok:
			out = append(out, "connected "+s.linkText(names, l))
		case old.From != l.From || old.To != l.To || old.Style != l.Style:
			out = append(out, fmt.Sprintf("rewired %s (was %s)", s.linkText(names, l), prev.linkText(oldNames, old)))
		case old.Label != l.Label:
			out = append(out, "relabeled "+s.linkText(names, l))
		}
		delete(oldLinks, l.ID)
	}
	for _, l := range prev.Links {
		if _, gone := oldLinks[l.ID]; gone {
			out = append(out, "disconnected "+prev.linkText(oldNames, l))
		}
	}

	oldNotes := map[string]Note{}
	for _, n := range prev.Notes {
		oldNotes[n.ID] = n
	}
	for _, n := range s.Notes {
		old, ok := oldNotes[n.ID]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("wrote note %q%s", n.Text, s.nearText(names, n.Near)))
		case old.Text != n.Text:
			out = append(out, fmt.Sprintf("edited note %q to %q", old.Text, n.Text))
		}
		delete(oldNotes, n.ID)
	}
	for _, n := range prev.Notes {
		if _, gone := oldNotes[n.ID]; gone {
			out = append(out, fmt.Sprintf("removed note %q", n.Text))
		}
	}

	oldInk := map[string]int{}
	for _, i := range prev.Ink {
		oldInk[i.Near] = i.Strokes
	}
	for _, i := range s.Ink {
		if d := i.Strokes - oldInk[i.Near]; d > 0 {
			out = append(out, fmt.Sprintf("drew %d stroke(s)%s", d, s.nearText(names, i.Near)))
		} else if d < 0 {
			out = append(out, fmt.Sprintf("erased %d stroke(s)%s", -d, s.nearText(names, i.Near)))
		}
		delete(oldInk, i.Near)
	}
	for near, n := range oldInk {
		out = append(out, fmt.Sprintf("erased %d stroke(s)%s", n, prev.nearText(oldNames, near)))
	}

	oldImages := map[string]bool{}
	for _, i := range prev.Images {
		oldImages[i.ID] = true
	}
	for _, i := range s.Images {
		if !oldImages[i.ID] {
			out = append(out, "added an image"+s.nearText(names, i.Near))
		}
		delete(oldImages, i.ID)
	}
	if n := len(oldImages); n > 0 {
		out = append(out, fmt.Sprintf("removed %d image(s)", n))
	}
	return out
}
