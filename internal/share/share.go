// Package share implements "share to notes": uploads (voice memos, photos,
// any file) land in the vault with a note wrapping them, then registered
// processor plugins enrich the note asynchronously.
//
// # Upload lifecycle
//
//  1. POST /api/share/upload (multipart) is authenticated the same way as
//     task toggles: when kanban auth is configured, a valid session is
//     required.
//  2. The file streams to {vault}/Assets/Share/YYYY/MM/<stamp>-<name>,
//     written atomically (temp + rename), 100 MiB cap. The name is reserved
//     with O_EXCL and suffixed -2, -3, … on collision, so two shares of
//     image.jpg in the same second cannot overwrite each other.
//  3. A note is created at {vault}/_Inbox/Share <stamp>.md with frontmatter
//     and an ![[embed]] of the asset. The API responds here — the note is
//     immediately visitable.
//  4. Every active plugin runs in the background: it receives a JSON event
//     on stdin and whatever markdown it prints on stdout is appended to the
//     note under its own section. A failing plugin appends a warning
//     callout instead; it never blocks the upload.
//  5. The caller indexes the new note on its own before responding, then lets
//     a background pass reconcile the rest of the vault once the plugins are
//     done — a full rescan on the response path made every upload wait for a
//     whole-vault re-render.
//
// # Plugin contract
//
// A plugin is any executable, declared in amythest.yaml:
//
//	share_plugins:
//	  - /path/to/openai-transcribe
//
// At startup each plugin is probed with `<plugin> probe`; exit 0 activates
// it. This is where a plugin checks its own configuration — e.g. the
// examples/plugins/openai-transcribe plugin activates only when
// OPENAI_API_KEY is set. For each upload the plugin is invoked as
// `<plugin> process` with this JSON on stdin:
//
//	{"event":"share.upload","asset":"<abs path>","assetRel":"<vault-rel>",
//	 "note":"<abs path>","mime":"audio/mp4","title":"..."}
//
// Stdout (markdown) is appended to the share note. Non-zero exit or empty
// output appends nothing. Plugins get 5 minutes.
package share

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/base698/amythest/internal/vault"
)

const (
	MaxUploadBytes = 100 << 20
	pluginTimeout  = 5 * time.Minute
)

type Store struct {
	vaultRoot string
	plugins   []string // active (probed) plugin executables
}

