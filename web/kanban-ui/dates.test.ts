import assert from 'node:assert/strict'
import test from 'node:test'

import { formatDueDate } from './dates.ts'

test('formatDueDate preserves four-digit years below 100', () => {
  const formatted = formatDueDate('0099-01-01', 'en-US')
  assert.equal(formatted, 'Jan 1, 99')
})

test('formatDueDate does not shift a modern calendar date', () => {
  assert.equal(formatDueDate('2026-08-04', 'en-US'), 'Aug 4, 2026')
})
