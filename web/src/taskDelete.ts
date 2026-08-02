// Payload builders for the two-step deletion flow: cancel marks open tasks
// [-] cancelled (recoverable), purge permanently removes cancelled lines.
// Both are single-file batches guarded by the rendered whole-file version.

const versionRe = /^[0-9a-f]{64}$/

export interface SelectedTask {
  slug: string
  line: number
  text: string
  status: string
  version: string
}

export interface CancelPayload {
  slug: string
  expectedVersion: string
  items: { line: number; expectedText: string }[]
}

export interface PurgePayload {
  slug: string
  expectedVersion: string
  lines: number[]
}

export const taskCancelEndpoint = "api/tasks/cancel"
export const taskPurgeEndpoint = "api/tasks/purge"

function validate(task: SelectedTask) {
  if (!task.slug) throw new Error("A selected task is missing its source; refresh the page")
  if (!Number.isInteger(task.line) || task.line < 1) throw new Error("A selected task has an invalid line; refresh the page")
  if (!versionRe.test(task.version)) throw new Error("The task list is stale; refresh the page")
}

// buildCancelPayloads groups the open tasks in a selection by source file.
// Rows from one file must agree on the rendered version — a mismatch means
// the page shows mixed states and the whole request would 409 anyway.
export function buildCancelPayloads(tasks: SelectedTask[]): CancelPayload[] {
  const bySlug = new Map<string, CancelPayload>()
  for (const task of tasks) {
    if (task.status !== "open") continue
    validate(task)
    const existing = bySlug.get(task.slug)
    if (!existing) {
      bySlug.set(task.slug, {
        slug: task.slug,
        expectedVersion: task.version,
        items: [{ line: task.line, expectedText: task.text }],
      })
    } else {
      if (existing.expectedVersion !== task.version) throw new Error("The task list is stale; refresh the page")
      existing.items.push({ line: task.line, expectedText: task.text })
    }
  }
  return [...bySlug.values()]
}

// buildPurgePayloads groups the cancelled tasks in a selection by source file.
export function buildPurgePayloads(tasks: SelectedTask[]): PurgePayload[] {
  const bySlug = new Map<string, PurgePayload>()
  for (const task of tasks) {
    if (task.status !== "cancelled") continue
    validate(task)
    const existing = bySlug.get(task.slug)
    if (!existing) {
      bySlug.set(task.slug, { slug: task.slug, expectedVersion: task.version, lines: [task.line] })
    } else {
      if (existing.expectedVersion !== task.version) throw new Error("The task list is stale; refresh the page")
      existing.lines.push(task.line)
    }
  }
  return [...bySlug.values()]
}
