// Package canvas stores shared drawings: a person draws in the browser
// (Excalidraw), an agent reads a plain-language summary of what is on the
// canvas and what changed, and either side can push content to the other.
//
// # Layout
//
// Each canvas is one directory under the store root (<data_dir>/canvases),
// outside the vault so a scratch drawing never lands in the notes index:
//
//	<id>/meta.json      title, revision, hand-off state
//	<id>/scene.json     the Excalidraw scene (elements, appState, files)
//	<id>/drawing.svg    last rendering, with the scene embedded by Excalidraw
//	<id>/drawing.png    last rendering as PNG, for panes, notes and messages
//	<id>/inbox/<n>.json content an agent pushed that the editor has not merged
//
// The server never edits a scene. Agents push inbox items (Mermaid, an image,
// a line of text); the editor converts and places them, then saves. That keeps
// layout in one place — the browser that already knows how to draw — and lets
// a push wait safely until someone opens the canvas.
package canvas

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MaxSceneBytes   = 30 << 20 // embedded images live inside the scene
	MaxSVGBytes     = 30 << 20
	MaxPNGBytes     = 20 << 20
	MaxInboxBytes   = 10 << 20
	MaxTitleRunes   = 200
	MaxMessageRunes = 2000
	maxInboxPending = 64
)

var (
	ErrNotFound = errors.New("canvas not found")
	ErrConflict = errors.New("canvas changed since it was loaded")
	ErrInvalid  = errors.New("invalid canvas request")

	idPattern = regexp.MustCompile(`^[a-z2-7]{12}$`)
)

