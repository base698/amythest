import { createElement, useEffect, useRef, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import type { Card } from './BoardView.tsx'
import { boardLabel } from './boardOrganization.ts'

export interface FocusBoard {
  name: string
  displayName?: string
  icon?: string
  color?: string
  sortOrder?: number
  archived?: boolean
  focusCardId?: string
  cards: Card[]
}

export interface VaultTask {
  slug: string
  path: string
  line: number
  text: string
  status: string
  due?: string
  scheduled?: string
  start?: string
  recurrence?: string
  priority: number
  doneDate?: string
  tags?: string[]
  version: string
}

export interface TaskGroup {
  name: string
  tasks: VaultTask[]
}

export interface ReviewCard {
  board: FocusBoard
  card: Card
}

export interface FocusEditorIdentity {
  board: string
  cardId: string
}

export function focusEditorIdentity(editor: { board: string; card: Pick<Card, 'id'> }): FocusEditorIdentity {
  return { board: editor.board, cardId: editor.card.id }
}

export function focusEditorMatches(editor: { board: string; card: Pick<Card, 'id'> } | null, target: FocusEditorIdentity): boolean {
  return editor?.board === target.board && editor.card.id === target.cardId
}

export async function runFocusDataRefresh<T>({ request, isCurrent, apply }: {
  request: () => Promise<T>
  isCurrent: () => boolean
  apply: (value: T) => void
}): Promise<boolean> {
  const value = await request()
  if (!isCurrent()) return false
  apply(value)
  return true
}

export function isEditorOperationBusy(busy: boolean, attachmentBusy: string): boolean {
  return busy || attachmentBusy !== ''
}

export async function runFocusEditorMutation<T>({ target, request, current, applyIfCurrent, refresh }: {
  target: FocusEditorIdentity
  request: () => Promise<T>
  current: () => { board: string; card: Pick<Card, 'id'> } | null
  applyIfCurrent?: (value: T) => void
  refresh?: () => Promise<unknown>
}): Promise<T> {
  if (!focusEditorMatches(current(), target)) throw new Error('Focus editor is no longer current')
  const value = await request()
  if (focusEditorMatches(current(), target)) applyIfCurrent?.(value)
  await refresh?.()
  return value
}

export function collectReviewCards(boards: readonly FocusBoard[]): ReviewCard[] {
  return boards.flatMap((board) => board.cards
    .filter((card) => card.status === 'verify')
    .map((card) => ({ board, card })))
}

export function flattenOpenTasks(groups: readonly TaskGroup[]): VaultTask[] {
  return groups.flatMap((group) => group.tasks).filter((task) => task.status === 'open')
}

export function taskSourcePath(notesBase: string, slug: string): string {
  const base = notesBase.endsWith('/') ? notesBase : `${notesBase}/`
  return base + slug.split('/').map(encodeURIComponent).join('/')
}

export function isDialogCloseKey(event: Pick<KeyboardEvent, 'key'> | Pick<ReactKeyboardEvent, 'key'>): boolean {
  return event.key === 'Escape'
}

const DIALOG_FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

interface InertState {
  count: number
  hadAttribute: boolean
  ariaHidden: string | null
  propertySupported: boolean
  propertyValue: boolean
}

const inertStates = new WeakMap<HTMLElement, InertState>()
const dialogStacks = new WeakMap<Document, HTMLElement[]>()

export function isEffectivelyDisabled(element: HTMLElement): boolean {
  return (typeof element.matches === 'function' && element.matches(':disabled')) || element.hasAttribute('disabled') || element.getAttribute('aria-disabled') === 'true'
}

function inertDialogBackground(dialog: HTMLElement): () => void {
  const backdrop = dialog.closest?.('.modal-backdrop') as HTMLElement | null
  const parent = backdrop?.parentElement
  if (!backdrop || !parent) return () => undefined
  const background = Array.from(parent.children).filter((element) => element !== backdrop) as HTMLElement[]
  for (const element of background) {
    const existing = inertStates.get(element)
    if (existing) {
      existing.count++
      continue
    }
    const propertySupported = 'inert' in element
    inertStates.set(element, {
      count: 1,
      hadAttribute: element.hasAttribute('inert'),
      ariaHidden: element.getAttribute('aria-hidden'),
      propertySupported,
      propertyValue: propertySupported ? element.inert : false,
    })
    element.setAttribute('inert', '')
    element.setAttribute('aria-hidden', 'true')
    if (propertySupported) element.inert = true
  }
  return () => {
    for (const element of background) {
      const state = inertStates.get(element)
      if (!state) continue
      state.count--
      if (state.count > 0) continue
      if (!state.hadAttribute) element.removeAttribute('inert')
      if (state.ariaHidden === null) element.removeAttribute('aria-hidden')
      else element.setAttribute('aria-hidden', state.ariaHidden)
      if (state.propertySupported) element.inert = state.propertyValue
      inertStates.delete(element)
    }
  }
}

export function activateDialogFocus(dialog: HTMLElement, { onClose, closeDisabled = () => false }: {
  onClose: () => void
  closeDisabled?: () => boolean
}): () => void {
  const ownerDocument = dialog.ownerDocument
  const invoker = ownerDocument.activeElement as HTMLElement | null
  const releaseBackground = inertDialogBackground(dialog)
  const stack = dialogStacks.get(ownerDocument) || []
  stack.push(dialog)
  dialogStacks.set(ownerDocument, stack)
  const isTopDialog = () => stack[stack.length - 1] === dialog
  const focusable = () => Array.from(dialog.querySelectorAll<HTMLElement>(DIALOG_FOCUSABLE))
    .filter((element) => !isEffectivelyDisabled(element) && element.getAttribute('aria-hidden') !== 'true')
  if (!dialog.contains(ownerDocument.activeElement)) (focusable()[0] || dialog).focus()

  const handleKeyDown = (event: KeyboardEvent) => {
    if (!isTopDialog()) return
    if (isDialogCloseKey(event)) {
      if (!closeDisabled()) {
        event.preventDefault()
        onClose()
      }
      return
    }
    if (event.key !== 'Tab') return
    const controls = focusable()
    if (controls.length === 0) {
      event.preventDefault()
      dialog.focus()
      return
    }
    const first = controls[0]
    const last = controls[controls.length - 1]
    if (event.shiftKey && ownerDocument.activeElement === first) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && ownerDocument.activeElement === last) {
      event.preventDefault()
      first.focus()
    } else if (!dialog.contains(ownerDocument.activeElement)) {
      event.preventDefault()
      first.focus()
    }
  }
  const handleFocusIn = (event: FocusEvent) => {
    if (!isTopDialog() || dialog.contains(event.target as Node)) return
    ;(focusable()[0] || dialog).focus()
  }
  ownerDocument.addEventListener('keydown', handleKeyDown)
  ownerDocument.addEventListener('focusin', handleFocusIn)
  return () => {
    ownerDocument.removeEventListener('keydown', handleKeyDown)
    ownerDocument.removeEventListener('focusin', handleFocusIn)
    const stackIndex = stack.lastIndexOf(dialog)
    if (stackIndex !== -1) stack.splice(stackIndex, 1)
    if (stack.length === 0) dialogStacks.delete(ownerDocument)
    releaseBackground()
    const fallback = ownerDocument.querySelector?.<HTMLElement>('[data-dialog-restore]') || null
    const restoreTarget = invoker?.isConnected && !isEffectivelyDisabled(invoker) ? invoker : fallback
    if (restoreTarget?.isConnected && !isEffectivelyDisabled(restoreTarget)) restoreTarget.focus()
  }
}

