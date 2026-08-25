package tui

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/base698/amythest/internal/apiclient"
)

// notesView is the note-finding screen. Search-first, exactly as it has
// always been: type to search the vault (server FTS), enter opens the note.
// Tab (outside the input) flips to a Browse layer — folder tree, Recent /
// Untagged / Tags virtual folders, clin-style t:/f: filters — and a preview
// pane renders the selected note beside either list on wide terminals.
type notesView struct {
	client  *apiclient.Client
	input   textinput.Model
	typing  bool
	results []apiclient.SearchResult
	cursor  int
	busy    bool

	browsing bool
	browse   browseState
	bfilter  textinput.Model
	bTyping  bool

	preview previewState
	now     func() time.Time
}

type notesFoundMsg struct {
	query   string
	results []apiclient.SearchResult
}

type browseLoadedMsg struct {
	notes []apiclient.NoteEntry
	index map[string]apiclient.ContentEntry
}

// previewState debounces and caches note previews (clin's settle-debounce
// idea: rapid cursor movement never thrashes the network).
type previewState struct {
	on    bool
	slug  string
	note  *apiclient.Note
	cache map[string]*apiclient.Note
	order []string
	gen   int
}

type previewTickMsg struct{ gen int }
type previewLoadedMsg struct {
	slug string
	note *apiclient.Note
}

const previewCacheCap = 32
const previewMinWidth = 110

func newNotesView(client *apiclient.Client) *notesView {
	ti := textinput.New()
	ti.Prompt = "search notes: "
	ti.Placeholder = "type to search · tab to browse"
	ti.CharLimit = 200
	bf := textinput.New()
	bf.Prompt = "filter (t:tag f:folder text): "
	bf.CharLimit = 120
	return &notesView{
		client:  client,
		input:   ti,
		typing:  true,
		bfilter: bf,
		preview: previewState{on: true, cache: map[string]*apiclient.Note{}},
		now:     time.Now,
	}
}

func (v *notesView) Title() string {
	if v.browsing {
		return "notes · browse"
	}
	return "notes"
}
func (v *notesView) Busy() bool      { return v.busy }
func (v *notesView) Capturing() bool { return v.typing || v.bTyping }

func (v *notesView) Init() tea.Cmd { return v.input.Focus() }

func (v *notesView) searchCmd(query string) tea.Cmd {
	client := v.client
	return func() tea.Msg {
		results, err := client.SearchNotes(context.Background(), query)
		if err != nil {
			return fail(err)
		}
		return notesFoundMsg{query: query, results: results}
	}
}

func (v *notesView) browseLoadCmd() tea.Cmd {
	client := v.client
	return func() tea.Msg {
		ctx := context.Background()
		notes, err := client.ListNotes(ctx)
		if err != nil {
			return fail(err)
		}
		index, err := client.ContentIndex(ctx)
		if err != nil {
			return fail(err)
		}
		return browseLoadedMsg{notes: notes, index: index}
	}
}

// selectedSlug is whichever note the cursor is on in the active layer.
func (v *notesView) selectedSlug() string {
	if v.browsing {
		if v.browse.cursor < len(v.browse.rows) {
			return v.browse.rows[v.browse.cursor].slug
		}
		return ""
	}
	if v.cursor < len(v.results) {
		return v.results[v.cursor].Slug
	}
	return ""
}

// schedulePreview arms the settle debounce for the current selection.
func (v *notesView) schedulePreview() tea.Cmd {
	if !v.preview.on {
		return nil
	}
	slug := v.selectedSlug()
	if slug == "" || slug == v.preview.slug {
		return nil
	}
	if note, ok := v.preview.cache[slug]; ok {
		v.preview.slug, v.preview.note = slug, note
		return nil
	}
	v.preview.gen++
	gen := v.preview.gen
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return previewTickMsg{gen} })
}

