package wall

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
)

func TestBoxForKeepsProportionsWithinLimits(t *testing.T) {
	// A 4:3 picture in cells half as wide as tall: 60 columns need 22.5 rows.
	if c, r := boxFor(1200, 900, 60, 100, 0.5); c != 60 || r != 23 {
		t.Errorf("wide enough: %dx%d", c, r)
	}
	// Too tall for the pane: rows are capped and columns shrink to match.
	if c, r := boxFor(1200, 900, 60, 12, 0.5); c != 32 || r != 12 {
		t.Errorf("capped: %dx%d", c, r)
	}
	if c, r := boxFor(0, 900, 60, 12, 0.5); c != 0 || r != 0 {
		t.Errorf("empty image: %dx%d", c, r)
	}
	if c, r := boxFor(4000, 10, 60, 12, 0.5); c != 60 || r != 1 {
		t.Errorf("a sliver still takes a row: %dx%d", c, r)
	}
}

func TestKittyRowsAddressEveryCell(t *testing.T) {
	rows := kittyRows(0x010203, 3, 2)
	if len(rows) != 2 {
		t.Fatalf("rows: %d", len(rows))
	}
	// The image id travels as the foreground colour.
	if !strings.HasPrefix(rows[0], "\x1b[38;2;1;2;3m") || !strings.HasSuffix(rows[0], "\x1b[39m") {
		t.Fatalf("colour: %q", rows[0])
	}
	for r, row := range rows {
		cells := []rune(strings.TrimSuffix(strings.TrimPrefix(row, "\x1b[38;2;1;2;3m"), "\x1b[39m"))
		if len(cells) != 9 {
			t.Fatalf("row %d: %d runes", r, len(cells))
		}
		for c := 0; c < 3; c++ {
			if cells[c*3] != placeholder || cells[c*3+1] != diacritics[r] || cells[c*3+2] != diacritics[c] {
				t.Errorf("row %d col %d: %U %U %U", r, c, cells[c*3], cells[c*3+1], cells[c*3+2])
			}
		}
	}
	// The protocol's table: first marks, and its length.
	if diacritics[0] != 0x0305 || diacritics[1] != 0x030D || len(diacritics) != 297 {
		t.Errorf("diacritics table changed: %U %U %d", diacritics[0], diacritics[1], len(diacritics))
	}
}

func TestKittyTransmitChunksAndRoundTrips(t *testing.T) {
	data := bytes.Repeat([]byte{7, 8, 9}, 5000) // 20000 base64 chars: 5 chunks
	seq := kittyTransmit(42, 10, 4, data)
	parts := strings.Split(strings.TrimSuffix(seq, "\x1b\\"), "\x1b\\")
	if len(parts) != 5 {
		t.Fatalf("chunks: %d", len(parts))
	}
	payload := ""
	for i, p := range parts {
		head, body, ok := strings.Cut(strings.TrimPrefix(p, "\x1b_G"), ";")
		if !ok || len(body) > 4096 {
			t.Fatalf("chunk %d malformed (%d bytes)", i, len(body))
		}
		wantMore := "m=1"
		if i == len(parts)-1 {
			wantMore = "m=0"
		}
		if i == 0 {
			if head != "a=T,U=1,q=2,f=100,i=42,c=10,r=4,m=1" {
				t.Errorf("first chunk keys: %s", head)
			}
		} else if head != wantMore {
			t.Errorf("chunk %d keys: %s", i, head)
		}
		payload += body
	}
	got, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("payload did not round-trip: %v", err)
	}
	if one := kittyTransmit(1, 2, 2, []byte("x")); !strings.Contains(one, "m=0;") || strings.Count(one, "\x1b_G") != 1 {
		t.Errorf("single chunk: %q", one)
	}
}

