// Interactive STL viewer. Lazy-loaded by main.ts only when a page contains
// a model embed, because the WebGL context and the mesh itself are far more
// expensive than the rest of a note.
//
// No 3D library: an STL is a flat list of triangles with face normals, so
// parsing is a loop over a DataView and drawing is one shader. Pulling in a
// scene graph for that would cost more than the whole bundle.

type Mesh = { positions: Float32Array; normals: Float32Array; triangles: number }

const MAX_BYTES = 64 * 1024 * 1024

// parseSTL handles both encodings. Binary is the common case; the header is
// not a reliable discriminator (plenty of binary files start with "solid"),
// so the triangle-count arithmetic decides.
export function parseSTL(buffer: ArrayBuffer): Mesh {
  const view = new DataView(buffer)
  if (buffer.byteLength >= 84) {
    const count = view.getUint32(80, true)
    if (84 + count * 50 === buffer.byteLength) return parseBinary(view, count)
  }
  return parseASCII(new TextDecoder().decode(buffer))
}

function parseBinary(view: DataView, count: number): Mesh {
  const positions = new Float32Array(count * 9)
  const normals = new Float32Array(count * 9)
  let offset = 84
  for (let i = 0; i < count; i++) {
    const nx = view.getFloat32(offset, true)
    const ny = view.getFloat32(offset + 4, true)
    const nz = view.getFloat32(offset + 8, true)
    offset += 12
    for (let v = 0; v < 3; v++) {
      const base = i * 9 + v * 3
      positions[base] = view.getFloat32(offset, true)
      positions[base + 1] = view.getFloat32(offset + 4, true)
      positions[base + 2] = view.getFloat32(offset + 8, true)
      normals[base] = nx
      normals[base + 1] = ny
      normals[base + 2] = nz
      offset += 12
    }
    offset += 2 // attribute byte count, unused
  }
  return finish({ positions, normals, triangles: count })
}

function parseASCII(text: string): Mesh {
  const verts: number[] = []
  const norms: number[] = []
  let nx = 0, ny = 0, nz = 0
  for (const raw of text.split("\n")) {
    const line = raw.trim()
    if (line.startsWith("facet normal")) {
      const n = line.split(/\s+/)
      nx = Number(n[2]) || 0
      ny = Number(n[3]) || 0
      nz = Number(n[4]) || 0
    } else if (line.startsWith("vertex")) {
      const p = line.split(/\s+/)
      verts.push(Number(p[1]) || 0, Number(p[2]) || 0, Number(p[3]) || 0)
      norms.push(nx, ny, nz)
    }
  }
  const triangles = Math.floor(verts.length / 9)
  return finish({
    positions: new Float32Array(verts.slice(0, triangles * 9)),
    normals: new Float32Array(norms.slice(0, triangles * 9)),
    triangles,
  })
}

// finish replaces any zero-length normal with the triangle's own geometric
// normal. Exporters write those more often than you would hope, and a zero
// normal renders as a black facet.
function finish(mesh: Mesh): Mesh {
  const { positions, normals, triangles } = mesh
  for (let i = 0; i < triangles; i++) {
    const b = i * 9
    if (normals[b] !== 0 || normals[b + 1] !== 0 || normals[b + 2] !== 0) continue
    const ax = positions[b + 3] - positions[b], ay = positions[b + 4] - positions[b + 1], az = positions[b + 5] - positions[b + 2]
    const bx = positions[b + 6] - positions[b], by = positions[b + 7] - positions[b + 1], bz = positions[b + 8] - positions[b + 2]
    let cx = ay * bz - az * by, cy = az * bx - ax * bz, cz = ax * by - ay * bx
    const len = Math.hypot(cx, cy, cz) || 1
    cx /= len; cy /= len; cz /= len
    for (let v = 0; v < 3; v++) {
      normals[b + v * 3] = cx
      normals[b + v * 3 + 1] = cy
      normals[b + v * 3 + 2] = cz
    }
  }
  return mesh
}

function bounds(positions: Float32Array) {
  const min = [Infinity, Infinity, Infinity]
  const max = [-Infinity, -Infinity, -Infinity]
  for (let i = 0; i < positions.length; i += 3) {
    for (let a = 0; a < 3; a++) {
      const value = positions[i + a]
      if (value < min[a]) min[a] = value
      if (value > max[a]) max[a] = value
    }
  }
  if (!isFinite(min[0])) return { center: [0, 0, 0], radius: 1, size: [0, 0, 0] }
  const center = [0, 1, 2].map((a) => (min[a] + max[a]) / 2)
  const size = [0, 1, 2].map((a) => max[a] - min[a])
  return { center, radius: Math.max(Math.hypot(...size) / 2, 1e-6), size }
}

