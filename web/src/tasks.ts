// Interactive task checkboxes: check/uncheck writes back to the vault file
// through /api/tasks/toggle (recurring tasks roll forward server-side),
// then the page refreshes in place to show the result.
import { refreshCurrent } from "./spa"
import { buildTaskTogglePayload } from "./taskToggle"
import {
  buildFileDispositionPayload,
  buildFileDispositionPrompt,
  buildFileHidePayload,
  buildTaskMovePayload,
  buildTriagePayload,
  type FileDispositionAction,
  type TriageAction,
} from "./taskTriage"
import {
  buildTaskDuePayload,
  taskDueClearSelector,
  taskDueEndpoint,
  taskDueSaveSelector,
} from "./taskDue"
import {
  buildCancelPayloads,
  buildPurgePayloads,
  taskCancelEndpoint,
  taskPurgeEndpoint,
  type SelectedTask,
} from "./taskDelete"
import { buildTaskPriorityPayload, taskPriorityEndpoint } from "./taskPriority"

let bound = false

// Select mode lives here, not in the DOM: refreshCurrent() morphs the whole
// document from a fresh server render, which would drop a body class.
let selecting = false

function applySelectMode() {
  document.body.classList.toggle("tasks-selecting", selecting)
  updateBulkBar()
}

export function setupTaskToggles() {
  // Runs on every SPA navigation (init), so re-assert the mode before the
  // bind guard returns.
  applySelectMode()
  if (bound) return
  bound = true

  document.addEventListener("change", (e) => {
    const box = e.target as HTMLInputElement
    if (box.matches?.("input.task-toggle")) {
      void toggle(box)
      return
    }
    if (box.matches?.("[data-task-select]")) updateBulkBar()
  })

  // <details> has no dismiss behaviour of its own: without this an opened
  // editor stays open when you click away or press Escape.
  document.addEventListener("keydown", (e) => {
    if ((e as KeyboardEvent).key === "Escape") closeTaskEditors()
  })

  document.addEventListener("click", (e) => {
    const target = e.target as Element
    closeTaskEditors(target.closest?.(taskEditorSelector) ?? null)
    const moveButton = target.closest?.<HTMLButtonElement>("[data-task-move-submit]")
    if (moveButton) {
      void moveTaskToBoard(moveButton)
      return
    }
    const prioButton = target.closest?.<HTMLButtonElement>("[data-task-prio-set]")
    if (prioButton) {
      void setPriority(prioButton)
      return
    }
    const cancelButton = target.closest?.<HTMLButtonElement>("[data-task-cancel]")
    if (cancelButton) {
      void cancelTask(cancelButton)
      return
    }
    const purgeButton = target.closest?.<HTMLButtonElement>("[data-task-purge]")
    if (purgeButton) {
      void purgeTask(purgeButton)
      return
    }
    if (target.closest?.("[data-task-bulk-cancel]")) {
      void bulkDelete("cancel")
      return
    }
    if (target.closest?.("[data-task-bulk-purge]")) {
      void bulkDelete("purge")
      return
    }
    if (target.closest?.("[data-task-select-mode]")) {
      selecting = !selecting
      if (!selecting) clearSelection()
      applySelectMode()
      return
    }
    if (target.closest?.("[data-task-select-none]")) {
      clearSelection()
      updateBulkBar()
      return
    }
    const button = target.closest?.<HTMLButtonElement>(`${taskDueSaveSelector}, ${taskDueClearSelector}`)
    if (!button) return
    void updateDueDate(button, button.matches(taskDueClearSelector))
  })
}

const taskEditorSelector = "details.task-due-editor, details.task-prio-editor, details.task-move-editor, details.task-actions"

// closeTaskEditors shuts every open task editor except the one being
// interacted with, so only one panel is ever open and clicking away dismisses.
function closeTaskEditors(keep: Element | null = null) {
  document.querySelectorAll<HTMLDetailsElement>(taskEditorSelector).forEach((el) => {
    if (el.open && el !== keep) el.open = false
  })
}

