// Slash commands for the ⌘K search modal. Typing "/" lists commands, "/tod"
// narrows by prefix, Enter runs the selection. Adding a command is one entry
// in the registry below — the search UI renders and dispatches them
// generically, so nothing else needs to change.
//
// Kept DOM-free so the registry and matching are unit-testable.

export type SlashContext = {
  base: string // site base URL, trailing slash included
  now: Date
}

export type SlashCommand = {
  name: string // typed after "/", no spaces
  description: string
  // href builds the navigation target; commands that need side effects
  // instead of navigation can grow a run() variant later.
  href: (ctx: SlashContext) => string
}

const DAILY_NOTES_SLUG_PREFIX = "Daily-Notes"

export const slashCommands: SlashCommand[] = [
  {
    name: "today",
    description: "Open today's daily note",
    href: ({ base, now }) => `${base}${DAILY_NOTES_SLUG_PREFIX}/${localDateStamp(now)}`,
  },
]

// parseSlashQuery returns the command text after a leading "/", or null when
// the query is not a slash command. A bare "/" returns "" (list everything).
export function parseSlashQuery(query: string): string | null {
  if (!query.startsWith("/")) return null
  return query.slice(1).trimStart()
}

export function matchSlashCommands(text: string): SlashCommand[] {
  const needle = text.toLowerCase()
  return slashCommands.filter((c) => c.name.startsWith(needle))
}

// Local-timezone date stamp: a daily note belongs to the user's day, not
// UTC's — toISOString would point at yesterday's note before 5pm PT.
export function localDateStamp(now: Date): string {
  const y = now.getFullYear()
  const m = String(now.getMonth() + 1).padStart(2, "0")
  const d = String(now.getDate()).padStart(2, "0")
  return `${y}-${m}-${d}`
}
