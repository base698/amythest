// In-place editing of note frontmatter properties from base table cells.
// Cells rendered with data-slug/data-key (plain note.* columns on
// notes-source bases) become editable on double-click; Enter saves via
// POST /api/notes/property, Escape or blur cancels.

export function setupBaseEditing() {
  document.addEventListener("dblclick", (e) => {
    const td = (e.target as HTMLElement).closest?.(
      "td.base-editable",
    ) as HTMLTableCellElement | null
    if (!td || td.querySelector("input")) return
    const { slug, key } = td.dataset
    if (!slug || !key) return

    const orig = (td.textContent ?? "").trim()
    const input = document.createElement("input")
    input.className = "base-cell-input"
    input.value = orig
    td.textContent = ""
    td.appendChild(input)
    input.focus()
    input.select()

    let done = false
    const finish = (text: string) => {
      if (done) return
      done = true
      input.remove()
      td.textContent = text
    }

    let saving = false
    input.addEventListener("blur", () => {
      if (!saving) finish(orig)
    })
    input.addEventListener("keydown", async (ev) => {
      if (ev.key === "Escape") {
        finish(orig)
        return
      }
      if (ev.key !== "Enter") return
      ev.preventDefault()
      const value = input.value.trim()
      saving = true
      input.disabled = true
      try {
        const res = await fetch(`${baseURL()}api/notes/property`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ slug, key, value }),
        })
        if (!res.ok) throw new Error((await res.text()) || res.statusText)
        finish(value)
      } catch (err) {
        saving = false
        input.disabled = false
        input.classList.add("base-cell-error")
        input.title = err instanceof Error ? err.message : String(err)
        input.focus()
      }
    })
  })
}

function baseURL(): string {
  const base = document.body.dataset.base || "/"
  return base.endsWith("/") ? base : base + "/"
}
