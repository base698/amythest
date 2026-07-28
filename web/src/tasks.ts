// Interactive task checkboxes: check/uncheck writes back to the vault file
// through /api/tasks/toggle (recurring tasks roll forward server-side),
// then the page refreshes in place to show the result.
import { refreshCurrent } from "./spa"

let bound = false

export function setupTaskToggles() {
  if (bound) return
  bound = true

  document.addEventListener("change", (e) => {
    const box = e.target as HTMLInputElement
    if (!box.matches?.("input.task-toggle")) return
    void toggle(box)
  })
}

async function toggle(box: HTMLInputElement) {
  const slug = box.dataset.slug
  const line = Number(box.dataset.line)
  if (!slug || !line) return
  const done = box.checked
  box.disabled = true
  try {
    const res = await fetch(`${baseURL()}api/tasks/toggle`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({ slug, line, done }),
    })
    if (!res.ok) {
      throw new Error((await res.text()) || `toggle failed (${res.status})`)
    }
    await refreshCurrent()
  } catch (err) {
    box.checked = !done
    box.disabled = false
    showToast(err instanceof Error ? err.message : "Couldn't update the task")
    return
  }
  box.disabled = false
}

let toastTimer: number | undefined

function showToast(text: string) {
  document.getElementById("amythest-toast")?.remove()
  const el = document.createElement("div")
  el.id = "amythest-toast"
  el.textContent = text.trim()
  document.body.append(el)
  window.clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => el.remove(), 4000)
}

function baseURL(): string {
  const base = document.body.dataset.base || "/"
  return base.endsWith("/") ? base : base + "/"
}
