// ⌘K search modal backed by the server-side FTS5 endpoint.
//
// Every handler is delegated from document and looks its elements up at
// event time: SPA morphs can replace the modal/input/list nodes at any
// moment, so captured element references (and element-bound listeners)
// go stale and made the dropdown stop responding.

import { matchSlashCommands, parseSlashQuery, type SlashCommand } from "./slashCommands"

type SearchResult = {
  slug: string
  title: string
  excerpt: string
  archived: boolean
  archived_reason?: string
}

let selected = 0
let results: SearchResult[] = []
let commands: SlashCommand[] = [] // non-empty while the query is a slash command
let debounce: number | undefined
let bound = false

const modal = () => document.getElementById("search-modal")
const input = () => document.getElementById("search-input") as HTMLInputElement | null
const list = () => document.getElementById("search-results")
const includeArchived = () => document.getElementById("search-include-archived") as HTMLInputElement | null

export function setupSearch() {
  if (bound) return
  bound = true

  document.addEventListener("keydown", (e) => {
    const m = modal()
    if (!m) return
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
      e.preventDefault()
      m.hidden ? open() : close()
      return
    }
    if (m.hidden) return
    if (e.key === "Escape") {
      close()
    } else if (e.key === "ArrowDown") {
      e.preventDefault()
      selected = Math.min(selected + 1, (commands.length || results.length) - 1)
      render()
    } else if (e.key === "ArrowUp") {
      e.preventDefault()
      selected = Math.max(selected - 1, 0)
      render()
    } else if (e.key === "Enter" && commands[selected]) {
      close()
      window.location.href = commands[selected].href({ base: baseURL(), now: new Date() })
    } else if (e.key === "Enter" && results[selected]) {
      close()
      window.location.href = `${baseURL()}${encodeSlug(results[selected].slug)}`
    }
  })

  document.addEventListener("click", (e) => {
    const target = e.target as Element
    if (target.closest?.("#search-open")) {
      open()
    } else if (target === modal()) {
      close()
    }
  })

  document.addEventListener("input", (e) => {
    const el = e.target as HTMLElement
    if (el.id !== "search-input") return
    window.clearTimeout(debounce)
    debounce = window.setTimeout(runQuery, 120)
  })

  document.addEventListener("change", (e) => {
    const el = e.target as HTMLElement
    if (el.id !== "search-include-archived") return
    runQuery()
  })
}

function open() {
  const m = modal()
  const i = input()
  if (!m || !i) return
  m.hidden = false
  i.value = ""
  results = []
  render()
  i.focus()
}

function close() {
  const m = modal()
  if (m) m.hidden = true
}

async function runQuery() {
  const i = input()
  if (!i) return
  const q = i.value.trim()
  const slash = parseSlashQuery(q)
  if (slash !== null) {
    commands = matchSlashCommands(slash)
    results = []
    selected = 0
    render()
    return
  }
  commands = []
  if (!q) {
    results = []
    render()
    return
  }
  try {
    const archived = includeArchived()?.checked ? "&include_archived=1" : ""
    const res = await fetch(`${baseURL()}api/search?q=${encodeURIComponent(q)}${archived}`)
    if (!res.ok) return
    const fresh = (await res.json()) as SearchResult[]
    // A stale slow response must not clobber a newer query's results.
    if (input()?.value.trim() !== q) return
    results = fresh
    selected = 0
    render()
  } catch {
    /* transient network failure: keep previous results */
  }
}

function render() {
  const l = list()
  if (!l) return
  l.innerHTML = ""
  if (commands.length > 0) {
    renderCommands(l)
    return
  }
  results.forEach((r, i) => {
    const li = document.createElement("li")
    li.className = i === selected ? "selected" : ""
    const a = document.createElement("a")
    a.href = `${baseURL()}${encodeSlug(r.slug)}`
    const title = document.createElement("div")
    title.className = "result-title"
    title.textContent = r.title
    if (r.archived) {
      const badge = document.createElement("span")
      badge.className = "archived-badge"
      badge.textContent = "Archived"
      title.append(badge)
    }
    const excerpt = document.createElement("div")
    excerpt.className = "result-excerpt"
    excerpt.innerHTML = r.excerpt // server-generated snippet with <b> marks
    a.append(title, excerpt)
    li.append(a)
    li.addEventListener("mouseenter", () => {
      if (selected !== i) {
        selected = i
        render()
      }
    })
    l.append(li)
  })
}

function renderCommands(l: HTMLElement) {
  commands.forEach((c, i) => {
    const li = document.createElement("li")
    li.className = i === selected ? "selected" : ""
    const a = document.createElement("a")
    a.href = c.href({ base: baseURL(), now: new Date() })
    const title = document.createElement("div")
    title.className = "result-title"
    title.textContent = `/${c.name}`
    const excerpt = document.createElement("div")
    excerpt.className = "result-excerpt"
    excerpt.textContent = c.description
    a.append(title, excerpt)
    li.append(a)
    li.addEventListener("mouseenter", () => {
      if (selected !== i) {
        selected = i
        render()
      }
    })
    l.append(li)
  })
}

function encodeSlug(slug: string): string {
  return slug.split("/").map(encodeURIComponent).join("/")
}

function baseURL(): string {
  const base = document.body.dataset.base || "/"
  return base.endsWith("/") ? base : base + "/"
}