func (v *notesView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case notesFoundMsg:
		v.busy = false
		v.results = msg.results
		v.cursor = 0
		return v, v.schedulePreview()

	case browseLoadedMsg:
		v.busy = false
		v.browse.notes = msg.notes
		v.browse.index = msg.index
		v.browse.loaded = true
		v.browse.rebuild(v.now())
		return v, v.schedulePreview()

	case previewTickMsg:
		if msg.gen != v.preview.gen {
			return v, nil // superseded by later movement
		}
		slug := v.selectedSlug()
		if slug == "" || slug == v.preview.slug {
			return v, nil
		}
		client := v.client
		return v, func() tea.Msg {
			note, err := client.GetNote(context.Background(), slug)
			if err != nil {
				return previewLoadedMsg{slug: slug} // blank preview, no error spam
			}
			return previewLoadedMsg{slug: slug, note: note}
		}

	case previewLoadedMsg:
		if msg.note != nil {
			v.preview.cache[msg.slug] = msg.note
			v.preview.order = append(v.preview.order, msg.slug)
			if len(v.preview.order) > previewCacheCap {
				delete(v.preview.cache, v.preview.order[0])
				v.preview.order = v.preview.order[1:]
			}
		}
		if msg.slug == v.selectedSlug() {
			v.preview.slug, v.preview.note = msg.slug, msg.note
		}
		return v, nil

	case openNoteMsg:
		v.busy = false
		return v, pushView(newNoteView(v.client, msg.note))

	case errMsg:
		v.busy = false
		return v, nil

	case tea.KeyMsg:
		if v.browsing {
			return v.updateBrowse(msg)
		}
		return v.updateSearch(msg)
	}
	return v, nil
}

// updateSearch is the original search-mode behavior, plus tab→browse and the
// preview hooks.
func (v *notesView) updateSearch(msg tea.KeyMsg) (view, tea.Cmd) {
	if v.typing {
		switch msg.Type {
		case tea.KeyTab:
			// Tab means "flip to browse" everywhere in the notes view —
			// including from inside the search input, where it would
			// otherwise be swallowed and force an esc first.
			v.typing = false
			v.input.Blur()
			return v.enterBrowse()
		case tea.KeyEsc:
			v.typing = false
			v.input.Blur()
			return v, nil
		case tea.KeyEnter:
			query := strings.TrimSpace(v.input.Value())
			if query == "" {
				return v, nil
			}
			v.typing = false
			v.input.Blur()
			v.busy = true
			return v, v.searchCmd(query)
		}
		var cmd tea.Cmd
		v.input, cmd = v.input.Update(msg)
		return v, cmd
	}
	switch msg.String() {
	case "tab":
		return v.enterBrowse()
	case "j", "down":
		if v.cursor < len(v.results)-1 {
			v.cursor++
		}
		return v, v.schedulePreview()
	case "k", "up":
		if v.cursor > 0 {
			v.cursor--
		}
		return v, v.schedulePreview()
	case "p":
		v.preview.on = !v.preview.on
		return v, v.schedulePreview()
	case "/", "i":
		v.typing = true
		return v, v.input.Focus()
	case "enter":
		if v.cursor < len(v.results) {
			v.busy = true
			return v, openNoteCmd(v.client, v.results[v.cursor].Slug)
		}
	}
	return v, nil
}

// enterBrowse flips to the browse layer, loading the vault listing once.
func (v *notesView) enterBrowse() (view, tea.Cmd) {
	v.browsing = true
	if !v.browse.loaded {
		v.busy = true
		return v, v.browseLoadCmd()
	}
	return v, v.schedulePreview()
}

