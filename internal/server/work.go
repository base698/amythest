package server

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/base698/amythest/internal/work"
)

type collectorKey struct{}

func collectorRequest(r *http.Request, id string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), collectorKey{}, id))
}
func collectorRoute(r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "POST /api/work/streams", "POST /api/work/events", "POST /api/work/heartbeat", "GET /api/work/bindings", "POST /api/work/bindings/ack":
		return true
	}
	return r.Method == "PUT" && len(r.URL.Path) == len("/api/work/media/")+64 && r.URL.Path[:len("/api/work/media/")] == "/api/work/media/"
}
func workReadRoute(r *http.Request) bool {
	if r.Method != "GET" && r.Method != "HEAD" {
		return false
	}
	switch r.URL.Path {
	case "/api/work/snapshot", "/api/work/search", "/api/work/inbox", "/api/work/milestones", "/api/work/milestones/collect", "/api/work/milestones/links":
		return true
	}
	return len(r.URL.Path) == len("/api/work/media/")+64 && r.URL.Path[:len("/api/work/media/")] == "/api/work/media/"
}
func (s *Server) mountWork() error {
	if !s.cfg.Work.Enabled {
		return nil
	}
	if s.cfg.AuthMode != "private" {
		return errors.New("work requires private authentication")
	}
	cfg := s.cfg.Work
	if cfg.DB == "" {
		cfg.DB = filepath.Join(s.cfg.DataDir, "work.db")
	}
	if cfg.MediaDir == "" {
		cfg.MediaDir = filepath.Join(s.cfg.DataDir, "work-media")
	}
	var e error
	s.work, e = work.Open(cfg)
	if e != nil {
		return e
	}
	s.ready.register("work", s.work.Ping)
	for _, pattern := range []string{"POST /api/work/streams", "POST /api/work/events", "POST /api/work/heartbeat", "GET /api/work/bindings", "POST /api/work/bindings/ack", "POST /api/work/bindings", "GET /api/work/snapshot", "GET /api/work/inbox", "GET /api/work/search", "POST /api/work/project", "POST /api/work/file", "POST /api/work/review", "POST /api/work/snooze", "POST /api/work/dismiss", "POST /api/work/reset", "POST /api/work/quarantine", "PUT /api/work/media/{hash}", "GET /api/work/media/{hash}",
		"GET /api/work/milestones", "POST /api/work/milestones", "POST /api/work/milestones/end", "GET /api/work/milestones/collect",
		"POST /api/work/milestones/update", "GET /api/work/milestones/links", "POST /api/work/milestones/links", "DELETE /api/work/milestones/links"} {
		s.mux.HandleFunc(pattern, s.handleWork)
	}
	return nil
}
func (s *Server) workError(w http.ResponseWriter, e error) {
	var f *work.Fault
	if errors.As(e, &f) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		s.writeJSON(w, f)
		return
	}
	slog.Error("work operation failed", "error", e)
	http.Error(w, "work temporarily unavailable", 503)
}
func (s *Server) handleWork(w http.ResponseWriter, r *http.Request) {
	collector, _ := r.Context().Value(collectorKey{}).(string)
	// Collector operations require the registered identity, even for owner cookies.
	if collectorRoute(r) && collector == "" {
		http.Error(w, "collector credential required", 403)
		return
	}
	decode := func(v any) bool {
		if e := jsonDecodeLimit(r, v, 2<<20); e != nil {
			http.Error(w, "invalid or oversized JSON", 400)
			return false
		}
		return true
	}
	var result any = map[string]bool{"ok": true}
	var e error
	switch r.Method + " " + r.URL.Path {
	case "POST /api/work/streams":
		var in struct {
			Generation string `json:"generation"`
		}
		if !decode(&in) {
			return
		}
		result, e = s.work.Stream(collector, in.Generation)
	case "POST /api/work/events":
		var in work.Batch
		if !decode(&in) {
			return
		}
		result, e = s.work.Ingest(collector, in)
	case "POST /api/work/heartbeat":
		var in struct {
			Name       string           `json:"name"`
			Syncing    bool             `json:"syncing"`
			Inactivity map[string]int64 `json:"inactivity"`
		}
		if !decode(&in) {
			return
		}
		e = s.work.Heartbeat(collector, in.Name, in.Syncing, in.Inactivity)
	case "GET /api/work/bindings":
		result, e = s.work.Bindings(collector)
	case "POST /api/work/bindings/ack":
		var in struct {
			ID      string `json:"id"`
			Version int64  `json:"version"`
		}
		if !decode(&in) {
			return
		}
		e = s.work.AcknowledgeBinding(collector, in.ID, in.Version)
	case "POST /api/work/bindings":
		var in struct {
			Request   string `json:"request_id"`
			Collector string `json:"collector"`
			Session   string `json:"session_id"`
			Card      string `json:"card_id"`
			Expected  int64  `json:"expected_version"`
		}
		if !decode(&in) {
			return
		}
		if in.Card != "" && !s.workCardExists(in.Card) {
			http.Error(w, "active card not found", 404)
			return
		}
		result, e = s.work.Bind("owner", in.Request, in.Collector, in.Session, in.Card, in.Expected)
	case "GET /api/work/snapshot":
		if run := r.URL.Query().Get("run_id"); run != "" {
			result, e = s.work.RunSnapshot(run)
		} else if r.URL.Query().Get("summary") == "1" {
			result, e = s.work.Overview()
		} else if q := r.URL.Query(); q.Has("after") || q.Has("before") {
			result, e = s.work.SnapshotRange(q.Get("card_id"), q.Get("project"), q.Get("after"), q.Get("before"))
		} else {
			result, e = s.work.Snapshot(r.URL.Query().Get("card_id"), r.URL.Query().Get("project"))
		}
	case "GET /api/work/inbox":
		result, e = s.work.Inbox()
	case "GET /api/work/milestones":
		q := r.URL.Query()
		result, e = s.work.Milestones(q.Get("project"), q.Get("open") == "1")
	case "POST /api/work/milestones":
		var in struct {
			Project string `json:"project"`
			Name    string `json:"name"`
			Started string `json:"started_at"`
			Ended   string `json:"ended_at"`
		}
		if !decode(&in) {
			return
		}
		result, e = s.work.StartMilestone(in.Project, in.Name, in.Started, in.Ended)
	case "POST /api/work/milestones/update":
		var in struct {
			ID string `json:"id"`
			work.MilestoneChange
		}
		if !decode(&in) {
			return
		}
		result, e = s.work.UpdateMilestone(in.ID, in.MilestoneChange)
	case "GET /api/work/milestones/links":
		result, e = s.work.MilestoneLinks(r.URL.Query().Get("id"))
	case "POST /api/work/milestones/links", "DELETE /api/work/milestones/links":
		var in struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
			Ref  string `json:"ref"`
		}
		if !decode(&in) {
			return
		}
		if r.Method == "POST" {
			result, e = s.work.LinkMilestone(in.ID, in.Kind, in.Ref, "owner")
		} else {
			result, e = map[string]bool{"ok": true}, s.work.UnlinkMilestone(in.ID, in.Kind, in.Ref)
		}
	case "POST /api/work/milestones/end":
		var in struct {
			ID string `json:"id"`
		}
		if !decode(&in) {
			return
		}
		result, e = s.work.EndMilestone(in.ID)
	case "GET /api/work/milestones/collect":
		m, d, links, err := s.work.Collect(r.URL.Query().Get("id"))
		result, e = map[string]any{"milestone": m, "snapshot": d, "links": links}, err
	case "GET /api/work/search":
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		result, e = s.work.Search(work.SearchOptions{Query: q.Get("q"), Card: q.Get("card_id"), Project: q.Get("project"), Kind: q.Get("kind"), After: q.Get("after"), Before: q.Get("before"), Limit: limit})
	case "POST /api/work/project":
		var in struct {
			Request  string `json:"request_id"`
			Session  string `json:"session_id"`
			Project  string `json:"project"`
			Expected int64  `json:"expected_version"`
		}
		if !decode(&in) {
			return
		}
		result, e = s.work.AssignProject(in.Request, in.Session, in.Project, in.Expected)
	case "POST /api/work/file":
		var in struct {
			Request  string `json:"request_id"`
			Link     string `json:"link_id"`
			Card     string `json:"card_id"`
			Featured bool   `json:"featured"`
		}
		if !decode(&in) {
			return
		}
		if in.Card != "" && !s.workCardExists(in.Card) {
			http.Error(w, "active card not found", 404)
			return
		}
		e = s.work.File(in.Request, in.Link, in.Card, in.Featured)
	case "POST /api/work/review":
		var in struct {
			Run     string `json:"run_id"`
			Version int64  `json:"version"`
		}
		if !decode(&in) {
			return
		}
		e = s.work.Review(in.Run, in.Version)
	case "POST /api/work/dismiss":
		var in struct {
			Items []work.AttentionVersion `json:"items"`
		}
		if !decode(&in) {
			return
		}
		e = s.work.Dismiss(in.Items)
	case "POST /api/work/snooze":
		var in struct {
			Subject string    `json:"subject"`
			Version string    `json:"version"`
			Until   time.Time `json:"until"`
		}
		if !decode(&in) {
			return
		}
		e = s.work.Snooze(in.Subject, in.Version, in.Until)
	case "POST /api/work/reset":
		var in struct {
			Collector string `json:"collector"`
		}
		if !decode(&in) {
			return
		}
		e = s.work.Reset(in.Collector)
	case "POST /api/work/quarantine":
		var in struct {
			Collector  string `json:"collector"`
			Generation string `json:"generation"`
			Seq        int64  `json:"seq"`
			Reason     string `json:"reason"`
		}
		if !decode(&in) {
			return
		}
		e = s.work.Quarantine(in.Collector, in.Generation, in.Seq, in.Reason)
	default:
		digest := r.PathValue("hash")
		if r.Method == "PUT" {
			result, e = s.work.Upload(collector, digest, r.Body)
		} else {
			f, m, err := s.work.OpenMedia(digest)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			defer f.Close()
			if r.URL.Query().Get("view") == "diff" {
				ok, err := s.work.IsCommitMedia(digest)
				if err != nil {
					s.workError(w, err)
					return
				}
				if !ok {
					http.NotFound(w, r)
					return
				}
				if m.Bytes > 512<<10 {
					http.Error(w, "Diff exceeds preview limit; download the patch instead.", 413)
					return
				}
				m.Type = "text/plain; charset=utf-8"
			}
			w.Header().Set("Content-Type", m.Type)
			w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
			if r.URL.Query().Get("download") == "1" || m.Type == "application/octet-stream" {
				name := filepath.Base(strings.ReplaceAll(r.URL.Query().Get("filename"), "\\", "/"))
				if name == "." || name == "/" || len(name) > 240 || strings.ContainsAny(name, "\r\n\x00") {
					name = "work-file"
				}
				w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
			}
			http.ServeContent(w, r, digest, time.Time{}, f)
			return
		}
	}
	if e != nil {
		s.workError(w, e)
		return
	}
	s.writeJSON(w, result)
}
func (s *Server) workCardExists(id string) bool {
	if s.kanban == nil {
		return false
	}
	boards, e := s.kanban.ListBoards()
	if e != nil {
		return false
	}
	for _, b := range boards {
		v, e := s.kanban.Load(b.Name)
		if e != nil || v.Archived {
			continue
		}
		for _, c := range v.Cards {
			if c.ID == id {
				return true
			}
		}
	}
	return false
}
