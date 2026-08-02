import test from "node:test"
import assert from "node:assert/strict"
import { buildCancelPayloads, buildPurgePayloads, type SelectedTask } from "./taskDelete.ts"

const version = "a".repeat(64)
const otherVersion = "b".repeat(64)

function task(overrides: Partial<SelectedTask>): SelectedTask {
  return { slug: "notes/project", line: 3, text: "Ship it", status: "open", version, ...overrides }
}

test("buildCancelPayloads groups open tasks by file and skips other statuses", () => {
  const payloads = buildCancelPayloads([
    task({ line: 3 }),
    task({ line: 7, text: "Also ship" }),
    task({ slug: "other", line: 2, text: "Elsewhere", version: otherVersion }),
    task({ line: 9, status: "cancelled" }),
    task({ line: 10, status: "done" }),
  ])
  assert.equal(payloads.length, 2)
  const first = payloads.find((p) => p.slug === "notes/project")
  assert.deepEqual(first, {
    slug: "notes/project",
    expectedVersion: version,
    items: [{ line: 3, expectedText: "Ship it" }, { line: 7, expectedText: "Also ship" }],
  })
})

test("buildCancelPayloads rejects mixed versions within one file", () => {
  assert.throws(() => buildCancelPayloads([
    task({ line: 1 }),
    task({ line: 2, version: otherVersion }),
  ]), /stale/)
})

test("buildCancelPayloads rejects malformed versions and lines", () => {
  assert.throws(() => buildCancelPayloads([task({ version: "short" })]), /stale/)
  assert.throws(() => buildCancelPayloads([task({ line: 0 })]), /invalid line/)
  assert.throws(() => buildCancelPayloads([task({ slug: "" })]), /missing its source/)
})

test("buildPurgePayloads collects only cancelled tasks as bare lines", () => {
  const payloads = buildPurgePayloads([
    task({ line: 4, status: "cancelled" }),
    task({ line: 9, status: "cancelled" }),
    task({ line: 2, status: "open" }),
  ])
  assert.deepEqual(payloads, [{ slug: "notes/project", expectedVersion: version, lines: [4, 9] }])
})
