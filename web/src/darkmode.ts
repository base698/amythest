// Theme controls: the ◐ light/dark toggle and the palette <select>.
// Both write <html> data attributes ([data-theme], [data-palette]) and
// localStorage, and both fire "themechange" so mermaid/graph re-render.
// Listeners are delegated from document so they survive SPA morphs; the
// bound flag lives in module state because morphing strips DOM attributes.
let bound = false

function announce() {
  const root = document.documentElement
  document.dispatchEvent(
    new CustomEvent("themechange", {
      detail: { theme: root.dataset.theme, palette: root.dataset.palette ?? "default" },
    }),
  )
}

function applyPalette(name: string) {
  const root = document.documentElement
  if (name === "default") delete root.dataset.palette
  else root.dataset.palette = name
  localStorage.setItem("palette", name)
  announce()
}

// syncPicker re-applies the stored palette (localStorage is the source of
// truth — a morph can strip the <html> attribute) and points the freshly
// rendered select at it; a stored palette missing from the options
// (config change) resets to default.
function syncPicker() {
  const select = document.getElementById("theme-select") as HTMLSelectElement | null
  if (!select) return
  const stored = localStorage.getItem("palette") ?? "default"
  if ([...select.options].some((o) => o.value === stored)) {
    if ((document.documentElement.dataset.palette ?? "default") !== stored) {
      applyPalette(stored)
    }
    select.value = stored
  } else {
    applyPalette("default")
    select.value = "default"
  }
}

export function setupDarkmode() {
  syncPicker()
  if (bound) return
  bound = true
  document.addEventListener("click", (e) => {
    if (!(e.target as Element).closest?.("#darkmode-toggle")) return
    const root = document.documentElement
    const next = root.dataset.theme === "dark" ? "light" : "dark"
    root.dataset.theme = next
    localStorage.setItem("theme", next)
    announce()
  })
  document.addEventListener("change", (e) => {
    const select = (e.target as Element).closest?.("#theme-select") as HTMLSelectElement | null
    if (!select) return
    applyPalette(select.value)
  })
}
