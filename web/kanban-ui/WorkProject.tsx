import { useContext, useEffect, useRef, useState } from 'react'
import type { Card } from './BoardView'
import { ArtifactView, WorkContext, mediaURL, useProjectSnapshot, type Artifact, type Document, type Snapshot, type WorkspaceSnapshot } from './Work'
import { artifactsInRange, dayKey, fourDayRange, inputsInRange, isMedia, lastDays, localDay, pickProject, projectCounts, shiftDay, tabRows } from './work-project'

type ProjectProps = { boards: { name: string; cards: Card[] }[]; workspace?: WorkspaceSnapshot; onOpen: (board: string, card: Card) => void }
const SAVED = 'amythest.work.project'
function saved(): string { try { return localStorage.getItem(SAVED) || '' } catch { return '' } }
const ENDING = 'amythest.work.project.ending'
function savedEnding(): string {
 const today = dayKey(new Date())
 try { const value = localStorage.getItem(ENDING) || ''; return localDay(value) && value <= today ? value : today } catch { return today }
}

// WorkProjects is the Project view: for one herdr workspace, its open agent
// tabs and their cards, plus inputs and output in a selected date window.
export function WorkProjects({ boards, workspace, onOpen }: ProjectProps) {
 const { data: overview } = useContext(WorkContext)
 const [choice, setChoice] = useState(saved)
 const [ending, setEnding] = useState(savedEnding)
 const range = fourDayRange(ending), today = dayKey(new Date())
 const projects = overview?.projects || []
 const project = workspace ? workspace.project : pickProject(projects, overview?.runs || [], choice)
 const history = useProjectSnapshot(project, range.after, range.before)
 const { data, error } = history
 const current = workspace?.data || data
 if (!overview || !projects.length) return null
 const choose = (name: string) => { setChoice(name); try { localStorage.setItem(SAVED, name) } catch { /* private browsing: just not remembered */ } }
 const chooseEnding = (value: string) => { if (!localDay(value) || value > today) return; setEnding(value); try { localStorage.setItem(ENDING, value) } catch { /* Remember when storage is available. */ } }
 const cards = boards.flatMap(b => b.cards.map(c => ({ board: b.name, id: c.id, title: c.title })))
 // Tabs and counts come from the overview so they are current even before
 // this project's own snapshot has loaded.
 const slots = overview.slots.filter(s => s.project === project)
 const rows = tabRows(slots, overview.sessions || [], cards, overview.machines)
 const counts = projectCounts(slots)
 const artifacts = artifactsInRange(data?.artifacts || [], range)
 const inputs = inputsInRange(data?.documents || [], range), attachments = artifacts.filter(a => a.input)
 const days = lastDays(artifacts, localDay(ending)!, 4, inputs), media = artifacts.filter(a => !a.input && isMedia(a)).slice(0, 8)
 const commits = artifacts.filter(a => !a.input && a.kind === 'commit').slice(0, 6)
 const open = (id: string) => { for (const b of boards) { const c = b.cards.find(c => c.id === id); if (c) return onOpen(b.name, c) } }
 const machines = overview.machines.filter(m => slots.some(s => s.collector === m.collector) || (current?.runs || []).some(r => r.collector === m.collector))
 return <section className="work-panel work-project" aria-labelledby="work-project-title">
  <header className="work-inbox-heading"><div><h2 id="work-project-title">{workspace ? project : 'Projects'}</h2><p>Open tabs, shared inputs and output from this workspace.</p></div></header>
  {!workspace && <div className="work-project-picker" role="group" aria-label="Project">
   {projects.map(p => <button key={p} type="button" aria-pressed={p === project} onClick={() => choose(p)}>{p}</button>)}
  </div>}
  <p className="work-project-state">
   <span className="work-dot working" aria-hidden="true" />{counts.working} working
   <span className="work-dot blocked" aria-hidden="true" />{counts.blocked} blocked
   <span className="work-dot off" aria-hidden="true" />{counts.idle} idle
   {machines.length > 0 && <span className="work-project-machines">collectors: {machines.map(m => <span key={m.collector}>{m.name} <span className={`work-dot ${m.online ? 'done' : 'off'}`} aria-hidden="true" />{m.online ? 'online' : 'asleep'}</span>)}</span>}
  </p>
  {error && <p role="alert" className="form-error">{error}</p>}
  <div className="work-project-grid">
   <div className="work-project-box">
    <h3>Tabs</h3>
    {rows.length ? <ul className="work-tabs">{rows.map(r => <li key={r.key}>
     <span className="work-tab-name">{r.tab}</span>
     <span className="work-tab-card">{r.card
      ? <><button type="button" className="work-artifact-title" onClick={() => open(r.card!.id)}>{r.card.title}</button>{r.pending && <small> · assignment pending</small>}</>
      : <span className="muted">not assigned to a card</span>}</span>
     <span className={`work-agent ${r.status}`}>{r.agent} · {r.status}</span>
    </li>)}</ul> : <p className="muted">No agent tabs are open in this workspace.</p>}
    <p className="muted">Assign a tab to a card from the card's Work tab.</p>
   </div>
   <div className="work-project-box">
    <div className="work-date-controls">
     <label>Four days ending<input type="date" aria-label="Four days ending" value={ending} max={today} onChange={e => chooseEnding(e.target.value)} /></label>
     <div><button className="quiet" type="button" aria-label="Previous four days" onClick={() => chooseEnding(shiftDay(ending, -4))}>‹</button>
      <button className="quiet" type="button" aria-label="Next four days" disabled={ending >= today} onClick={() => { const next = shiftDay(ending, 4); chooseEnding(next > today ? today : next) }}>›</button>
      <button className="quiet" type="button" disabled={ending === today} onClick={() => chooseEnding(today)}>Today</button></div>
    </div>
    <ol className="work-days">{days.map(d => <li key={d.key}>
     <strong>{d.label}</strong>
     <span className="work-day-bar" aria-hidden="true"><span style={{ width: `${Math.round(d.share * 100)}%` }} /></span>
     <span className="work-day-counts">{data ? `${d.inputs} ${d.inputs === 1 ? 'input' : 'inputs'} · ${d.commits} ${d.commits === 1 ? 'commit' : 'commits'} · ${d.media} media` : 'Loading…'}</span>
     {d.notes.map((n, i) => <span className="work-day-note" key={i}>{n}</span>)}
    </li>)}</ol>
    <ProjectInputs key={`${project}:${ending}`} documents={inputs} attachments={attachments} snapshot={data} />
    <h3>Media in these four days</h3>
    {media.length ? <ul className="work-gallery">{media.map(a => <MediaTile key={a.id} artifact={a} dated />)}</ul>
     : <p className="muted">{data ? 'No images or videos recorded in this date range.' : 'Loading…'}</p>}
    <h3>Commits in these four days</h3>
    {commits.length ? <ul className="work-artifacts">{commits.map(a => <ArtifactView key={a.id} artifact={a} />)}</ul> : <p className="muted">{data ? 'No commits recorded in this date range.' : 'Loading…'}</p>}
   </div>
  </div>
 </section>
}

