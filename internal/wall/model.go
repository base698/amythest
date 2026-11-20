package wall

import (
	"context"
	"fmt"
	"image"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/base698/amythest/internal/apiclient"
)

// API is what the wall reads from the server.
type API interface {
	WorkOverview(ctx context.Context) (*apiclient.WorkSnapshot, error)
	WorkProject(ctx context.Context, project string) (*apiclient.WorkSnapshot, error)
	WorkMedia(ctx context.Context, hash string, limit int64) ([]byte, error)
	CardTitles(ctx context.Context, ids []string) (map[string]string, error)
	CanvasList(ctx context.Context, workspace string) ([]apiclient.CanvasMeta, error)
	CanvasDrawing(ctx context.Context, id, kind string) ([]byte, error)
	Endpoint() string
}

// Options configure a wall.
type Options struct {
	// Share opens the message reply picker with a snapshot of the selected item.
	Share func(Item) tea.Cmd

	Project    string        // show this project; empty: the current herdr workspace
	Graphics   string        // "kitty", "blocks" or "off"
	Refresh    time.Duration // how often to look for new items
	Out        io.Writer     // where image uploads go; must be serialized with the screen
	CellW      int           // a cell's width in pixels, 0 if unknown
	CellAspect float64       // a cell's width / height
	Locate     func() Place
	Open       func(url string) error
	OpenFile   func(FileTarget) tea.Cmd
	OpenCanvas func(id string) error
	// Messages lists the messages tagged to a project in Messages; nil when
	// messaging is not set up. OpenMessage shows one in Messages.
	Messages        func(ctx context.Context, project string) ([]MessageItem, error)
	OpenMessage     func(conversation, message string) error
	CanvasWorkspace string // current Herdr workspace when it differs from the Work project
	Now             func() time.Time
	// Show opens the wall on one item, enlarged (an image) or as its diff (a
	// commit), for callers that already know what the person wants to see.
	Show *Want
}

// Want names an item of the feed by what a search result knows about it.
type Want struct {
	Kind  string
	Title string
	Ref   string // a commit's id, when known
}

func (w Want) matches(it Item) bool {
	if w.Kind != "" && w.Kind != it.Art.Kind && w.Kind != it.Kind {
		return false
	}
	if w.Ref != "" && it.Art.Ref != "" && (w.Ref == it.Art.Ref || strings.HasPrefix(it.Art.Ref, w.Ref)) {
		return true
	}
	return w.Title != "" && (w.Title == it.Title || w.Title == it.Art.Title || w.Title == refName(it.Art))
}

const (
	maxImageBytes = 25 << 20
	maxDiffBytes  = 2 << 20
	maxSourceSide = 1600 // decoded images are reduced to this before being kept
	maxLoaded     = 30   // images kept decoded and uploaded at once
	maxFetching   = 3
)

type pic struct {
	state int // 0 not loaded, 1 loading, 2 ready, 3 failed
	src   image.Image
	err   string
	// Kitty uploads of this image, by box size. The feed and the enlarged view
	// use different boxes.
	placed map[[2]int]int
	blocks map[[2]int][]string
}

const (
	picNone = iota
	picLoading
	picReady
	picFailed
)

type ShareDone struct{ Err error }

// FileTarget opens the working copy of a diff's file on this computer.
type FileTarget struct {
	Repo string
	Path string
	Line int
}

// FileOpened is returned when the local editor exits (or could not open).
type FileOpened struct{ Err error }

type model struct {
	api  API
	o    Options
	w, h int

	place    Place
	project  string
	projects []string
	all      []Item
	items    []Item                  // all, filtered
	snap     *apiclient.WorkSnapshot // the project's tabs, runs and machines, for the summary
	summary  bool                    // show the summary above the feed
	cards    map[string]string       // titles of the cards tabs are assigned to
	asked    string                  // the card ids last asked for
	filter   string
	cursor   int
	top      int

	lines []string // the laid-out feed
	start []int    // first line of each item
	pics  map[string]*pic
	next  int // next Kitty image id

	view      string // "feed", "pick", "zoom" or "diff"
	help      bool
	zoom      []string
	pick      int
	diff      []string
	patch     []diffLine
	diffItem  Item
	diffTop   int
	diffLeft  int
	loading   bool
	synced    time.Time
	showing   string // media hash of an image to enlarge once it has loaded
	flash     string
	err       error
	canvasErr error
	fetching  int
}

type (
	startMsg struct {
		place    Place
		overview *apiclient.WorkSnapshot
		err      error
	}
	feedMsg struct {
		project   string
		snap      *apiclient.WorkSnapshot
		err       error
		canvases  []apiclient.CanvasMeta
		canvasErr error
		messages  []MessageItem
	}
	canvasOpenedMsg  struct{ err error }
	messageOpenedMsg struct{ err error }
	picMsg           struct {
		hash string
		img  image.Image
		err  error
	}
	diffMsg struct {
		item  Item
		title string
		text  string
		err   error
	}
	cardsMsg  struct{ titles map[string]string }
	tickMsg   struct{}
	flashMsg  struct{ text string }
	redrawMsg struct{}
)

