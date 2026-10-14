package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/config"
	"github.com/base698/amythest/internal/tasks"
)

func newNotePropertyTestServer(t *testing.T, content []byte) (*Server, string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "Project.md")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path, tasks.FileVersion(content)
}

func postNoteProperty(t *testing.T, s *Server, body string, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/notes/property", strings.NewReader(body))
	if csrf {
		req = withSessionCSRF(t, s)(req)
	} else {
		req = withSession(t, s)(req)
	}
	rec := httptest.NewRecorder()
	s.handleNoteProperty(rec, req)
	return rec
}

func propertyBody(version, key, value, valueType string) string {
	payload, _ := json.Marshal(map[string]string{
		"slug": "Project", "key": key, "value": value, "type": valueType, "expectedVersion": version,
	})
	return string(payload)
}

func TestHandleNotePropertyRequiresSessionAndMatchingCSRF(t *testing.T) {
	content := []byte("---\nstatus: active\n---\n# Project\n")
	s, _, version := newNotePropertyTestServer(t, content)
	_ = withSession(t, s) // configure auth without stamping the first request
	body := propertyBody(version, "status", "done", "string")

	rec := httptest.NewRecorder()
	s.handleNoteProperty(rec, httptest.NewRequest(http.MethodPost, "/api/notes/property", strings.NewReader(body)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without session status = %d, want 401", rec.Code)
	}

	rec = postNoteProperty(t, s, body, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("without CSRF status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}

	req := withSessionCSRF(t, s)(httptest.NewRequest(http.MethodPost, "/api/notes/property", strings.NewReader(body)))
	req.Header.Set("X-CSRF-Token", "wrong")
	rec = httptest.NewRecorder()
	s.handleNoteProperty(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("invalid CSRF status = %d, want 403", rec.Code)
	}
}

func TestHandleNotePropertyPreservesExplicitStringTypeAndReindexes(t *testing.T) {
	content := []byte("---\nliteral: \"false\"\n---\n# Project\n")
	s, _, version := newNotePropertyTestServer(t, content)
	rec := postNoteProperty(t, s, propertyBody(version, "literal", "0123", "string"), true)
	if rec.Code != http.StatusOK {
		t.Fatalf("set status = %d body=%s", rec.Code, rec.Body.String())
	}

	note, ok := s.vault.Load().BySlug("Project")
	if !ok || note.FM.MetaString("literal") != "0123" {
		t.Fatalf("reindexed note = %#v", note)
	}
	rec = httptest.NewRecorder()
	s.handleNoteRaw(rec, httptest.NewRequest(http.MethodGet, "/api/note?slug=Project", nil))
	var got struct {
		Version    string `json:"version"`
		Properties []struct {
			Key   string `json:"key"`
			Type  string `json:"type"`
			Value any    `json:"value"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version == version {
		t.Fatal("whole-note version did not change after property mutation")
	}
	if len(got.Properties) != 1 || got.Properties[0].Type != "string" || got.Properties[0].Value != "0123" {
		t.Fatalf("properties = %#v", got.Properties)
	}
}

func TestHandleNotePropertyEmptyValueClearsPropertyAndReindexes(t *testing.T) {
	content := []byte("---\ntasks: false\nstatus: active\n---\n# Project\n")
	s, path, version := newNotePropertyTestServer(t, content)
	rec := postNoteProperty(t, s, propertyBody(version, "tasks", "", "bool"), true)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d body=%s", rec.Code, rec.Body.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "tasks:") || !strings.Contains(string(after), "status: active") || !strings.HasSuffix(string(after), "# Project\n") {
		t.Fatalf("file after clear = %q", after)
	}
	note, ok := s.vault.Load().BySlug("Project")
	if !ok {
		t.Fatal("reindexed note missing")
	}
	if _, exists := note.FM.Meta["tasks"]; exists || note.FM.MetaString("status") != "active" {
		t.Fatalf("reindexed frontmatter = %#v", note.FM.Meta)
	}
}

func TestHandleNotePropertyRejectsMissingMalformedAndStaleVersions(t *testing.T) {
	content := []byte("---\nstatus: active\n---\n# Project\n")
	for name, tc := range map[string]struct {
		version string
		status  int
	}{
		"missing":   {"", http.StatusBadRequest},
		"malformed": {strings.Repeat("z", 64), http.StatusBadRequest},
		"stale":     {strings.Repeat("a", 64), http.StatusConflict},
	} {
		t.Run(name, func(t *testing.T) {
			s, path, _ := newNotePropertyTestServer(t, content)
			rec := postNoteProperty(t, s, propertyBody(tc.version, "status", "done", "string"), true)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.status, rec.Body.String())
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(content) {
				t.Fatalf("rejected write changed file to %q", after)
			}
		})
	}
}

func TestHandleNotePropertyEnforcesInputLimits(t *testing.T) {
	content := []byte("---\nstatus: active\n---\n# Project\n")
	s, _, version := newNotePropertyTestServer(t, content)
	valid := map[string]string{"slug": "Project", "key": "status", "value": "done", "type": "string", "expectedVersion": version}
	tests := map[string]func(map[string]string){
		"slug length":  func(p map[string]string) { p["slug"] = strings.Repeat("s", 513) },
		"key syntax":   func(p map[string]string) { p["key"] = "not valid" },
		"key length":   func(p map[string]string) { p["key"] = "a" + strings.Repeat("b", 64) },
		"value length": func(p map[string]string) { p["value"] = strings.Repeat("v", 10_001) },
		"scalar type":  func(p map[string]string) { p["type"] = "object" },
		"null payload": func(p map[string]string) { p["type"], p["value"] = "null", "not-empty" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			payload := make(map[string]string, len(valid))
			for key, value := range valid {
				payload[key] = value
			}
			mutate(payload)
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			rec := postNoteProperty(t, s, string(raw), true)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleNotePropertyAcceptsMaximumLengths(t *testing.T) {
	content := []byte("# Project\n")
	s, _, version := newNotePropertyTestServer(t, content)
	payload := propertyBody(version, "a"+strings.Repeat("b", 63), strings.Repeat("v", 10_000), "string")
	rec := postNoteProperty(t, s, payload, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("boundary status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleNotePropertyExactCurrentVersionSucceeds(t *testing.T) {
	content := []byte("---\nstatus: active\n---\n# Project\n")
	s, _, version := newNotePropertyTestServer(t, content)
	rec := postNoteProperty(t, s, propertyBody(version, "status", "done", "string"), true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s version=%s", rec.Code, rec.Body.String(), fmt.Sprintf("%.8s…", version))
	}
}

func TestHandleNotePropertyRoundTripsSpecialYAMLNumbers(t *testing.T) {
	for _, special := range []string{".nan", ".inf", "-.inf"} {
		t.Run(special, func(t *testing.T) {
			content := []byte("---\nvalue: " + special + "\n---\n# Project\n")
			s, path, version := newNotePropertyTestServer(t, content)
			rec := postNoteProperty(t, s, propertyBody(version, "value", special, "number"), true)
			if rec.Code != http.StatusOK {
				t.Fatalf("unchanged special number status = %d body=%s", rec.Code, rec.Body.String())
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(after), "value: "+special) {
				t.Fatalf("special number was not preserved: %q", after)
			}

			rec = httptest.NewRecorder()
			s.handleNoteRaw(rec, httptest.NewRequest(http.MethodGet, "/api/note?slug=Project", nil))
			var got struct {
				Properties []struct {
					Key   string `json:"key"`
					Type  string `json:"type"`
					Value any    `json:"value"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Properties) != 1 || got.Properties[0].Key != "value" || got.Properties[0].Type != "number" || got.Properties[0].Value != special {
				t.Fatalf("round-tripped property = %#v", got.Properties)
			}
		})
	}
}
