// Lazy-loaded mermaid renderer: main.ts imports this chunk only when the
// page actually contains a mermaid block.
import mermaid from "mermaid"

export async function renderMermaid() {
  const dark = document.documentElement.dataset.theme === "dark"
  mermaid.initialize({ startOnLoad: false, theme: dark ? "dark" : "default" })
  const blocks = document.querySelectorAll<HTMLElement>("pre.mermaid:not([data-processed])")
  await mermaid.run({ nodes: blocks })
}
