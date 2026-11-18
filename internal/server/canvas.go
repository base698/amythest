package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/base698/amythest/internal/canvas"
)

// Canvas routes. Everything — including reads — requires a signed-in
// session: a canvas is private scratch work, unlike published notes.
func (s *Server) mountCanvas() {
	s.mux.HandleFunc("GET /canvas", s.handleCanvasPage)
	s.mux.HandleFunc("GET /canvas/", s.handleCanvasPage)
	s.mux.HandleFunc("GET /canvas/{id}", s.handleCanvasPage)
	s.mux.HandleFunc("GET /api/canvas", s.handleCanvasList)
	s.mux.HandleFunc("POST /api/canvas", s.handleCanvasCreate)
	s.mux.HandleFunc("GET /api/canvas/{id}", s.handleCanvasGet)
	s.mux.HandleFunc("PUT /api/canvas/{id}", s.handleCanvasSave)
	s.mux.HandleFunc("GET /api/canvas/{id}/meta", s.handleCanvasMeta)
	s.mux.HandleFunc("GET /api/canvas/{id}/summary", s.handleCanvasSummary)
	s.mux.HandleFunc("GET /api/canvas/{id}/inbox", s.handleCanvasInbox)
	s.mux.HandleFunc("POST /api/canvas/{id}/push", s.handleCanvasPush)
	s.mux.HandleFunc("POST /api/canvas/{id}/send", s.handleCanvasSend)
	s.mux.HandleFunc("POST /api/canvas/{id}/link", s.handleCanvasLink)
	s.mux.HandleFunc("GET /api/canvas/{id}/{file}", s.handleCanvasDrawing)
}

func canvasError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, canvas.ErrNotFound):
		writeCanvasJSON(w, http.StatusNotFound, map[string]string{"error": "canvas not found"})
	case errors.Is(err, canvas.ErrConflict):
		writeCanvasJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, canvas.ErrInvalid):
		writeCanvasJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeCanvasJSON(w, http.StatusInternalServerError, map[string]string{"error": "canvas storage failed"})
	}
}

func writeCanvasJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// canvasURL is the editor link for a canvas, relative to the site origin.
func (s *Server) canvasURL(id string) string { return s.base() + "canvas/" + id }

type canvasMetaOut struct {
	canvas.Meta
	URL string `json:"url"`
}

func (s *Server) canvasOut(m canvas.Meta) canvasMetaOut {
	return canvasMetaOut{Meta: m, URL: s.canvasURL(m.ID)}
}

func (s *Server) handleCanvasPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id != "" && !canvas.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	base := s.base()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex")
	fmt.Fprintf(w, `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>Canvas</title>
<link rel="stylesheet" href="%[1]sstatic/canvas-ui/main.css">
</head><body>
<div id="canvas-root" data-base="%[2]s" data-id="%[3]s"></div>
<script type="module" src="%[1]sstatic/canvas-ui/main.js"></script>
</body></html>`, html.EscapeString(base), html.EscapeString(base), html.EscapeString(id))
}

func (s *Server) handleCanvasList(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "use canvases") {
		return
	}
	list, err := s.canvases.List()
	if err != nil {
		canvasError(w, err)
		return
	}
	q := r.URL.Query()
	workspace, board := strings.TrimSpace(q.Get("workspace")), strings.TrimSpace(q.Get("board"))
	out := make([]canvasMetaOut, 0, len(list))
	for _, m := range list {
		if workspace != "" && !strings.EqualFold(m.Workspace, workspace) {
			continue
		}
		if board != "" && !strings.EqualFold(m.Board, board) {
			continue
		}
		out = append(out, s.canvasOut(m))
	}
	writeCanvasJSON(w, http.StatusOK, out)
}

type canvasCreateIn struct {
	Title     string `json:"title"`
	Mermaid   string `json:"mermaid"`
	Text      string `json:"text"`
	Workspace string `json:"workspace"`
	Board     string `json:"board"`
	Card      string `json:"card"`
}

func (s *Server) handleCanvasCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSessionCSRF(w, r, "use canvases") {
		return
	}
	var in canvasCreateIn
	if err := jsonDecodeLimit(r, &in, canvas.MaxInboxBytes+(1<<16)); err != nil {
		writeCanvasJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	var seed []canvas.InboxItem
	if strings.TrimSpace(in.Mermaid) != "" {
		seed = append(seed, canvas.InboxItem{Kind: "mermaid", Data: in.Mermaid, From: "create"})
	}
	if strings.TrimSpace(in.Text) != "" {
		seed = append(seed, canvas.InboxItem{Kind: "text", Data: in.Text, From: "create"})
	}
	m, err := s.canvases.Create(in.Title, canvas.Placement{Workspace: in.Workspace, Board: in.Board, Card: in.Card}, seed...)
	if err != nil {
		canvasError(w, err)
		return
	}
	writeCanvasJSON(w, http.StatusCreated, s.canvasOut(m))
}

func (s *Server) handleCanvasGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "use canvases") {
		return
	}
	m, scene, err := s.canvases.Scene(r.PathValue("id"))
	if err != nil {
		canvasError(w, err)
		return
	}
	writeCanvasJSON(w, http.StatusOK, struct {
		Meta  canvasMetaOut   `json:"meta"`
		Scene json.RawMessage `json:"scene"`
	}{s.canvasOut(m), scene})
}

