import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import {
  FocusReviewSection,
  FocusTaskDialog,
  FocusTasksSection,
  activateDialogFocus,
  collectReviewCards,
  flattenOpenTasks,
  focusEditorIdentity,
  isDialogCloseKey,
  isEditorOperationBusy,
  isEffectivelyDisabled,
  runFocusDataRefresh,
  runFocusEditorMutation,
  settleFocusData,
  taskSourcePath,
  type FocusBoard,
  type VaultTask,
} from './focusExperience.ts'

const card = (id: string, status: 'verify' | 'ready', title = id) => ({
  id, status, title, description: '', priority: 'p2' as const, labels: [], comments: [], createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z',
})

const boards: FocusBoard[] = [
  { name: 'alpha', displayName: 'Alpha', sortOrder: 1, archived: false, cards: [card('a1', 'verify'), card('a2', 'ready')] },
  { name: 'beta', displayName: 'Beta', sortOrder: 2, archived: false, cards: [card('b1', 'verify'), card('b2', 'verify')] },
]

const tasks: VaultTask[] = [
  { slug: 'Projects/Launch', path: 'Projects/Launch.md', line: 7, text: 'Ship <script>alert(1)</script>', status: 'open', priority: 1, version: 'a'.repeat(64) },
  { slug: 'Done', path: 'Done.md', line: 2, text: 'Finished', status: 'done', priority: 3, version: 'b'.repeat(64) },
]

test('collects every Verify card from every loaded active board', () => {
  assert.deepEqual(collectReviewCards(boards).map(({ board, card }) => `${board.name}:${card.id}`), [
    'alpha:a1', 'beta:b1', 'beta:b2',
  ])
})

test('flattens only open vault tasks from every API group', () => {
  assert.deepEqual(flattenOpenTasks([{ name: '', tasks }, { name: 'later', tasks: [{ ...tasks[0], slug: 'Later' }] }]).map((task) => task.slug), [
    'Projects/Launch', 'Later',
  ])
})

test('settles board and task loads independently and reports partial failures', () => {
  const result = settleFocusData(
    [{ name: 'alpha', displayName: 'Alpha' }, { name: 'beta', displayName: 'Beta' }],
    [{ status: 'fulfilled', value: boards[0] }, { status: 'rejected', reason: new Error('offline') }],
    { status: 'rejected', reason: new Error('tasks offline') },
  )
  assert.deepEqual(result.boards.map((board) => board.name), ['alpha'])
  assert.deepEqual(result.tasks, [])
  assert.equal(result.boardError, 'Could not load focus data for: Beta')
  assert.equal(result.taskError, 'Could not load open tasks: tasks offline')
})

test('review DOM exposes every card as an in-focus open action and escapes text', () => {
  const unsafeBoards = [{ ...boards[0], cards: [card('unsafe', 'verify', '<img src=x onerror=alert(1)>')] }]
  const html = renderToStaticMarkup(createElement(FocusReviewSection, { boards: unsafeBoards, onOpen: () => {} }))
  assert.match(html, /Review across boards/)
  assert.match(html, /Open &lt;img src=x onerror=alert\(1\)&gt;/)
  assert.doesNotMatch(html, /<img src=x/)
  assert.match(html, /data-focus-card="alpha:unsafe"/)
})

test('task list and detail DOM are safe and link to the encoded source note', () => {
  const open = flattenOpenTasks([{ name: '', tasks }])
  const list = renderToStaticMarkup(createElement(FocusTasksSection, { tasks: open, busy: false, error: '', onOpen: () => {} }))
  assert.match(list, /Open vault tasks/)
  assert.match(list, /Ship &lt;script&gt;alert\(1\)&lt;\/script&gt;/)
  assert.doesNotMatch(list, /<script>/)

  const detail = renderToStaticMarkup(createElement(FocusTaskDialog, { task: open[0], notesBase: '/notes/', onClose: () => {} }))
  assert.match(detail, /role="dialog"/)
  assert.match(detail, /href="\/notes\/Projects\/Launch"/)
  assert.match(detail, /Open source note/)
})