func TestBlockRowsUseTwoPixelsPerCell(t *testing.T) {
	// Top half red, bottom half blue, 2 cells wide and 1 cell (2 pixels) tall.
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			c := color.RGBA{255, 0, 0, 255}
			if y >= 2 {
				c = color.RGBA{0, 0, 255, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	rows := blockRows(img, 2, 1)
	want := strings.Repeat("\x1b[38;2;255;0;0m\x1b[48;2;0;0;255m▀", 2) + "\x1b[0m"
	if len(rows) != 1 || rows[0] != want {
		t.Fatalf("rows: %q", rows)
	}
}

func TestShrinkAveragesAndDecodeReadsPNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, color.RGBA{0, 0, 0, 255})
	img.SetRGBA(1, 0, color.RGBA{200, 0, 0, 255})
	img.SetRGBA(0, 1, color.RGBA{0, 100, 0, 255})
	img.SetRGBA(1, 1, color.RGBA{0, 0, 40, 255})
	if got := shrink(img, 1, 1).RGBAAt(0, 0); got != (color.RGBA{50, 25, 10, 255}) {
		t.Errorf("average: %+v", got)
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	back, err := decode(b.Bytes())
	if err != nil || back.Bounds().Dx() != 2 {
		t.Fatalf("decode: %v", err)
	}
	if _, err := decode([]byte("<svg/>")); err == nil {
		t.Error("an unsupported format must be an error")
	}
}

func snapshot() *apiclient.WorkSnapshot {
	return &apiclient.WorkSnapshot{
		Projects: []string{"Robot", "notes"},
		Sessions: []apiclient.WorkSession{{ID: "s1", Agent: "claude", Title: "gait"}},
		Runs:     []apiclient.WorkRun{{ID: "r1", Session: "s1"}, {ID: "r2", Session: "s1"}},
		Artifacts: []apiclient.WorkArtifact{
			{ID: "a1", Kind: "commit", Ref: "0123456789abcdef", Title: "Fix gait", Repo: "git@github.com:me/robot.git", RunID: "r1", MediaHash: strings.Repeat("d", 64), Available: true, Created: "2026-11-17T10:00:00.000000000Z"},
			{ID: "a2", Kind: "image", Ref: "/w/out/walk.png", Title: "/w/out/walk.png", RunID: "r1", MediaHash: strings.Repeat("a", 64), Available: true, Created: "2026-11-17T09:00:00.000000000Z"},
			{ID: "a2", Kind: "image", Ref: "/w/out/walk.png", RunID: "r2", MediaHash: strings.Repeat("a", 64), Available: true}, // the same artifact linked to a second run
			{ID: "a3", Kind: "video", Ref: "/w/out/clip.mp4", RunID: "r2", Created: "2026-11-17T08:00:00.000000000Z"},
			{ID: "a4", Kind: "commit", Ref: "fedcba9876", Title: "Local only", Repo: "/Users/me/src/robot", RunID: "zz"},
		},
	}
}

func TestBuildFeedDedupesAndNamesItems(t *testing.T) {
	items := buildFeed(snapshot())
	if len(items) != 4 {
		t.Fatalf("items: %d", len(items))
	}
	if items[0].Kind != "commit" || items[0].Title != "Fix gait" || items[0].Tab != "gait" || items[0].Agent != "claude" {
		t.Errorf("commit: %+v", items[0])
	}
	if items[1].Kind != "image" || items[1].Title != "walk.png" {
		t.Errorf("image: %+v", items[1])
	}
	if items[2].Kind != "video" || items[2].Title != "clip.mp4" {
		t.Errorf("video: %+v", items[2])
	}
	if items[3].Tab != "" { // its run is not in the snapshot
		t.Errorf("unknown run: %+v", items[3])
	}
	if !items[1].matches("media") || items[1].matches("code") || !items[0].matches("code") || !items[0].matches("all") {
		t.Error("filters")
	}
	if got := items[0].webURL("https://h/notes/"); got != "https://github.com/me/robot/commit/0123456789abcdef" {
		t.Errorf("github commit: %s", got)
	}
	if got := items[1].webURL("https://h/notes"); got != "https://h/notes/api/work/media/"+strings.Repeat("a", 64) {
		t.Errorf("media: %s", got)
	}
	if got := items[2].webURL("https://h/notes"); got != "" {
		t.Errorf("a file that was never uploaded has nowhere to open: %s", got)
	}
	if got := items[3].webURL("https://h/notes"); got != "" {
		t.Errorf("a local-only commit without a diff: %s", got)
	}
}

func TestGithubRepoForms(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:me/robot.git":      "me/robot",
		"https://github.com/me/robot":      "me/robot",
		"github.com/me/robot":              "me/robot",
		"ssh://git@github.com/me/robot":    "me/robot",
		"https://gitlab.com/me/robot":      "",
		"/Users/me/src/robot":              "",
		"https://github.com/me/robot/tree": "",
	} {
		if got := githubRepo(in); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}

func TestAgo(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{20 * time.Second: "now", 5 * time.Minute: "5m", 3 * time.Hour: "3h", 50 * time.Hour: "2d"} {
		if got := ago(now.Add(-d), now); got != want {
			t.Errorf("%v: %q", d, got)
		}
	}
	if ago(time.Time{}, now) != "" {
		t.Error("an unknown time shows nothing")
	}
}

func fakeRunner(replies map[string]string) Runner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		if out, ok := replies[key]; ok {
			return []byte(out), nil
		}
		return nil, errors.New("unexpected command: " + key)
	}
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLocateInHerdrApp(t *testing.T) {
	run := fakeRunner(map[string]string{"hc ls --json": `{"terminals":[
		{"address":"t1","where":"robot / gait"},{"address":"t2","where":"robot / sim"},
		{"address":"t3","where":"notes / 1"},{"address":"t9","where":"robot / wall"}]}`})
	p := Locate(env(map[string]string{"HC_TERMINAL_ID": "t9"}), run)
	if p.Host != "app" || p.Project != "robot" || strings.Join(p.Panes, ",") != "t1,t2,t9" {
		t.Fatalf("place: %+v", p)
	}
	// Never placed in the window: still the App, but no workspace.
	placementWait = time.Millisecond
	if p := Locate(env(map[string]string{"HC_TERMINAL_ID": "t77"}), run); p.Host != "app" || p.Project != "" {
		t.Fatalf("unplaced: %+v", p)
	}
	// Placed a moment after its program started: found on a later try.
	calls := 0
	late := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if calls++; calls < 3 {
			return []byte(`{"terminals":[{"address":"t9","where":""}]}`), nil
		}
		return run(ctx, name, args...)
	}
	if p := Locate(env(map[string]string{"HC_TERMINAL_ID": "t9"}), late); p.Project != "robot" || calls != 3 {
		t.Fatalf("late placement: %+v after %d calls", p, calls)
	}
}

