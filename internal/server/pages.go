package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/base698/amythest/internal/index"
	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/vault"
)

// kanbanAwareURL is noteURL for callers that only have the base string.
func kanbanAwareURL(base, slug string) string {
	if dest, ok := markdown.KanbanBoardURL(base, slug, ""); ok {
		return dest
	}
	return base + slug
}

// noteLink is a link to a note with its URL already resolved (board notes
// need a different destination than their slug).
type noteLink struct {
	Title string
	URL   string
}

type crumb struct {
	Name string
	URL  string
}

type pageData struct {
	SiteName    string
	Base        string
	Title       string
	HTML        template.HTML
	TOC         []markdown.Heading
	Tags        []string
	Breadcrumbs []crumb
	Tree        *treeNode
	Slug        string
	Backlinks   []noteLink
	HasKanban   bool
}

// handleNote serves a rendered note, or a folder/tag listing fallback.
func (s *Server) handleNote(w http.ResponseWriter, r *http.Request) {
	v := s.vault.Load()
	slug := strings.Trim(r.URL.Path, "/")
	if dec, err := url.PathUnescape(slug); err == nil {
		slug = dec
	}

	n, ok := v.BySlug(slug)
	if !ok && slug == "" {
		n, ok = v.Resolve("Home")
	}
	if ok && s.noteOutOfSync(v, n) {
		// An out-of-band edit (git pull, iCloud sync) landed since the last
		// rescan. Reconcile before rendering so the page — including the task
		// versions embedded in its rows — matches what mutations validate
		// against; otherwise their 409 "refresh and retry" stays false until
		// the periodic rescan fires.
		if err := s.Rescan(); err != nil {
			slog.Warn("stale-note rescan", "slug", n.Slug, "err", err)
		} else {
			v = s.vault.Load()
			n, ok = v.BySlug(n.Slug)
		}
	}
	if !ok {
		if s.tryFolderPage(w, v, slug) {
			return
		}
		// Alias / renamed-note fallback: resolve like a wikilink target and
		// redirect to the canonical slug.
		if n, found := v.Resolve(strings.ReplaceAll(slug, "-", " ")); found {
			http.Redirect(w, r, s.base()+n.Slug, http.StatusFound)
			return
		}
		if n, found := v.Resolve(slug); found {
			http.Redirect(w, r, s.base()+n.Slug, http.StatusFound)
			return
		}
		s.notFound(w, r)
		return
	}

	page, err := s.db.Page(n.Slug)
	if err != nil {
		if !errors.Is(err, index.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Cache miss (e.g. file changed mid-rescan): render live.
		res, rerr := s.engine.Render(v, n)
		if rerr != nil {
			http.Error(w, rerr.Error(), http.StatusInternalServerError)
			return
		}
		page = &index.Page{Slug: n.Slug, Title: n.Title, HTML: res.HTML, TOC: res.TOC}
	}

	refs, err := s.db.Backlinks(n.Slug)
	if err != nil {
		refs = nil
	}
	backlinks := make([]noteLink, 0, len(refs))
	for _, ref := range refs {
		backlinks = append(backlinks, noteLink{Title: ref.Title, URL: s.noteURL(ref.Slug)})
	}

	s.renderPage(w, pageData{
		SiteName:    s.cfg.SiteName,
		Base:        s.base(),
		Title:       page.Title,
		HTML:        s.expandDynamicBlocks(page.HTML),
		TOC:         page.TOC,
		Tags:        page.Tags,
		Breadcrumbs: breadcrumbs(s.base(), n.Slug),
		Tree:        s.tree.Load(),
		Slug:        n.Slug,
		Backlinks:   backlinks,
	})
}

// noteOutOfSync reports whether a note's on-disk content no longer matches
// the snapshot hash — the signature of an out-of-band edit (git pull, iCloud
// sync) landing between periodic rescans. A read failure counts as out of
// sync so a deleted note 404s on the next request instead of at the next
// timer tick.
func (s *Server) noteOutOfSync(v *vault.Vault, n *vault.Note) bool {
	src, err := os.ReadFile(filepath.Join(v.Root, filepath.FromSlash(n.Path)))
	if err != nil {
		return true
	}
	sum := sha256.Sum256(src)
	return hex.EncodeToString(sum[:]) != n.Hash
}

// tryFolderPage renders a listing when the slug names a folder.
func (s *Server) tryFolderPage(w http.ResponseWriter, v *vault.Vault, slug string) bool {
	if slug == "" {
		return false
	}
	prefix := slug + "/"
	type entry struct {
		Name   string
		URL    string
		Folder bool
	}
	var notes []entry
	folders := map[string]bool{}
	for _, n := range v.Notes {
		if !strings.HasPrefix(n.Slug, prefix) {
			continue
		}
		rest := strings.TrimPrefix(n.Slug, prefix)
		if i := strings.Index(rest, "/"); i >= 0 {
			folders[rest[:i]] = true
		} else {
			notes = append(notes, entry{Name: n.Title, URL: s.noteURL(n.Slug)})
		}
	}
	if len(notes) == 0 && len(folders) == 0 {
		return false
	}

	var b bytes.Buffer
	b.WriteString("<ul class=\"listing\">")
	folderNames := make([]string, 0, len(folders))
	for f := range folders {
		folderNames = append(folderNames, f)
	}
	sort.Strings(folderNames)
	for _, f := range folderNames {
		b.WriteString(`<li class="listing-folder"><a href="` +
			template.HTMLEscapeString(s.base()+slug+"/"+f) + `">` + template.HTMLEscapeString(f) + `/</a></li>`)
	}
	sort.Slice(notes, func(i, j int) bool { return strings.ToLower(notes[i].Name) < strings.ToLower(notes[j].Name) })
	for _, n := range notes {
		b.WriteString(`<li><a class="internal" href="` + template.HTMLEscapeString(n.URL) + `">` +
			template.HTMLEscapeString(n.Name) + `</a></li>`)
	}
	b.WriteString("</ul>")

	s.renderPage(w, pageData{
		SiteName:    s.cfg.SiteName,
		Base:        s.base(),
		Title:       path.Base(slug),
		HTML:        template.HTML(b.String()),
		Breadcrumbs: breadcrumbs(s.base(), slug+"/x"),
		Tree:        s.tree.Load(),
		Slug:        slug,
	})
	return true
}

// handleTags serves /tags/ (all tags) and /tags/{tag} (notes with tag).
func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	tag := strings.Trim(strings.TrimPrefix(r.URL.Path, "/tags"), "/")
	if dec, err := url.PathUnescape(tag); err == nil {
		tag = dec
	}
	var b bytes.Buffer
	title := "Tags"
	if tag == "" {
		tags, err := s.db.Tags()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		names := make([]string, 0, len(tags))
		for t := range tags {
			names = append(names, t)
		}
		sort.Strings(names)
		b.WriteString(`<ul class="listing tag-listing">`)
		for _, t := range names {
			b.WriteString(`<li><a class="tag-link" href="` + template.HTMLEscapeString(s.base()+"tags/"+t) + `">#` +
				template.HTMLEscapeString(t) + `</a> <span class="count">` +
				template.HTMLEscapeString(itoa(tags[t])) + `</span></li>`)
		}
		b.WriteString("</ul>")
	} else {
		title = "#" + tag
		refs, err := s.db.TagNotes(tag)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		b.WriteString(`<ul class="listing">`)
		for _, ref := range refs {
			b.WriteString(`<li><a class="internal" href="` + template.HTMLEscapeString(s.noteURL(ref.Slug)) + `">` +
				template.HTMLEscapeString(ref.Title) + `</a></li>`)
		}
		b.WriteString("</ul>")
	}

	s.renderPage(w, pageData{
		SiteName:    s.cfg.SiteName,
		Base:        s.base(),
		Title:       title,
		HTML:        template.HTML(b.String()),
		Breadcrumbs: []crumb{{Name: "Home", URL: s.base()}, {Name: "Tags", URL: s.base() + "tags/"}},
		Tree:        s.tree.Load(),
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func (s *Server) notFound(w http.ResponseWriter, _ *http.Request) {
	s.renderPageStatus(w, http.StatusNotFound, pageData{
		SiteName: s.cfg.SiteName,
		Base:     s.base(),
		Title:    "Not found",
		HTML:     "<p>That page doesn't exist (or hasn't been synced yet).</p>",
		Tree:     s.tree.Load(),
	})
}

func (s *Server) renderPage(w http.ResponseWriter, data pageData) {
	s.renderPageStatus(w, http.StatusOK, data)
}

func (s *Server) renderPageStatus(w http.ResponseWriter, status int, data pageData) {
	data.HasKanban = s.kanban != nil
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "base.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// inlineAssetExts are content types browsers may render in place without a
// script execution risk on our origin. Everything else downloads as an
// opaque attachment; SVG stays inline but sandboxed since it can script.
var inlineAssetExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".avif": true, ".bmp": true, ".ico": true,
	".mp3": true, ".m4a": true, ".wav": true, ".ogg": true, ".opus": true, ".flac": true,
	".mp4": true, ".webm": true, ".mov": true, ".m4v": true,
	".pdf": true, ".txt": true,
}

// handleAsset serves non-markdown vault files under /assets/. Only files the
// scan recorded are served — never symlinks or paths the scan skipped.
func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	v := s.vault.Load()
	rel := strings.TrimPrefix(r.URL.Path, "/assets/")
	if dec, err := url.PathUnescape(rel); err == nil {
		rel = dec
	}
	abs, ok := v.AssetPath(rel)
	if !ok || !v.HasAsset(rel) {
		http.NotFound(w, r)
		return
	}
	if st, err := os.Lstat(abs); err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	ext := strings.ToLower(filepath.Ext(abs))
	switch {
	case ext == ".svg":
		w.Header().Set("Content-Security-Policy", "sandbox")
	case !inlineAssetExts[ext]:
		w.Header().Set("Content-Disposition", "attachment")
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	http.ServeFile(w, r, abs)
}

// noteURL is the canonical link to a note. Board notes go to the board UI:
// their markdown path lands in the SPA, which reads trailing segments as
// label filters and would show an empty board.
func (s *Server) noteURL(slug string) string {
	return kanbanAwareURL(s.base(), slug)
}

func (s *Server) base() string {
	b := s.cfg.BaseURL
	if !strings.HasSuffix(b, "/") {
		b += "/"
	}
	return b
}

func breadcrumbs(base string, slug string) []crumb {
	crumbs := []crumb{{Name: "Home", URL: base}}
	segs := strings.Split(slug, "/")
	for i, seg := range segs[:len(segs)-1] {
		crumbs = append(crumbs, crumb{
			Name: seg,
			URL:  base + strings.Join(segs[:i+1], "/"),
		})
	}
	return crumbs
}

// ---- explorer tree ----

type treeNode struct {
	Name     string
	URL      string // base-prefixed link; empty for folders without an index note
	IsDir    bool
	Children []*treeNode
}

func buildTree(v *vault.Vault, base string) *treeNode {
	root := &treeNode{IsDir: true}
	dirs := map[string]*treeNode{"": root}

	var mkdir func(p string) *treeNode
	mkdir = func(p string) *treeNode {
		if n, ok := dirs[p]; ok {
			return n
		}
		parent := mkdir(parentPath(p))
		n := &treeNode{Name: path.Base(p), IsDir: true}
		parent.Children = append(parent.Children, n)
		dirs[p] = n
		return n
	}

	for _, n := range v.Notes {
		node := mkdir(parentPath(n.Path))
		name := strings.TrimSuffix(path.Base(n.Path), ".md")
		if name == "index" {
			node.URL = kanbanAwareURL(base, n.Slug)
			continue
		}
		node.Children = append(node.Children, &treeNode{Name: n.Title, URL: kanbanAwareURL(base, n.Slug)})
	}
	sortTree(root)
	return root
}

func parentPath(p string) string {
	d := path.Dir(p)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

func sortTree(n *treeNode) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	for _, c := range n.Children {
		sortTree(c)
	}
}
