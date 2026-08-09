package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/base698/amythest/internal/share"
)

// handleSharePage renders the /share capture page.
func (s *Server) handleSharePage(w http.ResponseWriter, r *http.Request) {
	html := `
<div class="share-page">
  <section class="share-section" aria-labelledby="share-text-heading">
    <h2 id="share-text-heading">Quick text note</h2>
    <p>Save a title and optional description directly to <code>_Inbox/</code>.</p>
    <label class="share-title-label">Title
      <input id="share-text-title" type="text" maxlength="120" required autocomplete="off">
    </label>
    <label class="share-description-label">Description <span class="hint">optional</span>
      <textarea id="share-text-description" maxlength="20000" rows="6"></textarea>
    </label>
    <button id="share-text-save" type="button" class="share-primary" disabled>Save text note</button>
    <div id="share-text-status" class="muted share-status" role="status"></div>
  </section>
  <section class="share-section" aria-labelledby="share-upload-heading">
  <h2 id="share-upload-heading">File or voice memo</h2>
  <p>Record a voice memo or drop a file; it lands in the vault under
  <code>Assets/Share/</code> with a note in <code>_Inbox/</code>.` +
		s.sharePluginsBlurb() + `</p>
  <div class="share-controls">
    <button id="share-record" type="button" class="share-primary">● Record</button>
    <span id="share-timer" class="muted" hidden>0:00</span>
    <label class="share-file-label">or choose a file
      <input id="share-file" type="file">
    </label>
  </div>
  <label class="share-title-label">Title <span class="hint">optional</span>
    <input id="share-title" type="text" maxlength="120" placeholder="Share ` + time.Now().Format("2006-01-02 15:04") + `">
  </label>
  <button id="share-upload" type="button" class="share-primary" disabled>Upload to notes</button>
  <div id="share-status" class="muted share-status" role="status"></div>
  </section>
</div>`
	s.renderPage(w, pageData{
		SiteName:    s.cfg.SiteName,
		Base:        s.base(),
		Title:       "Share to notes",
		HTML:        template.HTML(html),
		Breadcrumbs: []crumb{{Name: "Home", URL: s.base()}},
		Tree:        s.tree.Load(),
		Slug:        "share",
	})
}

func (s *Server) sharePluginsBlurb() string {
	if s.share == nil || len(s.share.Plugins()) == 0 {
		return ""
	}
	return ` Active processors will enrich the note automatically (e.g. transcription).`
}

// handleShareUpload accepts the multipart upload, stores it, creates the
// note, indexes just that note, and kicks plugins off in the background.
//
// Everything on this path is scoped to the one note being created. The old
// code called Rescan here, which re-walks and re-hashes the whole vault under
// the exclusive cross-process vault lock and — because a new slug changes link
// resolution globally — re-renders every note before the response was written.
// That put a full-corpus reconcile (hundreds of ms per thousand notes, seconds
// on the Pi) inside every upload and serialized concurrent uploads behind it.
func (s *Server) handleShareUpload(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "share") {
		return
	}
	if s.share == nil {
		http.Error(w, "share not configured", http.StatusNotFound)
		return
	}

	// Cap the request body before reading any of it.
	r.Body = http.MaxBytesReader(w, r.Body, share.MaxUploadBytes+(1<<20))
	up, mime, title, status, err := s.readShareUpload(r)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}

	// The client navigates straight to noteURL, so the note must be resolvable
	// and renderable before we answer — but only the note must be.
	if err := s.indexShareUpload(up); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Plugins may take minutes (e.g. transcription); never block the upload.
	// The reconcile afterwards both picks up whatever the plugins appended and
	// performs the global re-render the new slug calls for.
	go func() {
		s.share.RunPlugins(up, mime, title)
		s.scheduleFullReconcile()
	}()

	s.writeJSON(w, map[string]any{
		"ok":      true,
		"note":    up.NoteSlug,
		"noteURL": s.base() + up.NoteSlug,
		"asset":   up.AssetRel,
		"plugins": len(s.share.Plugins()),
	})
}

const (
	maxShareTextBodyBytes    = 100 << 10
	maxShareTitleRunes       = 120
	maxShareDescriptionRunes = 20000
)

