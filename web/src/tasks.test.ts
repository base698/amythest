import assert from "node:assert/strict"
import test from "node:test"

import { buildTriagePayload } from "./taskTriage.ts"

test("buildTriagePayload validates and serializes a due-date decision", () => {
  assert.deepEqual(
    buildTriagePayload(
      { slug: "Project", line: 7, expectedText: "Ship prototype" },
      "due",
      "2026-08-15",
    ),
    {
      slug: "Project",
      line: 7,
      expectedText: "Ship prototype",
      action: "due",
      due: "2026-08-15",
    },
  )
})

test("buildTriagePayload rejects due action without a date", () => {
  assert.throws(
    () => buildTriagePayload({ slug: "Project", line: 7, expectedText: "Ship prototype" }, "due", ""),
    /Choose a due date/,
  )
})