function selectedTasks(): SelectedTask[] {
  return [...document.querySelectorAll<HTMLInputElement>("[data-task-select]:checked")].map((box) => ({
    slug: box.dataset.slug || "",
    line: Number(box.dataset.line),
    text: box.dataset.text || "",
    status: box.dataset.status || "",
    version: box.dataset.version || "",
  }))
}

function clearSelection() {
  document.querySelectorAll<HTMLInputElement>("[data-task-select]:checked").forEach((box) => { box.checked = false })
}

function updateBulkBar() {
  const bar = document.querySelector<HTMLElement>("[data-task-bulkbar]")
  if (!bar) return
  const modeButton = bar.querySelector<HTMLButtonElement>("[data-task-select-mode]")
  if (modeButton) modeButton.textContent = selecting ? "Done" : "Select"
  if (!selecting) return
  const selected = selectedTasks()
  const open = selected.filter((t) => t.status === "open").length
  const cancelled = selected.filter((t) => t.status === "cancelled").length
  const count = bar.querySelector("[data-task-selcount]")
  if (count) count.textContent = `${selected.length} selected — ${open} open, ${cancelled} cancelled`
  const cancelButton = bar.querySelector<HTMLButtonElement>("[data-task-bulk-cancel]")
  if (cancelButton) cancelButton.disabled = open === 0
  const purgeButton = bar.querySelector<HTMLButtonElement>("[data-task-bulk-purge]")
  if (purgeButton) purgeButton.disabled = cancelled === 0
}

