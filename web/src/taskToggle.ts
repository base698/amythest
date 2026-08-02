export type TaskTogglePayload = {
  slug: string
  line: number
  expectedVersion: string
  done: boolean
}

export function buildTaskTogglePayload(slug: string, line: number, expectedVersion: string, done: boolean): TaskTogglePayload {
  if (!slug || !Number.isInteger(line) || line < 1) throw new Error("Task location changed; refresh the page")
  if (!/^[0-9a-f]{64}$/.test(expectedVersion)) throw new Error("Task version missing; refresh the page")
  return { slug, line, expectedVersion, done }
}
