// Canvas: a shared drawing surface between a person and an agent.
//
// The person draws here (Excalidraw). Every change autosaves the scene plus an
// SVG and PNG rendering, so `amy canvas read` can summarize it and the PNG can
// go straight into panes, notes and messages. Agents push Mermaid, images and
// text into the canvas inbox; this page places them, selects them so the person
// sees what arrived, and saves. "Send to agent" wakes `amy canvas wait`.
import { StrictMode, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { createRoot } from "react-dom/client"
import type { ExcalidrawImperativeAPI, BinaryFileData, AppState } from "@excalidraw/excalidraw/types"
import type { ExcalidrawElement } from "@excalidraw/excalidraw/element/types"
import "@excalidraw/excalidraw/index.css"
import "./canvas.css"
import { flattenSubgraphs } from "./subgraphs.ts"

const rootEl = document.getElementById("canvas-root")!
const base = rootEl.dataset.base || "/"
const canvasID = rootEl.dataset.id || ""
// Fonts are served from our own bundle (see web/build.mjs), never a CDN.
;(window as any).EXCALIDRAW_ASSET_PATH = `${location.origin}${base}static/canvas-ui/`

// Excalidraw is large: load it after the asset path is set.
const ex = await import("@excalidraw/excalidraw")

interface Meta {
  id: string
  title: string
  rev: number
  sent: number
  inbox: number
  applied: number
  updated: string
  url: string
  drawing: boolean
  stale?: boolean
}

interface InboxItem {
  seq: number
  kind: "mermaid" | "image" | "text"
  name?: string
  data: string
  from?: string
}

class HttpError extends Error {
  constructor(public status: number, message: string, public body?: any) {
    super(message)
  }
}

let csrfToken = ""
async function csrf(): Promise<string> {
  if (csrfToken) return csrfToken
  const res = await fetch(`${base}kanban/api/session`, { credentials: "same-origin", cache: "no-store" })
  if (!res.ok) throw new HttpError(res.status, "Sign in to use canvases")
  csrfToken = (await res.json()).csrf || ""
  return csrfToken
}

async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" }
  if (method !== "GET") {
    headers["X-CSRF-Token"] = await csrf()
    headers["Content-Type"] = "application/json"
  }
  const res = await fetch(`${base}api/canvas${path}`, {
    method,
    headers,
    credentials: "same-origin",
    cache: "no-store",
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await res.text()
  let parsed: any = undefined
  try {
    parsed = text ? JSON.parse(text) : undefined
  } catch {
    parsed = undefined
  }
  if (!res.ok) {
    const msg = parsed?.error || text || res.statusText
    throw new HttpError(res.status, res.status === 401 ? "Sign in to use canvases" : msg, parsed)
  }
  return parsed as T
}

function prefersDark(): boolean {
  const forced = document.documentElement.dataset.theme
  if (forced) return forced === "dark"
  return matchMedia("(prefers-color-scheme: dark)").matches
}

function slug(title: string): string {
  return (
    title
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 60) || "canvas"
  )
}

function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(String(r.result).replace(/^data:[^,]*,/, ""))
    r.onerror = () => reject(r.error)
    r.readAsDataURL(blob)
  })
}

// A cheap fingerprint of what would be saved: element versions and files.
function sceneKey(elements: readonly ExcalidrawElement[], files: Record<string, unknown>): string {
  return `${ex.hashElementsVersion(elements)}:${Object.keys(files).length}`
}

function imageSize(dataURL: string): Promise<{ w: number; h: number }> {
  return new Promise((resolve) => {
    const img = new Image()
    img.onload = () => resolve({ w: img.naturalWidth || 320, h: img.naturalHeight || 240 })
    img.onerror = () => resolve({ w: 320, h: 240 })
    img.src = dataURL
  })
}

// ---------------------------------------------------------------- mermaid

