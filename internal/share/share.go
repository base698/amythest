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
//     written atomically (temp + rename), 100 MiB cap.
//  3. A note is created at {vault}/_Inbox/Share <stamp>.md with frontmatter
//     and an ![[embed]] of the asset. The API responds here — the note is
//     immediately visitable.
//  4. Every active plugin runs in the background: it receives a JSON event
//     on stdin and whatever markdown it prints on stdout is appended to the
//     note under its own section. A failing plugin appends a warning
//     callout instead; it never blocks the upload.
//  5. The caller triggers a rescan after upload and after plugins finish,
//     so search/graph/tasks pick the note up.
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
	"fmt"
	"io"
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

// SaveUpload stores the file and creates the wrapping note.
func (s *Store) SaveUpload(name, title, mime string, r io.Reader, now time.Time) (*Upload, error) {
	base := unsafeName.ReplaceAllString(filepath.Base(name), "-")
	if base == "" || base == "-" {
		base = "upload"
	}
	stamp := now.Format("20060102-150405")
	assetRel := filepath.ToSlash(filepath.Join("Assets", "Share",
		now.Format("2006"), now.Format("01"), stamp+"-"+base))

	assetAbs := filepath.Join(s.vaultRoot, filepath.FromSlash(assetRel))
	if err := os.MkdirAll(filepath.Dir(assetAbs), 0o755); err != nil {
		return nil, err
	}
	tmp := assetAbs + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
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
	if err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, assetAbs); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}

	noteTitle := strings.TrimSpace(title)
	if noteTitle == "" {
		noteTitle = "Share " + now.Format("2006-01-02 15:04")
	}
	noteRel := filepath.ToSlash(filepath.Join("_Inbox",
		unsafeName.ReplaceAllString(noteTitle, " ")+".md"))
	noteAbs := filepath.Join(s.vaultRoot, filepath.FromSlash(noteRel))
	if _, err := os.Stat(noteAbs); err == nil {
		noteRel = filepath.ToSlash(filepath.Join("_Inbox",
			unsafeName.ReplaceAllString(noteTitle, " ")+" "+stamp+".md"))
		noteAbs = filepath.Join(s.vaultRoot, filepath.FromSlash(noteRel))
	}
	if err := os.MkdirAll(filepath.Dir(noteAbs), 0o755); err != nil {
		return nil, err
	}

	if !safeMime.MatchString(mime) {
		mime = "application/octet-stream"
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "---\ntype: source\ncreated: %s\ntags: [share]\n---\n\n# %s\n\n![[%s]]\n",
		now.Format("2006-01-02"), escapeNoteText.Replace(noteTitle), assetRel)
	fmt.Fprintf(&b, "\n- **Uploaded:** %s\n- **File:** `%s` (%s)\n",
		now.Format("2006-01-02 15:04"), assetRel, mime)
	if err := os.WriteFile(noteAbs, b.Bytes(), 0o644); err != nil {
		return nil, err
	}

	return &Upload{AssetRel: assetRel, NoteRel: noteRel, NoteSlug: vault.Slugify(noteRel)}, nil
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