func (v *notesView) updateBrowse(msg tea.KeyMsg) (view, tea.Cmd) {
	if v.bTyping {
		switch msg.Type {
		case tea.KeyTab:
			// Symmetric: tab from the filter input flips back to search.
			v.bTyping = false
			v.bfilter.Blur()
			v.browsing = false
			return v, v.schedulePreview()
		case tea.KeyEsc:
			v.bTyping = false
			v.bfilter.Blur()
			v.bfilter.SetValue("")
			v.browse.filter = ""
			v.browse.rebuild(v.now())
			return v, nil
		case tea.KeyEnter:
			v.bTyping = false
			v.bfilter.Blur()
			return v, nil
		}
		var cmd tea.Cmd
		v.bfilter, cmd = v.bfilter.Update(msg)
		filter := strings.TrimSpace(v.bfilter.Value())
		// g: falls through to server FTS in search mode.
		if strings.HasPrefix(filter, "g:") {
			return v, cmd
		}
		v.browse.filter = filter
		v.browse.rebuild(v.now())
		return v, tea.Batch(cmd, v.schedulePreview())
	}
	b := &v.browse
	switch msg.String() {
	case "tab", "esc":
		if b.filter != "" && msg.String() == "esc" {
			b.filter = ""
			v.bfilter.SetValue("")
			b.rebuild(v.now())
			return v, nil
		}
		v.browsing = false
		if len(v.results) == 0 {
			// Nothing to land on — put the cursor back in the search box.
			v.typing = true
			return v, v.input.Focus()
		}
		return v, v.schedulePreview()
	case "j", "down":
		if b.cursor < len(b.rows)-1 {
			b.cursor++
		}
		return v, v.schedulePreview()
	case "k", "up":
		if b.cursor > 0 {
			b.cursor--
		}
		return v, v.schedulePreview()
	case "l", "enter":
		if b.cursor >= len(b.rows) {
			return v, nil
		}
		row := b.rows[b.cursor]
		if row.slug != "" {
			v.busy = true
			return v, openNoteCmd(v.client, row.slug)
		}
		b.expanded[row.folder] = !b.expanded[row.folder]
		b.rebuild(v.now())
	case "h":
		if b.cursor >= len(b.rows) {
			return v, nil
		}
		row := b.rows[b.cursor]
		if row.folder != "" && b.expanded[row.folder] {
			b.expanded[row.folder] = false
			b.rebuild(v.now())
			return v, nil
		}
		// Jump to the parent row.
		for i := b.cursor - 1; i >= 0; i-- {
			if b.rows[i].folder != "" && b.rows[i].depth < row.depth {
				b.cursor = i
				break
			}
		}
		return v, v.schedulePreview()
	case "s":
		b.sortByTitle = !b.sortByTitle
		b.rebuild(v.now())
	case "p":
		v.preview.on = !v.preview.on
		return v, v.schedulePreview()
	case "r":
		v.busy = true
		return v, v.browseLoadCmd()
	case "/":
		v.bTyping = true
		return v, v.bfilter.Focus()
	}
	if handled, cmd := v.maybeFTSFallthrough(); handled {
		return v, cmd
	}
	return v, nil
}

// maybeFTSFallthrough runs a committed g: filter through server FTS.
func (v *notesView) maybeFTSFallthrough() (bool, tea.Cmd) {
	filter := strings.TrimSpace(v.bfilter.Value())
	if !strings.HasPrefix(filter, "g:") {
		return false, nil
	}
	query := strings.TrimSpace(strings.TrimPrefix(filter, "g:"))
	if query == "" {
		return false, nil
	}
	v.browsing = false
	v.bfilter.SetValue("")
	v.browse.filter = ""
	v.input.SetValue(query)
	v.busy = true
	return true, v.searchCmd(query)
}

var tagRe = regexp.MustCompile(`</?b>`)

func (v *notesView) View(width, height int) string {
	listWidth := width
	showPreview := v.preview.on && width >= previewMinWidth
	if showPreview {
		listWidth = width * 55 / 100
	}
	var list string
	if v.browsing {
		list = v.browseView(listWidth, height)
	} else {
		list = v.searchView(listWidth, height)
	}
	if !showPreview {
		return list
	}
	previewWidth := width - listWidth - 3
	return lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(listWidth).Render(list),
		" │ ",
		v.previewPane(previewWidth, height),
	)
}