// New returns the wall as a Bubble Tea model.
func New(api API, o Options) tea.Model {
	if o.Refresh <= 0 {
		o.Refresh = 15 * time.Second
	}
	if o.CellAspect <= 0 {
		o.CellAspect = 0.5
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	return &model{api: api, o: o, filter: "all", view: "feed", pics: map[string]*pic{}, next: 1, loading: true, summary: true, cards: map[string]string{}}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.startCmd(), tea.Tick(m.o.Refresh, func(time.Time) tea.Msg { return tickMsg{} }))
}

func (m *model) startCmd() tea.Cmd {
	return func() tea.Msg {
		var p Place
		if m.o.Project == "" && m.o.Locate != nil {
			p = m.o.Locate()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		o, err := m.api.WorkOverview(ctx)
		return startMsg{place: p, overview: o, err: err}
	}
}

func (m *model) feedCmd(project string) tea.Cmd {
	workspace := project
	if project == m.o.Project && m.o.CanvasWorkspace != "" {
		workspace = m.o.CanvasWorkspace
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		s, err := m.api.WorkProject(ctx, project)
		canvases, canvasErr := m.api.CanvasList(ctx, workspace)
		var msgs []MessageItem
		if m.o.Messages != nil {
			// Tagged under the project, or under the workspace's own name.
			seen := map[string]bool{}
			for _, name := range []string{project, workspace} {
				if seen[name] {
					continue
				}
				seen[name] = true
				got, _ := m.o.Messages(ctx, name) // Messages being away never blocks the wall
				msgs = append(msgs, got...)
			}
		}
		return feedMsg{project: project, snap: s, err: err, canvases: canvases, canvasErr: canvasErr, messages: msgs}
	}
}

func (m *model) picCmd(it Item) tea.Cmd {
	hash := it.pictureKey()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var b []byte
		var err error
		if it.Canvas != nil {
			b, err = m.api.CanvasDrawing(ctx, it.Canvas.ID, "png")
		} else {
			b, err = m.api.WorkMedia(ctx, hash, maxImageBytes)
		}
		if err != nil {
			return picMsg{hash: hash, err: err}
		}
		img, err := decode(b)
		if err != nil {
			return picMsg{hash: hash, err: fmt.Errorf("not an image this terminal can draw")}
		}
		if b := img.Bounds(); b.Dx() > maxSourceSide || b.Dy() > maxSourceSide {
			w, h := b.Dx(), b.Dy()
			if w >= h {
				w, h = maxSourceSide, max(1, h*maxSourceSide/w)
			} else {
				w, h = max(1, w*maxSourceSide/h), maxSourceSide
			}
			img = shrink(img, w, h)
		}
		return picMsg{hash: hash, img: img}
	}
}

// cardsCmd looks up the titles of the cards this project's tabs are assigned
// to, once per set of cards.
func (m *model) cardsCmd() tea.Cmd {
	var ids []string
	for _, x := range m.snap.Slots {
		if x.Binding != nil && x.Status != "gone" && m.cards[x.Binding.Card] == "" {
			ids = append(ids, x.Binding.Card)
		}
	}
	// Ask once per card: one that is not on an open board never gets a title.
	fresh := ids[:0]
	for _, id := range ids {
		if !strings.Contains(","+m.asked+",", ","+id+",") {
			fresh = append(fresh, id)
			m.asked += "," + id
		}
	}
	if ids = fresh; len(ids) == 0 {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		titles, _ := m.api.CardTitles(ctx, ids) // without titles the tabs show the card ids
		return cardsMsg{titles}
	}
}