// Meta is everything about a canvas except its drawing.
type Meta struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	// Rev increases on every save.
	Rev int64 `json:"rev"`
	// Sent counts hand-offs: the person pressed "Send to agent". An agent
	// waiting on the canvas wakes when it changes.
	Sent        int64     `json:"sent"`
	SentRev     int64     `json:"sent_rev,omitempty"`
	SentAt      time.Time `json:"sent_at,omitzero"`
	SentMessage string    `json:"sent_message,omitempty"`
	// Inbox is the sequence number of the last item pushed; Applied is the
	// last one the editor merged into the scene.
	Inbox   int64 `json:"inbox"`
	Applied int64 `json:"applied"`
	// Drawing reports whether a rendering exists yet.
	Drawing bool `json:"drawing"`
	// Where the canvas belongs: a Herdr workspace, an Amythest board, and
	// optionally one card on it. Browsing filters, not access control.
	// Stale: the scene changed without new renderings (an ops edit); the
	// next editor to open it re-renders and saves.
	Stale     bool   `json:"stale,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Board     string `json:"board,omitempty"`
	Card      string `json:"card,omitempty"`
}

// Placement says where a canvas belongs. Empty fields are left unchanged by
// SetLink; Clear empties all three first.
type Placement struct {
	Workspace string `json:"workspace"`
	Board     string `json:"board"`
	Card      string `json:"card"`
	Clear     bool   `json:"clear,omitempty"`
}

func cleanRef(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120])
	}
	return s
}

func (l Placement) apply(m *Meta) {
	if l.Clear {
		m.Workspace, m.Board, m.Card = "", "", ""
	}
	if v := cleanRef(l.Workspace); v != "" {
		m.Workspace = v
	}
	if v := cleanRef(l.Board); v != "" {
		m.Board = v
	}
	if v := cleanRef(l.Card); v != "" {
		m.Card = v
	}
}

// SetLink records where a canvas belongs.
func (s *Store) SetLink(id string, l Placement) (Meta, error) {
	dir, err := s.dir(id)
	if err != nil {
		return Meta{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := readMeta(dir)
	if err != nil {
		return Meta{}, err
	}
	l.apply(&m)
	return m, s.writeMeta(dir, m)
}

// InboxItem is content pushed to a canvas for the editor to place.
type InboxItem struct {
	Seq  int64     `json:"seq"`
	Kind string    `json:"kind"` // mermaid | image | text
	Name string    `json:"name,omitempty"`
	MIME string    `json:"mime,omitempty"`
	Data string    `json:"data"` // Mermaid source, text, or a data: URL for images
	From string    `json:"from,omitempty"`
	At   time.Time `json:"at"`
}

// SaveInput is one editor save.
type SaveInput struct {
	BaseRev int64
	Title   *string
	Scene   []byte
	SVG     []byte
	PNG     []byte
	// Applied acknowledges inbox items merged into this scene.
	Applied int64
}

type Store struct {
	root string
	mu   sync.Mutex
	now  func() time.Time
}

func New(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &Store{root: root, now: time.Now}, nil
}

// ValidID reports whether id has the shape this store hands out.
func ValidID(id string) bool { return idPattern.MatchString(id) }

func (s *Store) dir(id string) (string, error) {
	if !ValidID(id) {
		return "", ErrNotFound
	}
	return filepath.Join(s.root, id), nil
}

func newID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:])
}

func cleanTitle(t string) string {
	t = strings.Join(strings.Fields(t), " ")
	if r := []rune(t); len(r) > MaxTitleRunes {
		t = string(r[:MaxTitleRunes])
	}
	if t == "" {
		t = "Untitled canvas"
	}
	return t
}

// Create makes an empty canvas. Seed items (for example the Mermaid an agent
// starts from) go into the inbox so the editor places them on first open.
func (s *Store) Create(title string, link Placement, seed ...InboxItem) (Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	m := Meta{ID: newID(), Title: cleanTitle(title), Created: now, Updated: now}
	link.apply(&m)
	dir := filepath.Join(s.root, m.ID)
	if err := os.MkdirAll(filepath.Join(dir, "inbox"), 0o700); err != nil {
		return Meta{}, err
	}
	if err := writeAtomic(filepath.Join(dir, "scene.json"), []byte(`{"type":"excalidraw","version":2,"elements":[],"files":{}}`)); err != nil {
		return Meta{}, err
	}
	for _, item := range seed {
		if err := s.pushLocked(dir, &m, item); err != nil {
			os.RemoveAll(dir)
			return Meta{}, err
		}
	}
	if err := s.writeMeta(dir, m); err != nil {
		return Meta{}, err
	}
	return m, nil
}

// List returns every canvas, most recently updated first.
func (s *Store) List() ([]Meta, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	out := []Meta{}
	for _, e := range entries {
		if !e.IsDir() || !ValidID(e.Name()) {
			continue
		}
		m, err := s.Meta(e.Name())
		if err == nil {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

func (s *Store) Meta(id string) (Meta, error) {
	dir, err := s.dir(id)
	if err != nil {
		return Meta{}, err
	}
	return readMeta(dir)
}

// Delete removes a canvas and its drawings/inbox. Serialize it with writes so
// an editor's in-flight autosave cannot recreate a deleted canvas.
func (s *Store) Delete(id string) error {
	dir, err := s.dir(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := readMeta(dir); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func readMeta(dir string) (Meta, error) {
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Meta{}, ErrNotFound
	}
	if err != nil {
		return Meta{}, err
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return Meta{}, err
	}
	return m, nil
}

func (s *Store) writeMeta(dir string, m Meta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "meta.json"), b)
}

// Scene returns the stored Excalidraw scene.
func (s *Store) Scene(id string) (Meta, []byte, error) {
	dir, err := s.dir(id)
	if err != nil {
		return Meta{}, nil, err
	}
	m, err := readMeta(dir)
	if err != nil {
		return Meta{}, nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "scene.json"))
	return m, b, err
}

// Drawing returns the last rendering; kind is "svg" or "png".
func (s *Store) Drawing(id, kind string) ([]byte, Meta, error) {
	dir, err := s.dir(id)
	if err != nil {
		return nil, Meta{}, err
	}
	if kind != "svg" && kind != "png" {
		return nil, Meta{}, ErrNotFound
	}
	m, err := readMeta(dir)
	if err != nil {
		return nil, Meta{}, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "drawing."+kind))
	if errors.Is(err, os.ErrNotExist) {
		return nil, m, ErrNotFound
	}
	return b, m, err
}

// Save stores a new revision. BaseRev must match the current revision so two
// open editors cannot silently overwrite each other.
func (s *Store) Save(id string, in SaveInput) (Meta, error) {
	dir, err := s.dir(id)
	if err != nil {
		return Meta{}, err
	}
	if len(in.Scene) == 0 || len(in.Scene) > MaxSceneBytes || len(in.SVG) > MaxSVGBytes || len(in.PNG) > MaxPNGBytes {
		return Meta{}, fmt.Errorf("%w: scene or rendering size out of bounds", ErrInvalid)
	}
	if !json.Valid(in.Scene) {
		return Meta{}, fmt.Errorf("%w: scene is not JSON", ErrInvalid)
	}
	if len(in.PNG) > 0 && !strings.HasPrefix(string(in.PNG[:min(8, len(in.PNG))]), "\x89PNG\r\n\x1a\n") {
		return Meta{}, fmt.Errorf("%w: rendering is not a PNG", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := readMeta(dir)
	if err != nil {
		return Meta{}, err
	}
	if in.BaseRev != m.Rev {
		return m, ErrConflict
	}
	if err := writeAtomic(filepath.Join(dir, "scene.json"), in.Scene); err != nil {
		return Meta{}, err
	}
	if len(in.SVG) > 0 {
		if err := writeAtomic(filepath.Join(dir, "drawing.svg"), in.SVG); err != nil {
			return Meta{}, err
		}
	}
	if len(in.PNG) > 0 {
		if err := writeAtomic(filepath.Join(dir, "drawing.png"), in.PNG); err != nil {
			return Meta{}, err
		}
		m.Drawing = true
	}
	if len(in.PNG) > 0 || len(in.SVG) > 0 {
		m.Stale = false
	}
	if in.Title != nil {
		m.Title = cleanTitle(*in.Title)
	}
	if in.Applied > m.Applied && in.Applied <= m.Inbox {
		for seq := m.Applied + 1; seq <= in.Applied; seq++ {
			os.Remove(filepath.Join(dir, "inbox", strconv.FormatInt(seq, 10)+".json"))
		}
		m.Applied = in.Applied
	}
	m.Rev++
	m.Updated = s.now().UTC()
	return m, s.writeMeta(dir, m)
}

// Send records a hand-off from the person to whoever is waiting on the canvas.
func (s *Store) Send(id, message string) (Meta, error) {
	dir, err := s.dir(id)
	if err != nil {
		return Meta{}, err
	}
	if r := []rune(strings.TrimSpace(message)); len(r) > MaxMessageRunes {
		message = string(r[:MaxMessageRunes])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := readMeta(dir)
	if err != nil {
		return Meta{}, err
	}
	m.Sent++
	m.SentRev = m.Rev
	m.SentAt = s.now().UTC()
	m.SentMessage = strings.TrimSpace(message)
	return m, s.writeMeta(dir, m)
}

// Push queues content for the editor to place on the canvas.
func (s *Store) Push(id string, item InboxItem) (Meta, InboxItem, error) {
	dir, err := s.dir(id)
	if err != nil {
		return Meta{}, item, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := readMeta(dir)
	if err != nil {
		return Meta{}, item, err
	}
	if m.Inbox-m.Applied >= maxInboxPending {
		return m, item, fmt.Errorf("%w: %d items are waiting; open the canvas to place them", ErrInvalid, maxInboxPending)
	}
	if err := s.pushLocked(dir, &m, item); err != nil {
		return m, item, err
	}
	item.Seq = m.Inbox
	return m, item, s.writeMeta(dir, m)
}

func (s *Store) pushLocked(dir string, m *Meta, item InboxItem) error {
	switch item.Kind {
	case "mermaid", "text":
		if strings.TrimSpace(item.Data) == "" {
			return fmt.Errorf("%w: %s is empty", ErrInvalid, item.Kind)
		}
	case "image":
		if !strings.HasPrefix(item.Data, "data:image/") {
			return fmt.Errorf("%w: image must be a data:image/ URL", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalid, item.Kind)
	}
	if len(item.Data) > MaxInboxBytes {
		return fmt.Errorf("%w: item exceeds %d bytes", ErrInvalid, MaxInboxBytes)
	}
	m.Inbox++
	item.Seq = m.Inbox
	item.At = s.now().UTC()
	b, err := json.Marshal(item)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "inbox"), 0o700); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "inbox", strconv.FormatInt(item.Seq, 10)+".json"), b)
}

// Pending returns inbox items the editor has not merged yet, oldest first.
func (s *Store) Pending(id string) ([]InboxItem, error) {
	dir, err := s.dir(id)
	if err != nil {
		return nil, err
	}
	m, err := readMeta(dir)
	if err != nil {
		return nil, err
	}
	out := []InboxItem{}
	for seq := m.Applied + 1; seq <= m.Inbox; seq++ {
		b, err := os.ReadFile(filepath.Join(dir, "inbox", strconv.FormatInt(seq, 10)+".json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var item InboxItem
		if json.Unmarshal(b, &item) == nil {
			out = append(out, item)
		}
	}
	return out, nil
}

func writeAtomic(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err = f.Write(b); err == nil {
		err = f.Chmod(0o600)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// FileName turns a title into a safe, readable file name (no extension).
func FileName(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 60 {
			break
		}
	}
	if name := strings.Trim(b.String(), "-"); name != "" {
		return name
	}
	return "canvas"
}

// Ops are element-level edits from a client that does not hold the whole
// scene (the Herdr pane): upserts merge fields into the element with that id
// (or add it), deletes mark elements deleted the way Excalidraw does. Each
// touched element gets a new version so open editors reconcile it.
type Ops struct {
	Upsert []map[string]any `json:"upsert"`
	Delete []string         `json:"delete"`
}

const maxOpsElements = 2000

// ApplyOps edits the stored scene in place and bumps the revision. The
// renderings go stale until an editor next saves (Meta.Stale).
func (s *Store) ApplyOps(id string, ops Ops) (Meta, error) {
	dir, err := s.dir(id)
	if err != nil {
		return Meta{}, err
	}
	if len(ops.Upsert)+len(ops.Delete) == 0 {
		return Meta{}, fmt.Errorf("%w: no operations", ErrInvalid)
	}
	if len(ops.Upsert)+len(ops.Delete) > maxOpsElements {
		return Meta{}, fmt.Errorf("%w: too many operations", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := readMeta(dir)
	if err != nil {
		return Meta{}, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "scene.json"))
	if err != nil {
		return Meta{}, err
	}
	var scene map[string]any
	if err := json.Unmarshal(raw, &scene); err != nil {
		return Meta{}, err
	}
	elements, _ := scene["elements"].([]any)
	index := map[string]map[string]any{}
	for _, e := range elements {
		if el, ok := e.(map[string]any); ok {
			if eid, _ := el["id"].(string); eid != "" {
				index[eid] = el
			}
		}
	}
	now := float64(s.now().UnixMilli())
	touch := func(el map[string]any) {
		v, _ := el["version"].(float64)
		el["version"] = v + 1
		el["versionNonce"] = float64(randInt31())
		el["updated"] = now
	}
	for _, up := range ops.Upsert {
		eid, _ := up["id"].(string)
		if eid == "" || len(eid) > 64 {
			return Meta{}, fmt.Errorf("%w: element without a valid id", ErrInvalid)
		}
		if t, ok := up["type"].(string); ok && !editableTypes[t] {
			return Meta{}, fmt.Errorf("%w: cannot add %q elements", ErrInvalid, t)
		}
		el, exists := index[eid]
		if !exists {
			if _, ok := up["type"].(string); !ok {
				return Meta{}, fmt.Errorf("%w: new element %s needs a type", ErrInvalid, eid)
			}
			el = newElementDefaults(up["type"].(string))
			elements = append(elements, el)
			index[eid] = el
		}
		for k, v := range up {
			if k == "version" || k == "versionNonce" {
				continue
			}
			el[k] = v
		}
		touch(el)
	}
	for _, eid := range ops.Delete {
		if el, ok := index[eid]; ok {
			el["isDeleted"] = true
			touch(el)
		}
	}
	scene["elements"] = elements
	b, err := json.Marshal(scene)
	if err != nil {
		return Meta{}, err
	}
	if len(b) > MaxSceneBytes {
		return Meta{}, fmt.Errorf("%w: scene too large", ErrInvalid)
	}
	if err := writeAtomic(filepath.Join(dir, "scene.json"), b); err != nil {
		return Meta{}, err
	}
	m.Rev++
	m.Stale = true
	m.Updated = s.now().UTC()
	return m, s.writeMeta(dir, m)
}

var editableTypes = map[string]bool{"rectangle": true, "ellipse": true, "diamond": true, "arrow": true, "line": true, "freedraw": true, "text": true}

// newElementDefaults are the fields Excalidraw expects on every element;
// the editor's restore() fills anything else.
func newElementDefaults(kind string) map[string]any {
	el := map[string]any{
		"type": kind, "angle": 0.0, "strokeColor": "#1e1e1e", "backgroundColor": "transparent",
		"fillStyle": "solid", "strokeWidth": 2.0, "strokeStyle": "solid", "roughness": 1.0, "opacity": 100.0,
		"groupIds": []any{}, "frameId": nil, "roundness": nil, "seed": float64(randInt31()), "version": 0.0,
		"isDeleted": false, "boundElements": []any{}, "link": nil, "locked": false,
	}
	switch kind {
	case "rectangle", "diamond", "ellipse":
		el["roundness"] = map[string]any{"type": 3.0}
	case "arrow":
		el["roundness"] = map[string]any{"type": 2.0}
		el["startArrowhead"] = nil
		el["endArrowhead"] = "arrow"
		el["startBinding"] = nil
		el["endBinding"] = nil
		el["elbowed"] = false
	case "text":
		el["fontSize"] = 20.0
		el["fontFamily"] = 5.0
		el["textAlign"] = "left"
		el["verticalAlign"] = "top"
		el["containerId"] = nil
		el["lineHeight"] = 1.25
		el["autoResize"] = true
	case "freedraw":
		el["pressures"] = []any{}
		el["simulatePressure"] = true
	}
	return el
}

func randInt31() int32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return int32(uint32(b[0])<<23 | uint32(b[1])<<15 | uint32(b[2])<<7 | uint32(b[3])>>1)
}
