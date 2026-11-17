import { useContext, useEffect, useRef, useState } from 'react'
import type { Card } from './BoardView'
import { ArtifactView, WorkContext, mediaURL, read, type Artifact, type Snapshot } from './Work'
import { galleryMedia, lastDays, pickProject, projectCounts, tabRows, uniqueArtifacts } from './work-project'

const SAVED = 'amythest.work.project'
function saved(): string { try { return localStorage.getItem(SAVED) || '' } catch { return '' } }

// WorkProjects is the Project view: for one herdr workspace, its open agent
// tabs and the cards they are assigned to, the last days' activity, and the
// media made today. It reads; assigning and filing stay where they are.
export function WorkProjects({ boards, onOpen }: { boards: { name: string; cards: Card[] }[]; onOpen: (board: string, card: Card) => void }) {
 const { data: overview } = useContext(WorkContext)
 const [choice, setChoice] = useState(saved), [data, setData] = useState<Snapshot | null>(null), [error, setError] = useState('')
 const projects = overview?.projects || []
 const project = pickProject(projects, overview?.runs || [], choice)
 useEffect(() => {
  if (!project) return
  let active = true
  setData(null)
  const poll = async () => {
   try { const d = await read<Snapshot>(`/snapshot?project=${encodeURIComponent(project)}`); if (active) { setData(d); setError('') } }
   catch (e) { if (active) setError(String(e)) }
  }
  void poll(); const timer = setInterval(poll, 15000)
  return () => { active = false; clearInterval(timer) }
 }, [project])
 if (!overview || !projects.length) return null
 const choose = (name: string) => { setChoice(name); try { localStorage.setItem(SAVED, name) } catch { /* private browsing: just not remembered */ } }
 const now = new Date()
 const cards = boards.flatMap(b => b.cards.map(c => ({ board: b.name, id: c.id, title: c.title })))
 // Tabs and counts come from the overview so they are current even before
 // this project's own snapshot has loaded.
 const slots = overview.slots.filter(s => s.project === project)
 const rows = tabRows(slots, overview.sessions || [], cards, overview.machines)
 const counts = projectCounts(slots, (data?.runs || []).filter(r => r.project === project))
 const artifacts = uniqueArtifacts(data?.artifacts || [])
 const days = lastDays(artifacts, now), gallery = galleryMedia(artifacts, now)
 const commits = artifacts.filter(a => a.kind === 'commit').slice(0, 6)
 const open = (id: string) => { for (const b of boards) { const c = b.cards.find(c => c.id === id); if (c) return onOpen(b.name, c) } }
 const machines = overview.machines.filter(m => slots.some(s => s.collector === m.collector) || (data?.runs || []).some(r => r.collector === m.collector))
 return <section className="work-panel work-project" aria-labelledby="work-project-title">
  <header className="work-inbox-heading"><div><h2 id="work-project-title">Projects</h2><p>What agents are doing and what they made, by herdr workspace.</p></div></header>
  <div className="work-project-picker" role="group" aria-label="Project">
   {projects.map(p => <button key={p} type="button" aria-pressed={p === project} onClick={() => choose(p)}>{p}</button>)}
  </div>
  <p className="work-project-state">
   <span className="work-dot working" aria-hidden="true" />{counts.working} working
   <span className="work-dot blocked" aria-hidden="true" />{counts.blocked} blocked
   <span className="work-dot done" aria-hidden="true" />{counts.unreviewed} done, not reviewed
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
    <h3>Last four days</h3>
    <ol className="work-days">{days.map(d => <li key={d.key}>
     <strong>{d.label}</strong>
     <span className="work-day-bar" aria-hidden="true"><span style={{ width: `${Math.round(d.share * 100)}%` }} /></span>
     <span className="work-day-counts">{d.commits} {d.commits === 1 ? 'commit' : 'commits'} · {d.media} media</span>
     {d.notes.map((n, i) => <span className="work-day-note" key={i}>{n}</span>)}
    </li>)}</ol>
    <h3>{gallery.today ? 'Today’s media' : 'Latest media'}</h3>
    {gallery.items.length ? <ul className="work-gallery">{gallery.items.map(a => <MediaTile key={a.id} artifact={a} dated={!gallery.today} />)}</ul>
     : <p className="muted">{data ? 'No images or videos recorded for this project yet.' : 'Loading…'}</p>}
    {commits.length > 0 && <><h3>Latest commits</h3><ul className="work-artifacts">{commits.map(a => <ArtifactView key={a.id} artifact={a} />)}</ul></>}
   </div>
  </div>
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