func (m *model) diffCmd(it Item) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		b, err := m.api.WorkMedia(ctx, it.Art.MediaHash, maxDiffBytes)
		return diffMsg{item: it, title: shortRef(it.Art.Ref) + "  " + it.Title, text: string(b), err: err}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case FileOpened:
		if msg.Err != nil {
			return m, m.say("open file: " + msg.Err.Error())
		}
		return m, nil
	case redrawMsg:
		return m, m.relayout()
	case ShareDone:
		if msg.Err != nil {
			return m, m.say("share: " + msg.Err.Error())
		}
		return m, m.relayout()
	case messageOpenedMsg:
		if msg.err != nil {
			return m, m.say("Messages: " + msg.err.Error())
		}
		return m, m.say("opened in Messages")
	case canvasOpenedMsg:
		if msg.err != nil {
			return m, m.say("canvas: " + msg.err.Error())
		}
		return m, m.say("opened in Herdr")
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		if m.view == "diff" {
			m.diffTop = min(m.diffTop, max(0, len(m.diff)-1))
			return m, nil // preserve the split feed's layout and scroll position
		}
		return m, m.relayout()
	case startMsg:
		if msg.err != nil {
			m.err, m.loading = msg.err, false
			return m, nil
		}
		m.place, m.projects = msg.place, msg.overview.Projects
		m.project = m.o.Project
		if m.project == "" {
			m.project = projectFor(msg.place, msg.overview)
		}
		if m.project == "" {
			m.view, m.loading = "pick", false
			return m, nil
		}
		return m, m.feedCmd(m.project)
	case feedMsg:
		if msg.project != m.project {
			return m, nil // an answer for a project since switched away from
		}
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err, m.synced = nil, m.o.Now()
		if len(msg.snap.Projects) > 0 {
			m.projects = msg.snap.Projects
		}
		keep := ""
		if m.cursor < len(m.items) {
			keep = m.items[m.cursor].Art.ID
		}
		m.canvasErr = msg.canvasErr
		m.all, m.snap = withMessages(withCanvases(buildFeed(msg.snap), msg.canvases), msg.messages), msg.snap
		m.prunePictures()
		m.applyFilter(keep)
		titles := m.cardsCmd()
		if want := m.o.Show; want != nil {
			m.o.Show = nil // only on the first load
			return m, tea.Batch(m.show(*want), titles)
		}
		return m, tea.Batch(m.relayout(), titles)
	case cardsMsg:
		for id, title := range msg.titles {
			m.cards[id] = title
		}
		return m, m.relayout()
	case picMsg:
		m.fetching--
		p := m.pics[msg.hash]
		if p == nil {
			return m, nil
		}
		if msg.err != nil {
			p.state, p.err = picFailed, msg.err.Error()
		} else {
			p.state, p.src = picReady, msg.img
		}
		if m.showing == msg.hash {
			m.showing = ""
			if p.state == picReady && m.view == "feed" && m.cursor < len(m.items) && m.items[m.cursor].Art.MediaHash == msg.hash {
				m.view = "zoom"
			}
		}
		return m, m.relayout()
	case diffMsg:
		if msg.err != nil {
			return m, m.say("no diff: " + msg.err.Error())
		}
		m.diff, m.diffTop, m.view = append([]string{msg.title, ""}, strings.Split(strings.ReplaceAll(msg.text, "\t", "    "), "\n")...), 0, "diff"
		m.patch, m.diffItem, m.diffLeft = parseDiff(m.diff), msg.item, 0
		return m, nil
	case tickMsg:
		next := tea.Tick(m.o.Refresh, func(time.Time) tea.Msg { return tickMsg{} })
		if m.project == "" {
			return m, next
		}
		return m, tea.Batch(next, m.feedCmd(m.project))
	case flashMsg:
		if m.flash == msg.text {
			m.flash = ""
		}
		return m, nil
	case tea.MouseMsg:
		if m.view == "feed" && msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			return m, m.selectAt(msg.X, msg.Y)
		}
		if m.view == "feed" || m.view == "diff" {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				return m, m.scroll(-3)
			case tea.MouseButtonWheelDown:
				return m, m.scroll(3)
			}
		}
		return m, nil
	case tea.KeyMsg:
		// Keys pressed quickly can arrive as one message of several runes.
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste {
			var cmds []tea.Cmd
			for _, r := range msg.Runes {
				_, c := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				cmds = append(cmds, c)
			}
			return m, tea.Batch(cmds...)
		}
		return m.key(msg)
	}
	return m, nil
}

