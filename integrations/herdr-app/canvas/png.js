// Export the native drawing, without viewport clipping, selection or toolbars.
// The macOS helper embeds cached images, rasterizes this SVG, and copies PNG.
import { outline, arrowhead } from "./geometry.js";

const xml = (s) => String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&apos;" })[c]);
const color = (s, fallback = "none") => /^#[\da-f]{3}(?:[\da-f]{3})?$/i.test(s ?? "") ? s : fallback;

export function pngDocument(snapshot, home) {
  const parts = [], files = [], extent = [];
  const addPoints = (pts, pad = 0) => {
    for (const [x, y] of pts) extent.push([x - pad, y - pad], [x + pad, y + pad]);
  };
  const path = (pts, stroke, width, dash, fill = "none", closed = false) => {
    addPoints(pts, width / 2);
    return `<${closed ? "polygon" : "polyline"} points="${pts.map((p) => p.join(",")).join(" ")}" fill="${fill}" stroke="${stroke}" stroke-width="${width}" stroke-linecap="round" stroke-linejoin="round"${dash ? ` stroke-dasharray="${dash}"` : ""}/>`;
  };
  for (const e of [...(snapshot.elements ?? [])].filter((e) => !e.isDeleted).sort((a, b) => (a.type === "text") - (b.type === "text"))) {
    const ink = color(e.strokeColor, "#1e1e1e"), width = e.strokeWidth || 2;
    const dash = e.strokeStyle === "dashed" ? "8 6" : e.strokeStyle === "dotted" ? "1.5 5" : "";
    let content = "";
    switch (e.type) {
      case "rectangle": case "ellipse": case "diamond": {
        const pts = outline(e), fill = color(e.backgroundColor);
        if (fill !== "none") content += `<g opacity="0.9">${path(pts, "none", 0, "", fill, true)}</g>`;
        content += path(pts, ink, width, dash, "none", true);
        break;
      }
      case "frame": case "magicframe":
        content = path(outline({ ...e, roundness: false }), "#868e96", 1, "6 4", "none", true);
        if (e.name) {
          content += `<text x="${e.x}" y="${e.y - 6}" font-size="12" fill="#868e96">${xml(e.name)}</text>`;
          addPoints([[e.x, e.y - 20], [e.x + e.name.length * 8, e.y]]);
        }
        break;
      case "arrow": case "line": case "freedraw": {
        if (!e.points?.length) break;
        const pts = e.points.map(([x, y]) => [e.x + x, e.y + y]);
        if (pts.length === 1) pts.push([pts[0][0] + 0.5, pts[0][1] + 0.5]);
        content = path(pts, ink, e.type === "freedraw" ? width * 0.8 : width, dash);
        if (e.type === "arrow" && e.startArrowhead) content += path(arrowhead(pts[0], pts[1], 14), ink, width);
        if (e.type === "arrow" && e.endArrowhead) content += path(arrowhead(pts.at(-1), pts.at(-2), 14), ink, width);
        break;
      }
      case "text": {
        if (!e.text) break;
        const size = e.fontSize || 20, lh = size * (e.lineHeight || 1.25);
        const lines = e.text.split("\n");
        const anchor = e.textAlign === "center" ? "middle" : e.textAlign === "right" ? "end" : "start";
        const x = e.x + (anchor === "middle" ? e.width / 2 : anchor === "end" ? e.width : 0);
        const font = [3, 8].includes(e.fontFamily) ? "Menlo, monospace" : "Helvetica, sans-serif";
        content = lines.map((line, i) => `<text xml:space="preserve" x="${x}" y="${e.y + i * lh + size}" font-family="${font}" font-size="${size}" text-anchor="${anchor}" fill="${ink}">${xml(line)}</text>`).join("");
        addPoints([[e.x, e.y], [e.x + e.width, e.y + Math.max(e.height || 0, lines.length * lh)]]);
        break;
      }
      case "image": {
        const source = snapshot.files?.[e.fileId];
        if (!source) throw new Error("An image is still loading; try Copy PNG again");
        const token = `amythest-image-${files.length}`;
        files.push({ token, path: source.startsWith("/") ? source : `${home}/${source}` });
        content = `<image href="${token}" x="${e.x}" y="${e.y}" width="${e.width}" height="${e.height}" preserveAspectRatio="none"/>`;
        addPoints([[e.x, e.y], [e.x + e.width, e.y + e.height]]);
        break;
      }
    }
    parts.push(`<g opacity="${Math.max(0, Math.min(100, e.opacity ?? 100)) / 100}">${content}</g>`);
  }
  if (!extent.length) extent.push([0, 0], [320, 180]);
  let x = Infinity, y = Infinity, right = -Infinity, bottom = -Infinity;
  for (const [px, py] of extent) {
    x = Math.min(x, px); y = Math.min(y, py); right = Math.max(right, px); bottom = Math.max(bottom, py);
  }
  const width = right - x + 48, height = bottom - y + 48;
  if (![x, y, width, height].every(Number.isFinite)) throw new Error("The drawing has invalid coordinates");
  return {
    svg: `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="${x - 24} ${y - 24} ${width} ${height}" font-family="Helvetica, sans-serif"><rect x="${x - 24}" y="${y - 24}" width="${width}" height="${height}" fill="white"/>${parts.join("")}</svg>`,
    files,
  };
}