async function postTaskAction(endpoint: string, payload: unknown) {
  const res = await fetch(`${baseURL()}${endpoint}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    body: JSON.stringify(payload),
  })
  if (!res.ok) throw new Error((await res.text()) || `request failed (${res.status})`)
}

async function setPriority(button: HTMLButtonElement) {
  const editor = button.closest<HTMLElement>("[data-task-prio-editor]")
  if (!editor) return
  const controls = editor.querySelectorAll<HTMLButtonElement>("button")
  try {
    const payload = buildTaskPriorityPayload({
      slug: editor.dataset.slug || "",
      line: Number(editor.dataset.line),
      expectedText: editor.dataset.expectedText || "",
      expectedStatus: editor.dataset.expectedStatus || "",
      expectedPriority: Number(editor.dataset.expectedPriority),
      expectedVersion: editor.dataset.expectedVersion || "",
    }, Number(button.dataset.taskPrioSet))
    controls.forEach((el) => { el.disabled = true })
    await postTaskAction(taskPriorityEndpoint, payload)
    await refreshCurrent()
  } catch (err) {
    controls.forEach((el) => { el.disabled = false })
    showToast(err instanceof Error ? err.message : "Couldn't set the priority")
  }
}

async function cancelTask(button: HTMLButtonElement) {
  const text = button.dataset.text || "this task"
  if (!window.confirm(`Cancel “${text}”?\n\nIt is marked [-] cancelled with today's date; purge later to remove it.`)) return
  button.disabled = true
  try {
    const payloads = buildCancelPayloads([{
      slug: button.dataset.slug || "",
      line: Number(button.dataset.line),
      text: button.dataset.text || "",
      status: "open",
      version: button.dataset.version || "",
    }])
    await postTaskAction(taskCancelEndpoint, payloads[0])
    await refreshCurrent()
    updateBulkBar()
  } catch (err) {
    button.disabled = false
    showToast(err instanceof Error ? err.message : "Couldn't cancel the task")
  }
}

async function purgeTask(button: HTMLButtonElement) {
  if (!window.confirm("Permanently remove this cancelled task line from the note?\n\nIndented sub-items are left in place. This cannot be undone.")) return
  button.disabled = true
  try {
    const payloads = buildPurgePayloads([{
      slug: button.dataset.slug || "",
      line: Number(button.dataset.line),
      text: "",
      status: "cancelled",
      version: button.dataset.version || "",
    }])
    await postTaskAction(taskPurgeEndpoint, payloads[0])
    await refreshCurrent()
    updateBulkBar()
  } catch (err) {
    button.disabled = false
    showToast(err instanceof Error ? err.message : "Couldn't purge the task")
  }
}

async function bulkDelete(kind: "cancel" | "purge") {
  const selected = selectedTasks()
  let payloads: { slug: string }[]
  let count = 0
  try {
    if (kind === "cancel") {
      const built = buildCancelPayloads(selected)
      count = built.reduce((n, p) => n + p.items.length, 0)
      payloads = built
    } else {
      const built = buildPurgePayloads(selected)
      count = built.reduce((n, p) => n + p.lines.length, 0)
      payloads = built
    }
  } catch (err) {
    showToast(err instanceof Error ? err.message : "Selection is stale; refresh the page")
    return
  }
  if (count === 0) {
    showToast(kind === "cancel" ? "Select open tasks to cancel" : "Select cancelled tasks to purge")
    return
  }
  const files = payloads.length
  const prompt = kind === "cancel"
    ? `Cancel ${count} open task${count === 1 ? "" : "s"} across ${files} file${files === 1 ? "" : "s"}?\n\nThey are marked [-] cancelled with today's date; purge later to remove them.`
    : `Permanently remove ${count} cancelled task${count === 1 ? "" : "s"} from ${files} file${files === 1 ? "" : "s"}?\n\nIndented sub-items are left in place. This cannot be undone.`
  if (!window.confirm(prompt)) return

  const buttons = document.querySelectorAll<HTMLButtonElement>("[data-task-bulkbar] button")
  buttons.forEach((el) => { el.disabled = true })
  try {
    for (const payload of payloads) {
      await postTaskAction(kind === "cancel" ? taskCancelEndpoint : taskPurgeEndpoint, payload)
    }
    await refreshCurrent()
    updateBulkBar()
  } catch (err) {
    buttons.forEach((el) => { el.disabled = false })
    updateBulkBar()
    showToast(err instanceof Error ? err.message : `Couldn't ${kind} the selected tasks`)
  }
}

let triageBound = false

export function setupTaskTriage() {
  if (triageBound) return
  triageBound = true

  document.addEventListener("click", (e) => {
    const target = e.target as Element
    const hideButton = target.closest?.<HTMLButtonElement>("[data-task-triage] button[data-file-hide]")
    if (hideButton) {
      void hideFileFromTasks(hideButton)
      return
    }
    const dispositionButton = target.closest?.<HTMLButtonElement>("[data-task-triage] button[data-file-disposition]")
    if (dispositionButton) {
      void disposeFile(dispositionButton)
      return
    }
    const fileButton = target.closest?.<HTMLButtonElement>("[data-task-triage] button[data-file-action]")
    if (fileButton) {
      void triageFile(fileButton)
      return
    }
    const button = target.closest?.<HTMLButtonElement>("[data-task-triage] button[data-action]")
    if (!button) return
    void triage(button)
  })
  document.addEventListener("input", (e) => {
    const input = e.target as HTMLInputElement
    if (!input.matches?.("[data-triage-filter]")) return
    const query = input.value.trim().toLowerCase()
    document.querySelectorAll<HTMLElement>("[data-triage-file]").forEach((file) => {
      const searchable = file.dataset.search || file.textContent || ""
      file.hidden = query !== "" && !searchable.toLowerCase().includes(query)
    })
  })
}

async function hideFileFromTasks(button: HTMLButtonElement) {
  const panel = button.closest<HTMLElement>("[data-selected-file]")
  if (!panel) return
  const path = panel.dataset.filePath || "this file"
  if (!window.confirm(`Hide all tasks from “${path}”?\n\nAmythest will add tasks: false to the note. The note remains readable and searchable.`)) return

  const allButtons = document.querySelectorAll<HTMLButtonElement>("[data-task-triage] button")
  try {
    const payload = buildFileHidePayload(panel.dataset.fileSlug || "", panel.dataset.fileVersion || "")
    allButtons.forEach((el) => { el.disabled = true })
    const res = await fetch(`${baseURL()}api/tasks/file/hide`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify(payload),
    })
    if (!res.ok) throw new Error((await res.text()) || `file hide failed (${res.status})`)
    await refreshCurrent()
  } catch (err) {
    allButtons.forEach((el) => { el.disabled = false })
    showToast(err instanceof Error ? err.message : "Couldn't hide the file from tasks")
  }
}

