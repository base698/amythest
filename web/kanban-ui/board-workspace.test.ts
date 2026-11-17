import test from 'node:test'
import assert from 'node:assert/strict'
import { boardWorkspaceView, workspaceUnfiled } from './board-workspace.ts'

test('Unassigned output excludes output attached directly or through a card run', () => {
 const artifacts = [{ id: 'unassigned', run_id: 'r1', card_id: '' }, { id: 'own-run', run_id: 'r2', card_id: '' },
  { id: 'other-board', run_id: 'r3', card_id: '' }, { id: 'filed', run_id: 'r1', card_id: 'gait' }, { id: 'input', run_id: 'r1', card_id: '', input: true }]
 const runs = [{ id: 'r1', card_id: '' }, { id: 'r2', card_id: 'gait' }, { id: 'r3', card_id: 'invoice' }]
 assert.deepEqual(workspaceUnfiled(artifacts, runs).map(a => a.id), ['unassigned'])
 assert.deepEqual(workspaceUnfiled(artifacts, []).map(a => a.id), [])
})

test('the selected view can follow the board picker and unknown choices fall back to Board', () => {
 for (const view of ['board', 'project', 'unfiled']) assert.equal(boardWorkspaceView(view), view)
 assert.equal(boardWorkspaceView('needs-you'), 'project')
 assert.equal(boardWorkspaceView(null), 'board')
 assert.equal(boardWorkspaceView('all-work'), 'board')
})
