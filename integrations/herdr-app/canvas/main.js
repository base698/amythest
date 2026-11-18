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
import { v_flex, h_flex } from "gpui-base";
import { exec, props, theme, appHome, checking, closeOverlay, run, spaces, focused } from "herdr";
import { binary } from "./config.js";
import { drawElement, fit } from "./draw.js";

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
      }
    });
  }

  pick(cx, browser) {
    if (this.at === 0) return this.create(cx, true);
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
          .child(div().text_size(11).text_color(t.dim).child("opens the editor too"));
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
    if (checking()) return;
    this.refresh(cx);
    cx.timer.every(POLL_MS, (cx) => this.refresh(cx));
  }

  refresh(cx) {
    if (this.loading) return;
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
          const first = !this.snap;
          this.snap = s;
          if (first && this.size) this.view = fit(s.elements, this.size.width, this.size.height);
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

  zoomAt(factor, px, py) {
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

  renderPane(t, cx) {
    const s = this.snap;
    const now = Date.now();
    for (const [id, until] of this.fresh) if (until < now) this.fresh.delete(id);
    const where = s ? [s.workspace, s.board && s.board !== s.workspace ? s.board : ""].filter(Boolean).join(" / ") : "";

    const header = h_flex()
      .flex_none()
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
        cx.notify();
      }))
      .child(this.button(t, "edit", "Edit ↗", (cx) => s?.url && cx.open_url(s.url)))
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
        if ((first || !this.view) && s) this.view = fit(s.elements, size.width, size.height);
        cx.notify();
      })
      .on_scroll_wheel((e, cx) => {
        if (!this.view) return;
        const lp = e.local_position ?? { x: (this.size?.width ?? 0) / 2, y: (this.size?.height ?? 0) / 2 };
        if (e.modifiers?.platform || e.modifiers?.control) this.zoomAt(Math.exp(-e.delta.y / 300), lp.x, lp.y);
        else this.view = { ...this.view, ox: this.view.ox + e.delta.x, oy: this.view.oy + e.delta.y };
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
      .on_mouse_down("left", (e, cx) => {
        if (e.click_count >= 2 && s && this.size) {
          this.view = fit(s.elements, this.size.width, this.size.height);
        } else if (this.view && e.local_position) {
          this.dragging = { x: e.position.x, y: e.position.y, ox: this.view.ox, oy: this.view.oy };
        }
        cx.notify();
      })
      .on_mouse_up("left", () => (this.dragging = null))
      .on_mouse_move((e, cx) => {
        const d = this.dragging;
        if (!d || !this.view) return;
        this.view = { ...this.view, ox: d.ox + e.position.x - d.x, oy: d.oy + e.position.y - d.y };
        cx.notify();
      })
      .on_drop((e, cx) => this.dropFiles(cx, e.paths));

    if (s && this.view) {
      const order = [...s.elements].sort((a, b) => (a.type === "text") - (b.type === "text"));
      const drawn = [];
      for (const e of order) drawn.push(...drawElement(e, this.view, s.files, { highlight: this.fresh.has(e.id), accent: ACCENT }));
      surface = surface.children(drawn);
      if (!s.elements.length) surface = surface.child(div().absolute().top(20).left(20).text_color("#868e96").child("Empty canvas — press Edit ↗ to draw, or drop an image here"));
    } else {
      surface = surface.child(div().absolute().top(20).left(20).text_color("#868e96").child(this.error || "Loading…"));
    }

    const footer = [];
    if (s?.pending > 0) footer.push(`${s.pending} pushed item${s.pending > 1 ? "s" : ""} waiting — they appear once the editor is open (Edit ↗)`);
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
