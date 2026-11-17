import { createElement, useEffect, useRef, useState } from 'react'
import { searchCards } from './cardSearch.ts'
import { boardLabel } from './boardOrganization.ts'
import { FocusTasksSection, type FocusBoard, type VaultTask } from './focusExperience.ts'
import type { Card } from './BoardView.tsx'

export function searchFocusCards(boards: FocusBoard[], query: string) {
  return boards.flatMap(board => board.cards
    .filter(card => searchCards([{ ...card, labels: [...(card.labels ?? []), board.name, boardLabel(board)] }], query).length > 0)
    .map(card => ({ board, card })))
}

export function searchFocusTasks(tasks: VaultTask[], query: string): VaultTask[] {
  return tasks.filter(task => searchCards([{
    id: task.slug, title: task.text, description: task.path, labels: task.tags,
    milestone: task.due,
  }], query).length > 0)
}

export interface NoteSearchResult {
  slug: string
  title: string
  url?: string
  kind?: string
  archived?: boolean
}

export function noteSearchPath(base: string, note: NoteSearchResult): string {
  // Only relative links under the notes mount may come from search results.
  const target = note.url && !/^(?:[a-z]+:|\/|\\)/i.test(note.url) && !note.url.split(/[/?#]/).includes('..')
    ? note.url : note.slug.split('/').map(encodeURIComponent).join('/')
  return `${base.endsWith('/') ? base : base + '/'}${target}`
}

export async function searchNotes(base: string, query: string, signal: AbortSignal, request: typeof fetch = fetch, includeArchived = false): Promise<NoteSearchResult[]> {
  const response = await request(`${base.endsWith('/') ? base : base + '/'}api/search?kind=note&q=${encodeURIComponent(query)}${includeArchived ? '&include_archived=1' : ''}`, {
    credentials: 'same-origin', signal,
  })
  if (!response.ok) throw new Error(response.status === 401 ? 'Sign in again to search notes.' : 'Could not search notes. Try again.')
  return (await response.json()) ?? []
}

export function FocusSearch({ query, onChange, notesBase, boards, tasks, busy, taskError, onOpen, onOpenTask, onCompleteTask }: {
  query: string; onChange: (value: string) => void; notesBase: string; boards: FocusBoard[]; tasks: VaultTask[];
  busy: boolean; taskError: string; onOpen: (board: string, card: Card) => void;
  onOpenTask: (task: VaultTask) => void; onCompleteTask: (task: VaultTask) => void;
}) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [notes, setNotes] = useState<NoteSearchResult[]>([])
  const [noteError, setNoteError] = useState('')
  const [loading, setLoading] = useState(false)
  const [includeArchived, setIncludeArchived] = useState(false)
  const term = query.trim()
  const cards = term ? searchFocusCards(boards, term) : []
  const matchingTasks = term ? searchFocusTasks(tasks, term) : []

  useEffect(() => {
    function shortcut(event: KeyboardEvent) {
      if (document.querySelector('[role="dialog"]')) return
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        inputRef.current?.focus()
        inputRef.current?.select()
        inputRef.current?.scrollIntoView({ block: 'center' })
      }
    }
    document.addEventListener('keydown', shortcut)
    return () => document.removeEventListener('keydown', shortcut)
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    setNotes([])
    setNoteError('')
    setLoading(Boolean(term))
    if (!term) return () => controller.abort()
    const timer = setTimeout(() => {
      void searchNotes(notesBase, term, controller.signal, fetch, includeArchived).then(results => {
        if (!controller.signal.aborted) setNotes(results)
      }).catch(error => {
        if (!controller.signal.aborted) setNoteError(error instanceof Error ? error.message : 'Could not search notes.')
      }).finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    }, 150)
    return () => { clearTimeout(timer); controller.abort() }
  }, [notesBase, term, includeArchived])

  return createElement('div', { className: 'focus-search' },
    createElement('div', { className: 'card-search', role: 'search', 'aria-label': 'Search Focus' },
      createElement('label', null,
        createElement('span', { className: 'eyebrow' }, 'Search cards, tasks and notes'),
        createElement('input', {
          ref: inputRef, type: 'search', value: query, maxLength: 200,
          placeholder: 'Search cards, tasks and notes…', 'aria-label': 'Search cards, tasks and notes',
          'aria-describedby': 'focus-search-scope',
          onChange: (event: { target: HTMLInputElement }) => onChange(event.target.value),
          onKeyDown: (event: { key: string; preventDefault: () => void }) => { if (event.key === 'Escape') { event.preventDefault(); onChange('') } },
        }),
      ),
      createElement('kbd', { className: 'focus-search-shortcut' }, '⌘ / Ctrl K'),
      query ? createElement('button', { className: 'quiet', type: 'button', onClick: () => { onChange(''); inputRef.current?.focus() } }, 'Clear search') : null,
    ),
    createElement('p', { id: 'focus-search-scope', className: 'muted' }, 'Cards across active boards, open vault tasks, and notes.'),
    term ? createElement('div', { className: 'focus-search-results' },
      createElement('p', { role: 'status', className: 'muted' }, `${cards.length} card${cards.length === 1 ? '' : 's'} · ${matchingTasks.length} task${matchingTasks.length === 1 ? '' : 's'}${loading ? ' · Searching notes…' : ` · ${notes.length} note${notes.length === 1 ? '' : 's'}`}`),
      createElement('section', { className: 'focus-section', 'aria-label': 'Matching cards' },
        createElement('h2', null, 'Cards'),
        cards.length ? createElement('div', { className: 'focus-review-list' }, cards.map(({ board, card }) =>
          createElement('button', { key: `${board.name}:${card.id}`, className: 'focus-review-card', type: 'button', disabled: busy, onClick: () => onOpen(board.name, card) },
            createElement('span', { className: 'focus-board-name' }, boardLabel(board)),
            createElement('strong', null, card.title),
            card.description ? createElement('span', { className: 'focus-review-description' }, card.description) : null,
          ),
        )) : createElement('p', { className: 'muted' }, busy ? 'Loading cards…' : 'No matching cards.'),
      ),
      createElement(FocusTasksSection, { tasks: matchingTasks, busy, error: taskError, onOpen: onOpenTask, onComplete: onCompleteTask, emptyMessage: 'No matching open tasks.' }),
      createElement('section', { className: 'focus-section', 'aria-label': 'Matching notes' },
        createElement('div', { className: 'focus-section-heading' },
          createElement('h2', null, 'Notes'),
          createElement('label', { className: 'focus-search-archived' },
            createElement('input', { type: 'checkbox', checked: includeArchived, onChange: (event: { target: HTMLInputElement }) => setIncludeArchived(event.target.checked) }),
            'Include archived notes',
          ),
        ),
        noteError ? createElement('p', { className: 'focus-inline-error', role: 'alert' }, noteError) : null,
        loading ? createElement('p', { className: 'muted' }, 'Searching notes…') : notes.length
          ? createElement('div', { className: 'focus-note-results' }, notes.map((note, index) => createElement('a', { key: `${note.slug}:${index}`, href: noteSearchPath(notesBase, note), className: 'focus-review-card' },
              createElement('strong', null, note.title, note.archived ? createElement('span', { className: 'archived-board-badge' }, 'Archived') : null), createElement('span', { className: 'focus-review-description' }, [note.kind, note.slug].filter(Boolean).join(' · ')),
            )))
          : !noteError ? createElement('p', { className: 'muted' }, 'No matching notes.') : null,
      ),
    ) : null,
  )
}