func TestLocateInTerminalHerdr(t *testing.T) {
	run := fakeRunner(map[string]string{
		"/opt/herdr workspace list": `{"result":{"workspaces":[{"workspace_id":"w1","label":"~"},{"workspace_id":"w2","label":"robot"}]}}`,
		"/opt/herdr pane list":      `{"result":{"panes":[{"pane_id":"w1:p1","workspace_id":"w1"},{"pane_id":"w2:p1","workspace_id":"w2"},{"pane_id":"w2:p4","workspace_id":"w2"}]}}`,
	})
	p := Locate(env(map[string]string{"HERDR_WORKSPACE_ID": "w2", "HERDR_BIN_PATH": "/opt/herdr"}), run)
	if p.Host != "herdr" || p.Project != "robot" || strings.Join(p.Panes, ",") != "w2:p1,w2:p4" {
		t.Fatalf("place: %+v", p)
	}
	if p := Locate(env(nil), run); p.Host != "" || p.Project != "" {
		t.Fatalf("outside herdr: %+v", p)
	}
	// A herdr started from inside a Herdr App terminal: the pane is herdr's.
	nested := env(map[string]string{"HERDR_WORKSPACE_ID": "w2", "HERDR_BIN_PATH": "/opt/herdr", "HC_TERMINAL_ID": "t9"})
	if p := Locate(nested, run); p.Host != "herdr" || p.Project != "robot" {
		t.Fatalf("nested: %+v", p)
	}
}

