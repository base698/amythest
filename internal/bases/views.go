package bases

import (
	"fmt"
	"html/template"
	"math/rand"
	"sort"
	"strings"

	"github.com/base698/amythest/internal/bases/expr"
)

type viewGroup struct {
	name string
	rows []*rowEnv
}

// RenderContext carries site context into view rendering.
type RenderContext struct {
	BaseURL  string   // site base url ("/")
	SelfURL  string   // URL of the page hosting the view (for tab links)
	ViewIdx  int
	Cols     []string // when set, overrides the view's column order
	Picker   bool     // render the column picker (full-page bases only)
}

type column struct {
	ref     string
	label   string
	node    expr.Node
	err     error
	width   int
	isTitle bool
}

// prepare filters, sorts, limits, and groups rows for a view.
func (b *Base) prepare(rows []*Row, viewIdx int) (View, []viewGroup, []*rowEnv, error) {
	if viewIdx < 0 || viewIdx >= len(b.Views) {
		viewIdx = 0
	}
	view := b.Views[viewIdx]

	if err := compileFilters(b.Filters); err != nil {
		return view, nil, nil, err
	}
	if err := compileFilters(view.Filters); err != nil {
		return view, nil, nil, err
	}

	// Filter.
	var kept []*rowEnv
	for _, r := range rows {
		env := newRowEnv(r, b)
		ok, err := matches(b.Filters, env)
		if err != nil {
			return view, nil, nil, err
		}
		if !ok {
			continue
		}
		ok, err = matches(view.Filters, env)
		if err != nil {
			return view, nil, nil, err
		}
		if ok {
			kept = append(kept, env)
		}
	}

	// Sort.
	type sortKey struct {
		node expr.Node
		desc bool
	}
	var keys []sortKey
	for _, s := range view.Sort {
		n, err := expr.Parse(s.Property)
		if err != nil {
			return view, nil, nil, fmt.Errorf("sort %q: %w", s.Property, err)
		}
		keys = append(keys, sortKey{n, strings.EqualFold(s.Direction, "DESC")})
	}
	if len(keys) > 0 {
		sort.SliceStable(kept, func(i, j int) bool {
			for _, k := range keys {
				vi, _ := expr.Eval(k.node, kept[i])
				vj, _ := expr.Eval(k.node, kept[j])
				c := expr.Compare(vi, vj)
				if c != 0 {
					if k.desc {
						return c > 0
					}
					return c < 0
				}
			}
			return false
		})
	}

	if view.Sample > 0 && len(kept) > view.Sample {
		rand.Shuffle(len(kept), func(i, j int) { kept[i], kept[j] = kept[j], kept[i] })
		kept = kept[:view.Sample]
	}

	if view.Limit > 0 && len(kept) > view.Limit {
		kept = kept[:view.Limit]
	}

	// Group.
	groups := []viewGroup{{name: "", rows: kept}}
	if view.GroupBy != nil && view.GroupBy.Property != "" {
		gnode, err := expr.Parse(view.GroupBy.Property)
		if err != nil {
			return view, nil, nil, fmt.Errorf("groupBy: %w", err)
		}
		pos := map[string]int{}
		groups = nil
		for _, env := range kept {
			v, _ := expr.Eval(gnode, env)
			k := v.Text()
			if i, ok := pos[k]; ok {
				groups[i].rows = append(groups[i].rows, env)
			} else {
				pos[k] = len(groups)
				groups = append(groups, viewGroup{name: k, rows: []*rowEnv{env}})
			}
		}
		sort.SliceStable(groups, func(i, j int) bool {
			if strings.EqualFold(view.GroupBy.Direction, "DESC") {
				return groups[i].name > groups[j].name
			}
			return groups[i].name < groups[j].name
		})
	}
	return view, groups, kept, nil
}

// ViewData is a structured (non-HTML) rendering of a view, for APIs.
type ViewData struct {
	View    string          `json:"view"`
	Columns []string        `json:"columns"`
	Groups  []ViewDataGroup `json:"groups"`
}

type ViewDataGroup struct {
	Name string     `json:"name,omitempty"`
	Rows [][]string `json:"rows"`
	// Slugs is row-parallel note identity so API clients (the amy TUI) can
	// open the note behind a row.
	Slugs []string `json:"slugs,omitempty"`
}

