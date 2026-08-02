export type TriageAction = "backlog" | "due" | "reference" | "cancel"

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
