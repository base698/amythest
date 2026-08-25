// Persist explorer folder open/closed state across navigations.
const KEY = "explorer-open"

// Elements that already carry a toggle listener; setupExplorer re-runs on
// every SPA navigation but must not stack duplicate listeners on nodes the
// morph kept alive.
const wired = new WeakSet<Element>()

const DRAWER_KEY = "sidebar-drawer-open"

export function setupExplorer() {
  setupDrawer()
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

// setupDrawer wires the mobile ☰ toggle: on narrow screens the whole
// navigation drawer folds away so the note content leads the page.
function setupDrawer() {
  const toggle = document.getElementById("sidebar-toggle")
  const sidebar = document.querySelector(".sidebar-left")
  if (!toggle || !sidebar) return
  const apply = (open: boolean) => {
    sidebar.classList.toggle("drawer-open", open)
    toggle.setAttribute("aria-expanded", String(open))
  }
  apply(localStorage.getItem(DRAWER_KEY) === "1")
  if (wired.has(toggle)) return
  wired.add(toggle)
  toggle.addEventListener("click", () => {
    const open = !sidebar.classList.contains("drawer-open")
    apply(open)
    localStorage.setItem(DRAWER_KEY, open ? "1" : "0")
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