func TestProjectForPrefersTheWorkspacesOwnSessions(t *testing.T) {
	o := &apiclient.WorkSnapshot{
		Projects: []string{"Robot", "Hexapod", "notes"},
		Slots: []apiclient.WorkSlot{
			{Pane: "t1", Project: "Hexapod"}, {Pane: "t2", Project: "Hexapod"}, // moved to another project in Amythest
			{Pane: "t3", Project: "notes"},
		},
	}
	if got := projectFor(Place{Project: "robot", Panes: []string{"t1", "t2", "t9"}}, o); got != "Hexapod" {
		t.Errorf("reassigned sessions: %q", got)
	}
	// One matching pane could be another machine's pane of the same id: the
	// workspace name wins, in the spelling the server has.
	if got := projectFor(Place{Project: "robot", Panes: []string{"t3"}}, o); got != "Robot" {
		t.Errorf("single stray match: %q", got)
	}
	if got := projectFor(Place{Project: "new space"}, o); got != "new space" {
		t.Errorf("unknown project: %q", got)
	}
	if got := projectFor(Place{}, o); got != "" {
		t.Errorf("no workspace: %q", got)
	}
}

func TestDetectGraphics(t *testing.T) {
	yes, no := func() bool { return true }, func() bool { return false }
	for want, vars := range map[string]map[string]string{
		"kitty": {"HC_TERMINAL_ID": "t1"},
		"off":   {"AMY_WALL_IMAGES": "off", "HC_TERMINAL_ID": "t1"},
	} {
		if got := DetectGraphics(env(vars), no); got != want {
			t.Errorf("%v: got %s want %s", vars, got, want)
		}
	}
	// Terminal herdr: only what the terminal answers decides.
	herdr := env(map[string]string{"HERDR_ENV": "1", "TERM_PROGRAM": "ghostty"})
	if DetectGraphics(herdr, yes) != "kitty" || DetectGraphics(herdr, no) != "blocks" || DetectGraphics(herdr, nil) != "blocks" {
		t.Error("terminal herdr must follow the terminal's answer")
	}
	if got := DetectGraphics(env(map[string]string{"HERDR_ENV": "1", "HC_TERMINAL_ID": "t1"}), no); got != "blocks" {
		t.Errorf("herdr inside a Herdr App terminal: %s", got)
	}
	if got := DetectGraphics(env(map[string]string{"TMUX": "/tmp/x", "HC_TERMINAL_ID": "t1"}), yes); got != "blocks" {
		t.Errorf("tmux: %s", got)
	}
	// An unknown terminal that answers may still lack placeholders.
	if got := DetectGraphics(env(map[string]string{"TERM": "xterm-256color"}), yes); got != "blocks" {
		t.Errorf("plain terminal: %s", got)
	}
}

func TestAppPaneFindsOtherWallsInTheSameTab(t *testing.T) {
	run := fakeRunner(map[string]string{
		"herdr-app ls": `{"spaces":[{"tabs":[
			{"terminals":[{"terminal":"t1","pane":"pA"},{"terminal":"t2","pane":"pB"},{"terminal":"t9","pane":"pC"}]},
			{"terminals":[{"terminal":"t5","pane":"pZ"}]}]}]}`,
		"hc get t1 --json": `{"foreground":{"argv":["claude"]}}`,
		"hc get t2 --json": `{"foreground":{"argv":["/Users/me/bin/amy","wall","-pane"]}}`,
	})
	own, others := AppPane("t9", run)
	if own != "pC" || strings.Join(others, ",") != "pB" {
		t.Fatalf("own %q others %v", own, others)
	}
	if own, others := AppPane("t404", run); own != "" || others != nil {
		t.Fatalf("unknown terminal: %q %v", own, others)
	}
	if isWall([]string{"amy"}) || isWall([]string{"vim", "wall"}) || !isWall([]string{"zsh", "-c", "amy", "wall"}) {
		t.Error("isWall")
	}
}

type fakeAPI struct {
	overview, project *apiclient.WorkSnapshot
	media             map[string][]byte
	asked             []string
	cards             map[string]string
	cardsAsked        []string
}

func (f *fakeAPI) WorkOverview(context.Context) (*apiclient.WorkSnapshot, error) {
	return f.overview, nil
}
func (f *fakeAPI) WorkProject(_ context.Context, p string) (*apiclient.WorkSnapshot, error) {
	f.asked = append(f.asked, p)
	return f.project, nil
}
func (f *fakeAPI) WorkMedia(_ context.Context, hash string, _ int64) ([]byte, error) {
	if b, ok := f.media[hash]; ok {
		return b, nil
	}
	return nil, errors.New("not found")
}
func (f *fakeAPI) CardTitles(_ context.Context, ids []string) (map[string]string, error) {
	f.cardsAsked = append(f.cardsAsked, ids...)
	return f.cards, nil
}
func (f *fakeAPI) Endpoint() string { return "https://h/notes" }

