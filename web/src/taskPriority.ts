// Payload builder for tapping a task's priority badge to change its level.

const versionRe = /^[0-9a-f]{64}$/

export const taskPriorityEndpoint = "api/tasks/priority"

export interface PriorityContext {
  slug: string
  line: number
  expectedText: string
  expectedStatus: string
  expectedPriority: number
  expectedVersion: string
}

export interface PriorityPayload extends PriorityContext {
  priority: number
}

export function buildTaskPriorityPayload(ctx: PriorityContext, priority: number): PriorityPayload {
  if (!ctx.slug) throw new Error("This task is missing its source; refresh the page")
  if (!Number.isInteger(ctx.line) || ctx.line < 1) throw new Error("This task has an invalid line; refresh the page")
  if (!ctx.expectedStatus) throw new Error("This task is missing its status; refresh the page")
  if (!versionRe.test(ctx.expectedVersion)) throw new Error("This task list is stale; refresh the page")
  for (const value of [priority, ctx.expectedPriority]) {
    if (!Number.isInteger(value) || value < 0 || value > 5) {
      throw new Error("Priority must be between 0 and 5")
    }
  }
  return { ...ctx, priority }
}
