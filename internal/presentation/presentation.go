// Package presentation makes bounded, expiring view copies of indexed vault
// assets. It never changes source files or adds generated pages to the vault.
package presentation

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/base698/amythest/internal/vault"
	"golang.org/x/sys/unix"
)

const maxFile = 20 << 20
const maxBundle = 40 << 20
const maxStored = 256 << 20

var idPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var types = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp", ".pdf": "application/pdf", ".txt": "text/plain; charset=utf-8"}

type Store struct {
	root, base string
	mu         sync.Mutex
	now        func() time.Time
}
type Input struct {
	Action     string   `json:"action" jsonschema:"search finds filenames; open creates a private view from exact returned asset paths or a discovered note slug"`
	Query      string   `json:"query,omitempty"`
	Offset     int      `json:"offset,omitempty"`
	Limit      int      `json:"limit,omitempty" jsonschema:"filename search page size, 1 to 30; default 30"`
	SourceSlug string   `json:"source_slug,omitempty"`
	Assets     []string `json:"assets,omitempty" jsonschema:"up to 8 exact vault-relative paths from search or note embeds; never host filesystem paths"`
	Title      string   `json:"title,omitempty"`
}
type View struct {
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	SourceNote string    `json:"source_note,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
}
type Result struct {
	Assets     []string `json:"assets,omitempty"`
	NextOffset *int     `json:"next_offset,omitempty"`
	WebView    *View    `json:"web_view,omitempty"`
}
type file struct{ Name, Stored, MIME string }
type bundle struct {
	Title, SourceURL string
	Expires          time.Time
	Files            []file
}

func New(root, base string) (*Store, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	s := &Store{root: root, base: strings.TrimRight(base, "/"), now: time.Now}
	s.Cleanup()
	return s, nil
}

func (s *Store) Execute(v *vault.Vault, in Input) (Result, error) {
	if len(in.Query) > 200 || len(in.Title) > 200 || len(in.SourceSlug) > 1000 || in.Offset < 0 || in.Limit < 0 || in.Limit > 30 {
		return Result{}, fmt.Errorf("invalid presentation input")
	}
	if in.Action == "search" {
		if strings.TrimSpace(in.Query) == "" {
			return Result{}, fmt.Errorf("search query required")
		}
		matches := []string{}
		for _, rel := range v.Assets {
			if types[strings.ToLower(path.Ext(rel))] == "" {
				continue
			}
			match := true
			for _, term := range strings.Fields(strings.ToLower(in.Query)) {
				if !strings.Contains(strings.ToLower(rel), term) {
					match = false
					break
				}
			}
			if match {
				matches = append(matches, rel)
			}
		}
		sort.Strings(matches)
		start := min(in.Offset, len(matches))
		limit := in.Limit
		if limit == 0 {
			limit = 30
		}
		end := min(start+limit, len(matches))
		out := Result{Assets: matches[start:end]}
		if end < len(matches) {
			out.NextOffset = &end
		}
		return out, nil
	}
	if in.Action != "open" {
		return Result{}, fmt.Errorf("action must be search or open")
	}
	if in.Query != "" || in.Offset != 0 || len(in.Assets) > 8 {
		return Result{}, fmt.Errorf("open takes a source_slug and/or up to 8 assets")
	}
	sourceURL := ""
	if in.SourceSlug != "" {
		n, ok := v.BySlug(in.SourceSlug)
		if !ok {
			return Result{}, fmt.Errorf("source note not found; use its exact search slug")
		}
		sourceURL = s.base + "/" + escapePath(n.Slug)
		if in.Title == "" {
			in.Title = n.Title
		}
	}
	if len(in.Assets) == 0 {
		if sourceURL == "" {
			return Result{}, fmt.Errorf("source_slug or assets required")
		}
		return Result{WebView: &View{Type: "web_view", Title: in.Title, URL: sourceURL, SourceNote: in.SourceSlug}}, nil
	}
	if in.Title == "" {
		in.Title = "Filed content"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	entries, _ := os.ReadDir(s.root)
	var used int64
	for _, entry := range entries {
		if !entry.IsDir() || !idPattern.MatchString(entry.Name()) {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(s.root, entry.Name()))
		for _, f := range files {
			if st, e := f.Info(); e == nil {
				used += st.Size()
			}
		}
	}
	if len(entries) >= 64 || used+maxBundle > maxStored {
		return Result{}, fmt.Errorf("temporary view storage is full; try after older views expire")
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Result{}, err
	}
	id := hex.EncodeToString(idBytes)
	dir := filepath.Join(s.root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		return Result{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(dir)
		}
	}()
	b := bundle{Title: in.Title, SourceURL: sourceURL, Expires: s.now().Add(24 * time.Hour)}
	var total int64
	for i, rel := range in.Assets {
		if !v.HasAsset(rel) || rel != path.Clean(rel) || strings.Contains(rel, "\\") {
			return Result{}, fmt.Errorf("asset must be an exact indexed vault path")
		}
		ext := strings.ToLower(path.Ext(rel))
		mime := types[ext]
		if mime == "" {
			return Result{}, fmt.Errorf("unsupported file type; use PNG, JPEG, GIF, WebP, PDF or text")
		}
		f, err := openAsset(v.Root, rel)
		if err != nil {
			return Result{}, fmt.Errorf("asset unavailable")
		}
		data, err := io.ReadAll(io.LimitReader(f, maxFile+1))
		f.Close()
		if err != nil || len(data) > maxFile {
			return Result{}, fmt.Errorf("asset unavailable or exceeds 20 MiB")
		}
		// Do not turn mislabeled HTML/SVG into active same-origin content.
		if ext != ".txt" && http.DetectContentType(data) != mime {
			return Result{}, fmt.Errorf("asset content does not match its file type")
		}
		total += int64(len(data))
		if total > maxBundle {
			return Result{}, fmt.Errorf("view exceeds 40 MiB")
		}
		stored := fmt.Sprintf("%d%s", i, ext)
		if err = os.WriteFile(filepath.Join(dir, stored), data, 0600); err != nil {
			return Result{}, err
		}
		b.Files = append(b.Files, file{Name: path.Base(rel), Stored: stored, MIME: mime})
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return Result{}, err
	}
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0600); err != nil {
		return Result{}, err
	}
	committed = true
	return Result{WebView: &View{Type: "web_view", Title: b.Title, URL: s.base + "/presentations/" + id + "/", SourceNote: in.SourceSlug, ExpiresAt: b.Expires}}, nil
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

// Descriptor-relative, no-follow opens close the check/open symlink race,
// including a directory replaced since the vault was indexed.
func openAsset(root, rel string) (*os.File, error) {
	if !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("invalid path")
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ".") || part == "" {
			unix.Close(fd)
			return nil, fmt.Errorf("invalid path")
		}
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), rel)
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("not a regular file")
	}
	return f, nil
}

func (s *Store) Cleanup() { s.mu.Lock(); defer s.mu.Unlock(); s.cleanupLocked() }
func (s *Store) cleanupLocked() {
	entries, _ := os.ReadDir(s.root)
	for _, entry := range entries {
		if !entry.IsDir() || !idPattern.MatchString(entry.Name()) {
			continue
		}
		dir := filepath.Join(s.root, entry.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		var b bundle
		if err != nil || json.Unmarshal(raw, &b) != nil || !s.now().Before(b.Expires) {
			_ = os.RemoveAll(dir)
		}
	}
}

func (s *Store) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; frame-src 'self'; style-src 'unsafe-inline'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'")
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/presentations/"), "/")
	if len(parts) > 2 || len(parts) < 1 || !idPattern.MatchString(parts[0]) {
		http.NotFound(w, r)
		return
	}
	dir := filepath.Join(s.root, parts[0])
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	var b bundle
	if err != nil || json.Unmarshal(raw, &b) != nil || !s.now().Before(b.Expires) {
		http.Error(w, "This view has expired or is unavailable. Ask your assistant to show it again.", http.StatusGone)
		return
	}
	if len(parts) == 2 && parts[1] != "" {
		for _, f := range b.Files {
			if f.Stored == parts[1] {
				w.Header().Set("Content-Type", f.MIME)
				w.Header().Set("Content-Security-Policy", "sandbox")
				http.ServeFile(w, r, filepath.Join(dir, f.Stored))
				return
			}
		}
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = page.Execute(w, struct {
		bundle
		URL string
	}{b, s.base + "/presentations/" + parts[0] + "/"})
}

// A quiet document viewer: the artifact is the hero, not dashboard chrome.
var page = template.Must(template.New("view").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title><style>
:root{color-scheme:light dark;--paper:#ffffff;--ink:#20272d;--muted:#53616d;--link:#145f9b;--line:#d5dde3}body{margin:0;background:var(--paper);color:var(--ink);font:17px/1.5 Charter,Georgia,serif}main{max-width:960px;margin:auto;padding:24px 16px}h1{font-size:1.6rem;line-height:1.2;margin:0 0 12px}p{max-width:70ch;color:var(--muted)}a{color:var(--link);text-underline-offset:3px}a:focus-visible{outline:3px solid var(--link);outline-offset:4px}figure{margin:28px 0}img{display:block;max-width:100%;height:auto;margin:12px auto}iframe{width:100%;height:75vh;border:1px solid var(--line)}figcaption{font:15px/1.5 system-ui,sans-serif;overflow-wrap:anywhere}footer{border-top:1px solid var(--line);font:14px/1.5 system-ui,sans-serif;margin-top:32px;padding-top:12px;color:var(--muted)}@media(prefers-color-scheme:dark){:root{--paper:#19232c;--ink:#eef3f7;--muted:#b7c5d0;--link:#8bc8f6;--line:#43525f}}
</style><main><h1>{{.Title}}</h1>{{if .SourceURL}}<p><a href="{{.SourceURL}}">View source note</a></p>{{end}}{{range .Files}}<figure><figcaption><a href="{{$.URL}}{{.Stored}}">Open {{.Name}}</a></figcaption>{{if or (eq .MIME "application/pdf") (eq .MIME "text/plain; charset=utf-8")}}<iframe title="{{.Name}}" src="{{$.URL}}{{.Stored}}"></iframe>{{else}}<img alt="{{.Name}}" src="{{$.URL}}{{.Stored}}">{{end}}</figure>{{end}}<footer>Temporary view · Expires {{.Expires.Format "Jan 2, 2006 at 15:04 MST"}}.<br>Original files are unchanged. Ask your assistant to show them again after expiry.</footer></main></html>`))