test('Focus card and task open actions fail closed while data refreshes', () => {
  const review = renderToStaticMarkup(createElement(FocusReviewSection, { boards, busy: true, onOpen: () => assert.fail('disabled action opened') }))
  const taskList = renderToStaticMarkup(createElement(FocusTasksSection, { tasks: [tasks[0]], busy: true, error: '', onOpen: () => assert.fail('disabled action opened') }))

  assert.match(review, /data-focus-card="alpha:a1"[^>]*disabled=""/)
  assert.match(taskList, /data-focus-task="Projects\/Launch:7"[^>]*disabled=""/)
})

test('a stale Focus editor closed by refresh cannot later PUT', async () => {
  let current: { board: string; card: ReturnType<typeof card> } | null = { board: 'alpha', card: card('a1', 'verify', 'Stale title') }
  const target = focusEditorIdentity(current)
  let puts = 0

  current = null // refresh fails closed before starting its data request
  await assert.rejects(runFocusEditorMutation({
    target,
    request: async () => { puts++; return undefined },
    current: () => current,
  }), /no longer current/)

  assert.equal(puts, 0)
})

test('source-note routes preserve a mount and encode each slug segment', () => {
  assert.equal(taskSourcePath('/notes/', 'Projects/R&D plan'), '/notes/Projects/R%26D%20plan')
})

test('Escape is the only dialog-close keyboard shortcut', () => {
  assert.equal(isDialogCloseKey({ key: 'Escape' }), true)
  assert.equal(isDialogCloseKey({ key: 'Enter' }), false)
})

test('a settled Focus mutation cannot update a different card opened later', async () => {
  let resolveRequest!: (value: ReturnType<typeof card>) => void
  const request = new Promise<ReturnType<typeof card>>((resolve) => { resolveRequest = resolve })
  let current = { board: 'alpha', card: card('a1', 'verify') }
  const target = focusEditorIdentity(current)
  let refreshes = 0

  const mutation = runFocusEditorMutation({
    target,
    request: () => request,
    current: () => current,
    applyIfCurrent: (updated) => { current = { ...current, card: updated } },
    refresh: async () => { refreshes++ },
  })

  current = { board: 'beta', card: card('b1', 'verify', 'Card B') }
  resolveRequest(card('a1', 'verify', 'Updated card A'))
  await mutation

  assert.equal(current.board, 'beta')
  assert.equal(current.card.id, 'b1')
  assert.equal(current.card.title, 'Card B')
  assert.equal(refreshes, 1, 'Focus data still refreshes without clearing the newer overlay')
})

test('a post-mutation Focus refresh neither owns nor invalidates history navigation', async () => {
  let resolveMutation!: () => void
  let navigationGeneration = 4
  let focusView = true
  let refreshRequests = 0
  let applied = ''
  const mutationRequest = new Promise<void>((resolve) => { resolveMutation = resolve })

  const mutation = runFocusEditorMutation({
    target: { board: 'alpha', cardId: 'a1' },
    request: () => mutationRequest,
    current: () => ({ board: 'alpha', card: { id: 'a1' } }),
    refresh: () => {
      if (!focusView) return Promise.resolve(false)
      const navigation = navigationGeneration
      return runFocusDataRefresh({
        request: async () => { refreshRequests++; return 'Focus payload' },
        isCurrent: () => focusView && navigation === navigationGeneration,
        apply: (value) => { applied = value },
      })
    },
  })

  navigationGeneration++ // browser Back/Forward starts while the mutation is in flight
  focusView = false
  resolveMutation()
  await mutation

  assert.equal(navigationGeneration, 5, 'history navigation remains the current request')
  assert.equal(refreshRequests, 0, 'post-mutation refresh does not run after leaving Focus')
  assert.equal(applied, '', 'refresh cannot overwrite the view after navigation')
})

