package markdown_test

import (
	"os"
	"testing"

	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/vault"
)

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