const VERT = `
attribute vec3 position;
attribute vec3 normal;
uniform mat4 uModel, uView, uProj;
varying vec3 vNormal, vEye;
void main() {
  vec4 world = uModel * vec4(position, 1.0);
  vec4 eye = uView * world;
  vNormal = mat3(uModel) * normal;
  vEye = -eye.xyz;
  gl_Position = uProj * eye;
}`

// A headlight plus a dim fill, and a rim term so the silhouette stays
// readable against both light and dark page backgrounds.
const FRAG = `
precision mediump float;
varying vec3 vNormal, vEye;
uniform vec3 uColor, uRim;
void main() {
  vec3 n = normalize(vNormal);
  vec3 e = normalize(vEye);
  if (!gl_FrontFacing) n = -n;
  float key = max(dot(n, normalize(vec3(0.4, 0.7, 1.0))), 0.0);
  float fill = max(dot(n, normalize(vec3(-0.6, -0.3, 0.5))), 0.0) * 0.25;
  float rim = pow(1.0 - max(dot(n, e), 0.0), 2.5);
  vec3 c = uColor * (0.25 + 0.75 * key + fill) + uRim * rim * 0.6;
  gl_FragColor = vec4(c, 1.0);
}`

function compile(gl: WebGLRenderingContext, type: number, src: string) {
  const shader = gl.createShader(type)!
  gl.shaderSource(shader, src)
  gl.compileShader(shader)
  if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
    throw new Error(gl.getShaderInfoLog(shader) || "shader compile failed")
  }
  return shader
}

function perspective(fov: number, aspect: number, near: number, far: number) {
  const f = 1 / Math.tan(fov / 2)
  return new Float32Array([
    f / aspect, 0, 0, 0,
    0, f, 0, 0,
    0, 0, (far + near) / (near - far), -1,
    0, 0, (2 * far * near) / (near - far), 0,
  ])
}

// Model matrix: translate the mesh to its own centre, then yaw/pitch it.
// Z-up is the STL convention, so the initial pitch tips it toward the viewer.
function modelMatrix(center: number[], yaw: number, pitch: number) {
  const cy = Math.cos(yaw), sy = Math.sin(yaw)
  const cp = Math.cos(pitch), sp = Math.sin(pitch)
  const r = [
    cy, sy * sp, -sy * cp,
    0, cp, sp,
    sy, -cy * sp, cy * cp,
  ]
  const t = [-center[0], -center[1], -center[2]]
  return new Float32Array([
    r[0], r[1], r[2], 0,
    r[3], r[4], r[5], 0,
    r[6], r[7], r[8], 0,
    r[0] * t[0] + r[3] * t[1] + r[6] * t[2],
    r[1] * t[0] + r[4] * t[1] + r[7] * t[2],
    r[2] * t[0] + r[5] * t[1] + r[8] * t[2],
    1,
  ])
}

function cssColor(el: Element, name: string, fallback: [number, number, number]) {
  const raw = getComputedStyle(el).getPropertyValue(name).trim()
  const m = raw.match(/^#([0-9a-f]{6})$/i)
  if (m) {
    const n = parseInt(m[1], 16)
    return [((n >> 16) & 255) / 255, ((n >> 8) & 255) / 255, (n & 255) / 255] as [number, number, number]
  }
  const rgb = raw.match(/rgba?\(([^)]+)\)/)
  if (rgb) {
    const parts = rgb[1].split(",").map((v) => parseFloat(v) / 255)
    if (parts.length >= 3) return [parts[0], parts[1], parts[2]] as [number, number, number]
  }
  return fallback
}

