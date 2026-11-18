package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/canvas"
	"github.com/base698/amythest/internal/wall"
)

// currentWorkspace is the Herdr workspace this command runs in, or "".
func currentWorkspace() string {
	if os.Getenv("HERDR_WORKSPACE_ID") == "" && os.Getenv("HC_TERMINAL_ID") == "" {
		return ""
	}
	return wall.Locate(nil, nil).Project
}

// canvasPlacement fills in where a new canvas belongs: the given workspace
// (default: the current Herdr one) and its board, found the same way the
// workspace dashboard finds it (cli.yaml mapping, else a board named after
// the workspace).
func canvasPlacement(ctx context.Context, client *apiclient.Client, cfg apiclient.Config, workspace, boardRef, card string) canvas.Placement {
	at := canvas.Placement{Workspace: workspace, Board: boardRef, Card: card}
	if at.Workspace == "" {
		at.Workspace = currentWorkspace()
	}
	if at.Board != "" || at.Workspace == "" {
		return at
	}
	scope := cfg.Workspace(at.Workspace)
	if len(scope.Boards) > 0 {
		at.Board = scope.Boards[0]
		return at
	}
	if boards, err := client.ListBoards(ctx); err == nil {
		for _, b := range boards {
			if !b.Archived && scope.MatchesBoard(b) {
				at.Board = b.Name
				break
			}
		}
	}
	return at
}

func commentCanvasOnCard(ctx context.Context, client *apiclient.Client, at canvas.Placement, title, link string) {
	if _, err := client.AddComment(ctx, at.Board, at.Card, fmt.Sprintf("Canvas: [%s](%s)", title, link)); err != nil {
		fmt.Fprintf(os.Stderr, "canvas: could not comment on card %s: %v\n", at.Card, err)
	}
}

