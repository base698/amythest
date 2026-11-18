package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/canvas"
)

const canvasUsage = `amy canvas: draw with an agent.

  amy canvas new [-title T] [-mermaid FILE|-] [-text T] [-open|-pane]
                [-workspace W] [-board B] [-card C]
                                    start a canvas (optionally from Mermaid);
                                    inside Herdr it belongs to this workspace
  amy canvas list [-all]            this workspace's canvases (-all: every one)
  amy canvas open ID                open the editor in the browser
  amy canvas pane ID                show it natively in a Herdr pane
  amy canvas send ID [-text NOTE]   hand it to the agent (like the button)
  amy canvas link ID [-workspace W] [-board B] [-card C]
                                    file it under a workspace, board or card
                                    (a card also gets a comment linking it)
  amy canvas read ID [-png PATH]    what is drawn, and what changed since
                                    this machine last read it
  amy canvas wait ID [-timeout D]   block until the person presses
                                    "Send to agent", then read
  amy canvas push ID (-mermaid FILE|- | -image FILE | -text T)
                                    add content; the editor places it
  amy canvas export ID [-svg] [-o PATH|-]
                                    save the PNG (or SVG) and print its path
  amy canvas attach ID              export the PNG into the Messages hub

ID can be a full canvas id or "last" (the most recently changed canvas).
Agents: read prints a plain summary; look at the PNG for hand-drawn marks.
`

func runCanvas(args []string) {
	if err := canvasCommand(args); err != nil {
		fmt.Fprintln(os.Stderr, "canvas:", err)
		os.Exit(1)
	}
}

