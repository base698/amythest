// Share-to-notes capture page: record audio via MediaRecorder or pick a
// file, upload, then link to the created note. Delegated + looked-up at
// event time, like every island, to survive SPA morphs.

let bound = false
let recorder: MediaRecorder | null = null
let chunks: Blob[] = []
let recorded: Blob | null = null
let recordedName = ""
let timerHandle: number | undefined
let startedAt = 0

export const shareTextTitleSelector = "#share-text-title"
export const shareTextDescriptionSelector = "#share-text-description"
export const shareTextSaveSelector = "#share-text-save"
export const shareTextEndpoint = "api/share/text"

export function buildTextNotePayload(title: string, description: string) {
  title = title.trim()
  if (!title) throw new Error("Title is required")
  return { title, description }
}

export function setupShare() {
  if (bound) return
  bound = true

  document.addEventListener("click", (e) => {
    const t = e.target as Element
    if (t.closest?.("#share-record")) void toggleRecord()
    if (t.closest?.("#share-upload")) void upload()
    if (t.closest?.(shareTextSaveSelector)) void saveTextNote()
  })
  document.addEventListener("input", (e) => {
    const input = e.target as HTMLInputElement
    if (input.id === "share-text-title") updateTextReady()
  })
  document.addEventListener("change", (e) => {
    const input = e.target as HTMLInputElement
    if (input.id !== "share-file") return
    recorded = null
    recordedName = ""
    updateReady()
  })
}

const el = <T extends HTMLElement>(id: string) => document.getElementById(id) as T | null

function updateTextReady() {
  const title = el<HTMLInputElement>("share-text-title")
  const button = el<HTMLButtonElement>("share-text-save")
  if (button) button.disabled = !title?.value.trim()
}

async function saveTextNote() {
  const title = el<HTMLInputElement>("share-text-title")
  const description = el<HTMLTextAreaElement>("share-text-description")
  const button = el<HTMLButtonElement>("share-text-save")
  if (!title || !description || !button) return

  let payload: ReturnType<typeof buildTextNotePayload>
  try {
    payload = buildTextNotePayload(title.value, description.value)
  } catch (err) {
    textStatus(err instanceof Error ? err.message : "Title is required")
    return
  }
  button.disabled = true
  textStatus("Saving…")
  try {
    const res = await fetch(`${baseURL()}${shareTextEndpoint}`, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    })
    if (!res.ok) throw new Error((await res.text()) || `save failed (${res.status})`)
    const out = (await res.json()) as { noteURL: string; note: string }
    const s = el<HTMLDivElement>("share-text-status")
    if (s) {
      s.innerHTML = ""
      const a = document.createElement("a")
      a.className = "internal"
      a.href = out.noteURL
      a.textContent = out.note
      s.append("Saved to ", a, ".")
    }
    title.value = ""
    description.value = ""
    updateTextReady()
  } catch (err) {
    textStatus(err instanceof Error ? err.message : "Save failed")
    button.disabled = false
  }
}

function textStatus(text: string) {
  const s = el<HTMLDivElement>("share-text-status")
  if (s) s.textContent = text
}

async function toggleRecord() {
  const btn = el<HTMLButtonElement>("share-record")
  const timer = el<HTMLSpanElement>("share-timer")
  if (!btn) return

  if (recorder && recorder.state === "recording") {
    recorder.stop()
    return
  }
  try {
    const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    chunks = []
    const mime = MediaRecorder.isTypeSupported("audio/mp4") ? "audio/mp4" : "audio/webm"
    recorder = new MediaRecorder(stream, { mimeType: mime })
    recorder.ondataavailable = (ev) => chunks.push(ev.data)
    recorder.onstop = () => {
      stream.getTracks().forEach((t) => t.stop())
      recorded = new Blob(chunks, { type: mime })
      recordedName = `memo.${mime.includes("mp4") ? "m4a" : "webm"}`
      const fileInput = el<HTMLInputElement>("share-file")
      if (fileInput) fileInput.value = ""
      btn.textContent = "● Record again"
      btn.classList.remove("recording")
      window.clearInterval(timerHandle)
      updateReady()
      status(`Recorded ${fmt(Date.now() - startedAt)} of audio — ready to upload.`)
    }
    recorder.start()
    startedAt = Date.now()
    btn.textContent = "■ Stop"
    btn.classList.add("recording")
    if (timer) {
      timer.hidden = false
      timerHandle = window.setInterval(() => {
        timer.textContent = fmt(Date.now() - startedAt)
      }, 500)
    }
    status("Recording…")
  } catch (err) {
    status(err instanceof Error ? err.message : "Microphone unavailable")
  }
}

function currentFile(): { blob: Blob; name: string } | null {
  const input = el<HTMLInputElement>("share-file")
  const picked = input?.files?.[0]
  if (picked) return { blob: picked, name: picked.name }
  if (recorded) return { blob: recorded, name: recordedName }
  return null
}

function updateReady() {
  const btn = el<HTMLButtonElement>("share-upload")
  if (btn) btn.disabled = currentFile() === null
}

async function upload() {
  const file = currentFile()
  const btn = el<HTMLButtonElement>("share-upload")
  if (!file || !btn) return
  btn.disabled = true
  status("Uploading…")
  try {
    const form = new FormData()
    form.append("file", file.blob, file.name)
    const title = el<HTMLInputElement>("share-title")?.value ?? ""
    if (title.trim()) form.append("title", title.trim())
    const res = await fetch(`${baseURL()}api/share/upload`, {
      method: "POST",
      credentials: "same-origin",
      body: form,
    })
    if (!res.ok) throw new Error((await res.text()) || `upload failed (${res.status})`)
    const out = (await res.json()) as { noteURL: string; note: string; plugins: number }
    const processing = out.plugins > 0 ? " Processors are running; the note updates when they finish." : ""
    const s = el<HTMLDivElement>("share-status")
    if (s) {
      s.innerHTML = ""
      const a = document.createElement("a")
      a.className = "internal"
      a.href = out.noteURL
      a.textContent = out.note
      s.append("Saved to ", a, document.createTextNode("." + processing))
    }
    recorded = null
    updateReady()
  } catch (err) {
    status(err instanceof Error ? err.message : "Upload failed")
    btn.disabled = false
  }
}

function status(text: string) {
  const s = el<HTMLDivElement>("share-status")
  if (s) s.textContent = text
}

function fmt(ms: number): string {
  const total = Math.floor(ms / 1000)
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, "0")}`
}

function baseURL(): string {
  const base = document.body.dataset.base || "/"
  return base.endsWith("/") ? base : base + "/"
}
