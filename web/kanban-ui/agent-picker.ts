import type { Machine, SessionProject, Slot } from './Work'

export function agentOptions(slots: Slot[], sessions: SessionProject[], machines: Machine[], workspace: string, all: boolean, query: string) {
 const terms = query.toLocaleLowerCase().trim().split(/\s+/).filter(Boolean)
 return slots.filter(s => s.status !== 'gone' && (all || !!workspace && s.project === workspace)).map(slot => {
  const session = sessions.find(s => s.session_id === slot.session_id && s.collector === slot.collector)
  const machine = machines.find(m => m.collector === slot.collector)
  const name = (machine?.name || 'Collector').replace(/\s*lifecycle collector$/i, '').trim() || 'Collector'
  const title = session?.title && session.title !== slot.tab && session.title !== session.provider_ref ? session.title : ''
  const tab = slot.tab || slot.pane
  return { key: `${slot.collector}:${slot.pane}:${slot.session_id}`, slot, tab, title, agent: session?.agent || 'agent', machine: name, online: machine?.online ?? false }
 }).filter(o => terms.every(term => [o.tab, o.title, o.agent, o.machine, o.slot.project, o.slot.status, o.slot.pane].join(' ').toLocaleLowerCase().includes(term)))
  .sort((a, b) => Number(b.slot.project === workspace) - Number(a.slot.project === workspace) || a.slot.project.localeCompare(b.slot.project) || a.tab.localeCompare(b.tab, undefined, { numeric: true }) || a.key.localeCompare(b.key))
}
