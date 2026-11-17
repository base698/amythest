import { useRef, useState } from 'react'
import type { Machine, SessionProject, Slot } from './Work'
import { agentOptions } from './agent-picker'

export function AgentPicker({ workspace, card, slots, sessions, machines, busy, onAssign }: {
 workspace: string; card: string; slots: Slot[]; sessions: SessionProject[]; machines: Machine[]; busy: boolean; onAssign: (slot: Slot) => Promise<void>
}) {
 const [query, setQuery] = useState(''), [all, setAll] = useState(false), [selectedKey, setSelectedKey] = useState(''), [limit, setLimit] = useState(50)
 const assigning = useRef(false)
 const options = agentOptions(slots, sessions, machines, workspace, all, query)
 const selected = options.find(o => o.key === selectedKey)
 const bound = selected?.slot.binding?.card_id === card
 return <section className="work-agent-picker" aria-label="Assign a tab to this card">
  <header><h4>Assign a tab</h4><p>Future runs in that tab will be attached to this card.</p></header>
  <div className="work-agent-scope" role="group" aria-label="Agent workspace scope">
   <button type="button" disabled={busy} aria-pressed={!all} onClick={() => { setAll(false); setSelectedKey(''); setLimit(50) }}>{workspace || 'Board workspace'}</button>
   <button type="button" disabled={busy} aria-pressed={all} onClick={() => { setAll(true); setSelectedKey(''); setLimit(50) }}>All workspaces</button>
  </div>
  <label className="work-agent-search">Search tabs<input type="search" value={query} disabled={busy} placeholder="Agent, tab, title, or machine…" onChange={e => { setQuery(e.target.value); setSelectedKey(''); setLimit(50) }} /></label>
  {!workspace && !all ? <p className="muted">Link this board to a herdr workspace in Board settings, or search all workspaces.</p> : <>
   <ul className="work-agent-options" aria-label="Matching agent tabs">{options.slice(0, limit).map(o => <li key={o.key}>
    <button type="button" disabled={busy} aria-pressed={o.key === selectedKey} onClick={() => setSelectedKey(o.key)}>
     <span className="work-agent-option-heading"><strong>{/^\d+$/.test(o.tab) ? `Tab ${o.tab}` : o.tab}</strong><span className={`work-agent ${o.slot.status}`}>{o.agent} · {o.slot.status}</span></span>
     {o.title && <span>{o.title}</span>}
     <small>{o.slot.project || 'No workspace'} · {o.machine}{!o.online && ' · offline'}{o.slot.binding && ` · ${o.slot.binding.card_id === card ? 'Assigned to this card' : 'Assigned to another card'}`}</small>
    </button>
   </li>)}</ul>
   {!options.length && <p className="muted">{query ? 'No tabs match this search.' : 'No open agent tabs in this workspace.'}</p>}
   {options.length > limit && <button className="quiet" type="button" onClick={() => setLimit(limit + 50)}>Show more ({options.length - limit})</button>}
  </>}
  <div className="work-agent-assign"><span>{selected ? `${selected.agent} · ${selected.tab}` : `${options.length} ${options.length === 1 ? 'tab' : 'tabs'}`}</span>
   <button className="primary" type="button" disabled={busy || !selected || bound} onClick={async () => {
    if (!selected || assigning.current) return
    assigning.current = true
    try { await onAssign(selected.slot) } finally { assigning.current = false }
   }}>{busy ? 'Assigning…' : bound ? 'Assigned to this card' : 'Assign selected tab'}</button>
  </div>
 </section>
}