// drive feeds a message and runs the commands it produces, the way the
// program loop would, without timers.
func drive(t *testing.T, m tea.Model, msg tea.Msg) {
	t.Helper()
	queue := []tea.Msg{msg}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 200 {
			t.Fatal("messages never settled")
		}
		var cmd tea.Cmd
		_, cmd = m.Update(queue[0])
		queue = queue[1:]
		queue = append(queue, run(cmd)...)
	}
}

func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(50 * time.Millisecond):
		return nil // a timer; the test does not wait for it
	}
	switch v := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range v {
			out = append(out, run(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func keys(s ...string) []tea.Msg {
	var out []tea.Msg
	for _, k := range s {
		switch k {
		case "enter":
			out = append(out, tea.KeyMsg{Type: tea.KeyEnter})
		case "esc":
			out = append(out, tea.KeyMsg{Type: tea.KeyEsc})
		default:
			out = append(out, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		}
	}
	return out
}

func newWall(t *testing.T, api *fakeAPI, o Options) (*model, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	o.Out = out
	o.Now = func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) }
	m := New(api, o).(*model)
	drive(t, m, tea.WindowSizeMsg{Width: 60, Height: 30})
	for _, msg := range run(m.startCmd()) {
		drive(t, m, msg)
	}
	return m, out
}

func picture(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestWallShowsTheWorkspacesProjectWithInlineImages(t *testing.T) {
	api := &fakeAPI{overview: snapshot(), project: snapshot(), media: map[string][]byte{strings.Repeat("a", 64): picture(t)}}
	m, out := newWall(t, api, Options{Graphics: "kitty", Locate: func() Place { return Place{Host: "app", Project: "robot"} }})
	if len(api.asked) == 0 || api.asked[0] != "Robot" {
		t.Fatalf("asked for %v; the workspace robot is the server's project Robot", api.asked)
	}
	view := m.View()
	for _, want := range []string{"wall · Robot", "4 items", "01234567", "Fix gait", "me/robot · gait · 2h", "walk.png"} {
		if !strings.Contains(view, want) {
			t.Errorf("screen lacks %q", want)
		}
	}
	// The picture fills the rest of the screen; later items are further down the feed.
	feed := strings.Join(m.lines, "\n")
	for _, want := range []string{"clip.mp4", "not uploaded; it is on the machine that made it", "Local only"} {
		if !strings.Contains(feed, want) {
			t.Errorf("feed lacks %q", want)
		}
	}
	// The image was uploaded once, and its placeholder cells are on screen.
	if n := strings.Count(out.String(), "\x1b_Ga=T,U=1"); n != 1 {
		t.Errorf("uploads: %d", n)
	}
	if !strings.ContainsRune(view, placeholder) {
		t.Error("no image cells in the view")
	}
	// Redrawing and moving do not upload it again.
	for _, k := range keys("j", "k", "j") {
		drive(t, m, k)
	}
	if n := strings.Count(out.String(), "\x1b_Ga=T,U=1"); n != 1 {
		t.Errorf("uploads after moving: %d", n)
	}
	// Enter on the image enlarges it: a second, larger upload. Esc returns.
	drive(t, m, keys("enter")[0])
	if m.view != "zoom" || strings.Count(out.String(), "\x1b_Ga=T,U=1") != 2 || len(m.zoom) == 0 {
		t.Fatalf("zoom: view %s uploads %d rows %d", m.view, strings.Count(out.String(), "\x1b_Ga=T,U=1"), len(m.zoom))
	}
	drive(t, m, keys("esc")[0])
	if m.view != "feed" {
		t.Fatalf("after esc: %s", m.view)
	}
	// Quitting frees what was uploaded.
	before := strings.Count(out.String(), "a=d,d=I")
	m.quit()
	if strings.Count(out.String(), "a=d,d=I")-before != 2 {
		t.Errorf("uploads not freed on quit: %q", out.String()[len(out.String())-80:])
	}
}

func TestWallFiltersAndHandlesKeysTypedTogether(t *testing.T) {
	api := &fakeAPI{overview: snapshot(), project: snapshot()}
	m, _ := newWall(t, api, Options{Project: "Robot", Graphics: "off"})
	if len(m.items) != 4 {
		t.Fatalf("items: %d", len(m.items))
	}
	// "jj" arriving as one message still moves two items.
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("jj")})
	if m.cursor != 2 {
		t.Fatalf("cursor after jj: %d", m.cursor)
	}
	// Filtering keeps the selected item selected when it is still shown.
	drive(t, m, keys("f")[0])
	if m.filter != "media" || len(m.items) != 2 || m.items[m.cursor].Art.ID != "a3" {
		t.Fatalf("media filter: %s %d cursor on %s", m.filter, len(m.items), m.items[m.cursor].Art.ID)
	}
	drive(t, m, keys("f")[0])
	if m.filter != "code" || len(m.items) != 2 || !strings.Contains(m.View(), "2 commits") {
		t.Fatalf("code filter: %s %d", m.filter, len(m.items))
	}
	// With images off, a picture says how to see it.
	drive(t, m, keys("f")[0])
	if !strings.Contains(m.View(), "press o to open in the browser") {
		t.Errorf("no hint for an image that is not drawn:\n%s", m.View())
	}
}

func TestWallShowsACommitsDiffAndOpensLinks(t *testing.T) {
	diff := "commit 0123\n\n--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-old\n+new\n"
	api := &fakeAPI{overview: snapshot(), project: snapshot(), media: map[string][]byte{strings.Repeat("d", 64): []byte(diff)}}
	opened := ""
	m, _ := newWall(t, api, Options{Project: "Robot", Graphics: "off", Open: func(u string) error { opened = u; return nil }})
	drive(t, m, keys("enter")[0])
	if m.view != "diff" || !strings.Contains(m.View(), "+new") || !strings.Contains(m.View(), "01234567  Fix gait") {
		t.Fatalf("diff view (%s):\n%s", m.view, m.View())
	}
	drive(t, m, keys("esc")[0])
	drive(t, m, keys("o")[0])
	if opened != "https://github.com/me/robot/commit/0123456789abcdef" {
		t.Errorf("opened %q", opened)
	}
	// A commit with no captured diff and no web address says so instead of failing.
	drive(t, m, keys("G")[0])
	drive(t, m, keys("enter")[0])
	if m.view != "feed" || !strings.Contains(m.View(), "nothing to open") {
		t.Errorf("local-only commit: view %s\n%s", m.view, m.View())
	}
}

func TestWallOutsideHerdrAsksWhichProject(t *testing.T) {
	api := &fakeAPI{overview: snapshot(), project: snapshot()}
	m, _ := newWall(t, api, Options{Graphics: "off", Locate: func() Place { return Place{} }})
	if m.view != "pick" || !strings.Contains(m.View(), "Which project?") || !strings.Contains(m.View(), "notes") {
		t.Fatalf("picker:\n%s", m.View())
	}
	for _, k := range keys("j", "enter") {
		drive(t, m, k)
	}
	if m.project != "notes" || len(api.asked) != 1 || api.asked[0] != "notes" || m.view != "feed" {
		t.Fatalf("picked %q asked %v view %s", m.project, api.asked, m.view)
	}
}

func TestWallOpensStraightToAWantedItem(t *testing.T) {
	diff := "commit 0123\n+new\n"
	api := &fakeAPI{overview: snapshot(), project: snapshot(), media: map[string][]byte{strings.Repeat("d", 64): []byte(diff), strings.Repeat("a", 64): picture(t)}}
	// A commit named by its id opens as its diff.
	m, _ := newWall(t, api, Options{Project: "Robot", Graphics: "kitty", Show: &Want{Kind: "commit", Ref: "0123456789abcdef"}})
	if m.view != "diff" || m.cursor != 0 || !strings.Contains(m.View(), "+new") {
		t.Fatalf("commit: view %s cursor %d", m.view, m.cursor)
	}
	// An image named by its title is enlarged once it has loaded.
	m, _ = newWall(t, api, Options{Project: "Robot", Graphics: "kitty", Show: &Want{Kind: "image", Title: "walk.png"}})
	if m.view != "zoom" || m.cursor != 1 || len(m.zoom) == 0 {
		t.Fatalf("image: view %s cursor %d rows %d", m.view, m.cursor, len(m.zoom))
	}
	// Escape returns to the feed with that item selected; a later refresh does not reopen it.
	drive(t, m, keys("esc")[0])
	drive(t, m, feedMsg{project: "Robot", snap: snapshot()})
	if m.view != "feed" || m.cursor != 1 {
		t.Fatalf("after esc and refresh: view %s cursor %d", m.view, m.cursor)
	}
	// Something the feed does not hold says so.
	m, _ = newWall(t, api, Options{Project: "Robot", Graphics: "off", Show: &Want{Kind: "image", Title: "gone.png"}})
	if m.view != "feed" || !strings.Contains(m.View(), "older than this feed shows") {
		t.Fatalf("missing item: view %s\n%s", m.view, m.View())
	}
}

func projectSnapshot() *apiclient.WorkSnapshot {
	s := snapshot()
	bound := func(card string) *struct {
		Card         string `json:"card_id"`
		Acknowledged bool   `json:"acknowledged"`
	} {
		return &struct {
			Card         string `json:"card_id"`
			Acknowledged bool   `json:"acknowledged"`
		}{Card: card, Acknowledged: true}
	}
	s.Slots = []apiclient.WorkSlot{
		{Pane: "t10", Session: "s3", Status: "idle", Tab: "10"},
		{Pane: "t2", Session: "s1", Status: "working", Tab: "2", Binding: bound("k1")},
		{Pane: "t9", Session: "s9", Status: "gone", Tab: "9"},
		{Pane: "t4", Session: "s2", Status: "blocked", Tab: "", Binding: bound("k404")},
	}
	s.Sessions = append(s.Sessions, apiclient.WorkSession{ID: "s2", Agent: "codex"})
	s.Runs = []apiclient.WorkRun{
		{ID: "r1", Session: "s1", Finished: "2026-11-17T10:05:00Z", OutputVersion: 3, Reviewed: 1},
		{ID: "r2", Session: "s1", Finished: "2026-11-17T09:05:00Z", OutputVersion: 2, Reviewed: 2},
		{ID: "r3", Session: "s1", OutputVersion: 1},
		{ID: "r4", Session: "s1", Finished: "2026-11-10T09:05:00Z", OutputVersion: 2, Historical: true},
	}
	return s
}

func plain(lines []string) string {
	out := strings.Join(lines, "\n")
	for {
		i := strings.Index(out, "\x1b[")
		if i < 0 {
			return out
		}
		j := strings.IndexByte(out[i:], 'm')
		if j < 0 {
			return out
		}
		out = out[:i] + out[i+j+1:]
	}
}

func TestSummaryShowsStateTabsAndDays(t *testing.T) {
	snap := projectSnapshot()
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	text := plain(summaryLines(snap, buildFeed(snap), map[string]string{"k1": "Learned gait indoors"}, now, 60))
	for _, want := range []string{
		"1 working", "1 blocked", "1 to review", // r1 only: r2 is reviewed, r3 unfinished, r4 imported
		"claude · working  Learned gait indoors",
		"codex · blocked  card k404", // a card that is not on an open board goes by its id
		"agent · idle  no card",
		"Sat 14", "Mon 16", "Tue 17",
		"1c 2m", // today: one commit, an image and a video
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary lacks %q\n%s", want, text)
		}
	}
	// Numbered tabs in numeric order, then named ones; ended sessions left out.
	if i2, i10, i4 := strings.Index(text, "\n  2 "), strings.Index(text, "\n  10 "), strings.Index(text, "\n  t4 "); !(i2 > 0 && i2 < i10 && i10 < i4) || strings.Contains(text, "  9 ") {
		t.Errorf("tab order (2 at %d, 10 at %d, t4 at %d)\n%s", i2, i10, i4, text)
	}
	if got := summaryLines(nil, nil, nil, now, 60); got != nil {
		t.Errorf("no snapshot: %v", got)
	}
}

