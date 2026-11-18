// Editing for the native canvas pane: hit testing, the elements each tool
// makes, and the bookkeeping that keeps arrows and labels attached. Works on
// scene coordinates; main.js converts pointer positions and sends the
// resulting element edits to `amy canvas ops`.

export const TOOLS = [
  { id: "select", key: "v", glyph: "↖", name: "Select / move" },
  { id: "rectangle", key: "r", glyph: "▭", name: "Box" },
  { id: "ellipse", key: "o", glyph: "◯", name: "Ellipse" },
  { id: "diamond", key: "d", glyph: "◇", name: "Diamond" },
  { id: "arrow", key: "a", glyph: "→", name: "Arrow" },
  { id: "freedraw", key: "p", glyph: "✎", name: "Pen" },
  { id: "text", key: "t", glyph: "T", name: "Text" },
];

const SHAPES = new Set(["rectangle", "ellipse", "diamond"]);
const ID_CHARS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";

export function newId() {
  let s = "";
  for (let i = 0; i < 21; i++) s += ID_CHARS[Math.floor(Math.random() * ID_CHARS.length)];
  return s;
}

export function toScene(view, p) {
  return { x: (p.x - view.ox) / view.zoom, y: (p.y - view.oy) / view.zoom };
}

function box(e) {
  const x = Math.min(e.x, e.x + e.width), y = Math.min(e.y, e.y + e.height);
  return { x, y, w: Math.abs(e.width), h: Math.abs(e.height) };
}

function distToSegment(p, a, b) {
  const dx = b[0] - a[0], dy = b[1] - a[1];
  const len = dx * dx + dy * dy;
  const t = len ? Math.max(0, Math.min(1, ((p.x - a[0]) * dx + (p.y - a[1]) * dy) / len)) : 0;
  return Math.hypot(p.x - (a[0] + t * dx), p.y - (a[1] + t * dy));
}

// hit finds the topmost element under a scene point. tol is in scene units.
export function hit(elements, p, tol) {
  for (let i = elements.length - 1; i >= 0; i--) {
    const e = elements[i];
    if (e.containerId) continue; // a label moves with its shape
    if (e.points?.length) {
      for (let j = 1; j < e.points.length; j++) {
        const a = [e.x + e.points[j - 1][0], e.y + e.points[j - 1][1]];
        const b = [e.x + e.points[j][0], e.y + e.points[j][1]];
        if (distToSegment(p, a, b) <= tol) return e;
      }
      continue;
    }
    const b = box(e);
    if (p.x >= b.x - tol && p.x <= b.x + b.w + tol && p.y >= b.y - tol && p.y <= b.y + b.h + tol) return e;
  }
  return null;
}

// shapeAt finds the box, ellipse or diamond an arrow end should attach to.
export function shapeAt(elements, p, tol) {
  for (let i = elements.length - 1; i >= 0; i--) {
    const e = elements[i];
    if (!SHAPES.has(e.type)) continue;
    const b = box(e);
    if (p.x >= b.x - tol && p.x <= b.x + b.w + tol && p.y >= b.y - tol && p.y <= b.y + b.h + tol) return e;
  }
  return null;
}

const BASE = {
  angle: 0, strokeColor: "#1e1e1e", backgroundColor: "transparent", fillStyle: "solid",
  strokeWidth: 2, strokeStyle: "solid", roughness: 1, opacity: 100, isDeleted: false,
};

export function makeShape(type, a, b) {
  let x = Math.min(a.x, b.x), y = Math.min(a.y, b.y);
  let w = Math.abs(b.x - a.x), h = Math.abs(b.y - a.y);
  if (w < 8 && h < 8) {
    // A click, not a drag: a sensible default size around it.
    w = 140;
    h = 70;
    x = a.x - w / 2;
    y = a.y - h / 2;
  }
  return { ...BASE, id: newId(), type, x, y, width: w, height: h, roundness: type !== "ellipse", boundElements: [] };
}

// Linear elements keep x,y at their first point and points relative to it.
export function setPoints(e, abs) {
  const [x0, y0] = abs[0];
  e.x = x0;
  e.y = y0;
  e.points = abs.map(([x, y]) => [x - x0, y - y0]);
  const xs = e.points.map((p) => p[0]), ys = e.points.map((p) => p[1]);
  e.width = Math.max(...xs) - Math.min(...xs);
  e.height = Math.max(...ys) - Math.min(...ys);
  return e;
}

export function absPoints(e) {
  return e.points.map(([x, y]) => [e.x + x, e.y + y]);
}

export function makeArrow(a, b, from, to) {
  const e = { ...BASE, id: newId(), type: "arrow", roundness: true, startArrowhead: null, endArrowhead: "arrow" };
  setPoints(e, [[a.x, a.y], [b.x, b.y]]);
  e.startBinding = from ? { elementId: from.id, focus: 0, gap: 4 } : null;
  e.endBinding = to ? { elementId: to.id, focus: 0, gap: 4 } : null;
  return e;
}

export function makeStroke(points) {
  const e = { ...BASE, id: newId(), type: "freedraw", strokeWidth: 2, pressures: [], simulatePressure: true };
  return setPoints(e, points.map((p) => [p.x, p.y]));
}

// Text is measured roughly; Excalidraw clips text to its width, so err wide.
export function textSize(text, fontSize) {
  const lines = text.split("\n");
  const longest = Math.max(...lines.map((l) => [...l].length), 1);
  return { width: longest * fontSize * 0.62 + 8, height: lines.length * fontSize * 1.25 };
}

export function makeText(at, text, container) {
  const fontSize = 20;
  const { width, height } = textSize(text, fontSize);
  const e = {
    ...BASE, id: newId(), type: "text", text, originalText: text, fontSize, fontFamily: 5,
    lineHeight: 1.25, autoResize: true, width, height,
    textAlign: container ? "center" : "left", verticalAlign: container ? "middle" : "top",
    containerId: container ? container.id : null, x: at.x, y: at.y,
  };
  if (container) centerLabel(e, container);
  return e;
}

export function centerLabel(label, shape) {
  const b = box(shape);
  label.width = Math.min(label.width, Math.max(20, b.w - 10));
  label.x = b.x + (b.w - label.width) / 2;
  label.y = b.y + (b.h - label.height) / 2;
}

export function labelOf(elements, shape) {
  return elements.find((e) => e.type === "text" && e.containerId === shape.id && !e.isDeleted) ?? null;
}

// Fields an ops upsert should carry for an element the pane changed.
export function wire(e) {
  const out = { ...e };
  out.roundness = e.roundness ? { type: e.type === "arrow" ? 2 : 3 } : null;
  if (out.boundElements === undefined) delete out.boundElements;
  return out;
}