async function disposeFile(button: HTMLButtonElement) {
  const panel = button.closest<HTMLElement>("[data-selected-file]")
  if (!panel) return
  const action = button.dataset.fileDisposition as FileDispositionAction
  const path = panel.dataset.filePath || "this file"
  const prompt = buildFileDispositionPrompt(path, action)
  if (!window.confirm(prompt)) return

  const allButtons = document.querySelectorAll<HTMLButtonElement>("[data-task-triage] button")
  try {
    const payload = buildFileDispositionPayload(
      panel.dataset.fileSlug || "",
      action,
      panel.dataset.fileVersion || "",
    )
    allButtons.forEach((el) => { el.disabled = true })
    const res = await fetch(`${baseURL()}api/tasks/file`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify(payload),
    })
    if (!res.ok) throw new Error((await res.text()) || `file move failed (${res.status})`)
    await refreshCurrent()
  } catch (err) {
    allButtons.forEach((el) => { el.disabled = false })
    showToast(err instanceof Error ? err.message : "Couldn't move the file")
  }
}

async function triageFile(button: HTMLButtonElement) {
  const action = button.dataset.fileAction as TriageAction
  const cards = [...document.querySelectorAll<HTMLElement>("[data-task-triage] [data-triage-card]")]
  if (cards.length === 0) return
  const prompt = action === "reference"
    ? `Convert all ${cards.length} checkboxes in this file to reference bullets?`
    : `Mark all ${cards.length} tasks in this file as intentional #backlog?`
  if (!window.confirm(prompt)) return

  const allButtons = document.querySelectorAll<HTMLButtonElement>("[data-task-triage] button")
  try {
    const built = cards.map((card) => buildTriagePayload({
      slug: card.dataset.slug || "",
      line: Number(card.dataset.line),
      expectedText: card.dataset.text || "",
    }, action))
    const slug = built[0].slug
    if (built.some((item) => item.slug !== slug)) throw new Error("File selection changed; refresh the page")
    const items = built.map(({ line, action: itemAction, expectedText }) => ({
      line,
      action: itemAction,
      expectedText,
    }))
    allButtons.forEach((el) => { el.disabled = true })
    const res = await fetch(`${baseURL()}api/tasks/triage`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({ slug, items }),
    })
    if (!res.ok) throw new Error((await res.text()) || `file triage failed (${res.status})`)
    await refreshCurrent()
  } catch (err) {
    allButtons.forEach((el) => { el.disabled = false })
    showToast(err instanceof Error ? err.message : "Couldn't triage the file")
  }
}

async function triage(button: HTMLButtonElement) {
  const card = button.closest<HTMLElement>("[data-triage-card]")
  if (!card) return
  const action = button.dataset.action as TriageAction
  if ((action === "reference" || action === "cancel") && !window.confirm(
    action === "reference"
      ? "Convert this checkbox to a normal reference bullet?"
      : "Cancel this task as obsolete?",
  )) return

  try {
    const due = card.querySelector<HTMLInputElement>("[data-triage-due]")?.value || ""
    const payload = buildTriagePayload({
      slug: card.dataset.slug || "",
      line: Number(card.dataset.line),
      expectedText: card.dataset.text || "",
    }, action, due)
    card.querySelectorAll<HTMLButtonElement>("button").forEach((el) => { el.disabled = true })
    const res = await fetch(`${baseURL()}api/tasks/triage`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify(payload),
    })
    if (!res.ok) throw new Error((await res.text()) || `triage failed (${res.status})`)
    await refreshCurrent()
  } catch (err) {
    card.querySelectorAll<HTMLButtonElement>("button").forEach((el) => { el.disabled = false })
    showToast(err instanceof Error ? err.message : "Couldn't triage the task")
  }
}