test('Back from Focus reloads the retained board and invalidates a settling Focus refresh', async () => {
  const source = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8')
  const popstate = source.slice(source.indexOf('const handlePopState'), source.indexOf("window.addEventListener('popstate'"))
  const selectBoard = source.slice(source.indexOf('async function selectBoard'), source.indexOf('// Label filters live in the URL'))
  assert.match(popstate, /void selectBoard\(name, 'none'/, 'the retained board must reload even when its name is unchanged')
  assert.doesNotMatch(popstate, /if \(name !== activeBoard\)/)
  assert.match(selectBoard, /\+\+focusDataRequest\.current/, 'leaving Focus must invalidate a settling Focus refresh')

  let navigation = 8
  let focusRequest = 3
  let published = ''
  let settle!: (value: string) => void
  const refresh = runFocusDataRefresh({
    request: () => new Promise<string>((resolve) => { settle = resolve }),
    isCurrent: () => navigation === 8 && focusRequest === 3,
    apply: (value) => { published = value },
  })
  navigation++
  focusRequest++
  settle('stale Focus payload')
  assert.equal(await refresh, false)
  assert.equal(published, '', 'settling Focus data cannot publish over the board view')
})

test('editor operations are mutually exclusive with attachment work', () => {
  assert.equal(isEditorOperationBusy(false, ''), false)
  assert.equal(isEditorOperationBusy(true, ''), true)
  assert.equal(isEditorOperationBusy(false, 'upload'), true)
  assert.equal(isEditorOperationBusy(false, 'attachment-1'), true)
})

test('dialog focus enters, wraps in both directions, closes on Escape, and restores its invoker', () => {
  type FakeElement = { disabled?: boolean; isConnected: boolean; focus: () => void; hasAttribute: () => boolean; getAttribute: () => string | null }
  const listeners = new Map<string, (event: KeyboardEvent) => void>()
  const document = {
    activeElement: null as FakeElement | null,
    addEventListener: (type: string, listener: (event: KeyboardEvent) => void) => listeners.set(type, listener),
    removeEventListener: (type: string) => listeners.delete(type),
  }
  const element = (): FakeElement => ({
    isConnected: true,
    focus() { document.activeElement = this },
    hasAttribute: () => false,
    getAttribute: () => null,
  })
  const invoker = element()
  const first = element()
  const last = element()
  document.activeElement = invoker
  const dialog = {
    ownerDocument: document,
    contains: (candidate: FakeElement | null) => candidate === first || candidate === last,
    focus: () => undefined,
    querySelectorAll: () => [first, last],
  }
  let closes = 0
  const cleanup = activateDialogFocus(dialog as unknown as HTMLElement, { onClose: () => { closes++ } })
  const keydown = listeners.get('keydown')!
  const focusin = listeners.get('focusin')!
  assert.equal(document.activeElement, first, 'focus enters the dialog')

  document.activeElement = invoker
  focusin({ target: invoker } as unknown as KeyboardEvent)
  assert.equal(document.activeElement, first, 'programmatic background focus is redirected into the dialog')

  document.activeElement = last
  let prevented = false
  keydown({ key: 'Tab', shiftKey: false, preventDefault: () => { prevented = true } } as KeyboardEvent)
  assert.equal(prevented, true)
  assert.equal(document.activeElement, first, 'Tab wraps to the first control')

  document.activeElement = first
  keydown({ key: 'Tab', shiftKey: true, preventDefault: () => undefined } as KeyboardEvent)
  assert.equal(document.activeElement, last, 'Shift+Tab wraps to the last control')
  keydown({ key: 'Escape', shiftKey: false, preventDefault: () => undefined } as KeyboardEvent)
  assert.equal(closes, 1)

  cleanup()
  assert.equal(document.activeElement, invoker, 'focus returns to the control that opened the dialog')
})

test('dialog Escape is ignored while its mutation is busy', () => {
  const listeners = new Map<string, (event: KeyboardEvent) => void>()
  const control = { isConnected: true, focus: () => undefined, hasAttribute: () => false, getAttribute: () => null, matches: () => false }
  const document = {
    activeElement: control,
    addEventListener: (type: string, listener: (event: KeyboardEvent) => void) => listeners.set(type, listener),
    removeEventListener: (type: string) => listeners.delete(type),
  }
  const dialog = { ownerDocument: document, contains: () => true, focus: () => undefined, querySelectorAll: () => [control] }
  let closes = 0
  const cleanup = activateDialogFocus(dialog as unknown as HTMLElement, { onClose: () => { closes++ }, closeDisabled: () => true })
  listeners.get('keydown')!({ key: 'Escape', preventDefault: () => undefined } as KeyboardEvent)
  cleanup()
  assert.equal(closes, 0)
})

test('dialog focus excludes controls disabled by an ancestor fieldset', () => {
  const inheritedDisabled = {
    hasAttribute: () => false,
    getAttribute: () => null,
    matches: (selector: string) => selector === ':disabled',
  }
  assert.equal(isEffectivelyDisabled(inheritedDisabled as unknown as HTMLElement), true)
})

test('dialog cleanup restores focus to an enabled stable target when its invoker becomes disabled', () => {
  type FakeElement = { disabled?: boolean; isConnected: boolean; focus: () => void; hasAttribute: (name: string) => boolean; getAttribute: (name: string) => string | null; matches: (selector: string) => boolean }
  const invoker = {} as FakeElement
  const fallback = {} as FakeElement
  const document = {
    activeElement: null as FakeElement | null,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    querySelector: (selector: string) => selector === '[data-dialog-restore]' ? fallback : null,
  }
  const element = (target: FakeElement, disabled = false) => Object.assign(target, {
    disabled,
    isConnected: true,
    focus() { if (!this.disabled) document.activeElement = this },
    hasAttribute: (name: string) => name === 'disabled' && disabled,
    getAttribute: () => null,
    matches: (selector: string) => selector === ':disabled' && disabled,
  })
  element(invoker)
  element(fallback)
  const control = element({} as FakeElement)
  document.activeElement = invoker
  const dialog = { ownerDocument: document, contains: () => false, focus: () => undefined, querySelectorAll: () => [control], closest: () => null }
  const cleanup = activateDialogFocus(dialog as unknown as HTMLElement, { onClose: () => undefined })
  invoker.disabled = true
  invoker.hasAttribute = (name) => name === 'disabled'
  invoker.matches = (selector) => selector === ':disabled'
  cleanup()

  assert.equal(fallback.disabled, false)
  assert.equal(document.activeElement, fallback, 'focus lands on an enabled stable app target')
})

test('dialog background is inert while mounted and restores prior state across stacked dialogs', () => {
  type FakeNode = {
    inert?: boolean
    parentElement: FakeNode | null
    children: FakeNode[]
    attributes: Set<string>
    setAttribute: (name: string, value?: string) => void
    removeAttribute: (name: string) => void
    hasAttribute: (name: string) => boolean
    getAttribute: (name: string) => string | null
  }
  const node = (supportsInert = true): FakeNode => {
    const value = {
      parentElement: null,
      children: [],
      attributes: new Set<string>(),
      setAttribute(name: string) { this.attributes.add(name) },
      removeAttribute(name: string) { this.attributes.delete(name) },
      hasAttribute(name: string) { return this.attributes.has(name) },
      getAttribute(name: string) { return this.attributes.has(name) ? '' : null },
    } as FakeNode
    if (supportsInert) value.inert = false
    return value
  }
  const shell = node()
  const background = node(false)
  const firstBackdrop = node()
  const secondBackdrop = node()
  shell.children = [background, firstBackdrop]
  background.parentElement = firstBackdrop.parentElement = secondBackdrop.parentElement = shell
  const firstDialog = { ownerDocument: { activeElement: null, addEventListener: () => undefined, removeEventListener: () => undefined, querySelector: () => null }, contains: () => true, focus: () => undefined, querySelectorAll: () => [], closest: () => firstBackdrop }
  const firstCleanup = activateDialogFocus(firstDialog as unknown as HTMLElement, { onClose: () => undefined })
  assert.equal(background.hasAttribute('inert'), true, 'attribute fallback applies without an inert property')
  assert.equal(background.hasAttribute('aria-hidden'), true, 'assistive technology cannot traverse unsupported inert backgrounds')

  shell.children.push(secondBackdrop)
  const secondDialog = { ...firstDialog, closest: () => secondBackdrop }
  const secondCleanup = activateDialogFocus(secondDialog as unknown as HTMLElement, { onClose: () => undefined })
  assert.equal(firstBackdrop.hasAttribute('inert'), true, 'the underlying dialog is inert')
  firstCleanup()
  assert.equal(background.hasAttribute('inert'), true, 'closing the lower dialog cannot expose background behind the top dialog')
  secondCleanup()
  assert.equal(background.hasAttribute('inert'), false)
  assert.equal(background.hasAttribute('aria-hidden'), false)
  assert.equal(firstBackdrop.hasAttribute('inert'), false)
})

test('App wires identity-guarded Focus mutations to a data-only refresh path', () => {
  const source = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8')
  assert.match(source, /runFocusEditorMutation\(\{/)
  assert.match(source, /refresh: refreshFocusData/)
  assert.match(source, /focusEditorMatches\(current, target\)/)
})

test('App wires focus cards as an overlay without board URL navigation', () => {
  const source = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8')
  assert.match(source, /onOpen=\{\(name, card\) => \{ if \(!focusBusy && !busy\) setFocusEditor\(\{ board: name, card \}\) \}\}/)
  assert.match(source, /openOnBoardHref=\{`\$\{boardPath\(focusEditor\.board\)\}#card-/)
  assert.match(source, /useDialogFocus\(onClose, closeDisabled\)/)
  assert.doesNotMatch(source, /onOpen=\{async \(name, card\) => \{\s*if \(await selectBoard/)
})

test('App gives background Focus refreshes a token separate from navigation', () => {
  const source = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8')
  const refresh = source.slice(source.indexOf('async function refreshFocusData'), source.indexOf('async function loadFocusView'))
  assert.match(source, /const focusDataRequest = useRef\(0\)/)
  assert.match(source, /const focusModeRef = useRef\(focusMode\)/)
  assert.match(refresh, /if \(!focusModeRef\.current\) return false/)
  assert.match(refresh, /\+\+focusDataRequest\.current/)
  assert.match(refresh, /focusEditorRef\.current = null[\s\S]*setFocusEditor\(null\)/)
  assert.doesNotMatch(refresh, /\+\+navigationRequest\.current/)
})

test('Card editor disables every competing mutation control during attachment work', () => {
  const source = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8')
  const editor = source.slice(source.indexOf('function CardEditor'), source.indexOf('function displayName'))
  assert.match(editor, /const operationBusy = isEditorOperationBusy\(busy, attachmentBusy\)/)
  assert.match(editor, /fieldset className="editor-fields" disabled=\{readOnly \|\| operationBusy\}/)
  assert.match(editor, /textarea aria-label="Add comment" disabled=\{operationBusy\}/)
  assert.match(editor, /select aria-label="Destination board" disabled=\{operationBusy\}/)
  assert.match(editor, /danger-link" disabled=\{operationBusy\}/)
  const regularDescription = source.slice(source.indexOf("onSaveDescription={editor === 'new'"), source.indexOf("onComment={editor === 'new'"))
  assert.match(regularDescription, /setBusy\(true\); setError\(''\)/)
  assert.match(regularDescription, /finally \{ setBusy\(false\) \}/)
})