func (m *model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return m, m.quit()
	}
	if s == "?" {
		m.help = !m.help
		return m, nil
	}
	if m.help {
		if s == "esc" || s == "q" || s == "enter" {
			m.help = false
		}
		return m, nil
	}
	if m.view == "feed" || m.view == "diff" {
		if delta, ok := wallScroll(s, m.body()); ok {
			return m, m.scroll(delta)
		}
	}
	if s == "S" && m.o.Share != nil && m.view != "pick" && m.cursor < len(m.items) {
		if m.items[m.cursor].Canvas != nil {
			return m, m.say("Open the canvas and use Copy PNG to share it")
		}
		return m, m.o.Share(m.items[m.cursor])
	}
	switch m.view {
	case "pick":
		switch s {
		case "q", "esc":
			if m.project == "" {
				return m, m.quit()
			}
			m.view = "feed"
		case "j", "down":
			m.pick = min(m.pick+1, len(m.projects)-1)
		case "k", "up":
			m.pick = max(m.pick-1, 0)
		case "enter":
			if m.pick < len(m.projects) {
				m.project, m.view, m.loading = m.projects[m.pick], "feed", true
				m.all, m.items, m.cursor, m.top = nil, nil, 0, 0
				return m, tea.Batch(m.relayout(), m.feedCmd(m.project))
			}
		}
		return m, nil
	case "diff":
		switch s {
		case "q", "esc", "enter":
			m.view, m.diff, m.patch = "feed", nil, nil
			// Let the dashboard restore the split width before laying out the
			// feed; an intermediate full-width layout could clamp its scroll.
			return m, func() tea.Msg { return redrawMsg{} }
		case "o":
			return m, m.openDiffFile()
		case "]":
			m.jumpFile(1)
		case "[":
			m.jumpFile(-1)
		case "l", "right":
			m.diffLeft += 8
		case "h", "left":
			m.diffLeft = max(0, m.diffLeft-8)
		case "0":
			m.diffLeft = 0
		case "j", "down":
			return m, m.scroll(1)
		case "k", "up":
			return m, m.scroll(-1)
		case "g", "home":
			m.diffTop = 0
		case "G", "end":
			m.diffTop = max(0, len(m.diff)-m.body())
		}
		return m, nil
	case "zoom":
		switch s {
		case "q", "esc", "enter":
			m.view = "feed"
			return m, m.relayout()
		case "o":
			return m, m.open()
		}
		return m, nil
	}
	switch s {
	case "q", "esc":
		return m, m.quit()
	case "j", "down":
		return m, m.move(m.columns())
	case "k", "up":
		return m, m.move(-m.columns())
	case "l", "right":
		return m, m.move(1)
	case "h", "left":
		return m, m.move(-1)
	case "s":
		m.summary = !m.summary
		m.top = 0
		return m, m.relayout()
	case "g", "home":
		m.cursor = 0
		return m, m.reveal()
	case "G", "end":
		m.cursor = max(0, len(m.items)-1)
		return m, m.reveal()
	case "f", "c":
		next := map[string]string{"all": "media", "media": "code", "code": "canvas", "canvas": "messages", "messages": "all"}
		wasCanvas := m.filter == "canvas"
		keep := ""
		if m.cursor < len(m.items) {
			keep = m.items[m.cursor].Art.ID
		}
		m.filter = next[m.filter]
		if m.filter == "messages" && m.o.Messages == nil {
			m.filter = "all" // no Messages here, so nothing to filter for
		}
		if s == "c" {
			m.filter = "canvas"
			if wasCanvas {
				m.filter = "all"
			}
		}
		m.applyFilter(keep)
		return m, m.relayout()
	case "p":
		m.view, m.pick = "pick", 0
		for i, p := range m.projects {
			if p == m.project {
				m.pick = i
			}
		}
		return m, nil
	case "r":
		m.loading = true
		return m, m.feedCmd(m.project)
	case "o":
		return m, m.open()
	case "enter":
		if m.cursor >= len(m.items) {
			return m, nil
		}
		it := m.items[m.cursor]
		switch {
		case it.Msg != nil:
			if m.o.OpenMessage == nil {
				return m, m.say("Messages is not set up here")
			}
			return m, func() tea.Msg { return messageOpenedMsg{m.o.OpenMessage(it.Msg.Conversation, it.Msg.ID)} }
		case it.Canvas != nil:
			if m.o.OpenCanvas == nil {
				return m, m.say("press o to open this canvas in the browser")
			}
			return m, func() tea.Msg { return canvasOpenedMsg{m.o.OpenCanvas(it.Canvas.ID)} }
		case it.Kind == "commit" && it.Art.MediaHash != "" && it.Art.Available:
			return m, m.diffCmd(it)
		case it.Kind == "image" && m.picOf(it) != nil && m.picOf(it).state == picReady:
			m.view = "zoom"
			return m, m.relayout()
		}
		return m, m.open()
	}
	return m, nil
}

// Paging scrolls screen rows, so large previews can be read without jumping
// to the next item. j/k keep selecting items (or lines inside a diff).
func wallScroll(key string, height int) (int, bool) {
	switch key {
	case "ctrl+d":
		return max(1, height/2), true
	case "ctrl+u":
		return -max(1, height/2), true
	case "ctrl+f", "pgdown", " ":
		return height, true
	case "ctrl+b", "pgup", "b":
		return -height, true
	case "ctrl+e":
		return 1, true
	case "ctrl+y":
		return -1, true
	}
	return 0, false
}

// show selects the wanted item and opens it: a commit's diff, an image
// enlarged as soon as it has loaded. Anything else is just selected.
func (m *model) show(want Want) tea.Cmd {
	found := -1
	for i, it := range m.items {
		if want.matches(it) {
			found = i
			break
		}
	}
	if found < 0 {
		return tea.Batch(m.relayout(), m.say("that item is older than this feed shows"))
	}
	m.cursor = found
	it := m.items[found]
	cmds := []tea.Cmd{m.reveal()}
	switch {
	case it.Kind == "commit" && it.Art.MediaHash != "" && it.Art.Available:
		cmds = append(cmds, m.diffCmd(it))
	case it.Kind == "image":
		if p := m.picOf(it); p != nil {
			if p.state == picReady {
				m.view = "zoom"
				cmds = append(cmds, m.relayout())
			} else {
				m.showing = it.Art.MediaHash
			}
		}
	}
	return tea.Batch(cmds...)
}

func (m *model) open() tea.Cmd {
	if m.cursor >= len(m.items) {
		return nil
	}
	url := m.items[m.cursor].webURL(m.api.Endpoint())
	if url == "" {
		return m.say("nothing to open for this item")
	}
	if m.o.Open == nil {
		return m.say(url)
	}
	if err := m.o.Open(url); err != nil {
		return m.say(url)
	}
	return m.say("opened in browser")
}