function mount(container: HTMLElement, mesh: Mesh) {
  const canvas = document.createElement("canvas")
  canvas.className = "model-canvas"
  const gl = canvas.getContext("webgl", { antialias: true, alpha: true }) as WebGLRenderingContext | null
  if (!gl) throw new Error("WebGL unavailable")

  const program = gl.createProgram()!
  gl.attachShader(program, compile(gl, gl.VERTEX_SHADER, VERT))
  gl.attachShader(program, compile(gl, gl.FRAGMENT_SHADER, FRAG))
  gl.linkProgram(program)
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
    throw new Error(gl.getProgramInfoLog(program) || "program link failed")
  }
  gl.useProgram(program)

  const buffer = (data: Float32Array, name: string) => {
    const buf = gl.createBuffer()
    gl.bindBuffer(gl.ARRAY_BUFFER, buf)
    gl.bufferData(gl.ARRAY_BUFFER, data, gl.STATIC_DRAW)
    const loc = gl.getAttribLocation(program, name)
    gl.enableVertexAttribArray(loc)
    gl.vertexAttribPointer(loc, 3, gl.FLOAT, false, 0, 0)
  }
  buffer(mesh.positions, "position")
  buffer(mesh.normals, "normal")

  const { center, radius } = bounds(mesh.positions)
  const uModel = gl.getUniformLocation(program, "uModel")
  const uView = gl.getUniformLocation(program, "uView")
  const uProj = gl.getUniformLocation(program, "uProj")
  const uColor = gl.getUniformLocation(program, "uColor")
  const uRim = gl.getUniformLocation(program, "uRim")
  gl.enable(gl.DEPTH_TEST)

  const home = { yaw: 0.6, pitch: -1.0, zoom: 2.6 }
  let yaw = home.yaw, pitch = home.pitch, zoom = home.zoom
  let frame = 0

  const draw = () => {
    frame = 0
    const dpr = Math.min(window.devicePixelRatio || 1, 2)
    const w = Math.max(container.clientWidth, 1)
    const h = Math.max(Math.round(w * 0.6), 1)
    if (canvas.width !== Math.round(w * dpr) || canvas.height !== Math.round(h * dpr)) {
      canvas.width = Math.round(w * dpr)
      canvas.height = Math.round(h * dpr)
      canvas.style.height = `${h}px`
    }
    gl.viewport(0, 0, canvas.width, canvas.height)
    gl.clearColor(0, 0, 0, 0)
    gl.clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)

    const dist = radius * zoom
    gl.uniformMatrix4fv(uModel, false, modelMatrix(center, yaw, pitch))
    gl.uniformMatrix4fv(uView, false, new Float32Array([
      1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, -dist, 1,
    ]))
    gl.uniformMatrix4fv(uProj, false, perspective(0.9, canvas.width / canvas.height, radius * 0.01, dist + radius * 4))
    gl.uniform3fv(uColor, cssColor(container, "--accent", [0.55, 0.37, 0.75]))
    gl.uniform3fv(uRim, cssColor(container, "--fg", [1, 1, 1]))
    gl.drawArrays(gl.TRIANGLES, 0, mesh.triangles * 3)
  }
  const schedule = () => {
    if (!frame) frame = requestAnimationFrame(draw)
  }

  // Drag to orbit. Pitch is clamped just short of the poles so the model
  // never flips through its own axis mid-drag.
  let dragging = false, lastX = 0, lastY = 0
  canvas.addEventListener("pointerdown", (e) => {
    dragging = true
    lastX = e.clientX
    lastY = e.clientY
    canvas.setPointerCapture(e.pointerId)
  })
  canvas.addEventListener("pointermove", (e) => {
    if (!dragging) return
    yaw += (e.clientX - lastX) * 0.01
    pitch = Math.max(-Math.PI / 2 + 0.01, Math.min(Math.PI / 2 - 0.01, pitch + (e.clientY - lastY) * 0.01))
    lastX = e.clientX
    lastY = e.clientY
    schedule()
  })
  const release = (e: PointerEvent) => {
    dragging = false
    if (canvas.hasPointerCapture(e.pointerId)) canvas.releasePointerCapture(e.pointerId)
  }
  canvas.addEventListener("pointerup", release)
  canvas.addEventListener("pointercancel", release)
  canvas.addEventListener("dblclick", () => {
    yaw = home.yaw; pitch = home.pitch; zoom = home.zoom
    schedule()
  })
  // Only claim the wheel once the pointer is over the canvas and the page
  // is not mid-scroll past it; passive:false is required to preventDefault.
  canvas.addEventListener("wheel", (e) => {
    e.preventDefault()
    zoom = Math.max(1.2, Math.min(12, zoom * (e.deltaY > 0 ? 1.1 : 0.9)))
    schedule()
  }, { passive: false })

  container.replaceChildren(canvas)
  const meta = document.createElement("p")
  meta.className = "model-meta"
  meta.textContent = `${container.dataset.modelName || "model"} · ${mesh.triangles.toLocaleString()} triangles · drag to rotate, scroll to zoom, double-click to reset`
  container.appendChild(meta)

  new ResizeObserver(schedule).observe(container)
  document.addEventListener("themechange", schedule)
  draw()
}

export async function renderModels() {
  const nodes = Array.from(document.querySelectorAll<HTMLElement>(".model-embed[data-model-src]:not([data-model-state])"))
  for (const node of nodes) {
    node.dataset.modelState = "loading"
    const fallback = node.innerHTML
    const status = document.createElement("p")
    status.className = "model-meta"
    status.textContent = "loading model…"
    node.replaceChildren(status)
    try {
      const res = await fetch(node.dataset.modelSrc!)
      if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
      const size = Number(res.headers.get("content-length") || 0)
      if (size > MAX_BYTES) throw new Error("model is too large to display")
      const mesh = parseSTL(await res.arrayBuffer())
      if (!mesh.triangles) throw new Error("no triangles in file")
      mount(node, mesh)
      node.dataset.modelState = "ready"
    } catch (err) {
      // Put the plain download link back: a broken viewer should never
      // cost the reader access to the file.
      node.dataset.modelState = "error"
      node.innerHTML = fallback
      const note = document.createElement("p")
      note.className = "model-meta model-error"
      note.textContent = `could not display model: ${err instanceof Error ? err.message : String(err)}`
      node.appendChild(note)
    }
  }
}
