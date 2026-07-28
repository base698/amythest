// Persist explorer folder open/closed state across navigations.
const KEY = "explorer-open"

// Elements that already carry a toggle listener; setupExplorer re-runs on
// every SPA navigation but must not stack duplicate listeners on nodes the
// morph kept alive.
const wired = new WeakSet<Element>()

export function setupExplorer() {
  const explorer = document.querySelector(".explorer")
  if (!explorer) return

  let open: Record<string, boolean> = {}
  try {
    open = JSON.parse(localStorage.getItem(KEY) ?? "{}")
  } catch {
    /* fresh state */
  }

  explorer.querySelectorAll("details").forEach((d) => {
    const label = d.querySelector("summary")?.textContent?.trim() ?? ""
    const path = folderPath(d, label)
    if (path in open) d.open = open[path]
    if (wired.has(d)) return
    wired.add(d)
    d.addEventListener("toggle", () => {
      const saved = readState()
      saved[path] = d.open
      localStorage.setItem(KEY, JSON.stringify(saved))
    })
  })
}

function readState(): Record<string, boolean> {
  try {
    return JSON.parse(localStorage.getItem(KEY) ?? "{}")
  } catch {
    return {}
  }
}

function folderPath(el: Element, label: string): string {
  const parts = [label]
  let p = el.parentElement
  while (p && !p.classList.contains("explorer")) {
    if (p instanceof HTMLDetailsElement) {
      const s = p.querySelector(":scope > summary")?.textContent?.trim()
      if (s) parts.unshift(s)
    }
    p = p.parentElement
  }
  return parts.join("/")
}