func (m *model) say(text string) tea.Cmd {
	m.flash = text
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return flashMsg{text} })
}

// Close frees uploaded images, also when an embedding dashboard replaces the
// wall or exits from a different panel. Repeated closes are harmless.
func (m *model) Close() error {
	var b strings.Builder
	for _, p := range m.pics {
		for _, id := range p.placed {
			b.WriteString(kittyDelete(id))
		}
		clear(p.placed)
	}
	if b.Len() > 0 {
		_, err := io.WriteString(m.o.Out, b.String())
		return err
	}
	return nil
}

func (m *model) quit() tea.Cmd {
	m.Close()
	return tea.Quit
}

func (m *model) applyFilter(keepID string) {
	m.items = m.items[:0:0]
	for _, it := range m.all {
		if it.matches(m.filter) {
			m.items = append(m.items, it)
		}
	}
	m.cursor = 0
	for i, it := range m.items {
		if it.Art.ID == keepID {
			m.cursor = i
		}
	}
}

// body is the number of lines between the header and the footer.
func (m *model) body() int { return max(1, m.h-3) }

func (m *model) move(d int) tea.Cmd {
	m.cursor = max(0, min(m.cursor+d, len(m.items)-1))
	return m.reveal()
}

func (m *model) selectAt(x, y int) tea.Cmd {
	if x < 0 || x >= m.w || y < 2 || y >= 2+m.body() {
		return nil
	}
	line := m.top + y - 2
	columns := m.columns()
	for row := 0; row < len(m.start); row += columns {
		first, last := m.span(row)
		if line < first || line >= last {
			continue
		}
		col := 0
		if columns > 1 {
			tileWidth := (m.w - 2 - 2*(columns-1)) / columns
			col = max(0, min(columns-1, (x-2)/(tileWidth+2)))
		}
		m.cursor = min(row+col, len(m.items)-1)
		return m.relayout()
	}
	return nil
}

// span is the lines an item occupies: in the gallery, its whole row of tiles.
func (m *model) span(i int) (first, last int) {
	first, last = m.start[i], len(m.lines)
	for j := i + 1; j < len(m.start); j++ {
		if m.start[j] > first {
			last = m.start[j]
			break
		}
	}
	return first, last
}

// reveal scrolls so the selected item is on screen, then loads what is near.
// On the first item (or row) the view returns to the very top, where the
// summary is.
func (m *model) reveal() tea.Cmd {
	if m.cursor < len(m.start) {
		first, last := m.span(m.cursor)
		switch {
		case first == m.start[0]:
			m.top = 0
		case first < m.top || last-first >= m.body():
			m.top = first
		case last > m.top+m.body():
			m.top = last - m.body()
		}
	}
	return m.relayout()
}

func (m *model) scroll(d int) tea.Cmd {
	if m.view == "diff" {
		m.diffTop = max(0, min(m.diffTop+d, len(m.diff)-1))
		return nil
	}
	m.top = max(0, min(m.top+d, len(m.lines)-m.body()))
	// Keep the selection inside what is shown.
	for m.cursor+1 < len(m.start) && m.start[m.cursor+1] <= m.top {
		m.cursor++
	}
	for m.cursor > 0 && m.start[m.cursor] >= m.top+m.body() {
		m.cursor--
	}
	return m.relayout()
}

func (m *model) picOf(it Item) *pic {
	key := it.pictureKey()
	if key == "" {
		return nil
	}
	p := m.pics[key]
	if p == nil {
		p = &pic{placed: map[[2]int]int{}, blocks: map[[2]int][]string{}}
		m.pics[key] = p
	}
	return p
}

// Drop deleted items and superseded canvas revisions, including their terminal
// uploads. Late downloads are ignored because their cache key no longer exists.
func (m *model) prunePictures() {
	keep := make(map[string]bool, len(m.all))
	for _, it := range m.all {
		keep[it.pictureKey()] = true
	}
	for key, p := range m.pics {
		if keep[key] {
			continue
		}
		for _, id := range p.placed {
			io.WriteString(m.o.Out, kittyDelete(id))
		}
		delete(m.pics, key)
	}
}