// Data evaluates a view into columns and cell text.
func (b *Base) Data(rows []*Row, viewIdx int) (*ViewData, error) {
	if len(b.Views) == 0 {
		return nil, fmt.Errorf("base has no views")
	}
	view, groups, _, err := b.prepare(rows, viewIdx)
	if err != nil {
		return nil, err
	}
	cols := b.columns(view)
	out := &ViewData{View: view.Name}
	for _, c := range cols {
		out.Columns = append(out.Columns, c.label)
	}
	for _, g := range groups {
		dg := ViewDataGroup{Name: g.name}
		for _, env := range g.rows {
			row := make([]string, len(cols))
			for i, c := range cols {
				if c.err != nil {
					row[i] = "#ERR " + c.err.Error()
					continue
				}
				v, err := expr.Eval(c.node, env)
				if err != nil {
					row[i] = "#ERR " + err.Error()
					continue
				}
				row[i] = v.Text()
			}
			dg.Rows = append(dg.Rows, row)
			dg.Slugs = append(dg.Slugs, env.row.Slug)
		}
		out.Groups = append(out.Groups, dg)
	}
	return out, nil
}

// Render renders one view of the base over the note rows.
func (b *Base) Render(rows []*Row, ctx RenderContext) string {
	if len(b.Views) == 0 {
		return errorBox("base has no views")
	}
	if ctx.ViewIdx < 0 || ctx.ViewIdx >= len(b.Views) {
		ctx.ViewIdx = 0
	}
	view, groups, kept, err := b.prepare(rows, ctx.ViewIdx)
	if err != nil {
		return errorBox(err.Error())
	}

	var sb strings.Builder
	sb.WriteString(`<div class="base-view">`)
	b.renderTabs(&sb, ctx)

	switch view.Type {
	case "", "table":
		if ctx.Picker {
			b.renderColumnPicker(&sb, view, ctx)
		}
		b.renderTable(&sb, view, groups, ctx)
	case "list":
		b.renderList(&sb, groups, ctx)
	case "cards", "gallery":
		b.renderCards(&sb, view, groups, ctx)
	case "board":
		b.renderBoard(&sb, view, kept, ctx)
	default:
		sb.WriteString(errorBox("unsupported view type " + view.Type))
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

func (b *Base) renderTabs(sb *strings.Builder, ctx RenderContext) {
	if len(b.Views) <= 1 {
		return
	}
	sb.WriteString(`<nav class="base-tabs">`)
	for i, v := range b.Views {
		name := v.Name
		if name == "" {
			name = fmt.Sprintf("View %d", i+1)
		}
		cls := ""
		if i == ctx.ViewIdx {
			cls = ` class="active"`
		}
		fmt.Fprintf(sb, `<a%s href="%s?view=%d">%s</a>`, cls,
			template.HTMLEscapeString(ctx.SelfURL), i, template.HTMLEscapeString(name))
	}
	sb.WriteString(`</nav>`)
}

func (b *Base) columns(view View) []column {
	return b.buildColumns(view.Order, view)
}

// columnsFor applies the RenderContext column override, falling back to the
// view's own order.
func (b *Base) columnsFor(view View, ctx RenderContext) []column {
	if len(ctx.Cols) > 0 {
		return b.buildColumns(ctx.Cols, view)
	}
	return b.columns(view)
}

func (b *Base) buildColumns(refs []string, view View) []column {
	if len(refs) == 0 {
		refs = []string{"file.name"}
	}
	cols := make([]column, 0, len(refs))
	for _, ref := range refs {
		c := column{ref: ref, label: b.displayName(ref), width: view.ColumnSize[ref]}
		c.isTitle = ref == "file.name" || ref == "title" || ref == "note.title"
		c.node, c.err = expr.Parse(ref)
		cols = append(cols, c)
	}
	return cols
}

// availableRefs lists every column ref the base knows about: refs used by any
// view, declared properties, and formulas.
func (b *Base) availableRefs() []string {
	seen := map[string]bool{}
	var out []string
	add := func(r string) {
		if r != "" && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	add("file.name")
	for _, v := range b.Views {
		for _, r := range v.Order {
			add(r)
		}
	}
	props := make([]string, 0, len(b.Properties))
	for k := range b.Properties {
		props = append(props, k)
	}
	sort.Strings(props)
	for _, k := range props {
		add(k)
	}
	formulas := make([]string, 0, len(b.Formulas))
	for k := range b.Formulas {
		formulas = append(formulas, k)
	}
	sort.Strings(formulas)
	for _, k := range formulas {
		add("formula." + k)
	}
	return out
}

// renderColumnPicker emits a plain GET form of column checkboxes so table
// views work with any column set — no JS required.
func (b *Base) renderColumnPicker(sb *strings.Builder, view View, ctx RenderContext) {
	selected := map[string]bool{}
	for _, c := range b.columnsFor(view, ctx) {
		selected[c.ref] = true
	}
	sb.WriteString(`<details class="base-cols"><summary>Columns</summary>`)
	fmt.Fprintf(sb, `<form method="get" action="%s">`, template.HTMLEscapeString(ctx.SelfURL))
	fmt.Fprintf(sb, `<input type="hidden" name="view" value="%d">`, ctx.ViewIdx)
	for _, ref := range b.availableRefs() {
		checked := ""
		if selected[ref] {
			checked = " checked"
		}
		fmt.Fprintf(sb, `<label><input type="checkbox" name="cols" value="%s"%s> %s</label>`,
			template.HTMLEscapeString(ref), checked, template.HTMLEscapeString(b.displayName(ref)))
	}
	sb.WriteString(`<div class="base-cols-actions"><button type="submit">Apply</button>`)
	fmt.Fprintf(sb, `<a href="%s?view=%d">Reset</a></div>`,
		template.HTMLEscapeString(ctx.SelfURL), ctx.ViewIdx)
	sb.WriteString(`</form></details>`)
}

func (b *Base) displayName(ref string) string {
	if p, ok := b.Properties[ref]; ok && p.DisplayName != "" {
		return p.DisplayName
	}
	if i := strings.LastIndex(ref, "."); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

func (b *Base) renderTable(sb *strings.Builder, view View, groups []viewGroup, ctx RenderContext) {
	cols := b.columnsFor(view, ctx)
	sb.WriteString(`<div class="base-table-wrap"><table class="base-table"><thead><tr>`)
	for _, c := range cols {
		style := ""
		if c.width > 0 {
			style = fmt.Sprintf(` style="min-width:%dpx"`, c.width)
		}
		fmt.Fprintf(sb, `<th%s data-ref="%s">%s</th>`, style,
			template.HTMLEscapeString(c.ref), template.HTMLEscapeString(c.label))
	}
	sb.WriteString(`</tr></thead><tbody>`)
	for _, g := range groups {
		if g.name != "" || len(groups) > 1 {
			fmt.Fprintf(sb, `<tr class="base-group-row"><td colspan="%d">%s</td></tr>`,
				len(cols), template.HTMLEscapeString(orUntitled(g.name)))
		}
		for _, env := range g.rows {
			sb.WriteString("<tr>")
			for _, c := range cols {
				if key, ok := b.editableKey(c.ref); ok {
					fmt.Fprintf(sb, `<td class="base-editable" data-slug="%s" data-key="%s">`,
						template.HTMLEscapeString(env.row.Slug), template.HTMLEscapeString(key))
				} else {
					sb.WriteString("<td>")
				}
				sb.WriteString(b.cellHTML(c, env, ctx))
				sb.WriteString("</td>")
			}
			sb.WriteString("</tr>")
		}
	}
	sb.WriteString(`</tbody></table></div>`)
}

func (b *Base) renderList(sb *strings.Builder, groups []viewGroup, ctx RenderContext) {
	for _, g := range groups {
		if g.name != "" {
			fmt.Fprintf(sb, `<h4 class="base-group">%s</h4>`, template.HTMLEscapeString(g.name))
		}
		sb.WriteString(`<ul class="base-list">`)
		for _, env := range g.rows {
			fmt.Fprintf(sb, `<li><a class="internal" href="%s">%s</a></li>`,
				template.HTMLEscapeString(ctx.BaseURL+env.row.Slug),
				template.HTMLEscapeString(env.row.Title))
		}
		sb.WriteString(`</ul>`)
	}
}

func (b *Base) renderCards(sb *strings.Builder, view View, groups []viewGroup, ctx RenderContext) {
	size := view.CardSize
	if size <= 0 {
		size = 220
	}
	var imgNode expr.Node
	if view.Image != "" {
		imgNode, _ = expr.Parse(view.Image)
	}
	cols := b.columns(view)
	for _, g := range groups {
		if g.name != "" {
			fmt.Fprintf(sb, `<h4 class="base-group">%s</h4>`, template.HTMLEscapeString(g.name))
		}
		fmt.Fprintf(sb, `<div class="base-cards" style="--card-size:%dpx">`, size)
		for _, env := range g.rows {
			sb.WriteString(`<div class="base-card">`)
			if imgNode != nil {
				if v, err := expr.Eval(imgNode, env); err == nil && v.Truthy() {
					src := strings.Trim(strings.TrimSpace(v.Text()), "[]!")
					fmt.Fprintf(sb, `<img loading="lazy" src="%s" alt="">`,
						template.HTMLEscapeString(ctx.BaseURL+"assets/"+src))
				}
			}
			fmt.Fprintf(sb, `<a class="internal base-card-title" href="%s">%s</a>`,
				template.HTMLEscapeString(ctx.BaseURL+env.row.Slug),
				template.HTMLEscapeString(env.row.Title))
			for _, c := range cols {
				if c.isTitle {
					continue
				}
				sb.WriteString(`<div class="base-card-field"><span>` +
					template.HTMLEscapeString(c.label) + `</span> ` + b.cellHTML(c, env, ctx) + `</div>`)
			}
			sb.WriteString(`</div>`)
		}
		sb.WriteString(`</div>`)
	}
}

func (b *Base) renderBoard(sb *strings.Builder, view View, kept []*rowEnv, ctx RenderContext) {
	prop := view.BoardProp
	if prop == "" && view.GroupBy != nil {
		prop = view.GroupBy.Property
	}
	if prop == "" {
		sb.WriteString(errorBox("board view needs boardProperty or groupBy"))
		return
	}
	node, err := expr.Parse(prop)
	if err != nil {
		sb.WriteString(errorBox(err.Error()))
		return
	}
	colsOrder := []string{}
	byCol := map[string][]*rowEnv{}
	for _, env := range kept {
		v, _ := expr.Eval(node, env)
		k := orUntitled(v.Text())
		if _, ok := byCol[k]; !ok {
			colsOrder = append(colsOrder, k)
		}
		byCol[k] = append(byCol[k], env)
	}
	sort.Strings(colsOrder)
	sb.WriteString(`<div class="base-board">`)
	for _, cname := range colsOrder {
		fmt.Fprintf(sb, `<div class="base-board-col"><h4>%s <span class="count">%d</span></h4>`,
			template.HTMLEscapeString(cname), len(byCol[cname]))
		for _, env := range byCol[cname] {
			fmt.Fprintf(sb, `<a class="base-board-card internal" href="%s">%s</a>`,
				template.HTMLEscapeString(ctx.BaseURL+env.row.Slug),
				template.HTMLEscapeString(env.row.Title))
		}
		sb.WriteString(`</div>`)
	}
	sb.WriteString(`</div>`)
}

// editableKey reports whether a column ref is a plain note.<key> property
// on a notes-source base — the cells whose values map 1:1 to a frontmatter
// key and can be edited in place.
func (b *Base) editableKey(ref string) (string, bool) {
	if b.Source != "" && b.Source != "notes" {
		return "", false
	}
	key, ok := strings.CutPrefix(ref, "note.")
	if !ok || key == "" || key == "title" {
		return "", false
	}
	for _, r := range key {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return "", false
		}
	}
	return key, true
}

func (b *Base) cellHTML(c column, env *rowEnv, ctx RenderContext) string {
	if c.err != nil {
		return cellError(c.err)
	}
	v, err := expr.Eval(c.node, env)
	if err != nil {
		return cellError(err)
	}
	text := v.Text()
	if c.isTitle {
		return `<a class="internal" href="` + template.HTMLEscapeString(ctx.BaseURL+env.row.Slug) +
			`">` + template.HTMLEscapeString(orUntitled(text)) + `</a>`
	}
	return template.HTMLEscapeString(text)
}

func cellError(err error) string {
	return `<span class="cell-error" title="` + template.HTMLEscapeString(err.Error()) + `">⚠︎</span>`
}

func errorBox(msg string) string {
	return `<div class="callout" data-callout="warning"><div class="callout-title">Base error</div><div class="callout-content"><p>` +
		template.HTMLEscapeString(msg) + `</p></div></div>`
}

func orUntitled(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// compileFilters pre-parses leaf expressions so parse errors surface once.
func compileFilters(f *FilterNode) error {
	if f == nil {
		return nil
	}
	if f.Expr != "" {
		if _, err := expr.Parse(f.Expr); err != nil {
			return fmt.Errorf("filter %q: %w", f.Expr, err)
		}
		return nil
	}
	for _, c := range f.And {
		if err := compileFilters(c); err != nil {
			return err
		}
	}
	for _, c := range f.Or {
		if err := compileFilters(c); err != nil {
			return err
		}
	}
	return compileFilters(f.Not)
}
