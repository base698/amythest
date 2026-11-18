// Amythest canvas inside Herdr.
//
// Two roles, chosen by props:
//   - overlay (Cmd+Shift+C): this workspace's canvases and "New canvas".
//     Enter shows one in a pane beside the focused one; B opens the editor.
//   - pane ({id}): the canvas drawn natively, kept live. Drag the PNG chip
//     onto a terminal, Finder or Messages; drop images on it to add them.
//
// All server access goes through `amy canvas`, so the mod holds no
// credentials. Snapshots land in .cache/ (hidden, so writing them does not
// reload the mod) and images are read from there.

import { View, div } from "gpui-kit";
import { v_flex, h_flex, InputState, Input } from "gpui-base";
import { exec, props, theme, appHome, checking, closeOverlay, run, spaces, focused, writeTextFile } from "herdr";
import { binary } from "./config.js";
import { drawElement, fit, bounds } from "./draw.js";
import { pngDocument } from "./png.js";
import { TOOLS, toScene, hit, shapeAt, makeShape, makeArrow, makeStroke, makeText, textSize, centerLabel, labelOf, setPoints, absPoints, wire } from "./edit.js";

const MOD = "amythest-canvas";
const POLL_MS = 2000;
const HIGHLIGHT_MS = 8000;
const ACCENT = "#7c5cff";

function cacheDir() {
  return `${appHome()}/mods/${MOD}/.cache`;
}

async function amy(args, timeout = 20000) {
  const r = await exec(binary, ["canvas", ...args], { timeout });
  if (r.code !== 0) throw new Error((r.stderr || r.stdout || "amy canvas failed").trim().replace(/^canvas: /, ""));
  return r.stdout;
}

function currentSpace() {
  const ctx = focused();
  return spaces().find((s) => s.id === ctx?.space) ?? spaces().find((s) => s.active) ?? null;
}

