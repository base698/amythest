// Interactive spring graph on <canvas>, powered by d3-force. Behavior
// follows Quartz's graph view: local BFS neighbourhood on each page, a
// global modal, visited-node tinting, drag/zoom/pan, click to navigate.
import {
  forceCenter,
  forceCollide,
  forceLink,
  forceManyBody,
  forceSimulation,
  type Simulation,
  type SimulationLinkDatum,
  type SimulationNodeDatum,
} from "d3-force"

type ContentIndex = Record<string, { title: string; links: string[]; tags: string[] }>

interface GNode extends SimulationNodeDatum {
  id: string
  title: string
  degree: number
  current: boolean
  visited: boolean
}
type GLink = SimulationLinkDatum<GNode>

let indexCache: ContentIndex | null = null

async function contentIndex(): Promise<ContentIndex> {
  if (!indexCache) {
    const res = await fetch(baseURL() + "api/contentIndex")
    if (!res.ok) throw new Error(`content index: ${res.status}`)
    indexCache = await res.json()
  }
  return indexCache!
}

const VISITED_KEY = "graph-visited"

function visitedSet(): Set<string> {
  try {
    return new Set(JSON.parse(localStorage.getItem(VISITED_KEY) ?? "[]"))
  } catch {
    return new Set()
  }
}

function markVisited(slug: string) {
  const v = visitedSet()
  v.add(slug)
  localStorage.setItem(VISITED_KEY, JSON.stringify([...v].slice(-500)))
}

export function setupGraph() {
  const canvas = document.getElementById("graph-canvas") as HTMLCanvasElement | null
  if (canvas) {
    const slug = canvas.dataset.slug ?? ""
    markVisited(slug)
    void renderGraph(canvas, slug, 1)
  }
  if (!globalBound) {
    globalBound = true
    // Delegated so the bindings survive SPA morphs (which strip both DOM
    // attributes and can replace the button element).
    document.addEventListener("click", (e) => {
      const modal = document.getElementById("global-graph-modal")
      if (!modal) return
      if ((e.target as Element).closest?.("#graph-global-open")) {
        const globalCanvas = document.getElementById("global-graph-canvas") as HTMLCanvasElement | null
        const local = document.getElementById("graph-canvas") as HTMLCanvasElement | null
        if (globalCanvas) {
          modal.hidden = false
          void renderGraph(globalCanvas, local?.dataset.slug ?? "", -1)
        }
      } else if (e.target === modal) {
        modal.hidden = true
      }
    })
    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        const modal = document.getElementById("global-graph-modal")
        if (modal) modal.hidden = true
      }
    })
  }
}

let globalBound = false

let activeSim: Simulation<GNode, GLink> | null = null
let removeThemeListener: (() => void) | null = null
let renderGeneration = 0

