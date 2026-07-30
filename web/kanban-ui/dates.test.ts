import assert from 'node:assert/strict'
import test from 'node:test'

import { formatCreatedDate, formatDueDate } from './dates.ts'

test('formatDueDate preserves four-digit years below 100', () => {
  const formatted = formatDueDate('0099-01-01', 'en-US')
  assert.equal(formatted, 'Jan 1, 99')
})

test('formatDueDate does not shift a modern calendar date', () => {
  assert.equal(formatDueDate('2026-08-04', 'en-US'), 'Aug 4, 2026')
})

test('formatCreatedDate formats an ISO timestamp and preserves invalid input', () => {
  assert.equal(formatCreatedDate('2026-04-12T23:30:00Z', 'en-US', 'UTC'), 'Apr 12, 2026')
  assert.equal(formatCreatedDate('not-a-date', 'en-US', 'UTC'), 'not-a-date')
})