// imageRows returns the text rows that draw a loaded image in a box of at most
// maxCols×maxRows cells, uploading it to the terminal first if needed.
func (m *model) imageRows(p *pic, maxCols, maxRows int) []string {
	b := p.src.Bounds()
	cols, rows := boxFor(b.Dx(), b.Dy(), min(maxCols, maxCells), min(maxRows, maxCells), m.o.CellAspect)
	if cols == 0 {
		return nil
	}
	box := [2]int{cols, rows}
	if m.o.Graphics != "kitty" {
		if p.blocks[box] == nil {
			clear(p.blocks) // one size at a time is enough
			p.blocks[box] = blockRows(p.src, cols, rows)
		}
		return p.blocks[box]
	}
	id, ok := p.placed[box]
	if !ok {
		// Send no more pixels than the box can show.
		cellW := m.o.CellW
		if cellW <= 0 {
			cellW = 10
		}
		w := min(b.Dx(), cols*cellW)
		data, err := encodePNG(shrink(p.src, w, max(1, w*b.Dy()/b.Dx())))
		if err != nil {
			return nil
		}
		// Keep at most the feed's and the enlarged view's upload.
		for old, oldID := range p.placed {
			if len(p.placed) < 2 {
				break
			}
			io.WriteString(m.o.Out, kittyDelete(oldID))
			delete(p.placed, old)
		}
		id = m.next
		m.next++
		io.WriteString(m.o.Out, kittyTransmit(id, cols, rows, data))
		p.placed[box] = id
	}
	return kittyRows(id, cols, rows)
}

var (
	dim    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "244", Dark: "245"})
	accent = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "25", Dark: "81"})
	strong = lipgloss.NewStyle().Bold(true)
	sha    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "179"})
	bad    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "160", Dark: "203"})
	added  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "114"})
)

// clip shortens plain text to at most w cells.
func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// relayout rebuilds the feed's lines for the current size and state and
// starts loading the images near what is shown. The selected item keeps its
// place on screen when things above it change height.
func (m *model) relayout() tea.Cmd {
	if m.w <= 0 || m.h <= 0 {
		return nil
	}
	if m.view == "diff" {
		return nil
	}
	offset, anchored := 0, m.cursor < len(m.start)
	if anchored {
		offset = m.top - m.start[m.cursor]
	}
	m.build()
	if anchored && m.cursor < len(m.start) {
		m.top = m.start[m.cursor] + offset
	}
	m.top = max(0, min(m.top, len(m.lines)-m.body()))
	cmds := m.want()
	if len(cmds) > 0 {
		m.build() // show "loading…" for the loads just started
	}
	m.zoom = nil
	if m.view == "zoom" && m.cursor < len(m.items) {
		if p := m.picOf(m.items[m.cursor]); p != nil && p.state == picReady {
			m.zoom = m.imageRows(p, m.w, m.body()-1)
		}
	}
	return tea.Batch(cmds...)
}

// columns is how many tiles the media gallery puts side by side. One means
// the plain list.
func (m *model) columns() int {
	if m.filter != "media" || m.o.Graphics == "off" {
		return 1
	}
	switch {
	case m.w >= 110:
		return 4
	case m.w >= 80:
		return 3
	case m.w >= 44:
		return 2
	}
	return 1
}

// cell is one line of a gallery tile and the number of screen cells it
// takes. Image rows are measured by construction, not by counting runes.
type cell struct {
	text string
	w    int
}

// tile is one gallery cell: the picture, then its name and where it is from.
func (m *model) tile(i, w, maxRows int, now time.Time) []cell {
	it := m.items[i]
	text := func(s string) cell { return cell{s, lipgloss.Width(s)} }
	var lines []cell
	p := m.picOf(it)
	switch {
	case p != nil && p.state == picReady:
		b := p.src.Bounds()
		cols, _ := boxFor(b.Dx(), b.Dy(), min(w, maxCells), min(maxRows, maxCells), m.o.CellAspect)
		for _, row := range m.imageRows(p, w, maxRows) {
			lines = append(lines, cell{row, cols})
		}
	case p != nil && p.state == picFailed:
		lines = append(lines, text(dim.Render(clip(p.err, w))))
	case p != nil && p.state == picLoading:
		lines = append(lines, text(dim.Render("loading…")))
	case it.Kind == "video" && it.Art.Available:
		lines = append(lines, text(dim.Render(clip("▶ video · o to play", w))))
	case !it.Art.Available:
		lines = append(lines, text(dim.Render(clip("not uploaded", w))))
	}
	name := "  " + clip(it.Title, w-2)
	if i == m.cursor {
		name = accent.Render("▌ ") + strong.Render(clip(it.Title, w-2))
	}
	from := strings.Trim(it.Tab+" · "+ago(it.When, now), " ·")
	return append(lines, text(name), text("  "+dim.Render(clip(from, w-2))))
}

// buildGrid lays the items out as rows of n tiles: the media gallery.
func (m *model) buildGrid(n int) {
	const gap = 2
	tileW := (m.w - 2 - gap*(n-1)) / n
	maxRows := max(4, min(12, m.body()-4))
	now := m.o.Now()
	for row := 0; row < len(m.items); row += n {
		first := len(m.lines)
		var tiles [][]cell
		height := 0
		for i := row; i < row+n && i < len(m.items); i++ {
			m.start = append(m.start, first)
			t := m.tile(i, tileW, maxRows, now)
			tiles = append(tiles, t)
			height = max(height, len(t))
		}
		for k := 0; k < height; k++ {
			var b strings.Builder
			b.WriteString("  ")
			for j, t := range tiles {
				used := 0
				if k < len(t) {
					b.WriteString(t[k].text)
					used = t[k].w
				}
				if j < len(tiles)-1 {
					b.WriteString(strings.Repeat(" ", max(0, tileW-used)+gap))
				}
			}
			m.lines = append(m.lines, b.String())
		}
		m.lines = append(m.lines, "")
	}
}