async function renderGraph(canvas: HTMLCanvasElement, slug: string, depth: number) {
  const generation = ++renderGeneration
  activeSim?.stop()
  removeThemeListener?.()
  removeThemeListener = null

  const ci = await contentIndex()
  if (generation !== renderGeneration || !canvas.isConnected) return

  // Undirected adjacency.
  const adj = new Map<string, Set<string>>()
  const touch = (a: string, b: string) => {
    if (!adj.has(a)) adj.set(a, new Set())
    adj.get(a)!.add(b)
  }
  for (const [from, entry] of Object.entries(ci)) {
    for (const to of entry.links) {
      touch(from, to)
      touch(to, from)
    }
  }

  // Select nodes: BFS from slug (depth >= 0) or everything (depth < 0).
  let keep: Set<string>
  if (depth < 0) {
    keep = new Set(Object.keys(ci))
  } else {
    keep = new Set([slug])
    let frontier = [slug]
    for (let d = 0; d < depth; d++) {
      const next: string[] = []
      for (const s of frontier) {
        for (const n of adj.get(s) ?? []) {
          if (!keep.has(n)) {
            keep.add(n)
            next.push(n)
          }
        }
      }
      frontier = next
    }
  }

  const visited = visitedSet()
  const nodes: GNode[] = [...keep]
    .filter((s) => ci[s])
    .map((s) => ({
      id: s,
      title: ci[s].title,
      degree: adj.get(s)?.size ?? 0,
      current: s === slug,
      visited: visited.has(s),
    }))
  const byId = new Map(nodes.map((n) => [n.id, n]))
  const links: GLink[] = []
  for (const [from, entry] of Object.entries(ci)) {
    for (const to of entry.links) {
      if (byId.has(from) && byId.has(to)) {
        links.push({ source: from, target: to })
      }
    }
  }

  // Size canvas to its CSS box.
  const rect = canvas.getBoundingClientRect()
  const dpr = window.devicePixelRatio || 1
  canvas.width = rect.width * dpr
  canvas.height = rect.height * dpr
  const ctx = canvas.getContext("2d")!
  const W = rect.width
  const H = rect.height

  const sim = forceSimulation<GNode>(nodes)
    .force("charge", forceManyBody().strength(-100 * 0.5))
    .force(
      "link",
      forceLink<GNode, GLink>(links)
        .id((d) => d.id)
        .distance(30),
    )
    .force("center", forceCenter(0, 0).strength(0.3))
    .force("collide", forceCollide<GNode>((d) => radius(d) + 2))
  activeSim = sim

  let scale = depth < 0 ? 0.6 : 1.1
  let tx = 0
  let ty = 0
  let hovered: GNode | null = null

  const css = (name: string) =>
    getComputedStyle(document.documentElement).getPropertyValue(name).trim()

  function radius(d: GNode): number {
    return 3 + Math.min(6, Math.sqrt(d.degree))
  }

  function draw() {
    const accent = css("--accent") || "#8458b3"
    const muted = css("--fg-faint") || "#77717f"
    const visitedColor = css("--accent-soft") || "#b28cd9"
    const fg = css("--fg") || "#2b2b33"
    ctx.save()
    ctx.clearRect(0, 0, canvas.width, canvas.height)
    ctx.scale(dpr, dpr)
    ctx.translate(W / 2 + tx, H / 2 + ty)
    ctx.scale(scale, scale)

    ctx.strokeStyle = muted
    ctx.globalAlpha = 0.4
    ctx.lineWidth = 0.7
    for (const l of links) {
      const s = l.source as GNode
      const t = l.target as GNode
      ctx.beginPath()
      ctx.moveTo(s.x!, s.y!)
      ctx.lineTo(t.x!, t.y!)
      ctx.stroke()
    }
    ctx.globalAlpha = 1

    for (const n of nodes) {
      ctx.beginPath()
      ctx.arc(n.x!, n.y!, radius(n), 0, 2 * Math.PI)
      ctx.fillStyle = n.current ? accent : n.visited ? visitedColor : muted
      if (n === hovered) ctx.fillStyle = accent
      ctx.fill()
    }

    const showLabels = scale > 1.6 || nodes.length <= 40
    ctx.fillStyle = fg
    ctx.font = `${10 / Math.max(scale, 1)}px sans-serif`
    ctx.textAlign = "center"
    for (const n of nodes) {
      if (showLabels || n === hovered || n.current) {
        ctx.globalAlpha = n === hovered || n.current ? 1 : 0.7
        ctx.fillText(n.title, n.x!, n.y! + radius(n) + 10 / Math.max(scale, 1))
      }
    }
    ctx.restore()
  }

  sim.on("tick", draw)
  document.addEventListener("themechange", draw)
  removeThemeListener = () => document.removeEventListener("themechange", draw)
  canvas.dataset.graphNodes = String(nodes.length)
  draw()

  // ---- interaction ----
  const toWorld = (mx: number, my: number): [number, number] => [
    (mx - W / 2 - tx) / scale,
    (my - H / 2 - ty) / scale,
  ]
  const pick = (mx: number, my: number): GNode | null => {
    const [wx, wy] = toWorld(mx, my)
    let best: GNode | null = null
    let bestD = Number.POSITIVE_INFINITY
    for (const n of nodes) {
      const dx = n.x! - wx
      const dy = n.y! - wy
      const d = dx * dx + dy * dy
      const hitRadius = radius(n) + 8 / scale
      if (d <= hitRadius * hitRadius && d < bestD) {
        bestD = d
        best = n
      }
    }
    return best
  }

  let dragging: GNode | null = null
  let panning = false
  let lastX = 0
  let lastY = 0
  let moved = false

  canvas.onpointerdown = (e) => {
    const r = canvas.getBoundingClientRect()
    const n = pick(e.clientX - r.left, e.clientY - r.top)
    moved = false
    if (n) {
      dragging = n
      sim.alphaTarget(0.3).restart()
    } else {
      panning = true
    }
    lastX = e.clientX
    lastY = e.clientY
    canvas.setPointerCapture(e.pointerId)
  }
  canvas.onpointermove = (e) => {
    const r = canvas.getBoundingClientRect()
    if (dragging) {
      const [wx, wy] = toWorld(e.clientX - r.left, e.clientY - r.top)
      dragging.fx = wx
      dragging.fy = wy
      moved = true
    } else if (panning) {
      tx += e.clientX - lastX
      ty += e.clientY - lastY
      lastX = e.clientX
      lastY = e.clientY
      moved = true
      draw()
    } else {
      const h = pick(e.clientX - r.left, e.clientY - r.top)
      if (h !== hovered) {
        hovered = h
        canvas.style.cursor = h ? "pointer" : "default"
        canvas.title = h?.title ?? ""
        draw()
      }
    }
  }
  canvas.onpointerup = (e) => {
    if (dragging) {
      dragging.fx = null
      dragging.fy = null
      sim.alphaTarget(0)
      if (!moved) navigate(dragging.id)
    } else if (panning && !moved) {
      const r = canvas.getBoundingClientRect()
      const n = pick(e.clientX - r.left, e.clientY - r.top)
      if (n) navigate(n.id)
    }
    dragging = null
    panning = false
  }
  canvas.onwheel = (e) => {
    e.preventDefault()
    const factor = Math.exp(-e.deltaY * 0.002)
    scale = Math.min(6, Math.max(0.15, scale * factor))
    draw()
  }
}

function navigate(slug: string) {
  window.location.href = baseURL() + slug.split("/").map(encodeURIComponent).join("/")
}

function baseURL(): string {
  const base = document.body.dataset.base || "/"
  return base.endsWith("/") ? base : base + "/"
}
