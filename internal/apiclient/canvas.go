package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/base698/amythest/internal/canvas"
)

// CanvasMeta is a canvas as the server reports it, with its editor URL
// (relative to the endpoint's origin).
type CanvasMeta struct {
	canvas.Meta
	URL string `json:"url"`
}

type CanvasSummary struct {
	Meta    CanvasMeta     `json:"meta"`
	Summary canvas.Summary `json:"summary"`
	Text    string         `json:"text"`
	Pending int            `json:"pending"`
}

func canvasPath(id string, rest string) string {
	return "/api/canvas/" + url.PathEscape(id) + rest
}

func (c *Client) CanvasCreate(ctx context.Context, title, mermaid, text string, at canvas.Placement) (CanvasMeta, error) {
	var out CanvasMeta
	err := c.do(ctx, http.MethodPost, "/api/canvas", map[string]string{
		"title": title, "mermaid": mermaid, "text": text,
		"workspace": at.Workspace, "board": at.Board, "card": at.Card,
	}, &out)
	return out, err
}

// CanvasList lists canvases, newest first; workspace narrows to one Herdr
// workspace when set.
func (c *Client) CanvasList(ctx context.Context, workspace string) ([]CanvasMeta, error) {
	var out []CanvasMeta
	path := "/api/canvas"
	if workspace != "" {
		path += "?workspace=" + url.QueryEscape(workspace)
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) CanvasLink(ctx context.Context, id string, at canvas.Placement) (CanvasMeta, error) {
	var out CanvasMeta
	err := c.do(ctx, http.MethodPost, canvasPath(id, "/link"), at, &out)
	return out, err
}

// CanvasScene returns the stored Excalidraw scene as raw JSON.
func (c *Client) CanvasScene(ctx context.Context, id string) (CanvasMeta, json.RawMessage, error) {
	var out struct {
		Meta  CanvasMeta      `json:"meta"`
		Scene json.RawMessage `json:"scene"`
	}
	err := c.do(ctx, http.MethodGet, canvasPath(id, ""), nil, &out)
	return out.Meta, out.Scene, err
}

func (c *Client) CanvasMeta(ctx context.Context, id string) (CanvasMeta, error) {
	var out CanvasMeta
	err := c.do(ctx, http.MethodGet, canvasPath(id, "/meta"), nil, &out)
	return out, err
}

func (c *Client) CanvasSummary(ctx context.Context, id string) (CanvasSummary, error) {
	var out CanvasSummary
	err := c.do(ctx, http.MethodGet, canvasPath(id, "/summary"), nil, &out)
	return out, err
}

// CanvasPush queues Mermaid, text or an image (data: URL) for the editor to place.
func (c *Client) CanvasPush(ctx context.Context, id string, item canvas.InboxItem) (int64, error) {
	var out struct {
		Seq int64 `json:"seq"`
	}
	err := c.do(ctx, http.MethodPost, canvasPath(id, "/push"), item, &out)
	return out.Seq, err
}

// CanvasDrawing downloads the last rendering; kind is "png" or "svg".
func (c *Client) CanvasDrawing(ctx context.Context, id, kind string) ([]byte, error) {
	if kind != "png" && kind != "svg" {
		return nil, fmt.Errorf("kind must be png or svg")
	}
	sess, err := c.ensureSession(ctx, false)
	if err != nil {
		return nil, err
	}
	limit := int64(canvas.MaxPNGBytes)
	if kind == "svg" {
		limit = canvas.MaxSVGBytes
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Endpoint+canvasPath(id, "/drawing."+kind), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Cookie", sess.Cookie)
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, wrapUnavailableTransport(err)
		}
		if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && attempt == 0 {
			resp.Body.Close()
			if sess, err = c.ensureSession(ctx, true); err != nil {
				return nil, err
			}
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, decodeError(resp)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(b)) > limit {
			return nil, fmt.Errorf("drawing larger than %d MB", limit>>20)
		}
		return b, nil
	}
	return nil, fmt.Errorf("authentication failed")
}

// CanvasSend records a hand-off to whoever waits on the canvas.
func (c *Client) CanvasSend(ctx context.Context, id, message string) (CanvasMeta, error) {
	var out CanvasMeta
	err := c.do(ctx, http.MethodPost, canvasPath(id, "/send"), map[string]string{"message": message}, &out)
	return out, err
}