func canvasCommand(args []string) error {
	op := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		op, args = args[0], args[1:]
	}
	if op == "help" || op == "-h" || op == "--help" {
		fmt.Print(canvasUsage)
		return nil
	}
	fs := flag.NewFlagSet("amy canvas "+op, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, canvasUsage) }
	title := fs.String("title", "", "canvas title")
	mermaidFile := fs.String("mermaid", "", "Mermaid file to draw from (- for stdin)")
	text := fs.String("text", "", "text to place on the canvas")
	image := fs.String("image", "", "image file to place on the canvas")
	open := fs.Bool("open", false, "open the editor after creating")
	asJSON := fs.Bool("json", false, "print JSON")
	peek := fs.Bool("peek", false, "read without marking changes as seen")
	pngOut := fs.String("png", "", "read: also save the rendering to this path")
	svg := fs.Bool("svg", false, "export SVG instead of PNG")
	out := fs.String("o", "", "export path (default ./<title>.png; - for stdout)")
	timeout := fs.Duration("timeout", 30*time.Minute, "wait: give up after this long")
	endpoint := fs.String("endpoint", "", "amythest server URL")
	workspace := fs.String("workspace", "", "Herdr workspace (default: the current one)")
	boardRef := fs.String("board", "", "Amythest board (default: the workspace's board)")
	card := fs.String("card", "", "card id on the board")
	all := fs.Bool("all", false, "list: every workspace")
	pane := fs.Bool("pane", false, "new: show it in a Herdr pane")
	dir := fs.String("dir", "", "snapshot: cache folder")
	opsFile := fs.String("file", "", "ops: JSON file of element edits")
	rmFile := fs.Bool("rm", false, "ops: remove -file after reading it")
	since := fs.Int64("since", -1, "snapshot: skip when the canvas is still at this revision")
	// Accept flags after positional arguments too: `amy canvas read ID -png x`.
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return err
		}
		args = fs.Args()
		if len(args) > 0 {
			positional = append(positional, args[0])
			args = args[1:]
		}
	}

	var cfgArgs []string
	if *endpoint != "" {
		cfgArgs = []string{"-endpoint", *endpoint}
	}
	cfg, err := apiclient.LoadConfig(cfgArgs)
	if err != nil {
		return err
	}
	client := apiclient.New(cfg)
	ctx := context.Background()

	needID := func() (string, error) {
		if len(positional) != 1 {
			return "", fmt.Errorf("usage: amy canvas %s ID", op)
		}
		return resolveCanvasID(ctx, client, positional[0])
	}

	switch op {
	case "list", "ls":
		ws := *workspace
		if ws == "" && !*all {
			ws = currentWorkspace()
		}
		list, err := client.CanvasList(ctx, ws)
		if err != nil {
			return err
		}
		if ws != "" && !*asJSON {
			fmt.Printf("Canvases in %s (amy canvas list -all for every workspace)\n", ws)
		}
		if *asJSON {
			for i := range list {
				list[i].URL = canvasLink(cfg.Endpoint, list[i].URL)
			}
			return printJSON(list)
		}
		if len(list) == 0 {
			fmt.Println("No canvases yet. Start one: amy canvas new -title \"Sketch\" -open")
		}
		for _, m := range list {
			waiting := ""
			if m.Inbox > m.Applied {
				waiting = fmt.Sprintf("  (%d waiting to be placed)", m.Inbox-m.Applied)
			}
			where := m.Workspace
			if m.Board != "" && !strings.EqualFold(m.Board, m.Workspace) {
				where += "/" + m.Board
			}
			fmt.Printf("%s  %-36s  %-18s  %s%s\n", m.ID, m.Title, where, m.Updated.Local().Format("Jan 2 15:04"), waiting)
		}
		return nil

	case "new", "create":
		mermaid, err := readMaybeStdin(*mermaidFile)
		if err != nil {
			return err
		}
		at := canvasPlacement(ctx, client, cfg, *workspace, *boardRef, *card)
		m, err := client.CanvasCreate(ctx, *title, mermaid, *text, at)
		if err != nil {
			return err
		}
		link := canvasLink(cfg.Endpoint, m.URL)
		if at.Card != "" && at.Board != "" {
			commentCanvasOnCard(ctx, client, at, m.Title, link)
		}
		if *asJSON {
			return printJSON(map[string]string{"id": m.ID, "url": link, "title": m.Title, "workspace": m.Workspace, "board": m.Board})
		}
		fmt.Printf("%s  %s\n%s\n", m.ID, m.Title, link)
		if m.Workspace != "" {
			fmt.Printf("in %s%s\n", m.Workspace, map[bool]string{true: " / board " + m.Board, false: ""}[m.Board != ""])
		}
		if *pane {
			if err := openCanvasPane(m.ID); err != nil {
				return err
			}
		}
		if *open {
			return openURL(link)
		}
		return nil

	case "send":
		id, err := needID()
		if err != nil {
			return err
		}
		m, err := client.CanvasSend(ctx, id, *text)
		if err != nil {
			return err
		}
		fmt.Printf("sent %q (hand-off #%d) to whoever waits on it\n", m.Title, m.Sent)
		return nil

	case "pane", "show":
		id, err := needID()
		if err != nil {
			return err
		}
		return openCanvasPane(id)

	case "link":
		id, err := needID()
		if err != nil {
			return err
		}
		at := canvas.Placement{Workspace: *workspace, Board: *boardRef, Card: *card}
		if at.Card != "" && at.Board == "" {
			cur, err := client.CanvasMeta(ctx, id)
			if err != nil {
				return err
			}
			at.Board = cur.Board
			if at.Board == "" {
				return errors.New("give -board with -card: the canvas has no board yet")
			}
		}
		m, err := client.CanvasLink(ctx, id, at)
		if err != nil {
			return err
		}
		if at.Card != "" {
			commentCanvasOnCard(ctx, client, at, m.Title, canvasLink(cfg.Endpoint, m.URL))
		}
		fmt.Printf("%s  %s  workspace=%q board=%q card=%q\n", m.ID, m.Title, m.Workspace, m.Board, m.Card)
		return nil

	case "ops":
		// Element edits as JSON {"upsert": [...], "delete": [...]} from -file
		// (removed afterwards with -rm) or stdin. Used by the Herdr pane.
		id, err := needID()
		if err != nil {
			return err
		}
		src := *opsFile
		if src == "" {
			src = "-"
		}
		raw, err := readMaybeStdin(src)
		if err != nil {
			return err
		}
		if *rmFile && *opsFile != "" {
			os.Remove(*opsFile)
		}
		var ops canvas.Ops
		if err := json.Unmarshal([]byte(raw), &ops); err != nil {
			return fmt.Errorf("ops: %w", err)
		}
		m, err := client.CanvasOps(ctx, id, ops)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]int64{"rev": m.Rev})

	case "snapshot":
		id, err := needID()
		if err != nil {
			return err
		}
		if *dir == "" {
			return errors.New("give -dir")
		}
		return canvasSnapshot(ctx, client, cfg.Endpoint, id, *dir, *since)

	case "open":
		id, err := needID()
		if err != nil {
			return err
		}
		m, err := client.CanvasMeta(ctx, id)
		if err != nil {
			return err
		}
		link := canvasLink(cfg.Endpoint, m.URL)
		fmt.Println(link)
		return openURL(link)

	case "read":
		id, err := needID()
		if err != nil {
			return err
		}
		return canvasRead(ctx, client, cfg.Endpoint, id, *asJSON, *peek, *pngOut)

	case "wait":
		id, err := needID()
		if err != nil {
			return err
		}
		seen := loadCanvasSeen(id).Sent
		deadline := time.Now().Add(*timeout)
		announced := false
		for {
			m, err := client.CanvasMeta(ctx, id)
			if err != nil {
				return err
			}
			if m.Sent > seen {
				break
			}
			if !announced {
				fmt.Fprintf(os.Stderr, "Waiting for \"Send to agent\" on %q: %s\n", m.Title, canvasLink(cfg.Endpoint, m.URL))
				announced = true
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("nothing sent within %s", *timeout)
			}
			time.Sleep(2 * time.Second)
		}
		return canvasRead(ctx, client, cfg.Endpoint, id, *asJSON, false, *pngOut)

	case "push", "add":
		id, err := needID()
		if err != nil {
			return err
		}
		var items []canvas.InboxItem
		if *mermaidFile != "" {
			src, err := readMaybeStdin(*mermaidFile)
			if err != nil {
				return err
			}
			items = append(items, canvas.InboxItem{Kind: "mermaid", Data: src, From: "amy"})
		}
		if *image != "" {
			item, err := imageItem(*image)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		if *text != "" {
			items = append(items, canvas.InboxItem{Kind: "text", Data: *text, From: "amy"})
		}
		if len(items) == 0 {
			return fmt.Errorf("give -mermaid, -image or -text")
		}
		for _, item := range items {
			seq, err := client.CanvasPush(ctx, id, item)
			if err != nil {
				return err
			}
			fmt.Printf("queued %s #%d; it appears when the canvas is open\n", item.Kind, seq)
		}
		return nil

	case "export", "pull":
		id, err := needID()
		if err != nil {
			return err
		}
		kind := "png"
		if *svg {
			kind = "svg"
		}
		path, err := canvasExport(ctx, client, id, kind, *out)
		if err != nil {
			return err
		}
		if path != "-" {
			fmt.Println(path)
		}
		return nil

	case "attach":
		id, err := needID()
		if err != nil {
			return err
		}
		dir, err := os.MkdirTemp("", "amy-canvas-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		m, err := client.CanvasMeta(ctx, id)
		if err != nil {
			return err
		}
		path, err := canvasExport(ctx, client, id, "png", filepath.Join(dir, canvas.FileName(m.Title)+".png"))
		if err != nil {
			return err
		}
		return messageCommand([]string{"attach", "-file", path})
	}
	fmt.Fprint(os.Stderr, canvasUsage)
	return fmt.Errorf("unknown command %q", op)
}