func (m *model) build() {
	now := m.o.Now()
	width := m.w - 2 // a two-cell gutter holds the selection mark
	m.lines, m.start = m.lines[:0], m.start[:0]
	if m.summary && m.snap != nil {
		m.lines = append(m.lines, summaryLines(m.snap, m.all, m.cards, now, m.w)...)
	}
	if m.canvasErr != nil {
		m.lines = append(m.lines, "  "+bad.Render(clip("Canvases unavailable · r to retry", width)), "")
	}
	if n := m.columns(); n > 1 {
		m.buildGrid(n)
		return
	}
	for i, it := range m.items {
		m.start = append(m.start, len(m.lines))
		mark := "  "
		if i == m.cursor {
			mark = accent.Render("▌ ")
		}
		meta := []string{}
		for _, s := range []string{it.Tab, ago(it.When, now)} {
			if s != "" {
				meta = append(meta, s)
			}
		}
		metaText := strings.Join(meta, " · ")
		if it.Kind == "commit" {
			head := shortRef(it.Art.Ref)
			m.lines = append(m.lines, mark+sha.Render(head)+" "+strong.Render(clip(it.Title, width-len(head)-1)))
			m.lines = append(m.lines, "  "+dim.Render(clip(strings.TrimPrefix(githubOrRepo(it.Art.Repo)+" · "+metaText, " · "), width)), "")
			continue
		}
		icon := map[string]string{"image": "▣", "video": "▶", "pr": "⇄", "file": "□", "canvas": "◇", "message": "✉"}[it.Kind]
		if it.Kind == "canvas" {
			metaText = "canvas · " + metaText + " · enter draw"
		}
		if it.Kind == "message" {
			metaText = "message · " + metaText + " · enter opens in Messages"
		}
		m.lines = append(m.lines, mark+accent.Render(icon)+" "+strong.Render(clip(it.Title, width-2)))
		m.lines = append(m.lines, "  "+dim.Render(clip(metaText, width)))
		p := m.picOf(it)
		switch {
		case p != nil && m.o.Graphics != "off" && p.state == picReady:
			for _, row := range m.imageRows(p, width, max(4, m.body()-4)) {
				m.lines = append(m.lines, "  "+row)
			}
		case p != nil && m.o.Graphics != "off" && p.state == picFailed:
			m.lines = append(m.lines, "  "+dim.Render(clip(p.err+" · press o to open", width)))
		case p != nil && m.o.Graphics != "off":
			if p.state == picLoading {
				m.lines = append(m.lines, "  "+dim.Render("loading…"))
			}
		case it.Kind == "image" || it.Kind == "video":
			note := "press o to open in the browser"
			if !it.Art.Available {
				note = "not uploaded; it is on the machine that made it"
			} else if it.Kind == "video" {
				note = "video · press o to play in the browser"
			}
			m.lines = append(m.lines, "  "+dim.Render(clip(note, width)))
		}
		m.lines = append(m.lines, "")
	}
}

// near reports whether lines [a, b) fall within a screen of what is shown.
func (m *model) near(a, b int) bool {
	return b >= m.top-m.body() && a <= m.top+2*m.body()
}

func githubOrRepo(repo string) string {
	if r := githubRepo(repo); r != "" {
		return r
	}
	if i := strings.LastIndexAny(strings.TrimRight(repo, "/"), "/:"); i >= 0 {
		return strings.TrimSuffix(strings.TrimRight(repo, "/")[i+1:], ".git")
	}
	return repo
}

// want starts loading images near the view and, once too many are held,
// lets go of the ones farthest from the selection.
func (m *model) want() []tea.Cmd {
	if m.o.Graphics == "off" {
		return nil
	}
	var cmds []tea.Cmd
	loaded := 0
	for i, it := range m.items {
		p := m.picOf(it)
		if p == nil {
			continue
		}
		first, end := m.span(i)
		if p.state == picNone && m.fetching < maxFetching && m.near(first, end) {
			p.state = picLoading
			m.fetching++
			cmds = append(cmds, m.picCmd(it))
		}
		if p.state == picReady {
			loaded++
		}
	}
	for ; loaded > maxLoaded; loaded-- {
		far, dist := -1, -1
		for i, it := range m.items {
			if p := m.picOf(it); p != nil && p.state == picReady {
				if d := max(i-m.cursor, m.cursor-i); d > dist {
					far, dist = i, d
				}
			}
		}
		if far < 0 {
			break
		}
		p := m.picOf(m.items[far])
		var b strings.Builder
		for _, id := range p.placed {
			b.WriteString(kittyDelete(id))
		}
		io.WriteString(m.o.Out, b.String())
		*p = pic{placed: map[[2]int]int{}, blocks: map[[2]int][]string{}}
	}
	return cmds
}

