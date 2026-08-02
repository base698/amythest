package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/base698/amythest/internal/config"
	"github.com/base698/amythest/internal/kanban/auth"
)

// withSessionTB is withSession (authz_test.go) for benchmarks, which get a
// testing.TB rather than a *testing.T.
func withSessionTB(tb testing.TB, s *Server) func(*http.Request) *http.Request {
	tb.Helper()
	manager, err := auth.NewManager("test", "test-password-123", []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err != nil {
		tb.Fatal(err)
	}
	token, _, err := manager.NewSession(time.Now())
	if err != nil {
		tb.Fatal(err)
	}
	s.kanbanAuth = manager
	return func(r *http.Request) *http.Request {
		r.AddCookie(&http.Cookie{Name: kanbanSessionCookie, Value: token})
		return r
	}
}

// synthVault writes n notes with realistic-ish bodies (headings, wikilinks,
// tasks, code) so scan+render costs resemble a real vault.
func synthVault(tb testing.TB, n int) string {
	tb.Helper()
	root, err := filepath.EvalSymlinks(tb.TempDir())
	if err != nil {
		tb.Fatal(err)
	}
	for i := range n {
		dir := filepath.Join(root, fmt.Sprintf("Area%02d", i%20))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "---\ntitle: Note %04d\ntags: [synthetic, area%02d]\ncreated: 2024-01-02\n---\n\n", i, i%20)
		fmt.Fprintf(&b, "# Note %04d\n\nIntro paragraph linking [[Note %04d]] and [[Note %04d]].\n\n",
			i, (i+1)%n, (i+7)%n)
		for s := range 6 {
			fmt.Fprintf(&b, "## Section %d\n\nSome prose about #topic%d with `inline code` and a [link](https://example.com/%d).\n\n",
				s, s, s)
			fmt.Fprintf(&b, "- [ ] task %d-%d\n- [x] done %d-%d\n\n```go\nfunc f%d() int { return %d }\n```\n\n", i, s, i, s, s, s)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("Note %04d.md", i)), []byte(b.String()), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return root
}

func shareUploadRequest(tb testing.TB, title string, size int) *http.Request {
	return shareUploadRequestNamed(tb, "memo.m4a", title, size)
}

func shareUploadRequestNamed(tb testing.TB, filename, title string, size int) *http.Request {
	tb.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := fw.Write(bytes.Repeat([]byte("a"), size)); err != nil {
		tb.Fatal(err)
	}
	if err := mw.WriteField("title", title); err != nil {
		tb.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		tb.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/share/upload", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r
}

func newShareTestServer(tb testing.TB, notes int) (*Server, func(*http.Request) *http.Request) {
	tb.Helper()
	root := synthVault(tb, notes)
	s, err := New(config.Config{Vault: root, DataDir: tb.TempDir(), BaseURL: "/", SiteName: "Bench"})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { s.Close() })
	// authorizeTestServer needs *testing.T; inline the equivalent for TB.
	stamp := withSessionTB(tb, s)
	return s, stamp
}

// TestShareUploadLatency reports end-to-end handler latency over a synthetic
// vault. Run with -v to see the numbers:
//
//	go test ./internal/server -run TestShareUploadLatency -v
func TestShareUploadLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("latency measurement")
	}
	for _, notes := range []int{400} {
		t.Run(fmt.Sprintf("notes=%d", notes), func(t *testing.T) {
			s, stamp := newShareTestServer(t, notes)
			var total time.Duration
			const iters = 5
			for i := range iters {
				req := stamp(shareUploadRequest(t, fmt.Sprintf("Memo %d", i), 64<<10))
				rec := httptest.NewRecorder()
				start := time.Now()
				s.ServeHTTP(rec, req)
				elapsed := time.Since(start)
				total += elapsed
				if rec.Code != http.StatusOK {
					t.Fatalf("upload %d: status %d: %s", i, rec.Code, rec.Body.String())
				}
				t.Logf("upload %d: %v", i, elapsed)
			}
			t.Logf("notes=%d mean handler latency: %v", notes, total/iters)
		})
	}
}

// TestShareUploadConcurrentLatency measures the tail when uploads overlap.
func TestShareUploadConcurrentLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("latency measurement")
	}
	s, stamp := newShareTestServer(t, 400)
	const n = 4
	var wg sync.WaitGroup
	durations := make([]time.Duration, n)
	codes := make([]int, n)
	bodies := make([]string, n)
	start := time.Now()
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := stamp(shareUploadRequestNamed(t,
				fmt.Sprintf("memo%d.m4a", i), fmt.Sprintf("Concurrent %d", i), 64<<10))
			rec := httptest.NewRecorder()
			t0 := time.Now()
			s.ServeHTTP(rec, req)
			durations[i] = time.Since(t0)
			codes[i] = rec.Code
			bodies[i] = rec.Body.String()
		}()
	}
	wg.Wait()
	wall := time.Since(start)
	var worst time.Duration
	for i, d := range durations {
		if codes[i] != http.StatusOK {
			t.Fatalf("upload %d: status %d: %s", i, codes[i], bodies[i])
		}
		if d > worst {
			worst = d
		}
	}
	t.Logf("4 concurrent uploads: wall=%v worst=%v durations=%v", wall, worst, durations)
}