async function toggle(box: HTMLInputElement) {
  const slug = box.dataset.slug
  const line = Number(box.dataset.line)
  const done = box.checked
  box.disabled = true
  try {
    const payload = buildTaskTogglePayload(slug || "", line, box.dataset.version || "", done)
    const res = await fetch(`${baseURL()}api/tasks/toggle`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify(payload),
    })
    if (!res.ok) {
      throw new Error((await res.text()) || `toggle failed (${res.status})`)
    }
    await refreshCurrent()
  } catch (err) {
    box.checked = !done
    box.disabled = false
    showToast(err instanceof Error ? err.message : "Couldn't update the task")
    return
  }
  box.disabled = false
}

async function updateDueDate(button: HTMLButtonElement, clear: boolean) {
  const editor = button.closest<HTMLElement>("[data-task-due-editor]")
  const input = editor?.querySelector<HTMLInputElement>("[data-task-due-input]")
  if (!editor || !input) return
  if (clear && editor.dataset.expectedDue && !window.confirm("Clear this task's due date?")) return

  const controls = editor.querySelectorAll<HTMLInputElement | HTMLButtonElement>("input, button")
  try {
    const payload = buildTaskDuePayload({
      slug: editor.dataset.slug || "",
      line: Number(editor.dataset.line),
      expectedText: editor.dataset.expectedText || "",
      expectedStatus: editor.dataset.expectedStatus || "",
      expectedDue: editor.dataset.expectedDue || "",
      expectedVersion: editor.dataset.expectedVersion || "",
    }, clear ? "" : input.value)
    controls.forEach((el) => { el.disabled = true })
    const res = await fetch(`${baseURL()}${taskDueEndpoint}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify(payload),
    })
    if (!res.ok) throw new Error((await res.text()) || `due-date update failed (${res.status})`)
    await refreshCurrent()
  } catch (err) {
    controls.forEach((el) => { el.disabled = false })
    showToast(err instanceof Error ? err.message : "Couldn't update the due date")
  }
}

async function moveTaskToBoard(button: HTMLButtonElement) {
  const editor = button.closest<HTMLElement>("[data-task-move-editor]")
  const select = editor?.querySelector<HTMLSelectElement>("[data-task-board]")
  if (!editor || !select) return
  const board = select.value
  const task = editor.dataset.expectedText || "this task"
  if (!window.confirm(`Move “${task}” to the ${board} board?\n\nThe source checkbox will become a reference link to the new card.`)) return

  const controls = editor.querySelectorAll<HTMLSelectElement | HTMLButtonElement>("select, button")
  try {
    const payload = buildTaskMovePayload({
      board,
      slug: editor.dataset.slug || "",
      line: Number(editor.dataset.line),
      expectedText: editor.dataset.expectedText || "",
      expectedStatus: editor.dataset.expectedStatus || "",
      expectedVersion: editor.dataset.expectedVersion || "",
    })
    controls.forEach((el) => { el.disabled = true })
    const res = await fetch(`${baseURL()}api/tasks/move-to-board`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify(payload),
    })
    if (!res.ok) throw new Error((await res.text()) || `move to board failed (${res.status})`)
    await refreshCurrent()
  } catch (err) {
    controls.forEach((el) => { el.disabled = false })
    showToast(err instanceof Error ? err.message : "Couldn't move the task to a board")
  }
}

let toastTimer: number | undefined

function showToast(text: string) {
  document.getElementById("amythest-toast")?.remove()
  const el = document.createElement("div")
  el.id = "amythest-toast"
  el.textContent = text.trim()
  document.body.append(el)
  window.clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => el.remove(), 4000)
}

function baseURL(): string {
  const base = document.body.dataset.base || "/"
  return base.endsWith("/") ? base : base + "/"
}
