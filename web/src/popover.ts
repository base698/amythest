// Hover previews for internal links, Quartz-style: fetch the target page,
// extract its content, float it near the link.

const cache = new Map<string, string>()
let popover: HTMLDivElement | null = null
let hideTimer: number | undefined
let showTimer: number | undefined
let showGeneration = 0

let bound = false

export function setupPopovers() {
  if (bound) return
  bound = true

  document.addEventListener("mouseover", (e) => {
    const link = (e.target as Element).closest?.("a.internal:not(.broken)") as HTMLAnchorElement | null
    if (!link || link.closest("#search-modal")) return
    window.clearTimeout(showTimer)
    const generation = ++showGeneration
    showTimer = window.setTimeout(() => void show(link, generation), 250)
  })
  document.addEventListener("mouseout", (e) => {
    const link = (e.target as Element).closest?.("a.internal") as HTMLAnchorElement | null
    if (!link) return
    showGeneration++
    window.clearTimeout(showTimer)
    scheduleHide()
  })
  // Popovers live directly under <body>, outside the article morphed during
  // SPA navigation. Remove them before any click can navigate to a new note.
  document.addEventListener("click", () => {
    window.clearTimeout(showTimer)
    hide()
  })
  window.addEventListener("popstate", hide)
}

async function show(link: HTMLAnchorElement, generation: number) {
  const url = new URL(link.href)
  if (url.origin !== location.origin || url.pathname === location.pathname) return

  let html = cache.get(url.pathname)
  if (html === undefined) {
    try {
      const res = await fetch(url.pathname)
      if (!res.ok) return
      const doc = new DOMParser().parseFromString(await res.text(), "text/html")
      const article = doc.querySelector(".center article")
      html = article ? article.innerHTML : ""
      cache.set(url.pathname, html)
    } catch {
      return
    }
  }
  if (!html || generation !== showGeneration || !link.isConnected) return

  hide()
  popover = document.createElement("div")
  popover.className = "popover"
  popover.innerHTML = html
  popover.style.visibility = "hidden"
  popover.style.width = `${Math.min(480, window.innerWidth - 24)}px`
  popover.addEventListener("mouseenter", () => window.clearTimeout(hideTimer))
  popover.addEventListener("mouseleave", scheduleHide)
  document.body.append(popover)

  const r = link.getBoundingClientRect()
  const pw = Math.min(480, window.innerWidth - 24)
  let left = Math.max(12, Math.min(r.left, window.innerWidth - pw - 12))
  let top = r.bottom + 8
  const ph = Math.min(320, popover.offsetHeight)
  if (top + ph > window.innerHeight) top = Math.max(8, r.top - ph - 8)
  popover.style.left = `${left}px`
  popover.style.top = `${top}px`
  popover.style.width = `${pw}px`
  popover.style.visibility = "visible"

  // Scroll to fragment if the link targets a heading.
  if (url.hash) {
    const target = popover.querySelector(url.hash) as HTMLElement | null
    if (target) popover.scrollTop = Math.max(0, target.offsetTop - 12)
  }
}

function scheduleHide() {
  window.clearTimeout(hideTimer)
  hideTimer = window.setTimeout(hide, 200)
}

function hide() {
  showGeneration++
  popover?.remove()
  popover = null
}