func (s *Server) handleCanvasMeta(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "use canvases") {
		return
	}
	m, err := s.canvases.Meta(r.PathValue("id"))
	if err != nil {
		canvasError(w, err)
		return
	}
	writeCanvasJSON(w, http.StatusOK, s.canvasOut(m))
}

type canvasSaveIn struct {
	BaseRev int64           `json:"base_rev"`
	Title   *string         `json:"title"`
	Scene   json.RawMessage `json:"scene"`
	SVG     string          `json:"svg"`
	PNG     []byte          `json:"png"` // base64 in JSON
	Applied int64           `json:"applied"`
}

func (s *Server) handleCanvasSave(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSessionCSRF(w, r, "use canvases") {
		return
	}
	var in canvasSaveIn
	// Base64 inflates the PNG by a third; leave room for all three parts.
	if err := jsonDecodeLimit(r, &in, canvas.MaxSceneBytes+canvas.MaxSVGBytes+canvas.MaxPNGBytes*4/3+(1<<20)); err != nil {
		writeCanvasJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid or oversized save"})
		return
	}
	m, err := s.canvases.Save(r.PathValue("id"), canvas.SaveInput{
		BaseRev: in.BaseRev, Title: in.Title, Scene: in.Scene, SVG: []byte(in.SVG), PNG: in.PNG, Applied: in.Applied,
	})
	if errors.Is(err, canvas.ErrConflict) {
		writeCanvasJSON(w, http.StatusConflict, struct {
			Error string        `json:"error"`
			Meta  canvasMetaOut `json:"meta"`
		}{err.Error(), s.canvasOut(m)})
		return
	}
	if err != nil {
		canvasError(w, err)
		return
	}
	writeCanvasJSON(w, http.StatusOK, s.canvasOut(m))
}

func (s *Server) handleCanvasSummary(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "use canvases") {
		return
	}
	m, scene, err := s.canvases.Scene(r.PathValue("id"))
	if err != nil {
		canvasError(w, err)
		return
	}
	sum, err := canvas.Summarize(scene)
	if err != nil {
		canvasError(w, err)
		return
	}
	pending, _ := s.canvases.Pending(m.ID)
	writeCanvasJSON(w, http.StatusOK, struct {
		Meta    canvasMetaOut  `json:"meta"`
		Summary canvas.Summary `json:"summary"`
		Text    string         `json:"text"`
		Pending int            `json:"pending"`
	}{s.canvasOut(m), sum, sum.Text(), len(pending)})
}

func (s *Server) handleCanvasInbox(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "use canvases") {
		return
	}
	items, err := s.canvases.Pending(r.PathValue("id"))
	if err != nil {
		canvasError(w, err)
		return
	}
	writeCanvasJSON(w, http.StatusOK, items)
}

func (s *Server) handleCanvasPush(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSessionCSRF(w, r, "use canvases") {
		return
	}
	var in canvas.InboxItem
	if err := jsonDecodeLimit(r, &in, canvas.MaxInboxBytes+(1<<16)); err != nil {
		writeCanvasJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid or oversized item"})
		return
	}
	in.Seq = 0
	m, item, err := s.canvases.Push(r.PathValue("id"), in)
	if err != nil {
		canvasError(w, err)
		return
	}
	writeCanvasJSON(w, http.StatusCreated, struct {
		Meta canvasMetaOut `json:"meta"`
		Seq  int64         `json:"seq"`
	}{s.canvasOut(m), item.Seq})
}

func (s *Server) handleCanvasSend(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSessionCSRF(w, r, "use canvases") {
		return
	}
	var in struct {
		Message string `json:"message"`
	}
	if err := jsonDecodeLimit(r, &in, 1<<16); err != nil {
		writeCanvasJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	m, err := s.canvases.Send(r.PathValue("id"), in.Message)
	if err != nil {
		canvasError(w, err)
		return
	}
	writeCanvasJSON(w, http.StatusOK, s.canvasOut(m))
}

func (s *Server) handleCanvasLink(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSessionCSRF(w, r, "use canvases") {
		return
	}
	var in canvas.Placement
	if err := jsonDecodeLimit(r, &in, 1<<14); err != nil {
		writeCanvasJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	m, err := s.canvases.SetLink(r.PathValue("id"), in)
	if err != nil {
		canvasError(w, err)
		return
	}
	writeCanvasJSON(w, http.StatusOK, s.canvasOut(m))
}

func (s *Server) handleCanvasDrawing(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "use canvases") {
		return
	}
	ext, ok := strings.CutPrefix(r.PathValue("file"), "drawing.")
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, m, err := s.canvases.Drawing(r.PathValue("id"), ext)
	if err != nil {
		if errors.Is(err, canvas.ErrNotFound) {
			http.Error(w, "no drawing yet: open the canvas once so it can render", http.StatusNotFound)
			return
		}
		http.Error(w, "canvas storage failed", http.StatusInternalServerError)
		return
	}
	ctype := "image/png"
	if ext == "svg" {
		ctype = "image/svg+xml"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// A drawing is user content: never let an SVG run script or fetch.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; sandbox")
	if r.URL.Query().Get("download") != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", canvas.FileName(m.Title)+"."+ext))
	}
	_, _ = w.Write(b)
}