func TestWallHeadsTheFeedWithTheSummaryAndLooksUpCards(t *testing.T) {
	api := &fakeAPI{overview: snapshot(), project: projectSnapshot(), cards: map[string]string{"k1": "Learned gait indoors"}}
	m, _ := newWall(t, api, Options{Project: "Robot", Graphics: "off"})
	if strings.Join(api.cardsAsked, ",") != "k1,k404" {
		t.Fatalf("cards asked: %v", api.cardsAsked)
	}
	view := m.View()
	if !strings.Contains(view, "1 working") || !strings.Contains(view, "Learned gait indoors") || !strings.Contains(view, "Fix gait") {
		t.Fatalf("summary above the feed:\n%s", view)
	}
	// A refresh does not ask for the same cards again.
	drive(t, m, feedMsg{project: "Robot", snap: projectSnapshot()})
	if len(api.cardsAsked) != 2 {
		t.Errorf("asked again: %v", api.cardsAsked)
	}
	// s hides the summary; the feed starts at the top.
	drive(t, m, keys("s")[0])
	if view = m.View(); strings.Contains(view, "1 working") || !strings.Contains(view, "Fix gait") {
		t.Fatalf("after s:\n%s", view)
	}
	// Moving down and back to the first item returns to the summary.
	drive(t, m, keys("s")[0])
	for _, k := range keys("G", "g") {
		drive(t, m, k)
	}
	if m.top != 0 || m.cursor != 0 {
		t.Fatalf("back at the top: top %d cursor %d", m.top, m.cursor)
	}
}

