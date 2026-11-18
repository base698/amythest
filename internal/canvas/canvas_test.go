package canvas

import (
	"errors"
	"strings"
	"testing"
)

// A small generic architecture sketch: two boxes joined by a labeled arrow,
// a box inside a frame, a loose note, a scribble and a deleted element.
const sceneV1 = `{"type":"excalidraw","elements":[
 {"id":"web1","type":"rectangle","x":0,"y":0,"width":120,"height":60,"boundElements":[{"id":"t1","type":"text"}]},
 {"id":"t1","type":"text","x":10,"y":20,"width":100,"height":20,"text":"Web","originalText":"Web","containerId":"web1"},
 {"id":"api1","type":"rectangle","x":300,"y":0,"width":120,"height":60},
 {"id":"t2","type":"text","x":320,"y":20,"width":60,"height":20,"text":"API","originalText":"API","containerId":null},
 {"id":"a1","type":"arrow","x":120,"y":30,"width":180,"height":0,"points":[[0,0],[180,0]],
  "startBinding":{"elementId":"web1"},"endBinding":{"elementId":"api1"},"startArrowhead":null,"endArrowhead":"arrow"},
 {"id":"t3","type":"text","x":180,"y":10,"width":40,"height":20,"text":"calls","containerId":"a1"},
 {"id":"f1","type":"frame","x":250,"y":-50,"width":300,"height":200,"name":"Backend"},
 {"id":"note1","type":"text","x":300,"y":90,"width":100,"height":20,"text":"async?"},
 {"id":"ink1","type":"freedraw","x":280,"y":-10,"width":160,"height":80,"points":[[0,0],[1,1]]},
 {"id":"gone","type":"rectangle","x":900,"y":900,"width":10,"height":10,"isDeleted":true}
],"files":{}}`

func TestSummarize(t *testing.T) {
	s, err := Summarize([]byte(sceneV1))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Shapes) != 2 || s.Shapes[0].Label != "Web" || s.Shapes[1].Label != "API" {
		t.Fatalf("shapes %+v", s.Shapes)
	}
	if len(s.Links) != 1 || s.Links[0].From != "web1" || s.Links[0].To != "api1" || s.Links[0].Label != "calls" || s.Links[0].Style != "->" {
		t.Fatalf("links %+v", s.Links)
	}
	if len(s.Notes) != 1 || s.Notes[0].Near != "api1" {
		t.Fatalf("notes %+v", s.Notes)
	}
	if len(s.Ink) != 1 || s.Ink[0].Near != "api1" {
		t.Fatalf("ink %+v", s.Ink)
	}
	text := s.Text()
	for _, want := range []string{`"Web" -> "API" [calls]`, `"async?" near "API"`, "1 stroke(s) near \"API\""} {
		if !strings.Contains(text, want) {
			t.Errorf("summary missing %q:\n%s", want, text)
		}
	}
}

func TestLooseArrowGuessesEnds(t *testing.T) {
	scene := `{"elements":[
	 {"id":"a","type":"ellipse","x":0,"y":0,"width":50,"height":50,"text":""},
	 {"id":"b","type":"diamond","x":200,"y":0,"width":50,"height":50},
	 {"id":"x","type":"arrow","x":55,"y":25,"width":140,"height":0,"points":[[0,0],[140,0]],"endArrowhead":"arrow"}]}`
	s, err := Summarize([]byte(scene))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Links) != 1 || s.Links[0].From != "a" || s.Links[0].To != "b" || !s.Links[0].Loose {
		t.Fatalf("links %+v", s.Links)
	}
	if !strings.Contains(s.Text(), "unlabeled ellipse #a") {
		t.Fatalf("unlabeled name: %s", s.Text())
	}
}

