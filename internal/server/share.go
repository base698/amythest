package server

import (
	"html/template"
	"net/http"
	"time"
)

// handleSharePage renders the /share capture page.
func (s *Server) handleSharePage(w http.ResponseWriter, r *http.Request) {
	html := `
<div class="share-page">
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
  <div id="share-status" class="muted" role="status"></div>
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
// note, kicks plugins off in the background, and reindexes.
func (s *Server) handleShareUpload(w http.ResponseWriter, r *http.Request) {
	if s.kanbanAuth != nil {
		cookie, err := r.Cookie(kanbanSessionCookie)
		if err != nil {
			http.Error(w, "sign in to the kanban to share", http.StatusUnauthorized)
			return
		}
		if _, err := s.kanbanAuth.Verify(cookie.Value, time.Now()); err != nil {
			http.Error(w, "session expired: sign in to the kanban again", http.StatusUnauthorized)
			return
		}
	}
	if s.share == nil {
		http.Error(w, "share not configured", http.StatusNotFound)
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	mime := header.Header.Get("Content-Type")
	title := r.FormValue("title")
	up, err := s.share.SaveUpload(header.Filename, title, mime, file, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Rescan(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Plugins may take minutes (e.g. transcription); never block the upload.
	go func() {
		s.share.RunPlugins(up, mime, title)
		if err := s.Rescan(); err == nil {
			return
		}
	}()

	s.writeJSON(w, map[string]any{
		"ok":      true,
		"note":    up.NoteSlug,
		"noteURL": s.base() + up.NoteSlug,
		"asset":   up.AssetRel,
		"plugins": len(s.share.Plugins()),
	})
}