func (v *notesView) searchView(width, height int) string {
	var b strings.Builder
	b.WriteString(" " + v.input.View() + "\n\n")
	if v.busy {
		b.WriteString("  searching…\n")
	}
	if !v.typing && !v.busy && len(v.results) == 0 && v.input.Value() != "" {
		b.WriteString("  no notes matched\n")
	}
	for i, result := range v.results {
		prefix := "   "
		title := result.Title
		if i == v.cursor && !v.typing {
			prefix = cursorStyle.Render(" ▸ ")
			title = cursorStyle.Render(title)
		}
		if result.Archived {
			title += dimStyle.Render(" (archived)")
		}
		b.WriteString(prefix + title + "  " + dimStyle.Render(result.Slug) + "\n")
		excerpt := htmlUnescape(tagRe.ReplaceAllString(result.Excerpt, ""))
		for _, row := range wrapLine("     "+excerpt, max(30, width-4)) {
			b.WriteString(dimStyle.Render(row) + "\n")
		}
	}
	if !v.typing {
		b.WriteString("\n" + dimStyle.Render(" enter open · tab browse · p preview · / new search · esc back"))
	}
	return b.String()
}

func (v *notesView) browseView(width, height int) string {
	b := &v.browse
	var out strings.Builder
	if !b.loaded {
		return "\n  loading vault…"
	}
	sortLabel := "modified"
	if b.sortByTitle {
		sortLabel = "title"
	}
	out.WriteString(" " + groupHeaderStyle.Render("Browse") + "  " + dimStyle.Render("sort: "+sortLabel) + "\n")
	rowsAvail := max(3, height-4)
	if b.cursor < b.offset {
		b.offset = b.cursor
	}
	if b.cursor >= b.offset+rowsAvail {
		b.offset = b.cursor - rowsAvail + 1
	}
	end := min(len(b.rows), b.offset+rowsAvail)
	for i := b.offset; i < end; i++ {
		row := b.rows[i]
		indent := strings.Repeat("  ", row.depth)
		label := row.label
		if row.kind != "note" {
			label = columnTitleStyle.Render(label)
			if row.count > 0 {
				label += dimStyle.Render(" (" + itoa(row.count) + ")")
			}
			marker := "▸"
			if b.expanded[row.folder] {
				marker = "▾"
			}
			label = marker + " " + label
		}
		prefix := "  "
		if i == b.cursor {
			prefix = cursorStyle.Render("▸ ")
			if row.kind == "note" {
				label = cursorStyle.Render(label)
			}
		}
		out.WriteString(" " + prefix + indent + label + "\n")
	}
	hint := " enter open/expand · h/l fold · s sort · / filter · p preview · tab search"
	if v.bTyping || b.filter != "" {
		hint = " " + v.bfilter.View()
	}
	out.WriteString(dimStyle.Render(hint))
	return out.String()
}

func (v *notesView) previewPane(width, height int) string {
	if v.preview.note == nil {
		return dimStyle.Render("\n  (preview)")
	}
	note := v.preview.note
	var b strings.Builder
	b.WriteString(columnTitleStyle.Render(note.Title) + "\n" + dimStyle.Render(note.Path) + "\n\n")
	lines := strings.Split(strings.TrimRight(note.Markdown, "\n"), "\n")
	rendered := 0
	budget := height - 4
	for _, line := range lines {
		for _, row := range wrapLine(line, max(20, width-2)) {
			if rendered >= budget {
				return b.String()
			}
			b.WriteString(row + "\n")
			rendered++
		}
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func htmlUnescape(s string) string {
	replacer := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&#34;", `"`, "&#39;", "'", "&quot;", `"`)
	return replacer.Replace(s)
}

func openNoteCmd(client *apiclient.Client, ref string) tea.Cmd {
	return func() tea.Msg {
		note, err := client.GetNote(context.Background(), ref)
		if err != nil {
			return fail(err)
		}
		return openNoteMsg{note}
	}
}

type openNoteMsg struct{ note *apiclient.Note }
