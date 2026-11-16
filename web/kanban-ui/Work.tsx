import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { NOTES_BASE } from './mount'
import type { Card } from './BoardView'
import './work.css'
const API = `${NOTES_BASE}api/work`
type Run = { id: string; collector: string; session_id: string; card_id: string; project: string; started_at: string; finished_at: string; outcome: string; output_version: number; reviewed_through_version: number; historical: boolean; reviewable: boolean }
type Binding = { id: string; version: number; card_id: string; acknowledged: boolean }
type Slot = { collector: string; pane: string; session_id: string; status: string; project: string; tab: string; binding?: Binding; binding_version: number }
type Machine = { collector: string; name: string; online: boolean; syncing: boolean }
type Artifact = { id: string; link_id: string; run_id: string; card_id: string; kind: string; ref: string; title: string; media_hash: string; available: boolean; remote_path: string; attribution: string; featured: boolean }
type Document = { key: string; run_id: string; title: string; body: string }
type SessionProject = { session_id: string; collector: string; agent: string; provider_ref: string; title: string; status: string; project: string; source_project: string; assigned: boolean; project_version: number }
type Snapshot = { projects: string[]; sessions: SessionProject[]; runs: Run[]; slots: Slot[]; machines: Machine[]; artifacts: Artifact[]; documents: Document[] }
type Attention = { subject: string; reason: string; version: string; card_id: string; run_id: string; collector: string; title: string }
async function read<T>(path: string): Promise<T | null> {
 const response = await fetch(`${API}${path}`, { credentials: 'same-origin' })
 if (response.status === 404) return null
 if (!response.ok) throw new Error('Work is temporarily unavailable.')
 return response.json()
}
async function change(path: string, body: unknown) {
 const session = await fetch(`${NOTES_BASE}api/auth/session`).then(r => { if (!r.ok) throw new Error('Sign in again to continue.'); return r.json() })
 const response = await fetch(`${API}${path}`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': session.csrf }, body: JSON.stringify(body) })
 if (!response.ok) throw new Error(response.status === 409 ? 'This changed while you were viewing it. Refresh and try again.' : 'Could not save the Work change.')
}
const WorkContext = createContext<{ data: Snapshot | null; inbox: Attention[]; refresh: () => Promise<void> }>({ data: null, inbox: [], refresh: async () => {} })
export function WorkProvider({ children }: { children: ReactNode }) {
 const [data, setData] = useState<Snapshot | null>(null), [inbox, setInbox] = useState<Attention[]>([])
 async function refresh() { const [d, i] = await Promise.all([read<Snapshot>('/snapshot?summary=1'), read<Attention[]>('/inbox')]); setData(d); setInbox(i || []) }
 useEffect(() => { let active = true; const poll = async () => { try { const [d, i] = await Promise.all([read<Snapshot>('/snapshot?summary=1'), read<Attention[]>('/inbox')]); if (active) { setData(d); setInbox(i || []) } } catch { /* Keep the last snapshot during a transient disconnect. */ } }; void poll(); const timer = setInterval(poll, 15000); return () => { active = false; clearInterval(timer) } }, [])
 return <WorkContext.Provider value={{ data, inbox, refresh }}>{children}</WorkContext.Provider>
}
export function useWorkEnabled() { return useContext(WorkContext).data !== null }
export function WorkPill({ card }: { card: string }) {
 const { data, inbox } = useContext(WorkContext); if (!data) return null
 const pending = inbox.filter(i => i.card_id === card && i.reason !== 'offline')
 const run = data.runs.find(r => r.card_id === card && !r.finished_at && !r.historical)
 if (!pending.length && !run) return null
 const reason = ['blocked', 'stalled', 'needs-review'].find(v => pending.some(i => i.reason === v))
 return <span className="work-pill">{reason === 'needs-review' ? `${pending.length} to review` : reason || 'Working'}</span>
}
export function WorkPanel({ card, readOnly = false }: { card: string; readOnly?: boolean }) {
 const { data: overview, refresh } = useContext(WorkContext)
 const [data, setData] = useState<Snapshot | null>(null), [error, setError] = useState(''), [busy, setBusy] = useState(false), [slot, setSlot] = useState('')
 async function reload() { setData(await read<Snapshot>(`/snapshot?card_id=${encodeURIComponent(card)}`)) }
 useEffect(() => { let active = true; async function poll() { try { const d = await read<Snapshot>(`/snapshot?card_id=${encodeURIComponent(card)}`); if (active) { setData(d); setError('') } } catch (e) { if (active) setError(String(e)) } }; void poll(); const t = setInterval(poll, 10000); return () => { active = false; clearInterval(t) } }, [card])
 async function act(path: string, value: unknown) { setBusy(true); setError(''); try { await change(path, value); await reload(); await refresh() } catch (e) { setError(String(e)) } finally { setBusy(false) } }
 if (!overview) return null
 const slots = overview.slots.filter(s => s.status !== 'gone')
 return <section className="work-panel" aria-label="Card Work">
  <header><div><h3>Work</h3><p>Activity stays with the card where its run began.</p></div></header>
  {error && <p role="alert" className="form-error">{error}</p>}
  {!readOnly && <div className="work-binding"><label>Assign an agent to future runs<select value={slot} onChange={e => setSlot(e.target.value)}><option value="">Choose an agent…</option>{slots.map(s => <option key={s.session_id} value={s.session_id}>{overview.machines.find(m => m.collector === s.collector)?.name || 'Collector'} · {s.tab || s.pane} · {s.status}</option>)}</select></label><button className="quiet" type="button" disabled={busy || !slot} onClick={() => { const s = slots.find(s => s.session_id === slot)!; void act('/bindings', { request_id: crypto.randomUUID(), collector: s.collector, session_id: s.session_id, card_id: card, expected_version: s.binding_version }) }}>Assign</button></div>}
  {data?.slots.map(s => <p className="work-slot" key={s.session_id}>{s.tab || s.pane} · {s.status} · {s.binding?.acknowledged ? 'Assignment received' : 'Assignment pending'} <span>{data.machines.find(m => m.collector === s.collector)?.online ? (data.machines.find(m => m.collector === s.collector)?.syncing ? 'Syncing' : 'Online') : 'Asleep / unreachable'}</span></p>)}
  {data && !data.runs.length && <p className="muted">No runs on this card yet. Assign an agent before its next run.</p>}
  <ul className="work-artifacts">{data?.artifacts.filter(a => !data.runs.some(r => r.id === a.run_id)).map(a => <ArtifactView key={a.link_id} artifact={a} />)}</ul>
  {data?.runs.map(r => <article className="work-run" key={r.id}>
   <header><div><strong>{r.finished_at ? r.outcome : 'Working'}</strong>{r.historical && <span> · Historical import</span>}<p>{new Date(r.started_at).toLocaleString()} · {r.project || 'No project'}</p></div>{r.output_version > r.reviewed_through_version && <button className="quiet" type="button" disabled={busy || !r.reviewable} onClick={() => void act('/review', { run_id: r.id, version: r.output_version })}>Review shown output</button>}</header>
   {!r.reviewable && <p className="muted">This run exceeds the current view limit. Review is unavailable while output is omitted.</p>}
   <ul className="work-artifacts">{data.artifacts.filter(a => a.run_id === r.id).map(a => <ArtifactView key={a.link_id} artifact={a} file={!readOnly && !a.card_id ? () => void act('/file', { request_id: crypto.randomUUID(), link_id: a.link_id, card_id: card, featured: a.featured }) : undefined} />)}</ul>
   {data.documents.filter(d => d.run_id === r.id).map(d => <details key={d.key}><summary>{d.title || 'Observation'}</summary><pre>{d.body}</pre></details>)}
  </article>)}
 </section>
}
function ArtifactView({ artifact: a, file }: { artifact: Artifact; file?: () => void }) {
 const safe = /^https?:\/\//i.test(a.ref)
 return <li><span className="work-kind">{a.kind}</span>{!a.card_id && <small>Suggested · unfiled</small>}{file && <button type="button" className="quiet" onClick={file}>File on this card</button>} {safe ? <a href={a.ref} target="_blank" rel="noreferrer">{a.title || a.ref}</a> : <span>{a.title || a.ref}</span>}{a.media_hash && a.available ? <a href={`${API}/media/${a.media_hash}`} target="_blank" rel="noreferrer">Open file</a> : a.remote_path ? <small>On collector</small> : null}</li>
}
export function WorkInbox({ boards, onOpen }: { boards: { name: string; cards: Card[] }[]; onOpen: (board: string, card: Card) => void }) {
 const { data, inbox, refresh } = useContext(WorkContext), [error, setError] = useState('')
 if (!data) return null
 return <section className="work-panel"><h2>Needs you</h2>{error && <p role="alert">{error}</p>}{!inbox.length && <p className="muted">No agent activity needs attention.</p>}<ul className="work-attention">{inbox.map(i => { const board = boards.find(b => b.cards.some(c => c.id === i.card_id)), card = board?.cards.find(c => c.id === i.card_id); return <li key={i.subject}><div><strong>{card?.title || i.title || 'Unfiled run'}</strong><p>{i.reason.replaceAll('-', ' ')}</p></div>{card && board && <button className="quiet" type="button" onClick={() => onOpen(board.name, card)}>Open card</button>}<button className="quiet" type="button" onClick={async () => { try { await change('/snooze', { subject: i.subject, version: i.version, until: new Date(Date.now() + 3600000).toISOString() }); await refresh() } catch (e) { setError(String(e)) } }}>Snooze 1h</button></li> })}</ul><WorkGallery boards={boards} /></section>
}

function WorkGallery({ boards }: { boards: { name: string; cards: Card[] }[] }) {
 const { data: overview, refresh } = useContext(WorkContext)
 const [open, setOpen] = useState(false), [data, setData] = useState<Snapshot | null>(null), [project, setProject] = useState(''), [card, setCard] = useState(''), [query, setQuery] = useState(''), [hits, setHits] = useState<{ key: string; title: string; body: string }[] | null>(null), [error, setError] = useState('')
 useEffect(() => { if (!open) return; let active = true; void read<Snapshot>(`/snapshot?project=${encodeURIComponent(project)}`).then(d => { if (active) setData(d) }).catch(e => { if (active) setError(String(e)) }); return () => { active = false } }, [open, project])
 if (!overview) return null
 const projects = overview.projects || []
 return <details onToggle={e => setOpen(e.currentTarget.open)}><summary>Projects, unfiled output and Work search</summary>{open && <div className="work-panel">
  {error && <p role="alert">{error}</p>}
  <label>Project <select value={project} onChange={e => { setProject(e.target.value); setHits(null) }}><option value="">All projects</option>{projects.map(p => <option key={p}>{p}</option>)}</select></label>
  <h3>Sessions</h3><p className="muted">Move a session and all its runs to a project. New activity keeps your assignment.</p>
  <datalist id="work-project-names">{projects.map(p => <option key={p} value={p} />)}</datalist>
  <div className="work-sessions">{overview.sessions?.filter(s => !project || s.project === project).map(s => <SessionAssignment key={`${s.session_id}:${s.project_version}`} session={s} machine={overview.machines.find(m => m.collector === s.collector)?.name || 'Collector'} onSaved={async () => { await refresh(); setData(await read(`/snapshot?project=${encodeURIComponent(project)}`)); setHits(null) }} />)}</div>
  <form className="work-binding" onSubmit={async e => { e.preventDefault(); try { setHits(await read(`/search?q=${encodeURIComponent(query)}&project=${encodeURIComponent(project)}`)) } catch (e) { setError(String(e)) } }}><label>Search Work<input value={query} onChange={e => setQuery(e.target.value)} placeholder="Commits, observations, messages…" /></label><button type="submit" className="quiet">Search</button></form>
  {hits && <ul className="work-artifacts">{hits.length ? hits.map(h => <li key={h.key}><strong>{h.title}</strong><p>{h.body}</p></li>) : <li>No matching Work output.</li>}</ul>}
  <h3>Unfiled output</h3><p className="muted">Recent discoveries need your confirmation before they are filed.</p>
  <label>File under <select value={card} onChange={e => setCard(e.target.value)}><option value="">Choose a card…</option>{boards.flatMap(b => b.cards.map(c => <option key={c.id} value={c.id}>{b.name} · {c.title}</option>))}</select></label>
  <ul className="work-artifacts">{data?.artifacts.filter(a => !a.card_id).map(a => <ArtifactView key={a.link_id} artifact={a} file={card ? async () => { try { await change('/file', { request_id: crypto.randomUUID(), link_id: a.link_id, card_id: card, featured: a.featured }); setData(await read(`/snapshot?project=${encodeURIComponent(project)}`)); await refresh() } catch (e) { setError(String(e)) } } : undefined} />)}</ul>
  {data && !data.artifacts.some(a => !a.card_id) && <p className="muted">No unfiled output in this view.</p>}
 </div>}</details>
}

function SessionAssignment({ session: s, machine, onSaved }: { session: SessionProject; machine: string; onSaved: () => Promise<void> }) {
 const [name, setName] = useState(s.project), [busy, setBusy] = useState(false), [error, setError] = useState('')
 async function save(project: string) {
  setBusy(true); setError('')
  try { await change('/project', { request_id: crypto.randomUUID(), session_id: s.session_id, project, expected_version: s.project_version }); await onSaved() }
  catch (e) { setError(String(e)) } finally { setBusy(false) }
 }
 return <article className="work-session" aria-label={s.title || s.provider_ref}>
  <strong>{s.title || s.provider_ref || 'Agent session'}</strong>
  <p>{s.agent} · {machine} · {s.status === 'gone' ? 'Ended' : s.status}</p>
  <form className="work-binding" onSubmit={e => { e.preventDefault(); if (name.trim()) void save(name) }}>
   <label>Project<input list="work-project-names" value={name} onChange={e => setName(e.target.value)} maxLength={200} placeholder="Choose or create a project" /></label>
   <button className="quiet" type="submit" disabled={busy || !name.trim() || name.trim() === s.project}>Move session</button>
   {s.assigned && <button className="quiet" type="button" disabled={busy} onClick={() => void save('')}>Use collector project</button>}
  </form>
  {s.assigned && <p className="muted">Collector project: {s.source_project || 'None'}</p>}
  {error && <p role="alert">{error}</p>}
 </article>
}