// Text is measured when it is created. Excalidraw registers its fonts but
// only fetches one when first drawn, so measuring earlier uses a narrower
// fallback and the label is clipped once the real font arrives.
async function fontsReady(): Promise<void> {
  try {
    await Promise.all(["20px Nunito", "20px Excalifont"].map((f) => document.fonts.load(f)))
  } catch {
    /* measure with whatever is available */
  }
}

async function mermaidToElements(def: string): Promise<{ elements: ExcalidrawElement[]; files: BinaryFileData[] }> {
  const { parseMermaidToExcalidraw } = await import("@excalidraw/mermaid-to-excalidraw")
  const { source, groups } = flattenSubgraphs(def)
  const res = await parseMermaidToExcalidraw(source, { themeVariables: { fontSize: "20px" } })
  await fontsReady()
  // Mermaid lays boxes out for a plain sans font; Excalidraw's hand-drawn
  // default is much wider and clips labels. Nunito (6) matches closely.
  const NUNITO = 6
  let skel = (res.elements as any[]).map((e) =>
    e.label ? { ...e, label: { ...e.label, fontFamily: NUNITO } } : e.type === "text" ? { ...e, fontFamily: NUNITO } : e,
  )
  const boxes: any[] = []
  for (const g of groups) {
    const members = new Set(g.tokens)
    const inside = skel.filter((e) => e.id && e.type !== "arrow" && members.has(e.id) && typeof e.x === "number")
    if (!inside.length) continue
    const pad = 20 + 32 * g.height
    const header = 30
    const minX = Math.min(...inside.map((e) => e.x)) - pad
    const minY = Math.min(...inside.map((e) => e.y)) - pad - header
    const maxX = Math.max(...inside.map((e) => e.x + (e.width || 0))) + pad
    const maxY = Math.max(...inside.map((e) => e.y + (e.height || 0))) + pad
    boxes.push(
      { type: "rectangle", x: minX, y: minY, width: maxX - minX, height: maxY - minY, strokeStyle: "dashed", backgroundColor: "transparent" },
      { type: "text", x: minX + 12, y: minY + 8, text: g.label, fontSize: 16, fontFamily: NUNITO },
    )
  }
  // Outer boxes first so they sit behind what they contain.
  boxes.reverse()
  const ordered: any[] = []
  for (let i = 0; i < boxes.length; i += 2) ordered.push(boxes[i + 1], boxes[i])
  skel = [...ordered, ...skel]
  return {
    elements: fitLabels(ex.convertToExcalidrawElements(skel, { regenerateIds: true })),
    files: Object.values(res.files || {}) as BinaryFileData[],
  }
}

// fitLabels shrinks a bound label that is wider than its box (down to 12px)
// rather than resizing the box, which would pull it off its arrows.
function fitLabels(els: ExcalidrawElement[]): ExcalidrawElement[] {
  const byId = new Map(els.map((e) => [e.id, e]))
  return els.map((e) => {
    if (e.type !== "text" || !(e as any).containerId) return e
    const box = byId.get((e as any).containerId)
    if (!box || box.type === "arrow") return e
    const room = box.width - 20
    if (e.width <= room || room <= 0) return e
    const t = e as any
    const scale = Math.max(12 / t.fontSize, room / e.width)
    const width = e.width * scale
    const height = e.height * scale
    return {
      ...t,
      fontSize: t.fontSize * scale,
      width,
      height,
      x: box.x + (box.width - width) / 2,
      y: box.y + (box.height - height) / 2,
    } as ExcalidrawElement
  })
}

// ---------------------------------------------------------------- list page

