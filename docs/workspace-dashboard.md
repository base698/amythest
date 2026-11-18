# Workspace dashboard

`amy tasks` opens the general vault task list. Inside ordinary `amy`, **1** is
Today/Focus, **2** Tasks, **3** Boards (kanban cards), and **4** Notes.
`amy add "Water seedlings"` appends a task to today's daily note; `amy add`
opens a one-line capture input. `amy capture` remains an alias.

After installing `amy`, install the Herdr App shortcuts:

```sh
python3 scripts/install-herdr-app-messaging.py --binary ~/bin/amy
```

**Cmd+Shift+A** opens Amythest in a full terminal tab, scoped to the selected
Herdr workspace, with its Work wall alongside it. Pressing the shortcut again
closes that workspace's dashboard. **Ctrl+B, a** opens quick add; Enter saves,
Escape cancels. **Ctrl+B, Shift+A** also opens the dashboard. The installer
backs up `keys.json` and clones a compatible existing prefix mod before adding
bindings, keeping its other commands. Personal paths and keys stay outside Git.

From a terminal, use `amy workspace -workspace Garden` (or `amy board`).
Omitting `-workspace` uses the current Herdr context when available.

| Key | View/action |
| --- | --- |
| F1 | Today/Focus: due items and pinned cards |
| F2 | Vault tasks |
| F3 | Boards and their cards |
| F4 | Notes; type to search, Tab to browse |
| F7 | Search notes, cards, tasks and recorded agent work |
| Ctrl+W or F8 | Move keyboard focus between navigation and the wall |
| Tab / Shift+Tab | Switch panels, except where Notes/search use Tab locally |
| F9 | Switch between this workspace and All workspaces |
| Ctrl+C | Close the dashboard |

Numbers **1–4** and **7** also navigate while no input has focus. Function keys
leave search fields directly; cancel an editing prompt before navigating.
Click either panel to focus it; click a wall item to select it. Arrow keys or
**j/k** move through items, and the mouse wheel scrolls the panel under it.
Keyboard commands go only to the focused panel; the top bar says
**keyboard: wall**, **navigation** or **search**. Ctrl+W switches sides without
changing an unfinished input. Tab keeps its local meaning in Notes/search.

With the wall focused, press **?** for its keys:

| Key | Movement |
| --- | --- |
| j / k or ↓ / ↑ | Next / previous item (lines in a diff) |
| h / l or ← / → | Previous / next gallery tile |
| Ctrl+D / Ctrl+U | Half page down / up |
| Space / b or PageDown / PageUp | Full page down / up |
| Ctrl+E / Ctrl+Y | Scroll one line down / up |
| g or gg / G, Home / End | First / last item; top / bottom of a diff |

Ctrl+F/B also page down/up when the terminal passes them through. Herdr uses
Ctrl+B as its command prefix, so **b** or **PageUp** works there without a clash.
The left panel retains its own keys, including Space to toggle tasks.

Below 105 terminal columns, Ctrl+W/F8 switches between full-width panels. At wider
sizes both panels remain visible. The wall retains its project when F9 expands
browsing to All. Its own **p** picker can switch projects. Enter on a search
artifact opens it in the wall; Escape closes search without closing Amythest.

Boards show **two status columns at a time** (one in a narrow panel). **h/l**
or left/right slides across the statuses; **j/k** selects cards. Titles wrap,
with a short description below. Enter opens the complete card. In a card,
**Ctrl+D/U** scrolls half a page, **PageDown/PageUp** a full page, and **g/G**
jumps to the beginning/end, including comments below checklists. **j/k** still
selects checklist items; Space reveals a hidden selection before toggling it.

**Enter on a commit opens its diff across the full dashboard.** Added and
removed lines have colour and old/new line numbers. **Esc** returns to the
split wall at the selected item. **[ / ]** jumps between files; **h/l** scrolls
long lines sideways and **0** resets that offset. **o** opens the file named
in the diff header in **local Vim**, near the visible line. Quit Vim with
`:q` (or save and quit with `:wq`) to return to the same diff. This opens the
current working copy, so edits stay in your checkout. Amy matches the recorded
repository to the current directory, nearby checkouts, or local Herdr terminal
directories. A missing checkout or deleted file shows a message. Switching
panels with Ctrl+W keeps the diff open for your return.

The wall includes this workspace's **canvases** alongside recorded work, newest
first. Press **c** to toggle canvases only, or **f** to cycle all/media/commits/
canvases. **Enter** on a canvas opens its native Herdr pane; **o** explicitly
opens its browser editor. The list refreshes with the wall (**r** immediately).
Canvas previews appear below their titles, including drawings made only in the
native pane; edits replace the preview on the next refresh.

Image previews use pixel graphics in Herdr App and other
compatible terminals, including inside the dashboard. They clip and resize
with the wall panel. Other terminals fall back to coloured blocks. Override
the choice with `amy workspace -images kitty|blocks|off`, or set
`AMY_WALL_IMAGES` to the same value; `auto` is the default.

## Private workspace mapping

Defaults match boards by name, display name or Workspace metadata; notes and
tasks by a whole folder segment or workspace tag. Workspace names become tags
by lowercasing and replacing spaces with hyphens. Quick add from Herdr appends
that tag, shown above its input, so the task belongs to the workspace.
An untagged task in a general daily note remains available under All.

Override mappings in `~/.config/amythest/cli.yaml`:

```yaml
workspaces:
  garden:
    boards: [plants]
    folders: [Projects/Garden]
    tags: [garden]
    project: Botany
```

`boards` contains board IDs. `folders` matches complete folder prefixes, and
`tags` accepts tag names without `#`; either can include a note or task.
Quick add uses the first tag. `project` names the Work wall/search project.
No workspace mapping is stored in the repository. These are browsing filters,
not access controls: opening a note's explicit links keeps normal navigation.
All mode also restores configured tracker sources in the main UI; workspace
search covers Amythest's vault and Work records. Standalone `amy find` also
searches configured trackers and open terminal screens.

Update the server alongside the client: scoped vault search uses the optional
`scope_slugs` JSON-array query parameter on `/api/search` to filter before its
result limit. Older servers ignore it; client-side checks still exclude other
workspaces, but results can be incomplete until the server is updated.
