export interface TaskDueContext {
  slug: string
  line: number
  expectedText: string
  expectedStatus: string
  expectedDue: string
}

export interface TaskDuePayload extends TaskDueContext {
  due: string
}

export const taskDueSaveSelector = "[data-task-due-save]"
export const taskDueClearSelector = "[data-task-due-clear]"
export const taskDueEndpoint = "api/tasks/due"

export function buildTaskDuePayload(context: TaskDueContext, due: string): TaskDuePayload {
  if (!context.slug || !Number.isInteger(context.line) || context.line < 1 || !context.expectedStatus) {
    throw new Error("Task location is missing; refresh the page")
  }
  if (due !== "" && !/^\d{4}-\d{2}-\d{2}$/.test(due)) {
    throw new Error("Choose a valid due date")
  }
  return { ...context, due }
}
