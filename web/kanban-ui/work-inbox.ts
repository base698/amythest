export type Attention = {
 subject: string; reason: string; version: string; card_id: string; run_id: string; collector: string; title: string
 session_id: string; project: string; agent: string; finished_at: string; outcome: string; preview: string; output_count: number
}
export type AttentionGroup = { key: string; latest: Attention; items: Attention[] }
export function groupAttention(items: Attention[]): AttentionGroup[] {
 const groups = new Map<string, AttentionGroup>()
 for (const item of items) {
  // Different cards and failure states must never disappear inside a successful session.
  const key = item.reason === 'needs-review' && item.session_id
   ? JSON.stringify([item.collector, item.session_id, item.card_id, item.outcome]) : item.subject
  const group = groups.get(key)
  if (group) {
   group.items.push(item)
   if (item.finished_at > group.latest.finished_at) group.latest = item
  } else groups.set(key, { key, latest: item, items: [item] })
 }
 return [...groups.values()].map(g => ({ ...g, items: g.items.sort((a, b) => b.finished_at.localeCompare(a.finished_at)) }))
  .sort((a, b) => Number(a.latest.reason === 'needs-review') - Number(b.latest.reason === 'needs-review') || b.latest.finished_at.localeCompare(a.latest.finished_at))
}
export function attentionStatus(item: Attention, count = 1): string {
 if (item.reason === 'offline') return 'Collector is unreachable'
 if (item.reason === 'blocked') return 'Agent needs input'
 if (item.reason === 'stalled') return 'No recent activity'
 const subject = count === 1 ? '1 turn' : `${count} turns`
 switch (item.outcome) {
  case 'success': return `${subject} finished`
  case 'failure': return `${subject} reported an error`
  case 'cancelled': return `${subject} cancelled`
  default: return `${subject} ended without a confirmed result`
 }
}