func resolveCanvasID(ctx context.Context, client *apiclient.Client, ref string) (string, error) {
	if canvas.ValidID(ref) {
		return ref, nil
	}
	list, err := client.CanvasList(ctx, "")
	if err != nil {
		return "", err
	}
	if ref == "last" {
		if len(list) == 0 {
			return "", errors.New("no canvases yet")
		}
		return list[0].ID, nil
	}
	var hits []string
	for _, m := range list {
		if strings.HasPrefix(m.ID, ref) || strings.EqualFold(m.Title, ref) {
			hits = append(hits, m.ID)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return "", fmt.Errorf("no canvas matches %q", ref)
	}
	return "", fmt.Errorf("%q matches %d canvases; use the full id", ref, len(hits))
}

// canvasLink joins the server-relative editor path onto the endpoint origin.
func canvasLink(endpoint, rel string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return strings.TrimRight(endpoint, "/") + rel
	}
	return u.Scheme + "://" + u.Host + rel
}

func readMaybeStdin(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, canvas.MaxInboxBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > canvas.MaxInboxBytes {
		return "", fmt.Errorf("input exceeds %d bytes", canvas.MaxInboxBytes)
	}
	return string(b), nil
}

func imageItem(path string) (canvas.InboxItem, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return canvas.InboxItem{}, err
	}
	mime := http.DetectContentType(b)
	if strings.EqualFold(filepath.Ext(path), ".svg") {
		mime = "image/svg+xml"
	}
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml":
	default:
		return canvas.InboxItem{}, fmt.Errorf("%s is %s; use PNG, JPEG, GIF, WebP or SVG", path, mime)
	}
	data := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
	if len(data) > canvas.MaxInboxBytes {
		return canvas.InboxItem{}, fmt.Errorf("%s is too large to place (limit about %d MB)", path, canvas.MaxInboxBytes*3/4>>20)
	}
	return canvas.InboxItem{Kind: "image", Name: filepath.Base(path), MIME: mime, Data: data, From: "amy"}, nil
}