// openCanvasPane shows the canvas natively in a Herdr App pane beside the
// focused one (the amythest-canvas mod).
func openCanvasPane(id string) error {
	bin, err := exec.LookPath("herdr-app")
	if err != nil {
		return errors.New("the Herdr App is not installed here; use: amy canvas open " + id)
	}
	args, _ := json.Marshal(map[string]any{"direction": "right", "mod": "amythest-canvas", "props": map[string]string{"id": id}})
	out, err := exec.Command(bin, "do", "pane.split", string(args)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("herdr-app pane.split: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// snapshotElement is the part of an Excalidraw element the Herdr mod draws.
type snapshotElement struct {
	ID              string      `json:"id"`
	Type            string      `json:"type"`
	X               float64     `json:"x"`
	Y               float64     `json:"y"`
	Width           float64     `json:"width"`
	Height          float64     `json:"height"`
	Angle           float64     `json:"angle,omitempty"`
	StrokeColor     string      `json:"strokeColor,omitempty"`
	BackgroundColor string      `json:"backgroundColor,omitempty"`
	StrokeWidth     float64     `json:"strokeWidth,omitempty"`
	StrokeStyle     string      `json:"strokeStyle,omitempty"`
	Opacity         float64     `json:"opacity"`
	Points          [][]float64 `json:"points,omitempty"`
	Text            string      `json:"text,omitempty"`
	FontSize        float64     `json:"fontSize,omitempty"`
	FontFamily      int         `json:"fontFamily,omitempty"`
	TextAlign       string      `json:"textAlign,omitempty"`
	LineHeight      float64     `json:"lineHeight,omitempty"`
	ContainerID     string      `json:"containerId,omitempty"`
	FileID          string      `json:"fileId,omitempty"`
	StartArrowhead  string      `json:"startArrowhead,omitempty"`
	EndArrowhead    string      `json:"endArrowhead,omitempty"`
	Name            string      `json:"name,omitempty"`
	Roundness       bool        `json:"roundness,omitempty"`
	BoundElements   []boundRef  `json:"boundElements,omitempty"`
	StartBinding    *bindingRef `json:"startBinding,omitempty"`
	EndBinding      *bindingRef `json:"endBinding,omitempty"`
	IsDeleted       bool        `json:"-"`
}

type boundRef struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type bindingRef struct {
	ElementID string `json:"elementId"`
}

func (e *snapshotElement) UnmarshalJSON(b []byte) error {
	type plain snapshotElement
	var raw struct {
		plain
		ContainerID    *string         `json:"containerId"`
		FileID         *string         `json:"fileId"`
		StartArrowhead *string         `json:"startArrowhead"`
		EndArrowhead   *string         `json:"endArrowhead"`
		Name           *string         `json:"name"`
		Roundness      json.RawMessage `json:"roundness"`
		IsDeleted      bool            `json:"isDeleted"`
		Opacity        *float64        `json:"opacity"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*e = snapshotElement(raw.plain)
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	e.ContainerID, e.FileID = deref(raw.ContainerID), deref(raw.FileID)
	e.StartArrowhead, e.EndArrowhead, e.Name = deref(raw.StartArrowhead), deref(raw.EndArrowhead), deref(raw.Name)
	e.Roundness = len(raw.Roundness) > 0 && string(raw.Roundness) != "null"
	e.IsDeleted = raw.IsDeleted
	e.Opacity = 100
	if raw.Opacity != nil {
		e.Opacity = *raw.Opacity
	}
	return nil
}

type snapshotOut struct {
	Unchanged bool              `json:"unchanged,omitempty"`
	ID        string            `json:"id"`
	Title     string            `json:"title,omitempty"`
	Rev       int64             `json:"rev"`
	Workspace string            `json:"workspace,omitempty"`
	Board     string            `json:"board,omitempty"`
	URL       string            `json:"url,omitempty"`
	PNG       string            `json:"png,omitempty"`
	Pending   int64             `json:"pending,omitempty"`
	Elements  []snapshotElement `json:"elements,omitempty"`
	Files     map[string]string `json:"files,omitempty"`
}

var imageExt = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp", "image/svg+xml": ".svg"}

// canvasSnapshot writes what the Herdr mod needs into dir/<id>/ — embedded
// images as files (the mod can only show images from disk) and the latest
// PNG to drag out — and prints the drawable elements as JSON. With since set
// to the current revision it only reports that nothing changed.
func canvasSnapshot(ctx context.Context, client *apiclient.Client, endpoint, id, dir string, since int64) error {
	enc := json.NewEncoder(os.Stdout)
	if since >= 0 {
		m, err := client.CanvasMeta(ctx, id)
		if err != nil {
			return err
		}
		if m.Rev == since {
			return enc.Encode(snapshotOut{Unchanged: true, ID: id, Rev: m.Rev, Pending: m.Inbox - m.Applied})
		}
	}
	meta, scene, err := client.CanvasScene(ctx, id)
	if err != nil {
		return err
	}
	var raw struct {
		Elements []snapshotElement `json:"elements"`
		Files    map[string]struct {
			MIMEType string `json:"mimeType"`
			DataURL  string `json:"dataURL"`
		} `json:"files"`
	}
	if err := json.Unmarshal(scene, &raw); err != nil {
		return err
	}
	base := filepath.Join(dir, id)
	if err := os.MkdirAll(filepath.Join(base, "files"), 0o700); err != nil {
		return err
	}
	out := snapshotOut{ID: id, Title: meta.Title, Rev: meta.Rev, Workspace: meta.Workspace, Board: meta.Board,
		URL: canvasLink(endpoint, meta.URL), Pending: meta.Inbox - meta.Applied, Files: map[string]string{}}
	used := map[string]bool{}
	for _, e := range raw.Elements {
		if e.IsDeleted {
			continue
		}
		out.Elements = append(out.Elements, e)
		if e.FileID != "" {
			used[e.FileID] = true
		}
	}
	for fid := range used {
		f, ok := raw.Files[fid]
		ext, known := imageExt[f.MIMEType]
		if !ok || !known {
			continue
		}
		safe := canvas.FileName(fid) + ext
		path := filepath.Join(base, "files", safe)
		if _, err := os.Stat(path); err != nil {
			_, data, found := strings.Cut(f.DataURL, ";base64,")
			if !found {
				continue
			}
			b, err := base64.StdEncoding.DecodeString(data)
			if err != nil || writeFileAtomic(path, b) != nil {
				continue
			}
		}
		out.Files[fid] = path
	}
	if meta.Drawing || meta.Stale {
		if b, err := client.CanvasDrawing(ctx, id, "png"); err == nil {
			png := filepath.Join(base, canvas.FileName(meta.Title)+".png")
			if writeFileAtomic(png, b) == nil {
				out.PNG = png
			}
		}
	}
	return enc.Encode(out)
}

func writeFileAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err = tmp.Write(b); err == nil {
		err = tmp.Close()
	} else {
		tmp.Close()
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}
