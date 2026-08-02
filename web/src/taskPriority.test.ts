import test from "node:test"
import assert from "node:assert/strict"
import { buildTaskPriorityPayload, type PriorityContext } from "./taskPriority.ts"

const version = "a".repeat(64)

function ctx(overrides: Partial<PriorityContext> = {}): PriorityContext {
  return {
    slug: "notes/project",
    line: 3,
    expectedText: "Ship it",
    expectedStatus: "open",
    expectedPriority: 3,
    expectedVersion: version,
    ...overrides,
  }
}

test("buildTaskPriorityPayload carries full stale-safe task identity", () => {
  assert.deepEqual(buildTaskPriorityPayload(ctx(), 1), {
    slug: "notes/project",
    line: 3,
    expectedText: "Ship it",
    expectedStatus: "open",
    expectedPriority: 3,
    expectedVersion: version,
    priority: 1,
  })
})

test("buildTaskPriorityPayload accepts every level including none", () => {
  for (const level of [0, 1, 2, 3, 4, 5]) {
    assert.equal(buildTaskPriorityPayload(ctx(), level).priority, level)
  }
})

test("buildTaskPriorityPayload rejects out-of-range levels", () => {
  assert.throws(() => buildTaskPriorityPayload(ctx(), 6), /between 0 and 5/)
  assert.throws(() => buildTaskPriorityPayload(ctx(), -1), /between 0 and 5/)
  assert.throws(() => buildTaskPriorityPayload(ctx(), Number.NaN), /between 0 and 5/)
})

test("buildTaskPriorityPayload requires the rendered file version", () => {
  assert.throws(() => buildTaskPriorityPayload(ctx({ expectedVersion: "short" }), 1), /stale/)
})

test("buildTaskPriorityPayload requires source coordinates", () => {
  assert.throws(() => buildTaskPriorityPayload(ctx({ slug: "" }), 1), /missing its source/)
  assert.throws(() => buildTaskPriorityPayload(ctx({ line: 0 }), 1), /invalid line/)
  assert.throws(() => buildTaskPriorityPayload(ctx({ expectedStatus: "" }), 1), /missing its status/)
})
