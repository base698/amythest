# Canvas

A canvas is a drawing a person and an agent work on together. The person draws
in the browser (Excalidraw) or in a native Herdr pane; the agent
reads a plain-language summary of what is drawn and what changed, and pushes
Mermaid, images or text back.

## Opening one

- **Herdr App:** Cmd+Shift+C lists the current workspace's canvases. Enter
  shows one natively in a pane beside you, B opens the browser editor, Tab
  switches to every workspace. "New canvas" starts one here without opening
  a browser; **Browser ↗** in the pane opens the browser editor when wanted.
- **Terminal:** `amy canvas new -title "Sketch" -open` (add `-pane` for the
  Herdr pane, `-mermaid file.mmd` to start from a diagram).
- **Browser:** `/canvas/` lists every canvas with thumbnails.
  Each has a **Delete** button; confirmation permanently removes that canvas,
  its drawing and pending inputs. Existing links to it will stop working.

Inside a Herdr space a new canvas belongs to that workspace, and to its board
(the `cli.yaml` workspace mapping, else a board named after the workspace).
`amy canvas link ID -workspace W -board B -card C` refiles one; a card also
gets a comment linking the canvas.

## Working with an agent

```sh
amy canvas wait ID            # blocks until the person presses "Send to agent"
amy canvas read ID -png x.png # summary + changes since this machine last read
amy canvas push ID -mermaid flow.mmd   # or -image file.png, -text "…"
```

The summary comes from the drawing's structure (arrows bound to shapes,
labels, frames), not from pixels; hand-drawn marks are reported by where they
are, with the PNG for a closer look. Pushed items are placed by an open
editor, next to what is already drawn.

## Getting it out

- A canvas link on a line of its own in a note, or `![label](canvas link)`,
  shows the live drawing; kanban card text shows a thumbnail under the link.
- The editor copies a PNG, downloads PNG/SVG (both re-open as editable), and
  saves to a note.
- `amy canvas export ID [-svg] [-o path]`, `amy canvas attach ID` (Messages).
- In the Herdr pane, drag the PNG chip onto a terminal, Finder or Messages;
  drop images onto the pane to add them.
- **Copy PNG** in the Herdr pane (macOS) copies the whole current drawing,
  including edits still being saved. It works without opening the browser.
  The installer builds its native helper with the Xcode command line tools;
  no compiler is needed when copying.

## Storage and access

Canvases live in `<data_dir>/canvases/`, outside the vault. Every canvas route
needs a signed-in session, writes need the CSRF token, and drawings are served
with a sandboxed CSP. Install the Herdr mod and shortcut with
`python3 scripts/install-herdr-app-messaging.py --binary ~/bin/amy`.
