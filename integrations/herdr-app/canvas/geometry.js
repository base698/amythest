// Shared scene geometry for the native pane and PNG export.

export function bounds(elements) {
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
  for (const e of elements) {
    let xs = [e.x, e.x + (e.width || 0)];
    let ys = [e.y, e.y + (e.height || 0)];
    if (e.points?.length) {
      xs = e.points.map((p) => e.x + p[0]);
      ys = e.points.map((p) => e.y + p[1]);
    }
    minX = Math.min(minX, ...xs);
    maxX = Math.max(maxX, ...xs);
    minY = Math.min(minY, ...ys);
    maxY = Math.max(maxY, ...ys);
  }
  if (!isFinite(minX)) return { x: 0, y: 0, w: 1, h: 1 };
  return { x: minX, y: minY, w: Math.max(1, maxX - minX), h: Math.max(1, maxY - minY) };
}

// Points of a closed shape in scene space, rotated about its middle.
export function outline(e) {
  const { x, y } = e;
  const w = e.width, h = e.height;
  let pts;
  if (e.type === "diamond") pts = [[x + w / 2, y], [x + w, y + h / 2], [x + w / 2, y + h], [x, y + h / 2]];
  else if (e.type === "ellipse") {
    pts = [];
    for (let i = 0; i < 48; i++) {
      const t = (i / 48) * Math.PI * 2;
      pts.push([x + w / 2 + (w / 2) * Math.cos(t), y + h / 2 + (h / 2) * Math.sin(t)]);
    }
  } else if (e.roundness && w > 8 && h > 8) {
    const r = Math.min(w, h) * 0.18;
    pts = [];
    const corner = (cx, cy, start) => {
      for (let i = 0; i <= 6; i++) {
        const t = start + (i / 6) * (Math.PI / 2);
        pts.push([cx + r * Math.cos(t), cy + r * Math.sin(t)]);
      }
    };
    corner(x + w - r, y + r, -Math.PI / 2);
    corner(x + w - r, y + h - r, 0);
    corner(x + r, y + h - r, Math.PI / 2);
    corner(x + r, y + r, Math.PI);
  } else pts = [[x, y], [x + w, y], [x + w, y + h], [x, y + h]];
  if (e.angle) {
    const cx = x + w / 2, cy = y + h / 2, c = Math.cos(e.angle), s = Math.sin(e.angle);
    pts = pts.map(([px, py]) => [cx + (px - cx) * c - (py - cy) * s, cy + (px - cx) * s + (py - cy) * c]);
  }
  return pts;
}

export function arrowhead(tip, from, size) {
  const ang = Math.atan2(tip[1] - from[1], tip[0] - from[0]);
  const wing = (d) => [tip[0] - size * Math.cos(ang + d), tip[1] - size * Math.sin(ang + d)];
  return [wing(0.45), tip, wing(-0.45)];
}
