// Client-side navigation: fetch + DOM-diff via micromorph, so the graph
// simulation, theme, and explorer state survive page changes.
import morph from "micromorph"

let bound = false
let onNavCb: () => void = () => {}

// refreshCurrent re-fetches the current page and morphs it in place —
// used after server-side mutations (e.g. task toggles) so new content
// appears without losing scroll position or client state.
export async function refreshCurrent() {
  await go(new URL(location.href), false, onNavCb, true)
}

export function setupSPA(onNav: () => void) {
  if (bound) return
  bound = true
  onNavCb = onNav

  document.addEventListener("click", (e) => {
    if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
    const a = (e.target as Element).closest?.("a") as HTMLAnchorElement | null
    if (!a || !a.href || a.target === "_blank" || a.hasAttribute("download")) return
    const url = new URL(a.href)
    if (url.origin !== location.origin) return
    const base = document.body.dataset.base || "/"
    const prefix = base.endsWith("/") ? base : base + "/"
    if (url.pathname.startsWith(prefix + "assets/") || url.pathname.startsWith("/kanban") || url.pathname.startsWith(prefix + "kanban")) return
    if (url.pathname === location.pathname && url.hash) return // in-page anchor
    e.preventDefault()
    void go(url, true, onNav)
  })

  window.addEventListener("popstate", () => {
    void go(new URL(location.href), false, onNav)
  })
}

async function go(url: URL, push: boolean, onNav: () => void, keepScroll = false) {
  try {
    const res = await fetch(url.pathname + url.search)
    if (!res.ok) {
      location.href = url.href
      return
    }
    const html = await res.text()
    const next = new DOMParser().parseFromString(html, "text/html")
    if (push) history.pushState({}, "", url.href)
    document.title = next.title
    // Morph the body only. Every page shares one base-template head (only
    // the title differs, synced above), and morphing the live head is
    // actively harmful: libraries inject <style> nodes into it at runtime
    // (mermaid does), which shifts the positional diff and re-creates the
    // stylesheet <link>s — a visible unstyled flicker on task toggles.
    // Skipping the document node also leaves the <html> theme/palette
    // attributes untouched, so dark mode and the active theme survive
    // without hand-carrying them onto the fetched document.
    await morph(document.body, next.body)
    if (!keepScroll) {
      if (url.hash) {
        // Not querySelector: heading ids can start with a digit ("2026-plan"),
        // which is an invalid CSS selector.
        document.getElementById(decodeURIComponent(url.hash.slice(1)))?.scrollIntoView()
      } else {
        window.scrollTo(0, 0)
      }
    }
    onNav()
  } catch {
    location.href = url.href
  }
}
