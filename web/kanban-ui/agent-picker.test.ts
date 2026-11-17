import test from 'node:test'
import assert from 'node:assert/strict'
import { agentOptions } from './agent-picker.ts'
const slots = [
 { collector: 'mac', pane: 'p1', session_id: 's1', project: 'robot', tab: '1', status: 'working', binding_version: 0 },
 { collector: 'mac', pane: 'p2', session_id: 's2', project: 'proof', tab: '2', status: 'idle', binding_version: 0 },
 { collector: 'pi', pane: 'p3', session_id: 's3', project: 'robot', tab: '10', status: 'blocked', binding_version: 0 },
 { collector: 'pi', pane: 'p4', session_id: 's4', project: 'robot', tab: '4', status: 'gone', binding_version: 0 },
]
const sessions = [{ session_id: 's1', collector: 'mac', agent: 'claude', provider_ref: 'uuid', title: 'Gait tuning', status: 'working', project: 'robot', source_project: 'robot', assigned: false, project_version: 0 }]
const machines = [{ collector: 'mac', name: 'Justin Mac lifecycle collector', online: true, syncing: false }, { collector: 'pi', name: 'Pi', online: false, syncing: false }]
test('tab picker defaults to the linked workspace and never guesses for unlinked boards', () => {
 assert.deepEqual(agentOptions(slots, sessions, machines, 'robot', false, '').map(o => o.tab), ['1', '10'])
 assert.deepEqual(agentOptions(slots, sessions, machines, '', false, ''), [])
 assert.deepEqual(agentOptions(slots, sessions, machines, 'robot', true, '').map(o => o.slot.project), ['robot', 'robot', 'proof'])
})
test('tab search matches agent, title, machine and state, combining terms', () => {
 const [option] = agentOptions(slots, sessions, machines, 'robot', false, 'CLAUDE gait mac')
 assert.equal(option.slot.session_id, 's1')
 assert.equal(option.machine, 'Justin Mac')
 assert.equal(option.title, 'Gait tuning')
 assert.deepEqual(agentOptions(slots, sessions, machines, 'robot', false, 'proof'), [])
 assert.equal(agentOptions(slots, sessions, machines, 'robot', true, 'proof idle')[0].slot.session_id, 's2')
 assert.equal(agentOptions(slots, sessions, machines, 'robot', false, 'pi blocked')[0].online, false)
})
