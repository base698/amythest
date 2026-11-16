package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"github.com/base698/amythest/internal/config"
	"github.com/base698/amythest/internal/work"
	"mime"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestWorkMediaPreviewDownloadAndAuthentication(t *testing.T) {
	t.Setenv("AMYTHEST_USERNAME", "owner")
	t.Setenv("AMYTHEST_PASSWORD", "correct horse")
	t.Setenv("AMYTHEST_SESSION_SECRET", "01234567890123456789012345678901")
	t.Setenv("KANBAN_USERNAME", "")
	t.Setenv("KANBAN_PASSWORD", "")
	t.Setenv("KANBAN_SESSION_SECRET", "")
	s := hardenedServer(t, func(c *config.Config) {
		c.AuthMode = "private"
		c.AuthPublicURL = "https://notes.example.com"
		c.Work.Enabled = true
	})
	reader, _, e := s.kanbanAuth.CreateCredential("reader", "reader", time.Now(), time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	image := []byte("\x89PNG\r\n\x1a\nfixture")
	sum := sha256.Sum256(image)
	digest := hex.EncodeToString(sum[:])
	if _, e = s.work.Upload("collector", digest, bytes.NewReader(image)); e != nil {
		t.Fatal(e)
	}
	request := func(query, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/work/media/"+digest+query, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := request("", ""); w.Code != 401 {
		t.Fatal("anonymous media access", w.Code)
	}
	w := request("", reader)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Content-Disposition") != "" || !bytes.Equal(w.Body.Bytes(), image) {
		t.Fatal(w.Code, w.Header())
	}
	w = request("?download=1&filename="+url.QueryEscape("../sample image.png"), reader)
	kind, params, e := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
	if e != nil || kind != "attachment" || params["filename"] != "sample image.png" || !bytes.Equal(w.Body.Bytes(), image) {
		t.Fatal(w.Header(), e)
	}
	w = request("?download=1&filename="+url.QueryEscape("bad\r\nheader.png"), reader)
	_, params, e = mime.ParseMediaType(w.Header().Get("Content-Disposition"))
	if e != nil || params["filename"] != "work-file" {
		t.Fatal(w.Header(), e)
	}
	// Only actual commit artifacts may be rendered as plain text, and they remain private.
	if w = request("?view=diff", reader); w.Code != 404 {
		t.Fatal("non-commit exposed as diff", w.Code)
	}
	patch := []byte("commit example\n\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n")
	patchSum := sha256.Sum256(patch)
	digest = hex.EncodeToString(patchSum[:])
	if _, e = s.work.Upload("collector", digest, bytes.NewReader(patch)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.work.Stream("collector", "generation"); e != nil {
		t.Fatal(e)
	}
	at := time.Now()
	_, e = s.work.Ingest("collector", work.Batch{Generation: "generation", Events: []work.Event{
		{Seq: 1, At: at, Kind: "slot.seen", Pane: "pane", Session: "session"},
		{Seq: 2, At: at, Kind: "run.start", Pane: "pane", Session: "session", Run: "run"},
		{Seq: 3, At: at, Kind: "artifact", Pane: "pane", Session: "session", Run: "run", Artifact: &work.Artifact{Key: "commit", Kind: "commit", Ref: "abc", MediaHash: digest}},
	}})
	if e != nil {
		t.Fatal(e)
	}
	w = request("?view=diff", reader)
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" || w.Header().Get("Content-Disposition") != "" || !bytes.Equal(w.Body.Bytes(), patch) {
		t.Fatal(w.Code, w.Header())
	}
	if w = request("?view=diff", ""); w.Code != 401 {
		t.Fatal("anonymous diff allowed", w.Code)
	}

}
