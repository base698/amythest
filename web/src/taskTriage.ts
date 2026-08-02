export type TriageAction = "backlog" | "due" | "reference" | "cancel"
export type FileDispositionAction = "archive" | "trash"

export interface FileDispositionPayload {
  slug: string
  action: FileDispositionAction
  expectedVersion: string
}

export function buildFileDispositionPayload(
  slug: string,
  action: FileDispositionAction,
  expectedVersion: string,
): FileDispositionPayload {
  if (!slug || !/^[a-f0-9]{64}$/i.test(expectedVersion)) {
    throw new Error("File identity is stale; refresh the page")
  }
  return { slug, action, expectedVersion }
}

export function buildFileDispositionPrompt(path: string, action: FileDispositionAction): string {
  return action === "archive"
    ? `Archive the entire file “${path}” to Archive/Deleted?\n\nThe note is retained and can be recovered.`
    : `Move the entire file “${path}” to .trash/Amythest?\n\nThe note is retained and can be recovered.`
}

export interface TriageIdentity {
  slug: string
  line: number
  expectedText: string
}

export interface TriagePayload extends TriageIdentity {
  action: TriageAction
  due?: string
}

export function buildTriagePayload(
  identity: TriageIdentity,
  action: TriageAction,
  due = "",
): TriagePayload {
  if (!identity.slug || !Number.isInteger(identity.line) || identity.line < 1) {
    throw new Error("Task identity is incomplete; refresh the page")
  }
  if (action === "due" && !/^\d{4}-\d{2}-\d{2}$/.test(due)) {
    throw new Error("Choose a due date")
  }
  const payload: TriagePayload = { ...identity, action }
  if (action === "due") payload.due = due
  return payload
}

export interface FileHidePayload {
  slug: string
  expectedVersion: string
}

export function buildFileHidePayload(slug: string, expectedVersion: string): FileHidePayload {
  if (!slug || !/^[a-f0-9]{64}$/i.test(expectedVersion)) {
    throw new Error("File identity is stale; refresh the page")
  }
  return { slug, expectedVersion }
}

export interface TaskMovePayload {
  board: string
  slug: string
  line: number
  expectedText: string
  expectedStatus: string
  expectedVersion: string
}

export function buildTaskMovePayload(payload: TaskMovePayload): TaskMovePayload {
  if (!payload.board.trim()) throw new Error("Choose a board")
  if (!payload.slug || !Number.isInteger(payload.line) || payload.line < 1 ||
      payload.expectedStatus !== "open" || !/^[a-f0-9]{64}$/i.test(payload.expectedVersion)) {
    throw new Error("Task identity is stale; refresh the page")
  }
  return { ...payload, board: payload.board.trim() }
}
