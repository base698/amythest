// Pure pieces of the Project view: what a herdr workspace's agents are doing
// and what they made, by day. Kept free of React so they can be tested.

export type ProjectArtifact = { id: string; kind: string; title: string; ref: string; created_at?: string; media_hash: string; available: boolean; input?: boolean }
export type ProjectInput = { key: string; kind: string; title: string; created_at: string }
export type ProjectSlot = { collector: string; pane: string; session_id: string; status: string; project: string; tab: string; binding?: { card_id: string; acknowledged: boolean } }
export type ProjectSession = { session_id: string; agent: string; title: string }
export type ProjectRun = { project: string; started_at: string; finished_at: string; output_version: number; reviewed_through_version: number; historical: boolean }
export type ProjectMachine = { collector: string; name: string; online: boolean }
export type ProjectCard = { board: string; id: string; title: string }

// dayKey is the local calendar day: activity is grouped by the viewer's days,
// not UTC's.
export function dayKey(d: Date): string {
 const two = (n: number) => String(n).padStart(2, '0')
 return `${d.getFullYear()}-${two(d.getMonth() + 1)}-${two(d.getDate())}`
}

function when(a: { created_at?: string }): Date | null {
 const d = a.created_at ? new Date(a.created_at) : null
 return d && !Number.isNaN(d.getTime()) ? d : null
}

// One artifact can be linked to several runs; each appears once.
export function uniqueArtifacts<T extends { id: string }>(artifacts: T[]): T[] {
 const seen = new Set<string>()
 return artifacts.filter(a => !seen.has(a.id) && !!seen.add(a.id))
}

export function isMedia(a: { kind: string }): boolean { return a.kind === 'image' || a.kind === 'video' }

export type Day = { key: string; label: string; commits: number; media: number; inputs: number; share: number; notes: string[] }
const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

// lastDays summarizes the most recent days ending today, oldest first. share
// is each day's activity relative to the busiest shown day; notes are the
// day's newest commit titles.
export function lastDays(artifacts: ProjectArtifact[], now: Date, count = 4, inputs: ProjectInput[] = []): Day[] {
 const days: Day[] = []
 for (let back = count - 1; back >= 0; back--) {
  const d = new Date(now.getFullYear(), now.getMonth(), now.getDate() - back)
  days.push({ key: dayKey(d), label: `${WEEKDAYS[d.getDay()]} ${d.getDate()}`, commits: 0, media: 0, inputs: 0, share: 0, notes: [] })
 }
 const byKey = new Map(days.map(d => [d.key, d]))
 const dated = uniqueArtifacts(artifacts).map(a => ({ a, at: when(a) })).filter((x): x is { a: ProjectArtifact; at: Date } => x.at !== null)
 dated.sort((x, y) => y.at.getTime() - x.at.getTime())
 for (const { a, at } of dated) {
  const day = byKey.get(dayKey(at))
  if (!day) continue
  if (a.input) day.inputs++
  else if (a.kind === 'commit') { day.commits++; if (day.notes.length < 2 && a.title) day.notes.push(a.title) }
  else if (isMedia(a)) day.media++
 }
 const seen = new Set<string>()
 for (const input of inputs) {
  const at = when(input), day = at && byKey.get(dayKey(at))
  if (day && input.kind === 'chat' && input.title === 'user' && !seen.has(input.key)) { day.inputs++; seen.add(input.key) }
 }
 const busiest = Math.max(1, ...days.map(d => d.commits + d.media + d.inputs))
 for (const d of days) d.share = (d.commits + d.media + d.inputs) / busiest
 return days
}

// galleryMedia is today's images and videos, newest first; when today has
// none, the most recent ones instead, so the gallery is never empty for a
// project that has media.
export function galleryMedia<T extends ProjectArtifact>(artifacts: T[], now: Date, limit = 8): { today: boolean; items: T[] } {
 const media = uniqueArtifacts(artifacts).filter(isMedia)
 media.sort((x, y) => (when(y)?.getTime() ?? 0) - (when(x)?.getTime() ?? 0))
 const today = media.filter(a => { const at = when(a); return at !== null && dayKey(at) === dayKey(now) })
 return today.length ? { today: true, items: today.slice(0, limit) } : { today: false, items: media.slice(0, limit) }
}