function CanvasList() {
  const [items, setItems] = useState<Meta[] | null>(null)
  const [error, setError] = useState("")
  const [deleting, setDeleting] = useState<string | null>(null)
  const deletePending = useRef(false)
  useEffect(() => {
    api<Meta[]>("GET", "").then(setItems, (e) => setError(e.message))
  }, [])
  const create = async () => {
    try {
      const m = await api<Meta>("POST", "", { title: "Untitled canvas" })
      location.href = m.url
    } catch (e: any) {
      setError(e.message)
    }
  }
  const remove = async (canvas: Meta) => {
    if (deletePending.current || !window.confirm(`Delete “${canvas.title}”?\n\nThis permanently deletes the canvas and its drawing. Links to it will stop working.`)) return
    deletePending.current = true
    setDeleting(canvas.id)
    setError("")
    try {
      await api<void>("DELETE", `/${canvas.id}`)
      setItems((current) => current?.filter((item) => item.id !== canvas.id) ?? null)
    } catch (e: any) {
      setError(e.message)
    } finally {
      deletePending.current = false
      setDeleting(null)
    }
  }
  return (
    <div className="canvas-list">
      <header>
        <h1>Canvases</h1>
        <button className="primary" onClick={create}>
          New canvas
        </button>
      </header>
      {error && <SignInError message={error} />}
      {items && items.length === 0 && <p className="muted">Nothing yet. Start one here, or from a terminal with <code>amy canvas new</code>.</p>}
      <ul>
        {items?.map((m) => (
          <li key={m.id}>
            <a href={m.url}>
              {m.drawing ? <img src={`${base}api/canvas/${m.id}/drawing.png`} alt="" loading="lazy" /> : <span className="blank" />}
              <span className="title">{m.title}</span>
              <span className="muted">{new Date(m.updated).toLocaleString()}</span>
            </a>
            <button className="canvas-delete" aria-label={`Delete ${m.title}`} disabled={deleting !== null} onClick={() => remove(m)}>
              {deleting === m.id ? "Deleting…" : "Delete"}
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}

function SignInError({ message }: { message: string }) {
  return (
    <p className="error">
      {message}
      {message.startsWith("Sign in") && (
        <>
          {" "}
          — <a href={`${base}kanban/`}>sign in</a>, then reload.
        </>
      )}
    </p>
  )
}

// ---------------------------------------------------------------- editor

type Status = "loading" | "saved" | "dirty" | "saving" | "conflict" | "error"

function Editor({ id }: { id: string }) {
  const [initial, setInitial] = useState<any>(null)
  const [meta, setMeta] = useState<Meta | null>(null)
  const [title, setTitle] = useState("")
  const [status, setStatus] = useState<Status>("loading")
  const [message, setMessage] = useState("")
  const [toast, setToast] = useState("")
  const [sendOpen, setSendOpen] = useState(false)
  const [sendText, setSendText] = useState("")
  const [mermaidOpen, setMermaidOpen] = useState(false)
  const [mermaidText, setMermaidText] = useState("")
  const apiRef = useRef<ExcalidrawImperativeAPI | null>(null)
  const revRef = useRef(0)
  const appliedRef = useRef(0)
  const savedKeyRef = useRef("")
  const titleDirtyRef = useRef(false)
  // The scene was edited elsewhere without new renderings (the Herdr pane):
  // re-render and save so notes and the PNG catch up.
  const staleRef = useRef(false)
  const savingRef = useRef(false)
  const applyingRef = useRef(false)
  const timerRef = useRef<number | undefined>(undefined)
  const dark = useMemo(prefersDark, [])

  const flash = (t: string) => {
    setToast(t)
    window.setTimeout(() => setToast((cur) => (cur === t ? "" : cur)), 3500)
  }

  // Load.
  useEffect(() => {
    api<{ meta: Meta; scene: any }>("GET", `/${id}`).then(
      ({ meta, scene }) => {
        const restored = ex.restore(scene, null, null)
        revRef.current = meta.rev
        appliedRef.current = meta.applied
        staleRef.current = !!meta.stale
        savedKeyRef.current = sceneKey(restored.elements, restored.files)
        setMeta(meta)
        setTitle(meta.title)
        document.title = `${meta.title} — Canvas`
        setInitial({
          elements: restored.elements,
          appState: { ...restored.appState, theme: dark ? "dark" : "light" },
          files: restored.files,
          scrollToContent: true,
        })
        setStatus("saved")
      },
      (e) => {
        setStatus("error")
        setMessage(e.message)
      },
    )
  }, [id])

  const save = useCallback(
    async (force = false): Promise<boolean> => {
      const x = apiRef.current
      if (!x || savingRef.current) return false
      const elements = x.getSceneElements()
      const files = x.getFiles()
      const key = sceneKey(elements, files)
      if (!force && key === savedKeyRef.current && !titleDirtyRef.current) return true
      savingRef.current = true
      setStatus("saving")
      try {
        const appState = x.getAppState()
        const scene = JSON.parse(ex.serializeAsJSON(elements, appState, files, "local"))
        let svg = ""
        let png = ""
        if (elements.length > 0) {
          const exportState = { ...appState, exportEmbedScene: true, exportBackground: true, exportWithDarkMode: false }
          const svgEl = await ex.exportToSvg({ elements, appState: exportState, files })
          svg = svgEl.outerHTML
          const blob = await ex.exportToBlob({
            elements,
            appState: exportState,
            files,
            mimeType: "image/png",
            getDimensions: (w: number, h: number) => ({ width: w * 2, height: h * 2, scale: 2 }),
          })
          png = await blobToBase64(blob)
        }
        const m = await api<Meta>("PUT", `/${id}`, {
          base_rev: revRef.current,
          title: titleDirtyRef.current ? titleInputRef.current : undefined,
          scene,
          svg,
          png: png || undefined,
          applied: appliedRef.current,
        })
        revRef.current = m.rev
        titleDirtyRef.current = false
        savedKeyRef.current = key
        setMeta(m)
        // Changes made while this save was in flight still need saving.
        const nowKey = sceneKey(x.getSceneElements(), x.getFiles())
        setStatus(nowKey === key ? "saved" : "dirty")
        return true
      } catch (e: any) {
        if (e instanceof HttpError && e.status === 409) {
          setStatus("conflict")
          setMessage("This canvas was changed somewhere else.")
        } else {
          setStatus("error")
          setMessage(e.message)
        }
        return false
      } finally {
        savingRef.current = false
      }
    },
    [id],
  )

  const saveRef = useRef(save)
  saveRef.current = save

  const titleInputRef = useRef("")
  titleInputRef.current = title

  const scheduleSave = useCallback(() => {
    window.clearTimeout(timerRef.current)
    timerRef.current = window.setTimeout(() => void save(), 1200)
  }, [save])

  const onChange = useCallback(
    (elements: readonly ExcalidrawElement[], _state: AppState, files: Record<string, unknown>) => {
      if (applyingRef.current) return
      if (sceneKey(elements, files) !== savedKeyRef.current) {
        setStatus((s) => (s === "conflict" ? s : "dirty"))
        scheduleSave()
      }
    },
    [scheduleSave],
  )

  // Place inbox items to the right of what is already drawn, then select
  // them so the person sees what the agent added.
  const applyInbox = useCallback(async (items: InboxItem[]) => {
    const x = apiRef.current
    if (!x || items.length === 0) return
    applyingRef.current = true
    try {
      const existing = x.getSceneElementsIncludingDeleted()
      const live = existing.filter((e) => !e.isDeleted)
      let [, minY, maxX] = live.length ? ex.getCommonBounds(live) : [0, 0, -80, 0]
      const added: ExcalidrawElement[] = []
      const newFiles: BinaryFileData[] = []
      for (const item of items) {
        let els: ExcalidrawElement[] = []
        try {
          if (item.kind === "mermaid") {
            const res = await mermaidToElements(item.data)
            els = res.elements
            newFiles.push(...res.files)
          } else if (item.kind === "text") {
            await fontsReady()
            els = ex.convertToExcalidrawElements([{ type: "text", x: 0, y: 0, text: item.data, fontSize: 20 } as any])
          } else if (item.kind === "image") {
            const fileId = `inbox-${id}-${item.seq}` as any
            const { w, h } = await imageSize(item.data)
            const scale = Math.min(1, 640 / w)
            newFiles.push({ id: fileId, dataURL: item.data as any, mimeType: (item.data.slice(5, item.data.indexOf(";")) || "image/png") as any, created: Date.now() })
            els = ex.convertToExcalidrawElements([{ type: "image", x: 0, y: 0, width: w * scale, height: h * scale, fileId } as any])
          }
        } catch (e: any) {
          els = ex.convertToExcalidrawElements([
            { type: "text", x: 0, y: 0, text: `Could not place ${item.kind} from ${item.from || "agent"}: ${e?.message || e}`, fontSize: 16, strokeColor: "#c92a2a" } as any,
          ])
        }
        if (els.length) {
          const [bx, by] = ex.getCommonBounds(els)
          const dx = maxX + 80 - bx
          const dy = minY - by
          els = els.map((e) => ({ ...e, x: e.x + dx, y: e.y + dy }))
          const [, , nx] = ex.getCommonBounds(els)
          maxX = nx
          added.push(...els)
        }
        appliedRef.current = Math.max(appliedRef.current, item.seq)
      }
      if (newFiles.length) x.addFiles(newFiles)
      x.updateScene({
        elements: [...existing, ...added],
        appState: { selectedElementIds: Object.fromEntries(added.map((e) => [e.id, true])) as any },
        captureUpdate: ex.CaptureUpdateAction.IMMEDIATELY,
      })
      if (added.length) x.scrollToContent(added, { fitToContent: true, animate: true })
      flash(`Agent added ${items.length} item${items.length === 1 ? "" : "s"}`)
    } finally {
      applyingRef.current = false
    }
    await save(true)
  }, [id, save])

  // Poll for agent pushes and edits from another window.
  useEffect(() => {
    if (status === "loading" || status === "error") return
    let stopped = false
    let busy = false
    const tick = async () => {
      if (stopped || busy || savingRef.current || !apiRef.current) return
      busy = true
      try {
        const m = await api<Meta>("GET", `/${id}/meta`)
        if (m.rev !== revRef.current && !savingRef.current) {
          const unsaved = sceneKey(apiRef.current.getSceneElements(), apiRef.current.getFiles()) !== savedKeyRef.current
          if (unsaved) {
            setStatus("conflict")
            setMessage("This canvas was changed somewhere else.")
            return
          }
          await reload()
          return
        }
        if (m.inbox > appliedRef.current) {
          const items = await api<InboxItem[]>("GET", `/${id}/inbox`)
          await applyInbox(items.filter((i) => i.seq > appliedRef.current))
        }
      } catch {
        /* transient; next tick retries */
      } finally {
        busy = false
      }
    }
    void tick()
    const t = window.setInterval(tick, 2000)
    return () => {
      stopped = true
      window.clearInterval(t)
    }
  }, [id, status === "loading" || status === "error", applyInbox])

  const reload = async () => {
    const { meta, scene } = await api<{ meta: Meta; scene: any }>("GET", `/${id}`)
    const restored = ex.restore(scene, null, null)
    applyingRef.current = true
    try {
      apiRef.current?.addFiles(Object.values(restored.files))
      apiRef.current?.updateScene({ elements: restored.elements, captureUpdate: ex.CaptureUpdateAction.NEVER })
    } finally {
      applyingRef.current = false
    }
    revRef.current = meta.rev
    appliedRef.current = meta.applied
    savedKeyRef.current = sceneKey(restored.elements, restored.files)
    setMeta(meta)
    setTitle(meta.title)
    setStatus("saved")
    if (meta.stale) window.setTimeout(() => void saveRef.current(true), 300)
  }

  const keepMine = async () => {
    const m = await api<Meta>("GET", `/${id}/meta`)
    revRef.current = m.rev
    appliedRef.current = Math.max(appliedRef.current, m.applied)
    await save(true)
  }

  // Save before leaving.
  useEffect(() => {
    const before = (e: BeforeUnloadEvent) => {
      if (status === "dirty" || status === "saving" || status === "conflict") {
        e.preventDefault()
      }
    }
    window.addEventListener("beforeunload", before)
    return () => window.removeEventListener("beforeunload", before)
  }, [status])

  const pngBlob = async (): Promise<Blob | null> => {
    const x = apiRef.current
    if (!x) return null
    const elements = x.getSceneElements()
    if (!elements.length) {
      flash("Nothing to export yet")
      return null
    }
    return ex.exportToBlob({
      elements,
      appState: { ...x.getAppState(), exportEmbedScene: true, exportBackground: true, exportWithDarkMode: false },
      files: x.getFiles(),
      mimeType: "image/png",
      getDimensions: (w: number, h: number) => ({ width: w * 2, height: h * 2, scale: 2 }),
    })
  }

  const copyPNG = async () => {
    try {
      const blob = await pngBlob()
      if (!blob) return
      await navigator.clipboard.write([new ClipboardItem({ "image/png": blob })])
      flash("PNG copied — paste it into a message or note")
    } catch (e: any) {
      flash(`Copy failed: ${e.message}`)
    }
  }

  const download = async (kind: "png" | "svg") => {
    if (!(await save())) return
    const a = document.createElement("a")
    a.href = `${base}api/canvas/${id}/drawing.${kind}?download=1`
    a.click()
  }

  const saveToNote = async () => {
    try {
      const blob = await pngBlob()
      if (!blob) return
      const form = new FormData()
      form.append("title", title)
      form.append("file", new File([blob], `${slug(title)}.png`, { type: "image/png" }))
      const res = await fetch(`${base}api/share/upload`, {
        method: "POST",
        credentials: "same-origin",
        headers: { "X-CSRF-Token": await csrf() },
        body: form,
      })
      if (!res.ok) throw new Error((await res.text()) || res.statusText)
      const out = await res.json()
      flash("Saved to notes")
      window.open(out.noteURL, "_blank", "noopener")
    } catch (e: any) {
      flash(`Save to note failed: ${e.message}`)
    }
  }

  const send = async () => {
    if (!(await save())) return
    try {
      const m = await api<Meta>("POST", `/${id}/send`, { message: sendText })
      setMeta(m)
      setSendOpen(false)
      setSendText("")
      flash("Sent — the agent can read it now")
    } catch (e: any) {
      flash(`Send failed: ${e.message}`)
    }
  }

  const addMermaid = async () => {
    if (!mermaidText.trim()) return
    // Locally typed Mermaid goes through the same path an agent's does.
    await applyInbox([{ seq: appliedRef.current, kind: "mermaid", data: mermaidText, from: "you" }])
    setMermaidOpen(false)
    setMermaidText("")
  }

  const copyID = async () => {
    await navigator.clipboard.writeText(id)
    flash(`Copied ${id} — e.g. amy canvas read ${id}`)
  }

  if (status === "error" && !initial) {
    return (
      <div className="canvas-list">
        <SignInError message={message} />
        <a href={`${base}canvas/`}>All canvases</a>
      </div>
    )
  }
  if (!initial) return <div className="canvas-loading">Loading…</div>

  const statusLabel: Record<Status, string> = {
    loading: "Loading",
    saved: "Saved",
    dirty: "Unsaved",
    saving: "Saving…",
    conflict: "Changed elsewhere",
    error: "Not saved",
  }

  return (
    <div className="canvas-shell">
      <header className="canvas-bar">
        <a className="canvas-home" href={`${base}canvas/`} title="All canvases" aria-label="All canvases">
          ←
        </a>
        <input
          className="canvas-title"
          value={title}
          aria-label="Canvas title"
          onChange={(e) => {
            setTitle(e.target.value)
            titleDirtyRef.current = true
            setStatus("dirty")
            scheduleSave()
          }}
        />
        <span className={`canvas-status ${status}`} title={message}>
          {statusLabel[status]}
        </span>
        <span className="canvas-bar-spacer" />
        <button className="wide-only" onClick={() => setMermaidOpen(true)} title="Paste Mermaid to draw it">
          Mermaid
        </button>
        <button className="wide-only" onClick={copyPNG} title="Copy as PNG for messages, notes, chat">
          Copy PNG
        </button>
        <details className="canvas-menu">
          <summary>More</summary>
          <div>
            <button className="narrow-only" onClick={() => setMermaidOpen(true)}>Draw from Mermaid</button>
            <button className="narrow-only" onClick={copyPNG}>Copy PNG</button>
            <button onClick={() => download("png")}>Download PNG</button>
            <button onClick={() => download("svg")}>Download SVG</button>
            <button onClick={saveToNote}>Save to note</button>
            <button onClick={copyID}>Copy canvas id</button>
          </div>
        </details>
        <button className="primary" onClick={() => setSendOpen(true)} title="Hand this to the agent waiting on it">
          Send<span className="wide-only"> to agent</span>
          {meta && meta.sent > 0 ? ` (${meta.sent})` : ""}
        </button>
      </header>
      <div className="canvas-stage">
      <ex.Excalidraw
        excalidrawAPI={(a) => {
          if (apiRef.current) return
          apiRef.current = a
          // Open showing the whole drawing, wherever it was last scrolled.
          window.setTimeout(() => {
            const els = a.getSceneElements()
            if (els.length) a.scrollToContent(els, { fitToContent: true, animate: false })
            if (staleRef.current) {
              staleRef.current = false
              void saveRef.current(true)
            }
          }, 300)
        }}
        initialData={initial}
        onChange={onChange as any}
        theme={dark ? "dark" : "light"}
        name={title}
        UIOptions={{ canvasActions: { loadScene: true, saveToActiveFile: false, export: { saveFileToDisk: true } } }}
      />
      </div>
      {status === "conflict" && (
        <div className="canvas-banner">
          {message}
          <button onClick={() => void reload()}>Load theirs</button>
          <button onClick={() => void keepMine()}>Keep mine</button>
        </div>
      )}
      {status === "error" && (
        <div className="canvas-banner">
          {message} <button onClick={() => void save(true)}>Retry</button>
        </div>
      )}
      {sendOpen && (
        <Dialog title="Send to agent" onClose={() => setSendOpen(false)}>
          <textarea
            autoFocus
            placeholder="Optional: what should the agent look at?"
            value={sendText}
            onChange={(e) => setSendText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) void send()
            }}
          />
          <div className="row">
            <span className="muted">The agent reads it with <code>amy canvas read {id}</code></span>
            <button className="primary" onClick={send}>
              Send
            </button>
          </div>
        </Dialog>
      )}
      {mermaidOpen && (
        <Dialog title="Draw from Mermaid" onClose={() => setMermaidOpen(false)}>
          <textarea
            autoFocus
            className="mono"
            placeholder={"flowchart LR\n  A[Client] --> B[API]"}
            value={mermaidText}
            onChange={(e) => setMermaidText(e.target.value)}
          />
          <div className="row">
            <span className="muted">Added beside what is already drawn; you can edit it freely.</span>
            <button className="primary" onClick={addMermaid}>
              Add
            </button>
          </div>
        </Dialog>
      )}
      {toast && <div className="canvas-toast">{toast}</div>}
    </div>
  )
}

function Dialog({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  return (
    <div className="canvas-dialog-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="canvas-dialog" role="dialog" aria-label={title} onKeyDown={(e) => e.key === "Escape" && onClose()}>
        <h2>{title}</h2>
        {children}
      </div>
    </div>
  )
}

createRoot(rootEl).render(<StrictMode>{canvasID ? <Editor id={canvasID} /> : <CanvasList />}</StrictMode>)