func TestDiff(t *testing.T) {
	before, _ := Summarize([]byte(sceneV1))
	v2 := strings.Replace(sceneV1, `"text":"API","originalText":"API"`, `"text":"Gateway","originalText":"Gateway"`, 1)
	v2 = strings.Replace(v2, `{"id":"gone"`, `{"id":"db1","type":"rectangle","x":300,"y":300,"width":120,"height":60},
	 {"id":"t9","type":"text","x":320,"y":320,"width":60,"height":20,"text":"DB","containerId":"db1"},
	 {"id":"a2","type":"arrow","x":360,"y":60,"width":0,"height":240,"points":[[0,0],[0,240]],"startBinding":{"elementId":"api1"},"endBinding":{"elementId":"db1"},"endArrowhead":"arrow"},
	 {"id":"gone"`, 1)
	after, err := Summarize([]byte(v2))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(after.Diff(before), "\n")
	for _, want := range []string{`renamed "API" to "Gateway"`, `added shape "DB"`, `connected "Gateway" -> "DB"`} {
		if !strings.Contains(got, want) {
			t.Errorf("diff missing %q:\n%s", want, got)
		}
	}
	if d := after.Diff(after); len(d) != 0 {
		t.Fatalf("self diff %v", d)
	}
}

func TestStoreLifecycle(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Create("  Sample   flow ", Placement{Workspace: "Garden", Board: "plants"}, InboxItem{Kind: "mermaid", Data: "flowchart LR\n a-->b"})
	if err != nil || m.Title != "Sample flow" || m.Inbox != 1 || !ValidID(m.ID) || m.Workspace != "Garden" || m.Board != "plants" {
		t.Fatalf("create %+v %v", m, err)
	}
	pending, _ := s.Pending(m.ID)
	if len(pending) != 1 || pending[0].Kind != "mermaid" {
		t.Fatalf("pending %+v", pending)
	}
	png := []byte("\x89PNG\r\n\x1a\nrest")
	m2, err := s.Save(m.ID, SaveInput{BaseRev: 0, Scene: []byte(sceneV1), PNG: png, SVG: []byte("<svg/>"), Applied: 1})
	if err != nil || m2.Rev != 1 || m2.Applied != 1 || !m2.Drawing {
		t.Fatalf("save %+v %v", m2, err)
	}
	if pending, _ = s.Pending(m.ID); len(pending) != 0 {
		t.Fatalf("applied item still pending %+v", pending)
	}
	if _, err = s.Save(m.ID, SaveInput{BaseRev: 0, Scene: []byte(sceneV1)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	if _, err = s.Save(m.ID, SaveInput{BaseRev: 1, Scene: []byte(sceneV1), PNG: []byte("not png")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad png: %v", err)
	}
	if _, _, err = s.Push(m.ID, InboxItem{Kind: "image", Data: "javascript:alert(1)"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad image: %v", err)
	}
	sent, err := s.Send(m.ID, "look at the cache")
	if err != nil || sent.Sent != 1 || sent.SentRev != 1 {
		t.Fatalf("send %+v %v", sent, err)
	}
	b, _, err := s.Drawing(m.ID, "png")
	if err != nil || string(b) != string(png) {
		t.Fatalf("drawing %q %v", b, err)
	}
	if _, err = s.Meta("../etc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("traversal: %v", err)
	}
	linked, err := s.SetLink(m.ID, Placement{Card: "c42"})
	if err != nil || linked.Workspace != "Garden" || linked.Card != "c42" {
		t.Fatalf("link %+v %v", linked, err)
	}
	if cleared, _ := s.SetLink(m.ID, Placement{Clear: true, Board: "beds"}); cleared.Workspace != "" || cleared.Board != "beds" || cleared.Card != "" {
		t.Fatalf("clear %+v", cleared)
	}
	list, _ := s.List()
	if len(list) != 1 {
		t.Fatalf("list %+v", list)
	}
}

func TestLooseSelfLoopIsAMark(t *testing.T) {
	scene := `{"elements":[
	 {"id":"a","type":"rectangle","x":0,"y":0,"width":100,"height":50},
	 {"id":"x","type":"arrow","x":40,"y":45,"width":2,"height":10,"points":[[0,0],[2,10]],"endArrowhead":"arrow"}]}`
	s, _ := Summarize([]byte(scene))
	if len(s.Links) != 0 || len(s.Ink) != 1 || s.Ink[0].Near != "a" {
		t.Fatalf("links %+v ink %+v", s.Links, s.Ink)
	}
}
