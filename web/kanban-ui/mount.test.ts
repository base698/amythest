import test from 'node:test'
import assert from 'node:assert/strict'
import { kanbanMountFromPathname } from './mount.ts'

test('detects the bare mount', () => {
  assert.equal(kanbanMountFromPathname('/kanban'), '/kanban')
  assert.equal(kanbanMountFromPathname('/kanban/'), '/kanban')
  assert.equal(kanbanMountFromPathname('/kanban/proof/1.0'), '/kanban')
})

test('detects a proxied mount under a prefix', () => {
  assert.equal(kanbanMountFromPathname('/notes/kanban'), '/notes/kanban')
  assert.equal(kanbanMountFromPathname('/notes/kanban/proof'), '/notes/kanban')
})

test('a board named kanban does not shift the mount', () => {
  assert.equal(kanbanMountFromPathname('/kanban/kanban'), '/kanban')
})

test('prefixes merely containing the word kanban do not match', () => {
  assert.equal(kanbanMountFromPathname('/kanban-notes/x'), '/kanban')
})
