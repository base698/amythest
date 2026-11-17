import { test } from 'node:test'
import assert from 'node:assert/strict'
import { dayKey, galleryMedia, lastDays, pickProject, projectCounts, tabRows, uniqueArtifacts, type ProjectArtifact } from './work-project.ts'

// Local times, so the tests hold in any time zone.
const at = (day: number, hour: number) => new Date(2026, 10, day, hour, 0, 0).toISOString()
const now = new Date(2026, 10, 16, 15, 0, 0)
const art = (id: string, kind: string, title: string, created_at?: string): ProjectArtifact => ({ id, kind, title, ref: id, created_at, media_hash: 'h' + id, available: true })

const artifacts: ProjectArtifact[] = [
 art('c1', 'commit', 'Old commit', at(12, 9)),
 art('c2', 'commit', 'Mic board', at(13, 9)),
 art('i1', 'image', 'lawn.png', at(13, 10)),
 art('c3', 'commit', 'Stairs sim', at(16, 9)),
 art('c4', 'commit', 'Fast sim', at(16, 11)),
 art('c5', 'commit', 'Third today', at(16, 8)),
 art('v1', 'video', 'walk.mp4', at(16, 12)),
 art('v1', 'video', 'walk.mp4', at(16, 12)), // linked to a second run
 art('i2', 'image', 'late.png', at(16, 23)),
 art('f1', 'file', 'notes.txt', at(16, 10)),
 art('x1', 'image', 'undated.png'),
]

test('lastDays counts commits and media per local day, oldest first', () => {
 const days = lastDays(artifacts, now)
 assert.deepEqual(days.map(d => d.label), ['Fri 13', 'Sat 14', 'Sun 15', 'Mon 16'])
 assert.deepEqual(days.map(d => [d.commits, d.media]), [[1, 1], [0, 0], [0, 0], [3, 2]])
 // The newest two commit titles describe the day.
 assert.deepEqual(days[3].notes, ['Fast sim', 'Stairs sim'])
 assert.equal(days[3].share, 1)
 assert.equal(days[0].share, 2 / 5)
 assert.equal(days[1].share, 0)
 // Nothing at all still yields four empty days rather than dividing by zero.
 assert.deepEqual(lastDays([], now).map(d => d.share), [0, 0, 0, 0])
})

test('galleryMedia shows today, else the latest media', () => {
 const today = galleryMedia(artifacts, now)
 assert.equal(today.today, true)
 assert.deepEqual(today.items.map(a => a.id), ['i2', 'v1'])
 const earlier = galleryMedia(artifacts, new Date(2026, 10, 20, 9, 0, 0), 2)
 assert.equal(earlier.today, false)
 assert.deepEqual(earlier.items.map(a => a.id), ['i2', 'v1'])
 assert.deepEqual(galleryMedia([art('c', 'commit', 'x', at(16, 9))], now), { today: false, items: [] })
})

test('uniqueArtifacts and dayKey', () => {
 assert.equal(uniqueArtifacts(artifacts).length, artifacts.length - 1)
 assert.equal(dayKey(new Date(2026, 0, 5, 23, 59)), '2026-01-05')
})

test('tabRows lists open tabs with their cards', () => {
 const rows = tabRows([
  { collector: 'm', pane: 't10', session_id: 's10', status: 'idle', project: 'robot', tab: '10' },
  { collector: 'm', pane: 't2', session_id: 's2', status: 'working', project: 'robot', tab: '2', binding: { card_id: 'k1', acknowledged: false } },
  { collector: 'm', pane: 't3', session_id: 's3', status: 'gone', project: 'robot', tab: '3' },
  { collector: 'p', pane: 'w1:p2', session_id: 's4', status: 'blocked', project: 'robot', tab: '', binding: { card_id: 'missing', acknowledged: true } },
 ], [{ session_id: 's2', agent: 'claude', title: '2' }, { session_id: 's4', agent: 'hermes', title: '' }],
 [{ board: 'Robotics', id: 'k1', title: 'Learned gait' }], [{ collector: 'm', name: 'Mac', online: true }])
 assert.deepEqual(rows.map(r => r.tab), ['2', '10', 'w1:p2']) // numeric order; a tab with no name goes by its pane
 assert.deepEqual([rows[0].agent, rows[0].card?.title, rows[0].pending, rows[0].machine], ['claude', 'Learned gait', true, 'Mac'])
 assert.deepEqual([rows[1].agent, rows[1].card], ['agent', undefined])
 assert.deepEqual([rows[2].card, rows[2].pending, rows[2].machine], [undefined, false, '']) // bound to a card that is not on a loaded board
})

test('projectCounts and pickProject', () => {
 const run = (project: string, started: string, finished: string, out: number, seen: number, historical = false) =>
  ({ project, started_at: started, finished_at: finished, output_version: out, reviewed_through_version: seen, historical })
 const runs = [run('robot', '2026-11-16T10:00:00Z', '2026-11-16T10:05:00Z', 3, 1), run('robot', '2026-11-16T11:00:00Z', '', 2, 0),
  run('robot', '2026-11-15T10:00:00Z', '2026-11-15T10:05:00Z', 3, 3), run('notes', '2026-11-16T12:00:00Z', '2026-11-16T12:01:00Z', 1, 0, true)]
 assert.deepEqual(projectCounts([
  { collector: 'm', pane: 'a', session_id: '1', status: 'working', project: 'robot', tab: '' },
  { collector: 'm', pane: 'b', session_id: '2', status: 'blocked', project: 'robot', tab: '' },
  { collector: 'm', pane: 'c', session_id: '3', status: 'gone', project: 'robot', tab: '' },
 ], runs), { working: 1, blocked: 1, unreviewed: 1 })
 assert.equal(pickProject(['notes', 'robot'], runs, 'robot'), 'robot')
 assert.equal(pickProject(['notes', 'robot'], runs, 'deleted'), 'notes') // most recent run
 assert.equal(pickProject(['a', 'b'], [], ''), 'a')
 assert.equal(pickProject([], runs, 'robot'), '')
})
