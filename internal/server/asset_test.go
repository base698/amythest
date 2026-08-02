package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/base698/amythest/internal/config"
	"github.com/base698/amythest/internal/vault"
)

func TestHandleAssetServesOnlyScannedRegularFiles(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"Assets/pic.png":     "png-bytes",
		"Assets/page.html":   "<script>alert(1)</script>",
		".obsidian/app.json": `{"secret":true}`,
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("host file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "Assets", "link.png")); err != nil {
		t.Fatal(err)
	}

	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: config.Config{Vault: root, BaseURL: "/"}}
	s.vault.Store(v)

	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleAsset(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}

	if rec := get("/assets/Assets/pic.png"); rec.Code != http.StatusOK {
		t.Fatalf("scanned asset: status=%d", rec.Code)
	} else if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("asset response missing nosniff")
	}
	rec := get("/assets/Assets/page.html")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Disposition") != "attachment" {
		t.Fatalf("html must download as attachment: status=%d disposition=%q", rec.Code, rec.Header().Get("Content-Disposition"))
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("html served with active content type %q", got)
	}
	for _, target := range []string{
		"/assets/.obsidian/app.json",     // scan-skipped file
		"/assets/Assets/link.png",        // symlink inside the vault
		"/assets/../" + filepath.Base(outside), // traversal
		"/assets/Assets/missing.png",
	} {
		if rec := get(target); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status=%d, want 404", target, rec.Code)
		}
	}
}
