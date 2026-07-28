// Theme toggle. Delegated from document so it survives SPA morphs; the
// bound flag lives in module state because morphing strips DOM attributes.
let bound = false

export function setupDarkmode() {
  if (bound) return
  bound = true
  document.addEventListener("click", (e) => {
    if (!(e.target as Element).closest?.("#darkmode-toggle")) return
    const root = document.documentElement
    const next = root.dataset.theme === "dark" ? "light" : "dark"
    root.dataset.theme = next
    localStorage.setItem("theme", next)
    document.dispatchEvent(new CustomEvent("themechange", { detail: { theme: next } }))
  })
}
