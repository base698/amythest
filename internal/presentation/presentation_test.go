package presentation

import (
	"encoding/base64"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/base698/amythest/internal/vault"
)

func fixture(t *testing.T) (*Store, *vault.Vault, []byte) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(root, "Assets"), 0755); err != nil {
		t.Fatal(err)
	}
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aX1sAAAAASUVORK5CYII=")
	for rel, data := range map[string][]byte{"Assets/sample.png": png, "Assets/reference.pdf": []byte("%PDF-1.7\nsynthetic"), "Assets/readme.txt": []byte("Synthetic reference"), "Assets/active.html": []byte("<script>bad()</script>"), "Example.md": []byte("# Example\n![[Assets/sample.png]]")} {
		if err = os.WriteFile(filepath.Join(root, rel), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(t.TempDir(), "/notes")
	if err != nil {
		t.Fatal(err)
	}
	return s, v, png
}

func get(s *Store, raw string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", strings.TrimPrefix(raw, "/notes"), nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestFindCreateRenderPersistExpire(t *testing.T) {
	s, v, png := fixture(t)
	found, err := s.Execute(v, Input{Action: "search", Query: "sample"})
	if err != nil || len(found.Assets) != 1 || found.Assets[0] != "Assets/sample.png" {
		t.Fatalf("search %+v %v", found, err)
	}
	out, err := s.Execute(v, Input{Action: "open", SourceSlug: "Example", Title: "Example <script>bad()</script>", Assets: found.Assets})
	if err != nil {
		t.Fatal(err)
	}
	view := out.WebView
	if view.Type != "web_view" || !strings.HasPrefix(view.URL, "/notes/presentations/") || time.Until(view.ExpiresAt) < 23*time.Hour {
		t.Fatalf("contract %+v", view)
	}
	page := get(s, view.URL)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "<img") || strings.Contains(page.Body.String(), "<script>") || !strings.Contains(page.Body.String(), "/notes/Example") {
		t.Fatalf("page %d %s", page.Code, page.Body.String())
	}
	asset := get(s, view.URL+"0.png")
	if asset.Code != 200 || asset.Body.String() != string(png) || asset.Header().Get("Cache-Control") != "private, no-store" || asset.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("asset %d %+v", asset.Code, asset.Header())
	}
	if st, _ := os.Stat(filepath.Join(s.root, strings.Split(view.URL, "/")[3], "0.png")); st.Mode().Perm() != 0600 {
		t.Fatal("private copy permissions")
	}
	original, _ := os.ReadFile(filepath.Join(v.Root, "Assets/sample.png"))
	if string(original) != string(png) {
		t.Fatal("source changed")
	}
	restarted, err := New(s.root, "/notes")
	if err != nil || get(restarted, view.URL).Code != 200 {
		t.Fatal("view did not survive restart", err)
	}
	s.now = func() time.Time { return view.ExpiresAt.Add(time.Second) }
	if get(s, view.URL).Code != 410 || get(s, view.URL+"0.png").Code != 410 {
		t.Fatal("expired copy still served")
	}
	s.Cleanup()
	entries, _ := os.ReadDir(s.root)
	if len(entries) != 0 {
		t.Fatal("expired copies not cleaned")
	}
}

func TestCanonicalNoteAndMultiFileView(t *testing.T) {
	s, v, _ := fixture(t)
	out, err := s.Execute(v, Input{Action: "open", SourceSlug: "Example"})
	if err != nil || out.WebView.URL != "/notes/Example" {
		t.Fatalf("note %+v %v", out, err)
	}
	entries, _ := os.ReadDir(s.root)
	if len(entries) != 0 {
		t.Fatal("note should not be copied")
	}
	out, err = s.Execute(v, Input{Action: "open", Assets: []string{"Assets/reference.pdf", "Assets/readme.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(get(s, out.WebView.URL).Body.String(), "<iframe") || get(s, out.WebView.URL+"0.pdf").Header().Get("Content-Type") != "application/pdf" {
		t.Fatal("PDF view unavailable")
	}
}

func TestRejectUnsafeUnindexedAndMislabeledAssets(t *testing.T) {
	s, v, _ := fixture(t)
	for _, input := range []Input{
		{Action: "open", Assets: []string{"../outside.png"}}, {Action: "open", Assets: []string{"/etc/passwd"}},
		{Action: "open", Assets: []string{"Assets/../Assets/sample.png"}}, {Action: "open", Assets: []string{"Assets/active.html"}},
		{Action: "open", Assets: []string{".env"}}, {Action: "open", SourceSlug: "Missing"}, {Action: "open"},
		{Action: "search"}, {Action: "search", Query: "sample", Offset: -1},
	} {
		if _, err := s.Execute(v, input); err == nil {
			t.Errorf("accepted %+v", input)
		}
	}
	if err := os.WriteFile(filepath.Join(v.Root, "Assets/sample.png"), []byte("<html>not an image</html>"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(v, Input{Action: "open", Assets: []string{"Assets/sample.png"}}); err == nil {
		t.Fatal("accepted disguised HTML")
	}
	entries, _ := os.ReadDir(s.root)
	if len(entries) != 0 {
		t.Fatal("failed operation left files")
	}
}

func TestRejectSymlinkSwapAfterScan(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[directory], func(t *testing.T) {
			s, v, png := fixture(t)
			outside := t.TempDir()
			os.WriteFile(filepath.Join(outside, "sample.png"), png, 0600)
			original := filepath.Join(v.Root, "Assets/sample.png")
			target := filepath.Join(outside, "sample.png")
			if directory {
				original = filepath.Join(v.Root, "Assets")
				target = outside
			}
			if err := os.Rename(original, original+".saved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, original); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Execute(v, Input{Action: "open", Assets: []string{"Assets/sample.png"}}); err == nil {
				t.Fatal("followed symlink")
			}
		})
	}
}

func TestSizeLimitAndSearchPaging(t *testing.T) {
	s, v, _ := fixture(t)
	f, err := os.Create(filepath.Join(v.Root, "Assets/large.txt"))
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(maxFile + 1)
	f.Close()
	for i := 0; i < 32; i++ {
		os.WriteFile(filepath.Join(v.Root, "Assets", string(rune('A'+i))+"-entry.txt"), []byte("sample"), 0600)
	}
	v, _ = vault.Scan(v.Root)
	if _, err = s.Execute(v, Input{Action: "open", Assets: []string{"Assets/large.txt"}}); err == nil {
		t.Fatal("oversized file accepted")
	}
	first, err := s.Execute(v, Input{Action: "search", Query: "entry"})
	if err != nil || len(first.Assets) != 30 || first.NextOffset == nil {
		t.Fatal("first page", err)
	}
	next, err := s.Execute(v, Input{Action: "search", Query: "entry", Offset: *first.NextOffset})
	if err != nil || len(next.Assets) != 2 || next.NextOffset != nil {
		t.Fatal("next page", err)
	}
}
