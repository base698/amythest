package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/config"
)

func newCanvasServer(t *testing.T) *Server {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/", SiteName: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func canvasDo(s *Server, stamp func(*http.Request) *http.Request, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if stamp != nil {
		r = stamp(r)
	}
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, r)
	return w
}

func TestCanvasRequiresSession(t *testing.T) {
	s := newCanvasServer(t)
	authorizeTestServer(t, s)
	for _, c := range [][2]string{{"GET", "/api/canvas"}, {"POST", "/api/canvas"}, {"GET", "/api/canvas/aaaaaaaaaaaa/summary"}, {"GET", "/api/canvas/aaaaaaaaaaaa/drawing.png"}} {
		if w := canvasDo(s, nil, c[0], c[1], "{}"); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without session: %d", c[0], c[1], w.Code)
		}
	}
	// Writes also need CSRF.
	if w := canvasDo(s, withSession(t, s), "POST", "/api/canvas", "{}"); w.Code != http.StatusForbidden {
		t.Errorf("create without CSRF: %d", w.Code)
	}
}

func TestCanvasRoundTrip(t *testing.T) {
	s := newCanvasServer(t)
	auth := withSessionCSRF(t, s)
	w := canvasDo(s, auth, "POST", "/api/canvas", `{"title":"Sample","mermaid":"flowchart LR\n a-->b","workspace":"Garden","board":"plants"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var meta struct {
		ID    string `json:"id"`
		URL   string `json:"url"`
		Inbox int64  `json:"inbox"`
	}
	json.Unmarshal(w.Body.Bytes(), &meta)
	if meta.URL != "/canvas/"+meta.ID || meta.Inbox != 1 {
		t.Fatalf("meta %+v", meta)
	}
	if w = canvasDo(s, auth, "GET", "/api/canvas/"+meta.ID+"/inbox", ""); !strings.Contains(w.Body.String(), "a--\\u003eb") {
		t.Fatalf("inbox %s", w.Body)
	}
	scene := `{"elements":[{"id":"r1","type":"rectangle","x":0,"y":0,"width":10,"height":10},{"id":"t","type":"text","x":1,"y":1,"width":5,"height":5,"text":"Queue","containerId":"r1"}]}`
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nx"))
	body := `{"base_rev":0,"applied":1,"scene":` + scene + `,"svg":"<svg><script>x()</script></svg>","png":"` + png + `"}`
	if w = canvasDo(s, auth, "PUT", "/api/canvas/"+meta.ID, body); w.Code != http.StatusOK {
		t.Fatalf("save %d %s", w.Code, w.Body)
	}
	if w = canvasDo(s, auth, "PUT", "/api/canvas/"+meta.ID, body); w.Code != http.StatusConflict {
		t.Fatalf("stale save %d", w.Code)
	}
	if w = canvasDo(s, auth, "GET", "/api/canvas/"+meta.ID+"/summary", ""); !strings.Contains(w.Body.String(), `\"Queue\"`) {
		t.Fatalf("summary %s", w.Body)
	}
	w = canvasDo(s, auth, "GET", "/api/canvas/"+meta.ID+"/drawing.svg?download=1", "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") || !strings.Contains(w.Header().Get("Content-Disposition"), "sample.svg") {
		t.Fatalf("drawing %d %v", w.Code, w.Header())
	}
	if w = canvasDo(s, auth, "GET", "/api/canvas/"+meta.ID+"/drawing.html", ""); w.Code != http.StatusNotFound {
		t.Fatalf("bad extension %d", w.Code)
	}
	if w = canvasDo(s, auth, "POST", "/api/canvas/"+meta.ID+"/send", `{"message":"check the queue"}`); !strings.Contains(w.Body.String(), `"sent":1`) {
		t.Fatalf("send %s", w.Body)
	}
	if w = canvasDo(s, auth, "GET", "/api/canvas?workspace=garden", ""); !strings.Contains(w.Body.String(), meta.ID) {
		t.Fatalf("workspace filter %s", w.Body)
	}
	if w = canvasDo(s, auth, "GET", "/api/canvas?workspace=other", ""); strings.Contains(w.Body.String(), meta.ID) {
		t.Fatalf("other workspace %s", w.Body)
	}
	if w = canvasDo(s, auth, "POST", "/api/canvas/"+meta.ID+"/link", `{"card":"c1"}`); !strings.Contains(w.Body.String(), `"card":"c1"`) || !strings.Contains(w.Body.String(), `"board":"plants"`) {
		t.Fatalf("link %s", w.Body)
	}
	if w = canvasDo(s, nil, "GET", "/canvas/"+meta.ID, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `data-id="`+meta.ID+`"`) {
		t.Fatalf("page %d", w.Code)
	}
	if w = canvasDo(s, nil, "GET", "/canvas/..%2fx", ""); w.Code != http.StatusNotFound {
		t.Fatalf("bad id page %d", w.Code)
	}
}