func (m *model) View() string {
	if m.w <= 0 || m.h <= 0 {
		return ""
	}
	var b strings.Builder
	title := "wall"
	if m.view == "diff" {
		title = "diff"
		if row := m.diffTarget(); row != nil {
			title += " · " + row.file
		}
	}
	if m.project != "" && m.view != "diff" {
		title += " · " + m.project
	}
	right := ""
	switch {
	case m.err != nil:
		right = bad.Render(clip("error: "+m.err.Error(), m.w-lipgloss.Width(title)-3))
	case m.loading:
		right = dim.Render("loading…")
	case m.view == "diff":
		right = dim.Render(fmt.Sprintf("%d–%d / %d", m.diffTop+1, min(len(m.diff), m.diffTop+m.body()), len(m.diff)))
	case m.view == "feed":
		right = dim.Render(fmt.Sprintf("%d %s · %s", len(m.items), map[string]string{"all": "items", "media": "media", "code": "commits", "canvas": "canvases", "messages": "messages"}[m.filter], ago(m.synced, m.o.Now())))
	}
	title = clip(title, max(0, m.w-lipgloss.Width(right)-3))
	gap := max(1, m.w-lipgloss.Width(title)-lipgloss.Width(right)-2)
	b.WriteString(" " + strong.Render(title) + strings.Repeat(" ", gap) + right + "\n")
	b.WriteString(dim.Render(strings.Repeat("─", m.w)) + "\n")

	body := make([]string, 0, m.body())
	hint := "j/k ↑↓ move · Space/b page · ? keys · enter view · c canvases · f filter · o browser · s summary · p project · q close"
	if m.o.Share != nil {
		hint = "j/k ↑↓ move · Space/b page · ? keys · enter view · c canvases · S reply · f filter · s summary · p project · q close"
	}
	view := m.view
	if m.help {
		view = "help"
	}
	switch view {
	case "help":
		hint = "? / Esc return"
		for _, line := range []string{
			"Wall keys — only the focused panel receives them",
			"j/k or ↑↓       next / previous item",
			"h/l or ←→       previous / next gallery tile",
			"Ctrl+D / Ctrl+U half page down / up",
			"Space / b       full page down / up",
			"PgDn / PgUp     full page down / up",
			"Ctrl+F / Ctrl+B full page down / up*",
			"Ctrl+E / Ctrl+Y scroll one line down / up",
			"g or gg / G     first / last item (Home / End)",
			"Enter           open selection; Esc backs out",
			"c canvases · f filter · s summary · r refresh",
			"o browser · p project · S reply · q close",
			"Diffs: o Vim · [/] previous/next file",
			"Diffs: h/l horizontal scroll · 0 reset · Esc back",
			"Dashboard: Ctrl+W/F8 or click to switch panels.",
			"* Herdr uses Ctrl+B; use b or PgUp there.",
		} {
			body = append(body, " "+clip(line, m.w-2))
		}
	case "pick":
		hint = "j/k move · enter choose · esc back"
		if len(m.projects) == 0 {
			body = append(body, "", "  "+dim.Render("No projects have recorded work yet."))
		} else {
			body = append(body, "", "  "+dim.Render("Which project?"), "")
			for i, p := range m.projects {
				line := "    " + clip(p, m.w-6)
				if i == m.pick {
					line = "  " + accent.Render("▌ ") + strong.Render(clip(p, m.w-6))
				}
				body = append(body, line)
			}
		}
	case "diff":
		hint = "Esc back · o Vim · [/] files · j/k scroll · Space/b page · h/l sideways · ? keys"
		for i := min(m.diffTop, len(m.diff)); i < len(m.diff); i++ {
			if len(body) == m.body() {
				break
			}
			if i < len(m.patch) {
				body = append(body, m.patch[i].render(m.w, m.diffLeft))
			} else {
				body = append(body, " "+clip(m.diff[i], m.w-1))
			}
		}
	case "zoom":
		hint = "esc back · o open in browser"
		if m.cursor < len(m.items) {
			it := m.items[m.cursor]
			body = append(body, " "+strong.Render(clip(it.Title, m.w-2)))
			body = append(body, m.zoom...)
		}
	default:
		if len(m.items) == 0 && !m.loading && m.err == nil {
			what := "Nothing recorded for this project yet."
			if m.filter != "all" {
				what = "Nothing of this kind yet. Press f for everything."
			}
			body = append(body, "", "  "+dim.Render(clip(what, m.w-4)), "  "+dim.Render(clip("Press p to choose another project.", m.w-4)))
		}
		for i := m.top; i < len(m.lines) && len(body) < m.body(); i++ {
			body = append(body, m.lines[i])
		}
	}
	for len(body) < m.body() {
		body = append(body, "")
	}
	b.WriteString(strings.Join(body[:m.body()], "\n") + "\n")
	if m.flash != "" {
		hint = m.flash
	}
	b.WriteString(dim.Render(clip(" "+hint, m.w)))
	return b.String()
}
