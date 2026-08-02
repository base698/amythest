import assert from "node:assert/strict"
import test from "node:test"

import { buildFileDispositionPayload, buildFileDispositionPrompt, buildTriagePayload } from "./taskTriage.ts"
import { buildTaskDuePayload, taskDueClearSelector, taskDueEndpoint, taskDueSaveSelector } from "./taskDue.ts"
import { buildTaskTogglePayload } from "./taskToggle.ts"

test("buildTaskTogglePayload carries the exact rendered file version", () => {
  const version = "a".repeat(64)
  assert.deepEqual(buildTaskTogglePayload("Projects/Launch", 7, version, true), {
    slug: "Projects/Launch",
    line: 7,
    expectedVersion: version,
    done: true,
  })
  assert.throws(() => buildTaskTogglePayload("Projects/Launch", 7, "", true), /refresh/)
})

test("task due-date UI contract names both controls and its endpoint", () => {
  assert.equal(taskDueSaveSelector, "[data-task-due-save]")
  assert.equal(taskDueClearSelector, "[data-task-due-clear]")
  assert.equal(taskDueEndpoint, "api/tasks/due")
})

test("buildTaskDuePayload serializes a stale-safe due-date change", () => {
  assert.deepEqual(buildTaskDuePayload({
    slug: "Projects/Launch",
    line: 7,
    expectedText: "Ship release",
    expectedStatus: "open",
    expectedDue: "2026-08-15",
  }, "2026-08-20"), {
    slug: "Projects/Launch",
    line: 7,
    expectedText: "Ship release",
    expectedStatus: "open",
    expectedDue: "2026-08-15",
    due: "2026-08-20",
  })
})

test("buildTaskDuePayload represents clearing as an empty due date", () => {
  assert.deepEqual(buildTaskDuePayload({
    slug: "Projects/Launch",
    line: 7,
    expectedText: "Ship release",
    expectedStatus: "open",
    expectedDue: "2026-08-15",
  }, ""), {
    slug: "Projects/Launch",
    line: 7,
    expectedText: "Ship release",
    expectedStatus: "open",
    expectedDue: "2026-08-15",
    due: "",
  })
})

test("buildTaskDuePayload requires the rendered task status", () => {
  assert.throws(() => buildTaskDuePayload({
    slug: "Projects/Launch",
    line: 7,
    expectedText: "Ship release",
    expectedStatus: "",
    expectedDue: "2026-08-15",
  }, "2026-08-20"), /refresh/)
})

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

test("buildFileDispositionPayload validates a recoverable whole-file action", () => {
  const version = "a".repeat(64)
  assert.deepEqual(buildFileDispositionPayload("Projects/Old", "archive", version), {
    slug: "Projects/Old",
    action: "archive",
    expectedVersion: version,
  })
  assert.throws(() => buildFileDispositionPayload("Projects/Old", "trash", ""), /refresh/)
})

test("buildFileDispositionPrompt names the file and recoverable destination", () => {
  assert.match(buildFileDispositionPrompt("Projects/Old.md", "archive"), /Projects\/Old\.md.*Archive\/Deleted/s)
  assert.match(buildFileDispositionPrompt("Projects/Old.md", "trash"), /Projects\/Old\.md.*\.trash\/Amythest/s)
})