// TestShareUploadBackgroundReconcileFixesDanglingLink covers what the
// incremental index insert deliberately does not do: a note written earlier
// with [[Voice memo gamma]] renders that link broken until the new slug
// triggers a global re-render. The upload response must not wait for it, but
// the background pass must deliver it.
func TestShareUploadBackgroundReconcileFixesDanglingLink(t *testing.T) {
	s, stamp := newShareTestServer(t, 10)
	s.reconcileDelay = 5 * time.Millisecond

	root := s.cfg.Vault
	if err := os.WriteFile(filepath.Join(root, "Referrer.md"),
		[]byte("# Referrer\n\nSee [[Voice memo gamma]].\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Rescan(); err != nil {
		t.Fatal(err)
	}
	before := httptest.NewRecorder()
	s.ServeHTTP(before, httptest.NewRequest(http.MethodGet, "/Referrer", nil))
	if !strings.Contains(before.Body.String(), "internal broken") {
		t.Fatalf("expected a broken link before upload: %s", before.Body.String())
	}

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, stamp(shareUploadRequest(t, "Voice memo gamma", 256)))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: status %d: %s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		after := httptest.NewRecorder()
		s.ServeHTTP(after, httptest.NewRequest(http.MethodGet, "/Referrer", nil))
		if !strings.Contains(after.Body.String(), "internal broken") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("dangling link never resolved after upload: %s", after.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestShareUploadSameSecondSameName guards the asset-name collision: two
// phone shares of "image.jpg" in the same second must both land, with
// distinct assets and distinct notes.
func TestShareUploadSameSecondSameName(t *testing.T) {
	s, stamp := newShareTestServer(t, 10)
	seen := map[string]bool{}
	for i := range 3 {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, stamp(shareUploadRequestNamed(t, "image.jpg", "", 512)))
		if rec.Code != http.StatusOK {
			t.Fatalf("upload %d: status %d: %s", i, rec.Code, rec.Body.String())
		}
		var resp struct {
			Note    string `json:"note"`
			NoteURL string `json:"noteURL"`
			Asset   string `json:"asset"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if seen[resp.Asset] {
			t.Fatalf("upload %d reused asset path %q (previous upload overwritten)", i, resp.Asset)
		}
		seen[resp.Asset] = true
		page := httptest.NewRecorder()
		s.ServeHTTP(page, httptest.NewRequest(http.MethodGet, resp.NoteURL, nil))
		if page.Code != http.StatusOK {
			t.Fatalf("upload %d: GET %s status %d", i, resp.NoteURL, page.Code)
		}
	}
}

// TestShareUploadNoteImmediatelyResolvable is the correctness guard for the
// fast path: whatever noteURL the upload returns must render (not 404) on the
// very next request, with no rescan in between.
func TestShareUploadNoteImmediatelyResolvable(t *testing.T) {
	s, stamp := newShareTestServer(t, 25)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, stamp(shareUploadRequest(t, "Voice memo alpha", 2048)))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Note    string `json:"note"`
		NoteURL string `json:"noteURL"`
		Asset   string `json:"asset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.NoteURL == "" {
		t.Fatal("empty noteURL")
	}

	page := httptest.NewRecorder()
	s.ServeHTTP(page, httptest.NewRequest(http.MethodGet, resp.NoteURL, nil))
	if page.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d (note not reachable right after upload)", resp.NoteURL, page.Code)
	}
	if !strings.Contains(page.Body.String(), "Voice memo alpha") {
		t.Errorf("rendered note missing title; body: %s", page.Body.String()[:min(400, page.Body.Len())])
	}

	// The embedded asset must serve too — the note renders ![[asset]].
	asset := httptest.NewRecorder()
	s.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/"+resp.Asset, nil))
	if asset.Code != http.StatusOK {
		t.Fatalf("GET /assets/%s: status %d (asset not reachable right after upload)", resp.Asset, asset.Code)
	}

	// Search must find it without waiting for the periodic rescan.
	search := httptest.NewRecorder()
	s.ServeHTTP(search, httptest.NewRequest(http.MethodGet, "/api/search?q=alpha", nil))
	if search.Code != http.StatusOK {
		t.Fatalf("search: status %d", search.Code)
	}
	if !strings.Contains(search.Body.String(), resp.Note) {
		t.Errorf("new note %q not in search results: %s", resp.Note, search.Body.String())
	}

	// A later full rescan must agree with the incremental insert: no
	// duplicate rows, note still there.
	if err := s.Rescan(); err != nil {
		t.Fatal(err)
	}
	after := httptest.NewRecorder()
	s.ServeHTTP(after, httptest.NewRequest(http.MethodGet, resp.NoteURL, nil))
	if after.Code != http.StatusOK {
		t.Fatalf("GET %s after rescan: status %d", resp.NoteURL, after.Code)
	}
}