// New probes the configured plugins and returns the share store.
func New(vaultRoot string, pluginPaths []string) *Store {
	s := &Store{vaultRoot: vaultRoot}
	for _, p := range pluginPaths {
		abs, err := filepath.Abs(p)
		if err != nil {
			slog.Warn("share plugin skipped", "plugin", p, "err", err)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err = exec.CommandContext(ctx, abs, "probe").Run()
		cancel()
		if err != nil {
			slog.Info("share plugin inactive (probe failed)", "plugin", abs, "err", err)
			continue
		}
		s.plugins = append(s.plugins, abs)
		slog.Info("share plugin active", "plugin", abs)
	}
	return s
}

// Plugins returns the active plugin paths.
func (s *Store) Plugins() []string { return s.plugins }

type Upload struct {
	AssetRel string // vault-relative asset path
	NoteRel  string // vault-relative note path
	NoteSlug string
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// safeMime matches well-formed media types; anything else is replaced. The
// note body renders with raw HTML enabled, so client-supplied strings must
// never reach it unneutralized.
var safeMime = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9!#$&^_.+-]*/[a-zA-Z0-9][a-zA-Z0-9!#$&^_.+-]*$`)

// escapeNoteText neutralizes raw-HTML injection and structure breaks for
// client strings interpolated into the generated share note.
var escapeNoteText = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", "\r", " ", "\n", " ",
)

// Asset is a stored upload payload, before its note exists.
type Asset struct {
	Rel string // vault-relative asset path
}

// SaveUpload stores the file and creates the wrapping note.
func (s *Store) SaveUpload(name, title, mime string, r io.Reader, now time.Time) (*Upload, error) {
	a, err := s.SaveAsset(name, r, now)
	if err != nil {
		return nil, err
	}
	return s.CreateNote(a, title, mime, now)
}

// SaveAsset streams the payload into the vault. It is split from CreateNote
// so the handler can stream the multipart file part straight to its final
// home without knowing the title yet — multipart puts the file part first,
// and spooling 100 MiB through memory or a temp file to learn a form field
// doubles the I/O of every large share.
func (s *Store) SaveAsset(name string, r io.Reader, now time.Time) (*Asset, error) {
	base := unsafeName.ReplaceAllString(filepath.Base(name), "-")
	if base == "" || base == "-" {
		base = "upload"
	}
	stamp := now.Format("20060102-150405")
	dir := filepath.Join(s.vaultRoot, "Assets", "Share", now.Format("2006"), now.Format("01"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	ext := filepath.Ext(base)
	assetAbs, err := reserveUnique(dir, stamp+"-"+strings.TrimSuffix(base, ext), ext)
	if err != nil {
		return nil, err
	}

	// Write beside the reservation and rename over it, so a crash mid-upload
	// never leaves a truncated asset where the note points.
	tmp := assetAbs + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		_ = os.Remove(assetAbs)
		return nil, err
	}
	n, err := io.Copy(f, io.LimitReader(r, MaxUploadBytes+1))
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err == nil && n > MaxUploadBytes {
		err = fmt.Errorf("upload exceeds %d MiB", MaxUploadBytes>>20)
	}
	if err == nil {
		err = os.Rename(tmp, assetAbs)
	}
	if err != nil {
		_ = os.Remove(tmp)
		_ = os.Remove(assetAbs)
		return nil, err
	}

	rel, err := filepath.Rel(s.vaultRoot, assetAbs)
	if err != nil {
		return nil, err
	}
	return &Asset{Rel: filepath.ToSlash(rel)}, nil
}

// CreateNote writes the _Inbox note wrapping a stored asset.
func (s *Store) CreateNote(a *Asset, title, mime string, now time.Time) (*Upload, error) {
	noteTitle := strings.TrimSpace(title)
	if noteTitle == "" {
		noteTitle = "Share " + now.Format("2006-01-02 15:04")
	}
	dir := filepath.Join(s.vaultRoot, "_Inbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	noteAbs, err := reserveUnique(dir, unsafeName.ReplaceAllString(noteTitle, " "), ".md")
	if err != nil {
		return nil, err
	}

	if !safeMime.MatchString(mime) {
		mime = "application/octet-stream"
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "---\ntype: source\ncreated: %s\ntags: [share]\n---\n\n# %s\n\n![[%s]]\n",
		now.Format("2006-01-02"), escapeNoteText.Replace(noteTitle), a.Rel)
	fmt.Fprintf(&b, "\n- **Uploaded:** %s\n- **File:** `%s` (%s)\n",
		now.Format("2006-01-02 15:04"), a.Rel, mime)
	if err := os.WriteFile(noteAbs, b.Bytes(), 0o644); err != nil {
		_ = os.Remove(noteAbs)
		return nil, err
	}

	rel, err := filepath.Rel(s.vaultRoot, noteAbs)
	if err != nil {
		return nil, err
	}
	noteRel := filepath.ToSlash(rel)
	return &Upload{AssetRel: a.Rel, NoteRel: noteRel, NoteSlug: vault.Slugify(noteRel)}, nil
}

// CreateTextNote writes a share note without requiring an uploaded asset.
// The description is plain text: HTML is escaped because notes render with
// raw HTML enabled, while line breaks are preserved for readable paragraphs.
func (s *Store) CreateTextNote(title, description string, now time.Time) (*Upload, error) {
	noteTitle := strings.TrimSpace(title)
	if noteTitle == "" {
		return nil, errors.New("title is required")
	}
	dir := filepath.Join(s.vaultRoot, "_Inbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	base := strings.ToLower(unsafeName.ReplaceAllString(noteTitle, " "))
	base = strings.Join(strings.Fields(base), "-")
	base = strings.Trim(base, " ._-")
	if base == "" {
		base = "text-note-" + now.Format("2006-01-02-150405")
	} else if strings.EqualFold(base, "index") {
		base = "note-index"
	}
	noteAbs, err := reserveUnique(dir, base, ".md")
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "---\ntype: source\ncreated: %s\ntags: [share]\n---\n\n# %s\n",
		now.Format("2006-01-02"), html.EscapeString(noteTitle))
	if description = strings.TrimSpace(description); description != "" {
		description = strings.ReplaceAll(description, "\r\n", "\n")
		description = strings.ReplaceAll(description, "\r", "\n")
		fmt.Fprintf(&b, "\n%s\n", html.EscapeString(description))
	}
	if err := os.WriteFile(noteAbs, b.Bytes(), 0o644); err != nil {
		_ = os.Remove(noteAbs)
		return nil, err
	}
	rel, err := filepath.Rel(s.vaultRoot, noteAbs)
	if err != nil {
		return nil, err
	}
	noteRel := filepath.ToSlash(rel)
	return &Upload{NoteRel: noteRel, NoteSlug: vault.Slugify(noteRel)}, nil
}

// reserveUnique atomically claims dir/base+ext, appending -2, -3, … until it
// finds a free name. O_EXCL is what makes it safe: two shares of image.jpg in
// the same second (or two concurrent uploads) used to compute the same path
// and silently overwrite each other's asset and note.
func reserveUnique(dir, base, ext string) (string, error) {
	for i := 1; i < 1000; i++ {
		name := base + ext
		if i > 1 {
			name = fmt.Sprintf("%s-%d%s", base, i, ext)
		}
		abs := filepath.Join(dir, name)
		f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return abs, f.Close()
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("no free filename for %q in %s", base+ext, dir)
}

type pluginEvent struct {
	Event    string `json:"event"`
	Asset    string `json:"asset"`
	AssetRel string `json:"assetRel"`
	Note     string `json:"note"`
	Mime     string `json:"mime"`
	Title    string `json:"title"`
}

// RunPlugins executes every active plugin for an upload, appending each
// result to the note as it completes. Returns after all plugins finish;
// call from a goroutine and rescan afterwards.
func (s *Store) RunPlugins(up *Upload, mime, title string) {
	if len(s.plugins) == 0 {
		return
	}
	event, _ := json.Marshal(pluginEvent{
		Event:    "share.upload",
		Asset:    filepath.Join(s.vaultRoot, filepath.FromSlash(up.AssetRel)),
		AssetRel: up.AssetRel,
		Note:     filepath.Join(s.vaultRoot, filepath.FromSlash(up.NoteRel)),
		Mime:     mime,
		Title:    title,
	})
	for _, plugin := range s.plugins {
		out, err := runPlugin(plugin, event)
		name := filepath.Base(plugin)
		if err != nil {
			slog.Warn("share plugin failed", "plugin", name, "err", err)
			s.appendToNote(up, fmt.Sprintf(
				"\n> [!warning] Plugin %s failed\n> %s\n",
				name, strings.ReplaceAll(err.Error(), "\n", " ")))
			continue
		}
		if strings.TrimSpace(out) == "" {
			continue
		}
		s.appendToNote(up, "\n"+strings.TrimSpace(out)+"\n")
	}
}

func runPlugin(path string, event []byte) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pluginTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "process")
	cmd.Stdin = bytes.NewReader(event)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return stdout.String(), nil
}

func (s *Store) appendToNote(up *Upload, markdown string) {
	abs := filepath.Join(s.vaultRoot, filepath.FromSlash(up.NoteRel))
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		slog.Warn("share note append", "err", err)
		return
	}
	defer f.Close()
	_, _ = f.WriteString(markdown)
}