function ProjectInputs({ documents, attachments, snapshot }: { documents: Document[]; attachments: Artifact[]; snapshot: Snapshot | null }) {
 const [limit, setLimit] = useState(8)
 const sessionTitle = (runID: string) => {
  const run = snapshot?.runs.find(r => r.id === runID)
  const session = snapshot?.sessions.find(s => s.session_id === run?.session_id && s.collector === run?.collector)
  return session?.title || session?.agent || 'Session'
 }
 return <section className="work-inputs" aria-label="Inputs in these four days">
  <h3>Inputs in these four days</h3>
  <p className="muted">Messages and attachments shared with sessions.</p>
  {attachments.length > 0 && <ul className="work-gallery">{attachments.slice(0, limit).map(a => <MediaTile key={a.id} artifact={a} dated />)}</ul>}
  {documents.length > 0 && <ul className="work-input-messages">{documents.slice(0, limit).map(d => <li key={d.key}><details>
   <summary><span className="work-input-meta"><time dateTime={d.created_at}>{new Date(d.created_at).toLocaleString([], { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })}</time> · {sessionTitle(d.run_id)}</span><span>{d.body.slice(0, 180)}{d.body.length > 180 ? '…' : ''}</span></summary>
   <p>{d.body}</p>
  </details></li>)}</ul>}
  {Math.max(documents.length, attachments.length) > limit && <button className="quiet" type="button" onClick={() => setLimit(limit + 16)}>Show more inputs</button>}
  {!documents.length && !attachments.length && <p className="muted">{snapshot ? 'No shared inputs recorded in this date range.' : 'Loading…'}</p>}
 </section>
}

// MediaTile is one image or video of the gallery: the picture, its name, and
// a larger view on click. Files that were never uploaded say where they are.
function MediaTile({ artifact: a, dated }: { artifact: Artifact; dated: boolean }) {
 const [expanded, setExpanded] = useState(false), [failed, setFailed] = useState(false)
 const dialog = useRef<HTMLDialogElement>(null)
 const title = a.title || a.ref || 'Media', media = mediaURL(a.media_hash), shown = !!a.media_hash && a.available && !failed
 useEffect(() => { if (expanded) dialog.current?.showModal(); else dialog.current?.close() }, [expanded])
 return <li className="work-tile">
  {shown && a.kind === 'image' && <button className="work-image-preview" type="button" aria-label={`Enlarge ${title}`} onClick={() => setExpanded(true)}><img src={media} alt={title} loading="lazy" onError={() => setFailed(true)} /></button>}
  {shown && a.kind === 'video' && <video src={media} controls preload="metadata" />}
  {!shown && <div className="work-tile-missing">{failed ? 'Preview unavailable' : 'Not uploaded; it is on the machine that made it'}</div>}
  <span className="work-tile-name">{title}</span>
  {a.created_at && <time dateTime={a.created_at}>{new Date(a.created_at).toLocaleString([], dated ? { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' } : { hour: 'numeric', minute: '2-digit' })}</time>}
  {shown && a.kind === 'image' && <dialog ref={dialog} className="work-image-dialog" aria-label={title} onClose={() => setExpanded(false)} onClick={e => { if (e.target === e.currentTarget) setExpanded(false) }}>
   <header><strong>{title}</strong><button className="quiet" type="button" autoFocus onClick={() => setExpanded(false)}>Close</button></header>
   {expanded && <img src={media} alt={title} />}
   <footer><a href={media} target="_blank" rel="noreferrer">Open in new tab</a><a href={`${media}?download=1&filename=${encodeURIComponent(title)}`} download={title}>Download</a></footer>
  </dialog>}
 </li>
}