function ago(iso) {
  const s = Math.max(0, (Date.now() - Date.parse(iso)) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

export default class Canvas extends View {
  init(_p, cx) {
    const p = props() ?? {};
    this.mode = p.id ? "pane" : "picker";
    this.error = "";
    this.note = "";
    if (this.mode === "pane") this.initPane(p, cx);
    else this.initPicker(p, cx);
  }

  render(cx) {
    const t = theme();
    return this.mode === "pane" ? this.renderPane(t, cx) : this.renderPicker(t, cx);
  }

  flash(cx, text) {
    this.note = text;
    cx.notify();
    const mine = text;
    cx.timer.after(4000, (cx) => {
      if (this.note === mine) {
        this.note = "";
        cx.notify();
      }
    });
  }

  // ------------------------------------------------------------- picker

  initPicker(_p, cx) {
    this.space = currentSpace();
    this.all = !this.space;
    this.items = null;
    this.at = 0;
    this.handle = cx.focus_handle();
    cx.timer.after(120, () => this.handle.focus());
    if (checking()) return;
    this.loadList(cx);
  }

  loadList(cx) {
    cx.spawn(async (cx) => {
      try {
        const args = ["list", "-json"];
        if (this.all) args.push("-all");
        else args.push("-workspace", this.space.name);
        this.items = JSON.parse(await amy(args)) ?? [];
        this.error = "";
      } catch (e) {
        this.error = String(e.message || e);
        this.items = [];
      }
      this.at = Math.min(this.at, this.items.length);
      cx.notify();
    });
  }

  close() {
    closeOverlay(props().overlay);
  }

  showPane(id) {
    run("pane.split", { direction: "right", mod: MOD, props: { id } });
    this.close();
  }

  create(cx, browser) {
    if (this.creating) return;
    this.creating = true;
    cx.spawn(async (cx) => {
      try {
        const args = ["new", "-json"];
        if (this.space && !this.all) args.push("-workspace", this.space.name);
        const m = JSON.parse(await amy(args));
        if (browser) cx.open_url(m.url);
        this.showPane(m.id);
      } catch (e) {
        this.error = String(e.message || e);
        cx.notify();
      } finally {
        this.creating = false;
      }
    });
  }

  pick(cx, browser) {
    if (this.at === 0) return this.create(cx, browser);
    const m = this.items?.[this.at - 1];
    if (!m) return;
    if (browser) {
      cx.open_url(m.url);
      this.close();
    } else this.showPane(m.id);
  }

  renderPicker(t, cx) {
    const rows = [{ kind: "new" }, ...(this.items ?? []).map((m) => ({ kind: "canvas", m }))];
    const where = this.all ? "all workspaces" : this.space?.name;
    const row = (r, i) => {
      const sel = i === this.at;
      let el = h_flex()
        .id(`row-${i}`)
        .gap(10)
        .px(10)
        .py(7)
        .rounded(6)
        .items_center()
        .cursor_pointer()
        .when(sel, (d) => d.bg(t.selection ?? t.hover))
        .hover((s) => s.bg(t.hover))
        .on_click((_e, cx) => {
          this.at = i;
          this.pick(cx, false);
        });
      if (r.kind === "new") {
        return el
          .child(div().w(18).text_color(ACCENT).font_bold().child("+"))
          .child(div().flex_1().text_color(t.foreground).child(`New canvas${this.all ? "" : ` in ${where}`}`))
          .child(div().text_size(11).text_color(t.dim).child("opens here"));
      }
      const m = r.m;
      const tag = [m.workspace, m.board && m.board !== m.workspace ? m.board : ""].filter(Boolean).join(" / ");
      return el
        .child(div().w(18).text_color(t.dim).child("◇"))
        .child(div().flex_1().truncate().text_color(t.foreground).child(m.title))
        .when(this.all && !!tag, (d) => d.child(div().text_size(11).text_color(t.dim).child(tag)))
        .when(m.inbox > m.applied, (d) => d.child(div().text_size(11).text_color(ACCENT).child(`${m.inbox - m.applied} waiting`)))
        .child(div().text_size(11).text_color(t.dim).child(ago(m.updated)));
    };
    return v_flex()
      .id("canvas-picker")
      .track_focus(this.handle)
      .size_full()
      .p(16)
      .gap(8)
      .bg(t.popover ?? t.background)
      .text_size(13)
      .on_key_down((e, cx) => {
        const k = e.key;
        const n = rows.length;
        if (k === "escape") return this.close();
        if (k === "down") this.at = (this.at + 1) % n;
        else if (k === "up") this.at = (this.at + n - 1) % n;
        else if (k === "enter") return this.pick(cx, false);
        else if (k === "b") return this.pick(cx, true);
        else if (k === "tab" && this.space) {
          this.all = !this.all;
          this.items = null;
          this.loadList(cx);
        } else return;
        cx.stop_propagation();
        cx.notify();
      })
      .child(
        h_flex()
          .items_center()
          .child(div().flex_1().text_size(15).font_semibold().text_color(t.foreground).child("Canvases"))
          .child(div().text_size(11).text_color(t.dim).child(where ?? "")),
      )
      .when(!!this.error, (d) => d.child(div().text_color(t.blocked ?? "#e5534b").child(this.error)))
      .child(
        v_flex()
          .id("canvas-rows")
          .flex_1()
          .min_h(0)
          .overflow_y_scroll()
          .gap(2)
          .children(this.items === null ? [div().text_color(t.dim).child("Loading…")] : rows.map(row)),
      )
      .child(div().text_size(11).text_color(t.dim).child(`↑↓ choose · Enter show here · B open editor${this.space ? " · Tab " + (this.all ? "this workspace" : "all workspaces") : ""} · Esc close`));
  }

  // ------------------------------------------------------------- pane

  initPane(p, cx) {
    this.id = p.id;
    this.snap = null;
    this.view = null;
    this.size = null;
    this.fresh = new Map(); // element id -> highlight deadline
    this.dragging = null;
    this.handle = cx.focus_handle();
    this.tool = "select";
    this.selected = null;
    this.draft = null;
    this.drag = null;
    this.textEdit = null;
    this.pending = { upsert: new Map(), delete: new Set() };
    this.flushing = false;
    this.opsCount = 0;
    this.textInput = InputState.new({ placeholder: "Type, then Enter" });
    this.textInput.on("submit", (_e, cx) => this.commitText(cx));
    this.textInput.on("blur", (_e, cx) => this.commitText(cx));
    if (checking()) return;
    this.refresh(cx);
    cx.timer.every(POLL_MS, (cx) => this.refresh(cx));
  }

  // Local edits not yet saved win over what the server last sent.
  busy() {
    return this.flushing || this.pending.upsert.size > 0 || this.pending.delete.size > 0 || !!this.drag || !!this.textEdit;
  }

  refresh(cx) {
    if (this.loading || this.busy()) return;
    this.loading = true;
    cx.spawn(async (cx) => {
      try {
        const since = this.snap ? String(this.snap.rev) : "-1";
        const s = JSON.parse(await amy(["snapshot", this.id, "-dir", cacheDir(), "-since", since]));
        if (s.unchanged) {
          if (this.snap && this.snap.pending !== s.pending) {
            this.snap.pending = s.pending;
            cx.notify();
          }
        } else {
          const prefix = appHome() + "/";
          s.files = Object.fromEntries(Object.entries(s.files ?? {}).map(([k, v]) => [k, v.startsWith(prefix) ? v.slice(prefix.length) : v]));
          s.elements = s.elements ?? [];
          if (this.snap) {
            const before = new Set(this.snap.elements.map((e) => e.id));
            const until = Date.now() + HIGHLIGHT_MS;
            for (const e of s.elements) if (!before.has(e.id)) this.fresh.set(e.id, until);
            if (this.fresh.size) cx.timer.after(HIGHLIGHT_MS + 50, (cx) => cx.notify());
          }
          this.snap = s;
          // Follow the drawing (keep it fitted) until the person pans or
          // zooms; a canvas opened empty would otherwise stay framed on nothing.
          if (!this.userMoved && this.size) this.view = fit(s.elements, this.size.width, this.size.height);
        }
        this.error = "";
      } catch (e) {
        this.error = String(e.message || e);
      } finally {
        this.loading = false;
      }
      cx.notify();
    });
  }

  // ----------------------------------------------------------- editing

  copyPNG(cx) {
    if (!this.snap || this.copying) return;
    this.commitText(cx);
    // Freeze the drawing at the click, including local edits still being saved.
    // The server's PNG may predate native edits or may not exist at all.
    let drawing;
    try {
      drawing = pngDocument(this.snap, appHome());
    } catch (e) {
      this.flash(cx, `Could not copy PNG: ${String(e.message || e)}`);
      return;
    }
    this.copying = true;
    cx.notify();
    cx.spawn(async (cx) => {
      try {
        const file = `${cacheDir()}/copy-${Date.now()}-${this.opsCount++}.json`;
        await writeTextFile(file, JSON.stringify(drawing), null);
        const result = await exec(`${appHome()}/mods/${MOD}/copy-png`, [file], { timeout: 20000 });
        if (result.code !== 0) throw new Error((result.stderr || "PNG copy failed").trim());
        this.flash(cx, "PNG copied");
      } catch (e) {
        this.flash(cx, `Could not copy PNG: ${String(e.message || e)}`);
      } finally {
        this.copying = false;
        cx.notify();
      }
    });
  }

  els() {
    return this.snap?.elements ?? [];
  }

  byId(id) {
    return this.els().find((e) => e.id === id) ?? null;
  }

  queue(cx, upserts = [], deletes = []) {
    for (const u of upserts) this.pending.upsert.set(u.id, { ...(this.pending.upsert.get(u.id) ?? {}), ...u });
    for (const d of deletes) {
      this.pending.upsert.delete(d);
      this.pending.delete.add(d);
    }
    cx.timer.after(250, (cx) => this.flush(cx));
  }

  flush(cx) {
    if (this.flushing || (!this.pending.upsert.size && !this.pending.delete.size)) return;
    const ops = { upsert: [...this.pending.upsert.values()], delete: [...this.pending.delete] };
    this.pending = { upsert: new Map(), delete: new Set() };
    this.flushing = true;
    cx.spawn(async (cx) => {
      try {
        const path = `${cacheDir()}/ops-${Date.now()}-${this.opsCount++}.json`;
        await writeTextFile(path, JSON.stringify(ops), null);
        const out = JSON.parse(await amy(["ops", this.id, "-file", path, "-rm"]));
        if (this.snap && out.rev) this.snap.rev = out.rev;
      } catch (e) {
        this.flash(cx, `Not saved: ${String(e.message || e)}`);
        if (this.snap) this.snap.rev = -1; // reload what the server has
      } finally {
        this.flushing = false;
      }
      if (this.pending.upsert.size || this.pending.delete.size) this.flush(cx);
      cx.notify();
    });
  }

  setTool(cx, tool) {
    this.commitText(cx);
    this.tool = tool;
    this.draft = null;
    if (tool !== "select") this.selected = null;
    cx.notify();
  }

  // Arrows attached to a shape follow it: move the end bound to it.
  followArrows(shapeId, dx, dy, orig) {
    const moved = [];
    for (const a of this.els()) {
      if (a.type !== "arrow" || !a.points) continue;
      const startOn = a.startBinding?.elementId === shapeId, endOn = a.endBinding?.elementId === shapeId;
      if (!startOn && !endOn) continue;
      const base = orig.get(a.id) ?? absPoints(a);
      orig.set(a.id, base);
      const pts = base.map((p) => [...p]);
      if (startOn) pts[0] = [pts[0][0] + dx, pts[0][1] + dy];
      if (endOn) pts[pts.length - 1] = [pts[pts.length - 1][0] + dx, pts[pts.length - 1][1] + dy];
      setPoints(a, pts);
      moved.push(a);
    }
    return moved;
  }

  startTextEdit(cx, at, { container = null, existing = null } = {}) {
    this.commitText(cx);
    this.textEdit = { at, container, existing };
    this.textInput.set_value(existing?.text ?? "");
    cx.timer.after(30, () => this.textInput.focus());
    cx.notify();
  }

  commitText(cx) {
    const te = this.textEdit;
    if (!te) return;
    this.textEdit = null;
    const text = this.textInput.value().trim();
    const els = this.els();
    if (te.existing) {
      const e = te.existing;
      if (!text) {
        this.snap.elements = els.filter((x) => x.id !== e.id);
        this.queue(cx, [], [e.id]);
      } else if (text !== e.text) {
        Object.assign(e, { text, originalText: text }, textSize(text, e.fontSize || 20));
        const shape = e.containerId ? this.byId(e.containerId) : null;
        if (shape) centerLabel(e, shape);
        this.queue(cx, [{ id: e.id, text, originalText: text, x: e.x, y: e.y, width: e.width, height: e.height }]);
      }
    } else if (text) {
      const e = makeText(te.at, text, te.container);
      els.push(e);
      const ups = [wire(e)];
      if (te.container) {
        te.container.boundElements = [...(te.container.boundElements ?? []), { id: e.id, type: "text" }];
        ups.push({ id: te.container.id, boundElements: te.container.boundElements });
      }
      this.queue(cx, ups);
      this.selected = te.container?.id ?? e.id;
    }
    this.tool = "select";
    cx.notify();
  }

  deleteSelected(cx) {
    const e = this.byId(this.selected);
    if (!e) return;
    const gone = new Set([e.id]);
    for (const x of this.els()) if (x.containerId === e.id) gone.add(x.id);
    this.snap.elements = this.els().filter((x) => !gone.has(x.id));
    this.selected = null;
    this.queue(cx, [], [...gone]);
    cx.notify();
  }

  pointerDown(e, cx) {
    this.handle.focus();
    if (!this.view || !this.snap || !e.local_position) return;
    const sp = toScene(this.view, e.local_position);
    const tol = 6 / this.view.zoom;
    if (this.textEdit) this.commitText(cx);
    const els = this.els();
    if (e.click_count >= 2 && this.tool === "select") {
      const h = hit(els, sp, tol);
      if (h?.type === "text") this.startTextEdit(cx, { x: h.x, y: h.y }, { existing: h });
      else if (h && ["rectangle", "ellipse", "diamond"].includes(h.type)) {
        const label = labelOf(els, h);
        this.startTextEdit(cx, sp, label ? { existing: label } : { container: h });
      } else {
        this.view = fit(els, this.size.width, this.size.height);
        this.userMoved = false;
      }
      return cx.notify();
    }
    switch (this.tool) {
      case "select": {
        const h = hit(els, sp, tol);
        if (h) {
          this.selected = h.id;
          const orig = new Map([[h.id, { x: h.x, y: h.y }]]);
          const label = labelOf(els, h);
          if (label) orig.set(label.id, { x: label.x, y: label.y });
          this.drag = { kind: "move", start: sp, id: h.id, orig, arrows: new Map(), moved: false };
        } else {
          this.selected = null;
          this.drag = { kind: "pan", x: e.position.x, y: e.position.y, ox: this.view.ox, oy: this.view.oy };
        }
        break;
      }
      case "rectangle":
      case "ellipse":
      case "diamond":
        this.drag = { kind: "shape", start: sp };
        this.draft = makeShape(this.tool, sp, sp);
        break;
      case "arrow":
        this.drag = { kind: "arrow", start: sp };
        this.draft = makeArrow(sp, sp);
        break;
      case "freedraw":
        this.drag = { kind: "pen", pts: [sp] };
        this.draft = makeStroke([sp, sp]);
        break;
      case "text":
        this.startTextEdit(cx, sp);
        break;
    }
    cx.notify();
  }

  pointerMove(e, cx) {
    const d = this.drag;
    if (!d || !this.view) return;
    if (d.kind === "pan") {
      this.userMoved = true;
      this.view = { ...this.view, ox: d.ox + e.position.x - d.x, oy: d.oy + e.position.y - d.y };
      return cx.notify();
    }
    if (!e.local_position) return;
    const sp = toScene(this.view, e.local_position);
    if (d.kind === "move") {
      const dx = sp.x - d.start.x, dy = sp.y - d.start.y;
      for (const [id, o] of d.orig) {
        const el = this.byId(id);
        if (el) Object.assign(el, { x: o.x + dx, y: o.y + dy });
      }
      this.followArrows(d.id, dx, dy, d.arrows);
      d.moved = true;
    } else if (d.kind === "shape") {
      const id = this.draft.id;
      this.draft = { ...makeShape(this.tool, d.start, sp), id };
    } else if (d.kind === "arrow") {
      const pts = absPoints(this.draft);
      setPoints(this.draft, [pts[0], [sp.x, sp.y]]);
    } else if (d.kind === "pen") {
      const last = d.pts[d.pts.length - 1];
      if (Math.hypot(sp.x - last.x, sp.y - last.y) * this.view.zoom >= 2) {
        d.pts.push(sp);
        setPoints(this.draft, d.pts.map((p) => [p.x, p.y]));
      }
    }
    cx.notify();
  }

  pointerUp(e, cx) {
    const d = this.drag;
    this.drag = null;
    if (!d || !this.snap) return cx.notify();
    const els = this.els();
    const tol = 8 / (this.view?.zoom || 1);
    if (d.kind === "move" && d.moved) {
      const ups = [];
      for (const id of d.orig.keys()) {
        const el = this.byId(id);
        if (el) ups.push({ id, x: el.x, y: el.y });
      }
      for (const id of d.arrows.keys()) {
        const a = this.byId(id);
        if (a) ups.push({ id, x: a.x, y: a.y, width: a.width, height: a.height, points: a.points });
      }
      this.queue(cx, ups);
    } else if (d.kind === "shape" && this.draft) {
      const end = e.local_position ? toScene(this.view, e.local_position) : d.start;
      const shape = { ...makeShape(this.draft.type, d.start, end), id: this.draft.id };
      els.push(shape);
      this.queue(cx, [wire(shape)]);
      this.selected = shape.id;
      this.tool = "select";
    } else if (d.kind === "arrow" && this.draft) {
      const pts = absPoints(this.draft);
      const a = { x: pts[0][0], y: pts[0][1] }, b = { x: pts[1][0], y: pts[1][1] };
      if (Math.hypot(b.x - a.x, b.y - a.y) * this.view.zoom > 12) {
        const from = shapeAt(els, a, tol), to = shapeAt(els, b, tol);
        const arrow = makeArrow(a, b, from, to === from ? null : to);
        arrow.id = this.draft.id;
        els.push(arrow);
        const ups = [wire(arrow)];
        for (const sh of [from, to === from ? null : to]) {
          if (!sh) continue;
          sh.boundElements = [...(sh.boundElements ?? []), { id: arrow.id, type: "arrow" }];
          ups.push({ id: sh.id, boundElements: sh.boundElements });
        }
        this.queue(cx, ups);
        this.selected = arrow.id;
        this.tool = "select";
      }
    } else if (d.kind === "pen" && this.draft && d.pts.length > 1) {
      els.push(this.draft);
      this.queue(cx, [wire(this.draft)]);
    }
    this.draft = null;
    cx.notify();
  }

  keyDown(e, cx) {
    if (this.textEdit) {
      if (e.key === "escape") {
        this.textInput.set_value(this.textEdit.existing?.text ?? "");
        this.textEdit = null;
        cx.notify();
      }
      return;
    }
    const k = e.key;
    if (e.modifiers?.platform || e.modifiers?.control) return;
    const tool = TOOLS.find((t) => t.key === k);
    if (tool) this.setTool(cx, tool.id);
    else if (k === "backspace" || k === "delete") this.deleteSelected(cx);
    else if (k === "escape") {
      this.selected = null;
      this.draft = null;
      this.drag = null;
      this.tool = "select";
    } else if (k === "enter" && this.selected) {
      const sh = this.byId(this.selected);
      if (sh) {
        const label = sh.type === "text" ? sh : labelOf(this.els(), sh);
        const c = { x: sh.x + sh.width / 2, y: sh.y + sh.height / 2 };
        if (label) this.startTextEdit(cx, c, { existing: label });
        else if (["rectangle", "ellipse", "diamond"].includes(sh.type)) this.startTextEdit(cx, c, { container: sh });
      }
    } else return;
    cx.stop_propagation();
    cx.notify();
  }

  zoomAt(factor, px, py) {
    this.userMoved = true;
    const v = this.view;
    const zoom = Math.max(0.05, Math.min(8, v.zoom * factor));
    const k = zoom / v.zoom;
    this.view = { zoom, ox: px - (px - v.ox) * k, oy: py - (py - v.oy) * k };
  }

  zoomCenter(cx, factor) {
    if (!this.view || !this.size) return;
    this.zoomAt(factor, this.size.width / 2, this.size.height / 2);
    cx.notify();
  }

  dropFiles(cx, paths) {
    const images = paths.filter((p) => /\.(png|jpe?g|gif|webp|svg)$/i.test(p));
    if (!images.length) return this.flash(cx, "Drop PNG, JPEG, GIF, WebP or SVG images");
    cx.spawn(async (cx) => {
      try {
        for (const p of images) await amy(["push", this.id, "-image", p]);
        this.flash(cx, `Added ${images.length} image${images.length > 1 ? "s" : ""}; the editor places them`);
      } catch (e) {
        this.flash(cx, String(e.message || e));
      }
    });
  }

  send(cx) {
    cx.spawn(async (cx) => {
      try {
        await amy(["send", this.id]);
        this.flash(cx, "Sent — the agent can read it now");
      } catch (e) {
        this.flash(cx, String(e.message || e));
      }
    });
  }

  button(t, id, text, onClick, extra) {
    let b = div()
      .id(id)
      .px(8)
      .py(3)
      .rounded(5)
      .text_size(12)
      .text_color(t.foreground)
      .border_1()
      .border_color(t.border ?? t.dim)
      .cursor_pointer()
      .hover((s) => s.bg(t.hover))
      .child(text);
    if (onClick) b = b.on_click((_e, cx) => onClick(cx));
    return extra ? extra(b) : b;
  }

  selectionBox(e) {
    const b = bounds([e]);
    const z = this.view.zoom, pad = 6;
    return div()
      .absolute()
      .left(b.x * z + this.view.ox - pad)
      .top(b.y * z + this.view.oy - pad)
      .w(b.w * z + pad * 2)
      .h(b.h * z + pad * 2)
      .border_1()
      .border_color(ACCENT)
      .rounded(4);
  }

  textBox(t) {
    const te = this.textEdit;
    const z = this.view.zoom;
    let x = te.at.x * z + this.view.ox, y = te.at.y * z + this.view.oy;
    if (te.container) {
      x = (te.container.x + te.container.width / 2) * z + this.view.ox - 110;
      y = (te.container.y + te.container.height / 2) * z + this.view.oy - 16;
    }
    return div()
      .absolute()
      .left(Math.max(4, x))
      .top(Math.max(48, y))
      .w(220)
      .h(32)
      .px(8)
      .bg("#ffffff")
      .border_1()
      .border_color(ACCENT)
      .rounded(6)
      .child(Input.new(this.textInput).size_full().text_size(14).text_color("#1e1e1e"));
  }

  palette(t) {
    const row = h_flex()
      .absolute()
      .top(8)
      .left(8)
      .gap(2)
      .p(3)
      .rounded(8)
      .bg("#ffffff")
      .border_1()
      .border_color("#dee2e6");
    const buttons = TOOLS.map((tool) => {
      const on = this.tool === tool.id;
      return div()
        .id(`tool-${tool.id}`)
        .w(30)
        .h(30)
        .rounded(6)
        .flex()
        .items_center()
        .justify_center()
        .text_size(15)
        .cursor_pointer()
        .text_color(on ? "#ffffff" : "#1e1e1e")
        .bg(on ? ACCENT : "#ffffff")
        .hover((s) => (on ? s : s.bg("#f1f0ff")))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click((_e, cx) => this.setTool(cx, tool.id))
        .child(tool.glyph);
    });
    if (this.selected) {
      buttons.push(
        div()
          .id("tool-delete")
          .h(30)
          .px(8)
          .ml(6)
          .rounded(6)
          .flex()
          .items_center()
          .text_size(12)
          .cursor_pointer()
          .text_color("#c92a2a")
          .hover((s) => s.bg("#fff5f5"))
          .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
          .on_click((_e, cx) => this.deleteSelected(cx))
          .child("Delete"),
      );
    }
    return row.children(buttons);
  }

  renderPane(t, cx) {
    const s = this.snap;
    const now = Date.now();
    for (const [id, until] of this.fresh) if (until < now) this.fresh.delete(id);
    const where = s ? [s.workspace, s.board && s.board !== s.workspace ? s.board : ""].filter(Boolean).join(" / ") : "";

    const header = h_flex()
      .flex_none()
      .flex_wrap()
      .gap(6)
      .px(10)
      .py(6)
      .items_center()
      .bg(t.background)
      .border_b_1()
      .border_color(t.border ?? t.dim)
      .child(div().flex_1().truncate().text_size(13).font_semibold().text_color(t.foreground).child(s?.title ?? "Canvas"))
      .when(!!where, (d) => d.child(div().text_size(11).text_color(t.dim).child(where)))
      .child(this.button(t, "zoom-out", "−", (cx) => this.zoomCenter(cx, 1 / 1.25)))
      .child(this.button(t, "zoom-in", "+", (cx) => this.zoomCenter(cx, 1.25)))
      .child(this.button(t, "fit", "Fit", (cx) => {
        if (s && this.size) this.view = fit(s.elements, this.size.width, this.size.height);
        this.userMoved = false;
        cx.notify();
      }))
      .child(this.button(t, "edit", "Browser ↗", (cx) => s?.url && cx.open_url(s.url)))
      .when(!!s, (d) => d.child(this.button(t, "copy-png", this.copying ? "Copying…" : "Copy PNG", (cx) => this.copyPNG(cx))))
      .when(!!s?.png, (d) =>
        d.child(this.button(t, "png", "PNG ⇲", null, (b) => b.drag_paths([s.png]).on_click((_e, cx) => this.flash(cx, "Drag this onto a terminal, Finder or Messages")))),
      )
      .child(this.button(t, "send", "Send to agent", (cx) => this.send(cx), (b) => b.bg(ACCENT).text_color("#ffffff").border_color(ACCENT)));

    let surface = div()
      .id("surface")
      .relative()
      .flex_1()
      .min_h(0)
      .overflow_hidden()
      .bg("#ffffff")
      .on_size_change((size, cx) => {
        const first = !this.size;
        this.size = size;
        if ((first || !this.view || !this.userMoved) && s) this.view = fit(s.elements, size.width, size.height);
        cx.notify();
      })
      .on_scroll_wheel((e, cx) => {
        if (!this.view) return;
        const lp = e.local_position ?? { x: (this.size?.width ?? 0) / 2, y: (this.size?.height ?? 0) / 2 };
        if (e.modifiers?.platform || e.modifiers?.control) this.zoomAt(Math.exp(-e.delta.y / 300), lp.x, lp.y);
        else {
          this.userMoved = true;
          this.view = { ...this.view, ox: this.view.ox + e.delta.x, oy: this.view.oy + e.delta.y };
        }
        cx.stop_propagation();
        cx.notify();
      })
      .on_pinch((e, cx) => {
        if (!this.view) return;
        const lp = e.local_position ?? { x: (this.size?.width ?? 0) / 2, y: (this.size?.height ?? 0) / 2 };
        this.zoomAt(1 + e.delta, lp.x, lp.y);
        cx.stop_propagation();
        cx.notify();
      })
      .track_focus(this.handle)
      .on_key_down((e, cx) => this.keyDown(e, cx))
      .when(this.tool !== "select", (d) => d.cursor_crosshair())
      .on_mouse_down("left", (e, cx) => this.pointerDown(e, cx))
      .on_mouse_up("left", (e, cx) => this.pointerUp(e, cx))
      .on_mouse_move((e, cx) => this.pointerMove(e, cx))
      .on_drop((e, cx) => this.dropFiles(cx, e.paths));

    if (s && this.view) {
      const order = [...s.elements].sort((a, b) => (a.type === "text") - (b.type === "text"));
      const drawn = [];
      for (const e of order) drawn.push(...drawElement(e, this.view, s.files, { highlight: this.fresh.has(e.id), accent: ACCENT }));
      if (this.draft) drawn.push(...drawElement(this.draft, this.view, s.files, {}));
      const sel = this.selected && this.byId(this.selected);
      if (sel) drawn.push(this.selectionBox(sel));
      surface = surface.children(drawn);
      if (!s.elements.length && !this.draft) surface = surface.child(div().absolute().top(56).left(20).text_color("#868e96").child("Empty canvas — pick a tool above and draw, or drop an image here"));
      if (this.textEdit) surface = surface.child(this.textBox(t));
      surface = surface.child(this.palette(t));
    } else {
      surface = surface.child(div().absolute().top(20).left(20).text_color("#868e96").child(this.error || "Loading…"));
    }

    const footer = [];
    if (s?.pending > 0) footer.push(`${s.pending} pushed item${s.pending > 1 ? "s" : ""} waiting — they appear once the editor is open (Browser ↗)`);
    if (this.note) footer.push(this.note);
    else if (this.error && s) footer.push(this.error);

    return v_flex()
      .id("canvas-pane")
      .size_full()
      .bg(t.background)
      .child(header)
      .child(surface)
      .when(footer.length > 0, (d) =>
        d.child(div().flex_none().px(10).py(5).text_size(11).text_color(t.foreground).bg(t.background).border_t_1().border_color(t.border ?? t.dim).child(footer.join("  ·  "))),
      );
  }
}
