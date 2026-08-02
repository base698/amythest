package markdown_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/vault"
)

func TestRenderedTaskCheckboxCarriesExactFileVersion(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Task.md"), []byte("- [ ] Ship it\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := markdown.New("/").Render(v, v.Notes[0])
	if err != nil {
		t.Fatal(err)
	}
	if want := `data-version="` + v.Notes[0].Hash + `"`; !strings.Contains(string(res.HTML), want) {
		t.Fatalf("rendered checkbox missing %s: %s", want, res.HTML)
	}
}

// TestFullVaultSmoke renders every note in a real vault. Run with:
//
//	AMYTHEST_SMOKE_VAULT=~/notes go test ./internal/markdown -run Smoke -v
func TestFullVaultSmoke(t *testing.T) {
	root := os.Getenv("AMYTHEST_SMOKE_VAULT")
	if root == "" {
		t.Skip("set AMYTHEST_SMOKE_VAULT to run")
	}
	v, err := vault.Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(v.Notes) == 0 {
		t.Fatal("no notes found")
	}

	e := markdown.New("/")
	var rendered, fmErrs, links, broken int
	for _, n := range v.Notes {
		if n.FM.Err != nil {
			fmErrs++
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic rendering %s: %v", n.Path, r)
				}
			}()
			res, err := e.Render(v, n)
			if err != nil {
				t.Errorf("render %s: %v", n.Path, err)
				return
			}
			rendered++
			links += len(res.Links)
		}()
	}
	t.Logf("rendered=%d/%d frontmatterErrs=%d outgoingLinks=%d brokenSeen=%d",
		rendered, len(v.Notes), fmErrs, links, broken)
	if rendered != len(v.Notes) {
		t.Fatalf("only %d/%d notes rendered", rendered, len(v.Notes))
	}
}