func TestMediaFilterIsAGalleryOfTiles(t *testing.T) {
	snap := snapshot()
	for i, name := range []string{"b.png", "c.png", "d.png"} {
		snap.Artifacts = append(snap.Artifacts, apiclient.WorkArtifact{ID: "m" + name, Kind: "image", Ref: "/w/" + name, RunID: "r1",
			MediaHash: strings.Repeat(string(rune('b'+i)), 64), Available: true})
	}
	media := map[string][]byte{}
	for _, h := range []string{"a", "b", "c", "d"} {
		media[strings.Repeat(h, 64)] = picture(t)
	}
	api := &fakeAPI{overview: snapshot(), project: snap, media: media}
	m, _ := newWall(t, api, Options{Project: "Robot", Graphics: "blocks"})
	drive(t, m, keys("s")[0]) // the summary is not part of this
	drive(t, m, keys("f")[0])
	// 60 cells wide: two tiles to a row. Five media items: three rows.
	if m.filter != "media" || m.columns() != 2 || len(m.items) != 5 {
		t.Fatalf("filter %s columns %d items %d", m.filter, m.columns(), len(m.items))
	}
	if m.start[0] != m.start[1] || m.start[2] != m.start[3] || m.start[1] == m.start[2] || m.start[4] == m.start[3] {
		t.Fatalf("rows: %v", m.start)
	}
	// Two pictures of a row draw on the same lines, their names side by side.
	row := plain(m.lines[m.start[2]:m.start[4]])
	names := ""
	for _, l := range strings.Split(row, "\n") {
		if strings.Contains(l, "b.png") {
			names = l
		}
	}
	if !strings.Contains(names, "c.png") || strings.Index(names, "c.png") < 28 {
		t.Fatalf("names are not side by side in their columns: %q\n%s", names, row)
	}
	// Down moves a row, right moves a tile; the view follows.
	drive(t, m, keys("j")[0])
	if m.cursor != 2 {
		t.Fatalf("j: cursor %d", m.cursor)
	}
	drive(t, m, keys("l")[0])
	if m.cursor != 3 {
		t.Fatalf("l: cursor %d", m.cursor)
	}
	for _, k := range keys("j", "j", "k") {
		drive(t, m, k)
	}
	if m.cursor != 2 {
		t.Fatalf("j j k from the last row: cursor %d", m.cursor)
	}
	// A narrow pane, or images turned off, is the plain list again.
	drive(t, m, tea.WindowSizeMsg{Width: 40, Height: 30})
	if m.columns() != 1 {
		t.Errorf("narrow pane columns: %d", m.columns())
	}
}