// handleShareText creates a plain text note directly in _Inbox without an
// uploaded asset. Title is required; description is optional.
func (s *Server) handleShareText(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "share") {
		return
	}
	if s.share == nil {
		http.Error(w, "share not configured", http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxShareTextBodyBytes)
	var in struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "invalid text note", http.StatusBadRequest)
		return
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid text note", http.StatusBadRequest)
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(in.Title) > maxShareTitleRunes {
		http.Error(w, "title is too long", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(in.Description) > maxShareDescriptionRunes {
		http.Error(w, "description is too long", http.StatusBadRequest)
		return
	}
	up, err := s.share.CreateTextNote(in.Title, in.Description, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.indexShareUpload(up); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.scheduleFullReconcile()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	s.writeJSON(w, map[string]any{
		"ok":      true,
		"note":    up.NoteSlug,
		"noteURL": s.base() + up.NoteSlug,
	})
}

// maxShareFieldBytes caps the non-file multipart fields (just "title").
const maxShareFieldBytes = 4 << 10

// readShareUpload streams the multipart body: the file part goes straight to
// its final home in the vault, other fields are read into memory. Using the
// streaming reader instead of ParseMultipartForm avoids buffering up to 32 MiB
// in RAM and spooling anything larger to a temp file that is then copied into
// the vault — a doubled write of every photo and video on a Pi's SD card.
// Field order does not matter: the note is created once the parts are done.
// The returned status is the one to report when err is non-nil: a malformed
// or oversized body is the client's fault, a failed write is ours.
func (s *Server) readShareUpload(r *http.Request) (up *share.Upload, mime, title string, status int, err error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, "", "", http.StatusBadRequest, err
	}
	now := time.Now()
	var asset *share.Asset
	for {
		part, perr := mr.NextPart()
		if errors.Is(perr, io.EOF) {
			break
		}
		if perr != nil {
			return nil, "", "", http.StatusBadRequest, perr
		}
		switch {
		case part.FormName() == "file" && asset == nil:
			mime = part.Header.Get("Content-Type")
			// A read failure here is the request's (truncated, over the cap);
			// anything else means the vault write failed.
			asset, perr = s.share.SaveAsset(part.FileName(), part, now)
			if perr != nil {
				_ = part.Close()
				return nil, "", "", http.StatusBadRequest, perr
			}
		case part.FormName() == "title":
			var b []byte
			b, perr = io.ReadAll(io.LimitReader(part, maxShareFieldBytes))
			title = string(b)
		}
		_ = part.Close()
		if perr != nil {
			return nil, "", "", http.StatusBadRequest, perr
		}
	}
	if asset == nil {
		return nil, "", "", http.StatusBadRequest, errors.New("missing file field")
	}
	up, err = s.share.CreateNote(asset, title, mime, now)
	if err != nil {
		return nil, "", "", http.StatusInternalServerError, err
	}
	return up, mime, title, http.StatusOK, nil
}

// indexShareUpload publishes a just-written upload without a vault walk: the
// snapshot is derived from the current one plus the two new files, and only
// the new note is rendered into the index. rescanMu serializes this swap
// against full rescans so neither drops the other's snapshot.
//
// It deliberately does not take the cross-process vault lock. That lock exists
// so a scan never observes another process mid-write; here the only files read
// are the two this request just created, and every other file's metadata is
// carried over from the previous snapshot untouched — exactly as stale as it
// already was between rescans, and never torn.
func (s *Server) indexShareUpload(up *share.Upload) error {
	s.rescanMu.Lock()
	defer s.rescanMu.Unlock()
	rels := []string{up.NoteRel}
	if up.AssetRel != "" {
		rels = append(rels, up.AssetRel)
	}
	v := s.vault.Load().WithFiles(rels...)
	n, ok := v.BySlug(up.NoteSlug)
	if !ok {
		return fmt.Errorf("share note %q vanished before indexing", up.NoteRel)
	}
	if err := s.db.IndexNote(v, s.engine, n); err != nil {
		return err
	}
	s.vault.Store(v)
	s.tree.Store(buildTree(v, s.base()))
	return nil
}
