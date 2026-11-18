// Draws an Amythest canvas (an Excalidraw scene, as `amy canvas snapshot`
// prints it) with Herdr's native paths, text and images. Scene coordinates go
// through a view {zoom, ox, oy}: screen = scene * zoom + offset.

import { div, image, PathBuilder } from "gpui-kit";
import { bounds, outline, arrowhead } from "./geometry.js";
export { bounds } from "./geometry.js";

const PAPER_INK = "#1e1e1e";

export function fit(elements, width, height) {
  const b = bounds(elements);
  const pad = 32;
  const zoom = Math.max(0.05, Math.min(2, (width - pad * 2) / b.w, (height - pad * 2) / b.h));
  return { zoom, ox: (width - b.w * zoom) / 2 - b.x * zoom, oy: (height - b.h * zoom) / 2 - b.y * zoom };
}

function color(c, opacity) {
  if (!c || c === "transparent") return null;
  let hex = c.startsWith("#") ? c : null;
  if (!hex) return null;
  if (hex.length === 4) hex = "#" + [...hex.slice(1)].map((ch) => ch + ch).join("");
  if (hex.length !== 7) return hex.slice(0, 7);
  const a = Math.round(Math.max(0, Math.min(100, opacity ?? 100)) * 2.55);
  return a >= 255 ? hex : hex + a.toString(16).padStart(2, "0");
}

function dashes(e, z) {
  if (e.strokeStyle === "dashed") return [8 * z, 6 * z];
  if (e.strokeStyle === "dotted") return [1.5 * z, 5 * z];
  return null;
}

function paint(builder, fill) {
  return window.paint_path(builder.build(), fill).absolute().top(0).left(0).size_full();
}

function polyline(pts, width, dash) {
  let b = PathBuilder.stroke(width);
  if (dash) b = b.dash_array(dash);
  b = b.move_to(pts[0][0], pts[0][1]);
  for (const p of pts.slice(1)) b = b.line_to(p[0], p[1]);
  return b;
}

const FONT = { 3: "Menlo", 8: "Menlo" };

// One drawn element, or several (a shape and its outline), in screen space.
export function drawElement(e, view, files, opts = {}) {
  const z = view.zoom;
  const S = ([px, py]) => [px * z + view.ox, py * z + view.oy];
  const ink = color(opts.highlight ? opts.accent : e.strokeColor || PAPER_INK, e.opacity);
  const width = Math.max(1, (e.strokeWidth || 2) * z * (opts.highlight ? 1.6 : 1));
  const out = [];
  switch (e.type) {
    case "rectangle":
    case "diamond":
    case "ellipse": {
      const pts = outline(e).map(S);
      const fill = color(e.backgroundColor, (e.opacity ?? 100) * 0.9);
      if (fill) out.push(paint(PathBuilder.fill().add_polygon(pts, true), fill));
      if (ink) out.push(paint(polyline([...pts, pts[0]], width, dashes(e, z)), ink));
      break;
    }
    case "frame":
    case "magicframe": {
      const pts = outline({ ...e, roundness: false }).map(S);
      out.push(paint(polyline([...pts, pts[0]], 1, [6, 4]), "#868e96"));
      if (e.name) out.push(label(e.name, S([e.x, e.y - 18 / z]), 12, "#868e96"));
      break;
    }
    case "arrow":
    case "line":
    case "freedraw": {
      if (!e.points?.length || !ink) break;
      const pts = e.points.map((p) => S([e.x + p[0], e.y + p[1]]));
      if (pts.length === 1) pts.push([pts[0][0] + 0.5, pts[0][1] + 0.5]);
      out.push(paint(polyline(pts, e.type === "freedraw" ? Math.max(1, width * 0.8) : width, dashes(e, z)), ink));
      const head = Math.max(6, 14 * z);
      if (e.type === "arrow" && e.endArrowhead) out.push(paint(polyline(arrowhead(pts[pts.length - 1], pts[pts.length - 2], head), width), ink));
      if (e.type === "arrow" && e.startArrowhead) out.push(paint(polyline(arrowhead(pts[0], pts[1], head), width), ink));
      break;
    }
    case "text": {
      if (!e.text) break;
      const size = (e.fontSize || 20) * z;
      if (size < 3) break;
      const lh = size * (e.lineHeight || 1.25);
      const [sx, sy] = S([e.x, e.y]);
      let box = div().absolute().left(sx).top(sy).w(Math.max(4, e.width * z + 2)).text_size(size).line_height(`${lh}px`).text_color(ink || PAPER_INK);
      if (FONT[e.fontFamily]) box = box.font_family(FONT[e.fontFamily]);
      if (e.textAlign === "center") box = box.text_center();
      else if (e.textAlign === "right") box = box.text_right();
      out.push(box.children(e.text.split("\n").map((line) => div().whitespace_nowrap().child(line || " "))));
      break;
    }
    case "image": {
      const path = files[e.fileId];
      if (!path) break;
      const [sx, sy] = S([e.x, e.y]);
      out.push(image(path).absolute().left(sx).top(sy).w(Math.max(1, e.width * z)).h(Math.max(1, e.height * z)));
      break;
    }
  }
  return out;
}

function label(text, [x, y], size, c) {
  return div().absolute().left(x).top(y).text_size(size).text_color(c).whitespace_nowrap().child(text);
}