export function useDialogFocus(onClose: () => void, closeDisabled = false) {
  const dialogRef = useRef<HTMLElement>(null)
  const onCloseRef = useRef(onClose)
  const closeDisabledRef = useRef(closeDisabled)
  onCloseRef.current = onClose
  closeDisabledRef.current = closeDisabled
  useEffect(() => {
    if (!dialogRef.current) return
    return activateDialogFocus(dialogRef.current, {
      onClose: () => onCloseRef.current(),
      closeDisabled: () => closeDisabledRef.current,
    })
  }, [])
  return dialogRef
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : 'request failed'
}

export function settleFocusData(
  summaries: readonly Pick<FocusBoard, 'name' | 'displayName'>[],
  boardResults: readonly PromiseSettledResult<FocusBoard>[],
  taskResult: PromiseSettledResult<TaskGroup[]>,
): { boards: FocusBoard[]; tasks: VaultTask[]; boardError: string; taskError: string } {
  const boards = boardResults.flatMap((result) => result.status === 'fulfilled' && !result.value.archived ? [result.value] : [])
  const failedBoards = boardResults.flatMap((result, index) => result.status === 'rejected' ? [boardLabel(summaries[index])] : [])
  return {
    boards,
    tasks: taskResult.status === 'fulfilled' ? flattenOpenTasks(taskResult.value) : [],
    boardError: failedBoards.length ? `Could not load focus data for: ${failedBoards.join(', ')}` : '',
    taskError: taskResult.status === 'rejected' ? `Could not load open tasks: ${errorMessage(taskResult.reason)}` : '',
  }
}

function cardMeta(card: Card) {
  const values = [(card.priority || 'p2').toUpperCase(), card.assignee || 'Unassigned']
  if (card.dueDate) values.push(`Due ${card.dueDate}`)
  return createElement('div', { className: 'focus-card-meta' }, values.map((value) => createElement('span', { key: value }, value)))
}