func canvasExport(ctx context.Context, client *apiclient.Client, id, kind, out string) (string, error) {
	b, err := client.CanvasDrawing(ctx, id, kind)
	if err != nil {
		var apiErr *apiclient.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return "", errors.New("no drawing yet: open the canvas once so the browser can render it")
		}
		return "", err
	}
	if out == "-" {
		_, err = os.Stdout.Write(b)
		return "-", err
	}
	if out == "" {
		m, err := client.CanvasMeta(ctx, id)
		if err != nil {
			return "", err
		}
		out = canvas.FileName(m.Title) + "." + kind
	}
	if err := os.WriteFile(out, b, 0o644); err != nil {
		return "", err
	}
	return filepath.Abs(out)
}

// canvasSeen is what this machine last read of a canvas, so the next read
// can say what changed and `wait` knows which hand-off is new.
type canvasSeen struct {
	Rev     int64          `json:"rev"`
	Sent    int64          `json:"sent"`
	Summary canvas.Summary `json:"summary"`
}

func canvasSeenPath(id string) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "amythest", "canvas", id+".json")
}

func loadCanvasSeen(id string) canvasSeen {
	var s canvasSeen
	if b, err := os.ReadFile(canvasSeenPath(id)); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func saveCanvasSeen(id string, s canvasSeen) {
	path := canvasSeenPath(id)
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	if b, err := json.Marshal(s); err == nil {
		_ = os.WriteFile(path, b, 0o600)
	}
}

func canvasRead(ctx context.Context, client *apiclient.Client, endpoint, id string, asJSON, peek bool, pngOut string) error {
	sum, err := client.CanvasSummary(ctx, id)
	if err != nil {
		return err
	}
	prev := loadCanvasSeen(id)
	first := prev.Rev == 0 && prev.Sent == 0 && len(prev.Summary.Shapes) == 0
	var changes []string
	if !first {
		changes = sum.Summary.Diff(prev.Summary)
	}
	var png string
	if pngOut != "" && (sum.Meta.Drawing || sum.Meta.Stale) {
		if png, err = canvasExport(ctx, client, id, "png", pngOut); err != nil {
			return err
		}
	}
	if !peek {
		saveCanvasSeen(id, canvasSeen{Rev: sum.Meta.Rev, Sent: sum.Meta.Sent, Summary: sum.Summary})
	}
	newSend := sum.Meta.Sent > prev.Sent
	if asJSON {
		return printJSON(map[string]any{
			"id": id, "title": sum.Meta.Title, "rev": sum.Meta.Rev, "url": canvasLink(endpoint, sum.Meta.URL),
			"sent": sum.Meta.Sent, "new_send": newSend, "message": sum.Meta.SentMessage,
			"summary": sum.Summary, "text": sum.Text, "changes": changes, "first_read": first,
			"pending": sum.Pending, "png": png,
		})
	}
	fmt.Printf("Canvas %q (%s, rev %d)\n%s\n", sum.Meta.Title, id, sum.Meta.Rev, canvasLink(endpoint, sum.Meta.URL))
	if newSend && sum.Meta.SentAt.After(time.Time{}) {
		fmt.Printf("\nSent to you %s", sum.Meta.SentAt.Local().Format("Jan 2 15:04"))
		if sum.Meta.SentMessage != "" {
			fmt.Printf(" with: %q", sum.Meta.SentMessage)
		}
		fmt.Println()
	}
	switch {
	case first:
	case len(changes) == 0:
		fmt.Println("\nNo changes since you last read it.")
	default:
		fmt.Println("\nChanges since you last read it:")
		for _, c := range changes {
			fmt.Println("  - " + c)
		}
	}
	fmt.Println()
	fmt.Print(sum.Text)
	if sum.Pending > 0 {
		fmt.Printf("\n%d pushed item(s) are waiting for the canvas to be opened.\n", sum.Pending)
	}
	if png != "" {
		fmt.Printf("\nPNG: %s\n", png)
	}
	return nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