export type TabRow = { key: string; tab: string; agent: string; status: string; card?: ProjectCard; pending: boolean; machine: string }

// tabRows lists the workspace's open agent tabs with the card each is
// assigned to. Ended sessions are left out.
export function tabRows(slots: ProjectSlot[], sessions: ProjectSession[], cards: ProjectCard[], machines: ProjectMachine[]): TabRow[] {
 const agent = new Map(sessions.map(s => [s.session_id, s.agent]))
 const card = new Map(cards.map(c => [c.id, c]))
 const machine = new Map(machines.map(m => [m.collector, m.name]))
 const rows = slots.filter(s => s.status !== 'gone').map(s => ({
  key: `${s.collector}:${s.pane}:${s.session_id}`, tab: s.tab || s.pane, agent: agent.get(s.session_id) || 'agent', status: s.status,
  card: s.binding ? card.get(s.binding.card_id) : undefined, pending: !!s.binding && !s.binding.acknowledged, machine: machine.get(s.collector) || '',
 }))
 return rows.sort((a, b) => a.tab.localeCompare(b.tab, undefined, { numeric: true }) || a.key.localeCompare(b.key))
}

// The state line reflects live tabs. Old review receipts are not live alerts.
export function projectCounts(slots: ProjectSlot[]): { working: number; blocked: number; idle: number } {
 const open = slots.filter(s => s.status !== 'gone')
 return {
  working: open.filter(s => s.status === 'working').length,
  blocked: open.filter(s => s.status === 'blocked').length,
  idle: open.filter(s => s.status === 'idle' || s.status === 'done').length,
 }
}

// pickProject keeps the viewer's last choice while it still exists; otherwise
// it is the project with the most recent run, then the first by name.
export function pickProject(projects: string[], runs: ProjectRun[], saved: string): string {
 if (saved && projects.includes(saved)) return saved
 const recent = [...runs].filter(r => r.project && projects.includes(r.project)).sort((a, b) => b.started_at.localeCompare(a.started_at))[0]
 return recent?.project || projects[0] || ''
}

// Calendar arithmetic uses local midnights rather than 24-hour durations.
export function localDay(value: string): Date | null {
 if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return null
 const [year, month, day] = value.split('-').map(Number)
 const date = new Date(year, month - 1, day)
 return dayKey(date) === value ? date : null
}
export function shiftDay(value: string, days: number): string {
 const date = localDay(value)
 if (!date) throw new RangeError('Invalid calendar date')
 return dayKey(new Date(date.getFullYear(), date.getMonth(), date.getDate() + days))
}
export function fourDayRange(ending: string) {
 const start = shiftDay(ending, -3), until = shiftDay(ending, 1)
 return { start, ending, after: localDay(start)!.toISOString(), before: localDay(until)!.toISOString() }
}
export function artifactsInRange<T extends ProjectArtifact>(artifacts: T[], range: { after: string; before: string }): T[] {
 const start = new Date(range.after).getTime(), until = new Date(range.before).getTime()
 return uniqueArtifacts(artifacts).filter(a => { const at = when(a)?.getTime(); return at !== undefined && at >= start && at < until })
  .sort((a, b) => when(b)!.getTime() - when(a)!.getTime())
}

export function inputsInRange<T extends ProjectInput>(documents: T[], range: { after: string; before: string }): T[] {
 const start = Date.parse(range.after), end = Date.parse(range.before), seen = new Set<string>()
 return documents.filter(d => d.kind === 'chat' && d.title === 'user' && Date.parse(d.created_at) >= start && Date.parse(d.created_at) < end && !seen.has(d.key) && !!seen.add(d.key))
  .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))
}