export function FocusReviewSection({ boards, busy = false, onOpen }: { boards: FocusBoard[]; busy?: boolean; onOpen: (board: string, card: Card) => void }) {
  const reviews = collectReviewCards(boards)
  return createElement('section', { className: 'focus-section review-section', 'aria-labelledby': 'focus-review-title' },
    createElement('div', { className: 'focus-section-heading' },
      createElement('div', null,
        createElement('p', { className: 'eyebrow' }, 'Ready for a decision'),
        createElement('h2', { id: 'focus-review-title' }, 'Review across boards'),
      ),
      createElement('span', { className: 'focus-section-count' }, String(reviews.length)),
    ),
    reviews.length === 0
      ? createElement('p', { className: 'muted' }, 'No cards are in Verify.')
      : createElement('div', { className: 'focus-review-list' }, reviews.map(({ board, card }) =>
          createElement('button', {
            className: 'focus-review-card',
            'data-focus-card': `${board.name}:${card.id}`,
            disabled: busy,
            key: `${board.name}:${card.id}`,
            onClick: () => onOpen(board.name, card),
            type: 'button',
          },
          createElement('span', { className: 'focus-board-name' }, `${board.icon || '▦'} ${boardLabel(board)}`),
          createElement('strong', null, `Open ${card.title}`),
          card.description ? createElement('span', { className: 'focus-review-description' }, card.description) : null,
          cardMeta(card),
          ),
        )),
  )
}

export function FocusTasksSection({ tasks, busy, error, onOpen }: { tasks: VaultTask[]; busy: boolean; error: string; onOpen: (task: VaultTask) => void }) {
  return createElement('section', { className: 'focus-section tasks-section', 'aria-labelledby': 'focus-tasks-title' },
    createElement('div', { className: 'focus-section-heading' },
      createElement('div', null,
        createElement('p', { className: 'eyebrow' }, 'From the vault'),
        createElement('h2', { id: 'focus-tasks-title' }, 'Open vault tasks'),
      ),
      createElement('span', { className: 'focus-section-count' }, busy ? '…' : String(tasks.length)),
    ),
    error ? createElement('div', { className: 'focus-inline-error', role: 'alert' }, error) : null,
    busy && tasks.length === 0
      ? createElement('div', { className: 'focus-section-loading', role: 'status' }, 'Loading open tasks…')
      : tasks.length === 0
        ? createElement('p', { className: 'muted' }, error ? 'Open tasks are unavailable.' : 'No open vault tasks.')
        : createElement('div', { className: 'focus-task-list' }, tasks.map((task) =>
            createElement('button', {
              className: 'focus-task-row',
              'data-focus-task': `${task.slug}:${task.line}`,
              disabled: busy,
              key: `${task.slug}:${task.line}`,
              onClick: () => onOpen(task),
              type: 'button',
            },
            createElement('span', { className: 'focus-task-check', 'aria-hidden': 'true' }, '☐'),
            createElement('span', { className: 'focus-task-main' },
              createElement('strong', null, task.text),
              createElement('small', null, `${task.path} · line ${task.line}`),
            ),
            task.due ? createElement('span', { className: 'focus-task-due' }, `Due ${task.due}`) : null,
            ),
          )),
  )
}

export function FocusTaskDialog({ task, notesBase, onClose }: { task: VaultTask; notesBase: string; onClose: () => void }) {
  const dialogRef = useDialogFocus(onClose)

  return createElement('div', { className: 'modal-backdrop', onMouseDown: (event: { target: EventTarget; currentTarget: EventTarget }) => { if (event.target === event.currentTarget) onClose() } },
    createElement('section', { className: 'modal focus-task-dialog', ref: dialogRef, role: 'dialog', tabIndex: -1, 'aria-modal': 'true', 'aria-labelledby': 'focus-task-dialog-title' },
      createElement('header', null,
        createElement('div', null,
          createElement('p', { className: 'eyebrow' }, `${task.path} · line ${task.line}`),
          createElement('h2', { id: 'focus-task-dialog-title' }, 'Task detail'),
        ),
        createElement('button', { 'aria-label': 'Close', className: 'icon-button', onClick: onClose, type: 'button' }, '×'),
      ),
      createElement('p', { className: 'focus-task-detail-text' }, task.text),
      createElement('dl', { className: 'focus-task-detail-meta' },
        createElement('div', null, createElement('dt', null, 'Status'), createElement('dd', null, task.status)),
        createElement('div', null, createElement('dt', null, 'Priority'), createElement('dd', null, String(task.priority))),
        task.due ? createElement('div', null, createElement('dt', null, 'Due'), createElement('dd', null, task.due)) : null,
        task.recurrence ? createElement('div', null, createElement('dt', null, 'Repeats'), createElement('dd', null, task.recurrence)) : null,
      ),
      createElement('div', { className: 'modal-actions' },
        createElement('button', { className: 'quiet', onClick: onClose, type: 'button' }, 'Close'),
        createElement('a', { className: 'primary button-link', href: taskSourcePath(notesBase, task.slug) }, 'Open source note'),
      ),
    ),
  )
}
