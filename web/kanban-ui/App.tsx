import { FormEvent, useEffect, useState } from 'react'
import { BoardView, type Attachment, type Card, type Status } from './BoardView'
import {
  allLabelOptions,
  boardFilterURL,
  buildLabelOptions,
  filterCardsByLabels,
  filterSettingsFromSearch,
  type FilterMode,
} from './labels'
import { searchCards } from './cardSearch'
import { Markdown } from './MarkdownView'
import { toggleTaskLine } from './markdown'
import { KANBAN_MOUNT, NOTES_BASE, cardIdFromHash } from './mount.ts'
import './styles.css'

interface Session { user: string; csrf: string }
interface BoardSummary { name: string; counts: Partial<Record<Status, number>> }
interface BoardData { version: number; name: string; dispatchEnabled: boolean; cards: Card[] }
interface DispatchRun { id: number; board: string; cardId: string; cardTitle: string; assignee: string; agent?: string; status: string; pid?: number; startedAt: string; outcome?: string; summary?: string; error?: string; cancelRequestedAt?: string }

interface AgentCatalog { agents: string[]; default: string }

const ACTIVE_RUN_STATUSES = ['starting', 'running']

const NO_AGENTS: AgentCatalog = { agents: [], default: '' }

// Describes what happens when a card names no agent: the assignee picks one if
// it matches a configured agent, otherwise the catalog default runs.
function defaultAgentLabel(catalog: AgentCatalog, assignee: string): string {
  const matched = catalog.agents.find((name) => name.toLowerCase() === assignee.trim().toLowerCase())
  if (matched) return `Default — from assignee (${matched})`
  return catalog.default ? `Default (${catalog.default})` : 'Default'
}

const statusOptions: Array<{ value: Status; label: string }> = [
  { value: 'triage', label: 'Triage' }, { value: 'backlog', label: 'Backlog' },
  { value: 'ready', label: 'Ready' }, { value: 'in_progress', label: 'In progress' },
  { value: 'verify', label: 'Verify' }, { value: 'done', label: 'Done — archive' },
]

const API_BASE = `${KANBAN_MOUNT}/api`

async function api<T>(url: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(url, { credentials: 'same-origin', ...options })
  if (!response.ok) {
    // Error bodies are usually {error}, but proxies and empty 401s can
    // return non-JSON; those must not surface as JSON parse errors.
    let message = `Request failed (${response.status})`
    try {
      const value = await response.json()
      if (value?.error) message = value.error
    } catch {
      // keep the status-based message
    }
    throw new Error(message)
  }
  return (response.status === 204 ? null : await response.json()) as T
}

type Navigate = (path: string) => void
type HistoryMode = 'push' | 'replace' | 'none'

export function App({ navigate = (path) => window.location.assign(path) }: { navigate?: Navigate } = {}) {
  const [session, setSession] = useState<Session | null>()
  const [boards, setBoards] = useState<BoardSummary[]>([])
  const [activeBoard, setActiveBoard] = useState(() => boardNameFromPath(window.location.pathname) || 'proof')
  const [activeLabels, setActiveLabels] = useState<string[]>(() => labelsFromPath(window.location.pathname))
  const [filterMode, setFilterMode] = useState<FilterMode>(() => filterSettingsFromSearch(window.location.search).mode)
  const [excludedLabels, setExcludedLabels] = useState<string[]>(() => filterSettingsFromSearch(window.location.search).excluded)
  const [board, setBoard] = useState<BoardData | null>(null)
  const [editor, setEditor] = useState<Card | 'new' | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [jobsOpen, setJobsOpen] = useState(false)
  const [archiveOpen, setArchiveOpen] = useState(false)
  const [newBoardOpen, setNewBoardOpen] = useState(false)
  const [agents, setAgents] = useState<AgentCatalog>(NO_AGENTS)
  const [cardQuery, setCardQuery] = useState('')
  // A #card-<id> fragment (from the reference link a task move leaves in the
  // source note) opens that card once its board has loaded.
  const [pendingCardId, setPendingCardId] = useState('')

  useEffect(() => {
    api<Session>(`${API_BASE}/session`).then((value) => {
	  const next = safeNextPath(window.location.search)
	  if (next) {
		navigate(next)
		return
	  }
      setSession(value)
      // The dispatcher was retired and Amythest serves no /dispatch routes,
      // so asking only produced a 404 in the console on every sign-in. The
      // dispatch UI below is gated on a non-empty catalog and stays hidden;
      // restore this fetch if dispatch ever comes back.
      setAgents(NO_AGENTS)
      // A transient board-list failure must not read as "logged out":
      // keep the session and surface it in the error banner instead.
      void loadBoards(value).catch((err) => setError(err instanceof Error ? err.message : 'Failed to load boards'))
      setPendingCardId(cardIdFromHash(window.location.hash))
    }).catch(() => setSession(null))
  }, [])

  // Open the card named by #card-<id> once its board is loaded. Clearing the
  // id afterwards keeps a later manual close from reopening it.
  useEffect(() => {
    if (!pendingCardId || !board) return
    const card = board.cards.find((item) => item.id === pendingCardId)
    if (card) setEditor(card)
    else setError(`Card ${pendingCardId} is not on this board — it may have been archived or moved.`)
    setPendingCardId('')
  }, [board, pendingCardId])

  useEffect(() => {
    if (!session || boards.length === 0) return
    const handlePopState = () => {
      const name = boardNameFromPath(window.location.pathname)
      if (!name || !boards.some((item) => item.name === name)) return
      // Back/forward must restore the label filter as well as the board.
      setActiveLabels(labelsFromPath(window.location.pathname))
      const settings = filterSettingsFromSearch(window.location.search)
      setFilterMode(settings.mode)
      setExcludedLabels(settings.excluded)
      if (name !== activeBoard) void selectBoard(name, 'none', labelsFromPath(window.location.pathname), settings)
    }
    window.addEventListener('popstate', handlePopState)
    return () => window.removeEventListener('popstate', handlePopState)
  }, [session, boards, activeBoard])

  async function loadBoards(currentSession = session) {
    if (!currentSession) return
    const values = await api<BoardSummary[]>(`${API_BASE}/boards`)
    setBoards(values)
    const requested = boardNameFromPath(window.location.pathname)
    const matched = Boolean(requested) && values.some((item) => item.name === requested)
    const name = matched
      ? (requested as string)
      : values.some((item) => item.name === activeBoard) ? activeBoard : values[0]?.name
    // Only honour a label filter that came in on a URL naming a real board.
    if (name) await selectBoard(
      name,
      'replace',
      matched ? labelsFromPath(window.location.pathname) : [],
      matched ? filterSettingsFromSearch(window.location.search) : { mode: 'or', excluded: [] },
    )
  }

  async function selectBoard(
    name: string,
    historyMode: HistoryMode = 'push',
    labels: string[] = activeLabels,
    settings: { mode: FilterMode; excluded: string[] } = { mode: filterMode, excluded: excludedLabels },
  ) {
    setError('')
    const value = await api<BoardData>(`${API_BASE}/boards/${name}`)
    if (name !== activeBoard) setCardQuery('')
    setActiveBoard(name)
    setActiveLabels(labels)
    setFilterMode(settings.mode)
    setExcludedLabels(settings.excluded)
    setBoard(value)
    const path = boardFilterURL(name, labels, settings.mode, settings.excluded)
    if (historyMode !== 'none' && `${window.location.pathname}${window.location.search}` !== path) {
      window.history[historyMode === 'replace' ? 'replaceState' : 'pushState']({}, '', path)
    }
  }

  // Label filters live in the URL so a filtered board is linkable and survives
  // reload, back, and forward.
  function applyLabelFilter(labels: string[], mode = filterMode, excluded = excludedLabels) {
    setActiveLabels(labels)
    setFilterMode(mode)
    setExcludedLabels(excluded)
    const path = boardFilterURL(activeBoard, labels, mode, excluded)
    if (`${window.location.pathname}${window.location.search}` !== path) window.history.pushState({}, '', path)
  }

  function toggleIncludedLabel(label: string) {
    const labels = activeLabels.includes(label)
      ? activeLabels.filter((item) => item !== label)
      : [...activeLabels, label]
    applyLabelFilter(labels, filterMode, excludedLabels.filter((item) => item !== label))
  }

  function toggleExcludedLabel(label: string) {
    const excluded = excludedLabels.includes(label)
      ? excludedLabels.filter((item) => item !== label)
      : [...excludedLabels, label]
    applyLabelFilter(activeLabels.filter((item) => item !== label), filterMode, excluded)
  }

  async function mutate<T>(url: string, method: string, body?: unknown): Promise<T> {
    if (!session) throw new Error('Authentication required')
    return api<T>(url, {
      method,
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': session.csrf },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  }

  async function refresh() {
    await selectBoard(activeBoard, 'none', activeLabels, { mode: filterMode, excluded: excludedLabels })
    await loadBoardCounts()
  }
  async function loadBoardCounts() { setBoards(await api<BoardSummary[]>(`${API_BASE}/boards`)) }

  // Toggled straight from the card so blocking something is a single click,
  // without opening the editor.
  async function setCardBlocked(card: Card, blocked: boolean) {
    try {
      await mutate(`${API_BASE}/boards/${activeBoard}/cards/${card.id}`, 'PUT', { blocked })
      await refresh()
    } catch (caught) { setError(message(caught)) }
  }

  async function moveCard(card: Card, status: Status, beforeId?: string) {
    if (status === 'done' && !window.confirm(`Archive “${card.title}” as done?`)) return
    try {
      await mutate(`${API_BASE}/boards/${activeBoard}/cards/${card.id}/move`, 'POST', { status, beforeId })
      await refresh()
    } catch (caught) { setError(message(caught)) }
  }

  async function logout() {
    try { await mutate(`${API_BASE}/logout`, 'POST') } finally { setSession(null); setBoard(null); setBoards([]) }
  }

  if (session === undefined) return <div className="center-screen"><div className="spinner" aria-label="Loading" /></div>
  if (session === null) return <Login onLogin={async (value) => {
	const next = safeNextPath(window.location.search)
	if (next) {
	  navigate(next)
	  return
	}
	setSession(value)
	await loadBoards(value)
  }} />

  const labelFilteredCards = board
    ? filterCardsByLabels(board.cards, activeLabels, filterMode, excludedLabels)
    : []
  const visibleCards = searchCards(labelFilteredCards, cardQuery)

  return (
    <div className="app-shell">
      <header className="topbar">
        <div className="brand"><span className="brand-mark">A</span><span>Amythest Kanban</span></div>
        <nav className="board-tabs" aria-label="Boards">
          {boards.map((item) => (
            <button className={item.name === activeBoard ? 'active' : ''} key={item.name} onClick={() => selectBoard(item.name, 'push', [], { mode: 'or', excluded: [] })} type="button">
              {displayName(item.name)} <span>{Object.values(item.counts).reduce((sum, count) => sum + (count || 0), 0)}</span>
            </button>
          ))}
        </nav>
        <div className="top-actions">
          <button className="quiet" onClick={() => setNewBoardOpen(true)} type="button">New board</button>
          <a href={NOTES_BASE}>Notes</a>
          <button className="quiet" onClick={logout} type="button">Sign out</button>
        </div>
      </header>
      <main>
        <div className="board-toolbar">
          <div><p className="eyebrow">Board</p><h1>{displayName(activeBoard)}</h1></div>
          <div className="toolbar-actions">
            {board && agents.agents.length > 0 && <label className="dispatch-toggle"><input aria-label="Dispatch Ready cards assigned to an agent" type="checkbox" checked={board.dispatchEnabled} onChange={async (event) => { try { setBoard(await mutate<BoardData>(`${API_BASE}/boards/${activeBoard}/settings`, 'PUT', { dispatchEnabled: event.target.checked })) } catch (caught) { setError(message(caught)) } }} /> Dispatch Ready cards assigned to an agent <small>{board.dispatchEnabled ? 'Eligible Ready cards may launch.' : 'Dispatch is off; no cards will launch.'}</small></label>}
            <button className="quiet" onClick={() => setArchiveOpen(true)} type="button">Archive</button>
            {agents.agents.length > 0 && <button className="quiet" onClick={() => setJobsOpen(true)} type="button">Jobs</button>}
            <button className="quiet" onClick={refresh} type="button">Refresh</button>
            <button className="primary new-card-button" onClick={() => setEditor('new')} type="button"><span aria-hidden="true">＋</span> New card</button>
          </div>
        </div>
        {board && <CardSearch
          query={cardQuery}
          visible={visibleCards.length}
          total={labelFilteredCards.length}
          onChange={setCardQuery}
        />}
        {board && <LabelFilter
          board={board}
          active={activeLabels}
          excluded={excludedLabels}
          mode={filterMode}
          onToggleInclude={toggleIncludedLabel}
          onToggleExclude={toggleExcludedLabel}
          onModeChange={(next) => applyLabelFilter(activeLabels, next, excludedLabels)}
          onClear={() => applyLabelFilter([], 'or', [])}
        />}
        {error && <div className="error-banner" role="alert">{error}<button onClick={() => setError('')} type="button">Dismiss</button></div>}
        {board ? <BoardView cards={visibleCards} onMove={moveCard} onOpen={setEditor} onBlockedChange={setCardBlocked} /> : <div className="center-screen"><div className="spinner" /></div>}
      </main>
      <button aria-label="New card" className="fab-new-card" onClick={() => setEditor('new')} type="button">＋</button>
      {archiveOpen && <ArchivePanel board={activeBoard} onClose={() => setArchiveOpen(false)} onRestore={async (card) => {
        await mutate(`${API_BASE}/boards/${activeBoard}/archive/${card.id}/restore`, 'POST', { status: 'triage' })
        await refresh()
      }} />}
      {jobsOpen && <JobsPanel board={activeBoard} onClose={() => setJobsOpen(false)} onCancel={(id) => mutate<DispatchRun>(`${API_BASE}/dispatch/runs/${id}/cancel`, 'POST')} />}
      {newBoardOpen && <NewBoardDialog onClose={() => setNewBoardOpen(false)} onCreate={async (name) => {
        const created = await mutate<BoardData>(`${API_BASE}/boards`, 'POST', { name })
        setBoards((current) => [...current, { name: created.name, counts: {} }].sort((left, right) => left.name.localeCompare(right.name)))
        setActiveBoard(created.name)
        setBoard(created)
        setEditor(null)
        setArchiveOpen(false)
        setJobsOpen(false)
        window.history.pushState({}, '', boardPath(created.name))
        setNewBoardOpen(false)
      }} />}
      {editor && (
        <CardEditor
          board={activeBoard}
          card={editor === 'new' ? undefined : editor}
          busy={busy}
          agents={agents}
          onClose={() => setEditor(null)}
          onSave={async (input) => {
            setBusy(true); setError('')
            try {
              if (editor === 'new') await mutate(`${API_BASE}/boards/${activeBoard}/cards`, 'POST', input)
              else await mutate(`${API_BASE}/boards/${activeBoard}/cards/${editor.id}`, 'PUT', input)
              setEditor(null); await refresh()
            } catch (caught) { setError(message(caught)) } finally { setBusy(false) }
          }}
          onDelete={editor === 'new' ? undefined : async () => {
            if (!window.confirm(`Permanently delete “${editor.title}”? This cannot be undone.`)) return
            setBusy(true); setError('')
            try {
              await mutate(`${API_BASE}/boards/${activeBoard}/cards/${editor.id}`, 'DELETE')
              setEditor(null); await refresh()
            } catch (caught) { setError(message(caught)) } finally { setBusy(false) }
          }}
          onSaveDescription={editor === 'new' ? undefined : async (description) => {
            try {
              await mutate(`${API_BASE}/boards/${activeBoard}/cards/${editor.id}`, 'PUT', { description })
              await refresh()
            } catch (caught) { setError(message(caught)) }
          }}
          onComment={editor === 'new' ? undefined : async (body) => {
            setBusy(true)
            try {
              const updated = await mutate<Card>(`${API_BASE}/boards/${activeBoard}/cards/${editor.id}/comments`, 'POST', { body })
              setEditor(updated); await refresh()
            } catch (caught) { setError(message(caught)) } finally { setBusy(false) }
          }}
          onUpload={editor === 'new' ? undefined : async (file) => {
            if (!session) throw new Error('Authentication required')
            const body = new FormData()
            body.append('file', file)
            return api<Attachment>(`${API_BASE}/boards/${activeBoard}/cards/${editor.id}/attachments`, {
              method: 'POST', headers: { 'X-CSRF-Token': session.csrf }, body,
            })
          }}
          onDeleteAttachment={editor === 'new' ? undefined : async (attachment) => {
            await mutate(`${API_BASE}/boards/${activeBoard}/cards/${editor.id}/attachments/${attachment.id}`, 'DELETE')
          }}
          moveBoards={boards.filter((item) => item.name !== activeBoard)}
          onMoveBoard={editor === 'new' ? undefined : async (destinationBoard) => {
            if (!window.confirm(`Move “${editor.title}” from ${displayName(activeBoard)} to ${displayName(destinationBoard)}?`)) return
            setBusy(true); setError('')
            try {
              await mutate(`${API_BASE}/boards/${activeBoard}/cards/${editor.id}/board`, 'POST', { destinationBoard, confirm: true })
              setEditor(null)
              await refresh()
              setBoards(await api<BoardSummary[]>(`${API_BASE}/boards`))
            } catch (caught) { setError(message(caught)) } finally { setBusy(false) }
          }}
        />
      )}
    </div>
  )
}

function CardSearch({ query, visible, total, onChange }: {
  query: string
  visible: number
  total: number
  onChange: (query: string) => void
}) {
  const searching = query.trim().length > 0
  return <div className="card-search">
    <label>
      <span className="eyebrow">Search cards</span>
      <input
        aria-label="Search cards"
        maxLength={200}
        onChange={(event) => onChange(event.target.value)}
        placeholder="Title, description, assignee, label, milestone…"
        type="search"
        value={query}
      />
    </label>
    {searching && <span className={`card-search-result${visible === 0 ? ' empty' : ''}`} role="status">
      {visible} of {total} card{total === 1 ? '' : 's'}
    </span>}
    {searching && <button className="quiet" onClick={() => onChange('')} type="button">Clear search</button>}
  </div>
}

function LabelFilter({ board, active, excluded, mode, onToggleInclude, onToggleExclude, onModeChange, onClear }: {
  board: BoardData
  active: string[]
  excluded: string[]
  mode: FilterMode
  onToggleInclude: (label: string) => void
  onToggleExclude: (label: string) => void
  onModeChange: (mode: FilterMode) => void
  onClear: () => void
}) {
  const [expanded, setExpanded] = useState(false)
  const [query, setQuery] = useState('')
  const allOptions = allLabelOptions(board.cards)
  const totalLabels = allOptions.length
  const visible = expanded || query
    ? buildLabelOptions(board.cards, active, excluded, Number.MAX_SAFE_INTEGER)
    : buildLabelOptions(board.cards, active, excluded, 12)
  const needle = query.trim().toLowerCase()
  const options = needle ? visible.filter((option) => option.label.includes(needle)) : visible
  if (totalLabels === 0 && active.length === 0 && excluded.length === 0) return null
  const matches = filterCardsByLabels(board.cards, active, mode, excluded).length
  const filtering = active.length > 0 || excluded.length > 0
  return <section className="label-filter" aria-label="Card label filters">
    <div className="label-filter-heading">
      <div>
        <span className="eyebrow">Labels</span>
        <span className="muted label-summary">Useful labels first · {totalLabels} total</span>
      </div>
      <div className="label-filter-actions">
        {active.length > 1 && <div className="filter-mode" aria-label="Label match mode" role="group">
          <button aria-pressed={mode === 'or'} onClick={() => onModeChange('or')} type="button">Any</button>
          <button aria-pressed={mode === 'and'} onClick={() => onModeChange('and')} type="button">All</button>
        </div>}
        {totalLabels > 12 && <button className="quiet" onClick={() => { setExpanded((current) => !current); setQuery('') }} type="button">{expanded ? 'Show top labels' : `More labels (${totalLabels - 12})`}</button>}
        {filtering && <button className="quiet" onClick={onClear} type="button">Clear filter</button>}
      </div>
    </div>
    {(expanded || query) && <input aria-label="Search labels" className="label-search" maxLength={32} placeholder="Search labels…" type="search" value={query} onChange={(event) => setQuery(event.target.value)} />}
    <div className="label-options">
      {options.map(({ label, count }) => {
        const selected = active.includes(label)
        const omitted = excluded.includes(label)
        return <span className={`label-option${selected ? ' included' : ''}${omitted ? ' excluded' : ''}`} key={label}>
          <button
            aria-label={`Include ${label} (${count} ${count === 1 ? 'card' : 'cards'})`}
            aria-pressed={selected}
            className="label-main"
            onClick={() => onToggleInclude(label)}
            type="button"
          >{label} <strong>{count}</strong></button>
          <button aria-label={`Exclude ${label}`} aria-pressed={omitted} className="label-exclude" onClick={() => onToggleExclude(label)} title={`Exclude ${label}`} type="button">−</button>
        </span>
      })}
      {options.length === 0 && <span className="muted">No labels match “{query}”.</span>}
    </div>
    {filtering && <div className={`filter-result${matches === 0 ? ' empty' : ''}`} role="status">
      {matches === 0 ? 'No cards match this filter.' : `${matches} of ${board.cards.length} cards`}
      {active.length > 0 && <span> Included ({mode === 'or' ? 'any' : 'all'}): {active.join(', ')}</span>}
      {excluded.length > 0 && <span> Excluded: {excluded.join(', ')}</span>}
    </div>}
  </section>
}

function NewBoardDialog({ onClose, onCreate }: { onClose: () => void; onCreate: (name: string) => Promise<void> }) {
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  return <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose() }}>
    <section className="modal compact-modal" role="dialog" aria-modal="true" aria-labelledby="new-board-title">
      <header><div><p className="eyebrow">Note-backed Kanban</p><h2 id="new-board-title">Create new board</h2></div><button className="icon-button" onClick={onClose} aria-label="Close" type="button">×</button></header>
      <form onSubmit={async (event) => {
        event.preventDefault()
        setBusy(true); setError('')
        try { await onCreate(name) } catch (caught) { setError(message(caught)) } finally { setBusy(false) }
      }}>
        {error && <div className="form-error" role="alert">{error}</div>}
        <label>Board name<input autoFocus maxLength={64} pattern="[a-z0-9][a-z0-9-]{0,63}" placeholder="new-project" required value={name} onChange={(event) => setName(event.target.value)} /></label>
        <p className="hint">Use lowercase letters, numbers, and hyphens.</p>
        <div className="modal-actions"><button className="quiet" onClick={onClose} type="button">Cancel</button><button className="primary" disabled={busy} type="submit">{busy ? 'Creating…' : 'Create board'}</button></div>
      </form>
    </section>
  </div>
}

function ArchivePanel({ board, onClose, onRestore }: { board: string; onClose: () => void; onRestore: (card: Card) => Promise<void> }) {
  const [query, setQuery] = useState('')
  const [cards, setCards] = useState<Card[]>([])
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true
    api<Card[]>(`${API_BASE}/boards/${board}/archive?q=${encodeURIComponent(query)}&limit=100`)
      .then((value) => { if (active) { setCards(value); setError('') } })
      .catch((caught) => { if (active) setError(message(caught)) })
    return () => { active = false }
  }, [board, query])
  return <div className="modal-backdrop"><section className="modal archive-panel" role="dialog" aria-modal="true" aria-labelledby="archive-title"><header><div><p className="eyebrow">{displayName(board)} board</p><h2 id="archive-title">Archive</h2></div><button className="icon-button" onClick={onClose} aria-label="Close" type="button">×</button></header><label>Search archived cards<input aria-label="Search archived cards" maxLength={200} role="searchbox" value={query} onChange={(event) => setQuery(event.target.value)} /></label>{error && <div className="form-error" role="alert">{error}</div>}{cards.length === 0 ? <p className="muted">No archived cards match.</p> : cards.map((card) => <article className="job" key={card.id}><div><strong>{card.title}</strong>{card.labels.map((label) => <span className="label" key={label}>{label}</span>)}</div>{card.description && <p>{card.description}</p>}<p>Created {new Date(card.createdAt).toLocaleDateString()} · Updated {new Date(card.updatedAt).toLocaleDateString()} · Archived {card.doneAt ? new Date(card.doneAt).toLocaleDateString() : 'unknown'}</p><button aria-label={`Restore ${card.title}`} className="primary" disabled={busy === card.id} onClick={async () => { setBusy(card.id); setError(''); try { await onRestore(card); setCards((current) => current.filter((item) => item.id !== card.id)) } catch (caught) { setError(message(caught)) } finally { setBusy('') } }} type="button">{busy === card.id ? 'Restoring…' : 'Restore to Triage'}</button></article>)}</section></div>
}

function JobsPanel({ board, onClose, onCancel }: { board: string; onClose: () => void; onCancel: (id: number) => Promise<DispatchRun> }) {
  const [runs, setRuns] = useState<DispatchRun[]>([])
  const [logs, setLogs] = useState<Record<number, string>>({})
  const [busy, setBusy] = useState(0)
  const [error, setError] = useState('')
  const load = () => api<DispatchRun[]>(`${API_BASE}/dispatch/runs?board=${encodeURIComponent(board)}&limit=100`)
  useEffect(() => {
    let active = true
    const refresh = () => load().then((value) => { if (active) setRuns(value) }).catch(() => {})
    void refresh()
    const timer = window.setInterval(refresh, 5000)
    return () => { active = false; window.clearInterval(timer) }
  }, [board])
  // Active runs auto-tail so a running job shows progress without a click.
  useEffect(() => {
    let active = true
    const tail = () => {
      for (const run of runs.filter((item) => ACTIVE_RUN_STATUSES.includes(item.status))) {
        void api<{ log: string }>(`${API_BASE}/dispatch/runs/${run.id}/log?tail=200000`)
          .then((value) => { if (active) setLogs((current) => (current[run.id] === undefined ? current : { ...current, [run.id]: value.log })) })
          .catch(() => {})
      }
    }
    const timer = window.setInterval(tail, 5000)
    return () => { active = false; window.clearInterval(timer) }
  }, [runs])
  async function stop(run: DispatchRun) {
    if (!window.confirm(`Stop the worker for "${run.cardTitle}"? The card returns to Backlog and partial work stays on the review branch.`)) return
    setBusy(run.id); setError('')
    try { await onCancel(run.id); setRuns(await load()) } catch (caught) { setError(message(caught)) } finally { setBusy(0) }
  }
  return <div className="modal-backdrop"><section className="modal jobs-panel" role="dialog" aria-modal="true" aria-labelledby="jobs-title">
    <header><h2 id="jobs-title">Jobs</h2><button className="icon-button" onClick={onClose} aria-label="Close" type="button">×</button></header>
    {error && <div className="form-error" role="alert">{error}</div>}
    {runs.length === 0 ? <p className="muted">No dispatch runs for this board.</p> : runs.map((run) => {
      const active = ACTIVE_RUN_STATUSES.includes(run.status)
      return <article className="job" key={run.id}>
        <div><strong>{run.cardTitle}</strong> <span className={`job-status ${run.status}`}>{run.status}</span>{run.cancelRequestedAt && active && <span className="job-status cancelling"> stopping…</span>}</div>
        <p>{run.assignee}{run.agent ? ` · ${run.agent}` : ''}{run.pid ? ` · PID ${run.pid}` : ''} · {new Date(run.startedAt).toLocaleString()}{active && ` · ${elapsed(run.startedAt)} elapsed`}</p>
        {(run.outcome || run.error) && <p>{run.outcome || run.error}</p>}
        <button className="quiet" type="button" onClick={async () => { const value = await api<{ log: string }>(`${API_BASE}/dispatch/runs/${run.id}/log?tail=200000`); setLogs((current) => ({ ...current, [run.id]: value.log })) }}>{logs[run.id] === undefined ? 'View log' : 'Refresh log'}</button>
        {active && <button aria-label={`Stop ${run.cardTitle}`} className="danger" disabled={busy === run.id} onClick={() => stop(run)} type="button">{busy === run.id ? 'Stopping…' : 'Stop'}</button>}
        {logs[run.id] !== undefined && <pre>{logs[run.id]}</pre>}
      </article>
    })}
  </section></div>
}

function elapsed(startedAt: string): string {
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(startedAt).getTime()) / 1000))
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`
  return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`
}

function Login({ onLogin }: { onLogin: (session: Session) => Promise<void> }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError('')
    try { await onLogin(await api<Session>(`${API_BASE}/login`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username, password }) })) }
    catch (caught) { setError(message(caught)) } finally { setBusy(false) }
  }
  return (
    <div className="login-page">
      <form className="login-card" onSubmit={submit}>
        <span className="brand-mark large">A</span>
        <p className="eyebrow">Private workspace</p>
        <h1>Amythest Kanban</h1>
        <p className="muted">Sign in to manage the note-backed boards.</p>
        {error && <div className="form-error" role="alert">{error}</div>}
        <label>Username<input autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} /></label>
        <label>Password<input autoComplete="current-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} /></label>
        <button className="primary full" disabled={busy} type="submit">{busy ? 'Signing in…' : 'Sign in'}</button>
      </form>
    </div>
  )
}

interface EditorInput { title: string; description: string; dueDate: string; milestone: string; status: Status; assignee: string; agent: string; blocked: boolean; labels: string[] }
function CardEditor({ card, board, busy, agents, onClose, onSave, onDelete, onComment, onUpload, onDeleteAttachment, onSaveDescription, moveBoards, onMoveBoard }: {
  card?: Card; board: string; busy: boolean; agents: AgentCatalog; onClose: () => void; onSave: (input: EditorInput) => Promise<void>; onDelete?: () => Promise<void>; onComment?: (body: string) => Promise<void>; onUpload?: (file: File) => Promise<Attachment>; onDeleteAttachment?: (attachment: Attachment) => Promise<void>; onSaveDescription?: (description: string) => Promise<void>; moveBoards: BoardSummary[]; onMoveBoard?: (destinationBoard: string) => Promise<void>
}) {
  const [title, setTitle] = useState(card?.title || '')
  const [description, setDescription] = useState(card?.description || '')
  // Existing cards open on the rendered view; a new card starts in the editor.
  const [descriptionView, setDescriptionView] = useState<'preview' | 'write'>(card?.description ? 'preview' : 'write')
  const [dueDate, setDueDate] = useState(card?.dueDate || '')
  const [milestone, setMilestone] = useState(card?.milestone || '')
  const [status, setStatus] = useState<Status>(card?.status || 'triage')
  const initialAssignee = card?.assignee || 'Hermes'
  const [assigneeChoice, setAssigneeChoice] = useState<'Justin' | 'Hermes' | 'custom'>(initialAssignee === 'Justin' || initialAssignee === 'Hermes' ? initialAssignee : 'custom')
  const [customAssignee, setCustomAssignee] = useState(initialAssignee === 'Justin' || initialAssignee === 'Hermes' ? '' : initialAssignee)
  const [agent, setAgent] = useState(card?.agent || '')
  const [blocked, setBlocked] = useState(Boolean(card?.blocked))
  const [labels, setLabels] = useState(card?.labels.join(', ') || '')
  const [comment, setComment] = useState('')
  const [attachments, setAttachments] = useState<Attachment[]>(card?.attachments || [])
  const [attachmentFile, setAttachmentFile] = useState<File | null>(null)
  const [attachmentBusy, setAttachmentBusy] = useState('')
  const [attachmentError, setAttachmentError] = useState('')
  const [destinationBoard, setDestinationBoard] = useState('')
  const assignee = assigneeChoice === 'custom' ? customAssignee.trim() : assigneeChoice
  return (
    <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose() }}>
      <section className="modal card-editor" role="dialog" aria-modal="true" aria-labelledby="card-editor-title">
        <header>
          <div>
            <p className="eyebrow">{displayName(board)} board</p>
            <h2 id="card-editor-title">{card ? 'Edit card' : 'New card'}</h2>
            {card && <p className="card-meta">Created {new Date(card.createdAt).toLocaleDateString()} · Updated {new Date(card.updatedAt).toLocaleString()}</p>}
          </div>
          <div className="header-actions">
            <button
              aria-pressed={blocked}
              className={`blocked-chip${blocked ? ' on' : ''}`}
              onClick={() => setBlocked((current) => !current)}
              title="Blocked cards show a red border; automation skips them. Applies on save."
              type="button"
            ><span aria-hidden="true">⛔</span> Blocked</button>
            <button className="icon-button" onClick={onClose} aria-label="Close" type="button">×</button>
          </div>
        </header>
        <form onSubmit={(event) => { event.preventDefault(); void onSave({ title, description, dueDate, milestone, status, assignee, agent, blocked, labels: labels.split(',').map((value) => value.trim()).filter(Boolean) }) }}>
          <label className="title-field">Title<input autoFocus={!card} className="title-input" maxLength={200} placeholder="What needs doing?" required value={title} onChange={(event) => setTitle(event.target.value)} /></label>
          <div className="description-field">
            <div className="description-head">
              <span className="description-label">Description</span>
              <div className="view-toggle" role="group" aria-label="Description view">
                <button aria-pressed={descriptionView === 'preview'} onClick={() => setDescriptionView('preview')} type="button">Preview</button>
                <button aria-pressed={descriptionView === 'write'} onClick={() => setDescriptionView('write')} type="button">Edit</button>
              </div>
            </div>
            {descriptionView === 'write'
              ? <textarea aria-label="Description" autoFocus={Boolean(card)} maxLength={10000} placeholder="Details, links, acceptance criteria… markdown welcome" rows={6} value={description} onChange={(event) => setDescription(event.target.value)} />
              : description.trim()
                ? <div className="description-preview"><Markdown text={description} onToggleTask={(line, done) => {
                  // Checking a task stamps a ✅ done-date; unchecking clears it.
                  // On existing cards the change persists immediately, so ticking
                  // a checklist item doesn't require a separate Save.
                  const next = toggleTaskLine(description, line, done, localDateStamp())
                  setDescription(next)
                  if (onSaveDescription) void onSaveDescription(next)
                }} /></div>
                : <p className="description-empty muted">No description yet — switch to Edit to add one. Markdown and <code>- [ ]</code> task lists are supported.</p>}
          </div>
          <div className="form-grid">
            <label>Status<select value={status} onChange={(event) => setStatus(event.target.value as Status)}>{statusOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label>
            <label>Due date<input aria-label="Due date" type="date" value={dueDate} onChange={(event) => setDueDate(event.target.value)} /></label>
            <label>Milestone <span className="hint">separate from labels</span><input maxLength={32} placeholder="1.2" value={milestone} onChange={(event) => setMilestone(event.target.value)} /></label>
            <label>Labels <span className="hint">controlled topic labels, comma separated</span><input placeholder="amythest, operations" value={labels} onChange={(event) => setLabels(event.target.value)} /></label>
          </div>
          <fieldset className="assignee-fieldset">
            <legend>Assignee</legend>
            <div className="assignee-options">
              {(['Justin', 'Hermes'] as const).map((name) => <label className="assignee-radio" key={name}><input checked={assigneeChoice === name} name="assignee" onChange={() => setAssigneeChoice(name)} type="radio" />{name}</label>)}
              <button aria-pressed={assigneeChoice === 'custom'} className="quiet" onClick={() => setAssigneeChoice('custom')} type="button">Someone else…</button>
            </div>
            {assigneeChoice === 'custom' && <label className="assignee-custom">Custom assignee<input aria-label="Custom assignee" autoFocus maxLength={80} value={customAssignee} onChange={(event) => setCustomAssignee(event.target.value)} /></label>}
          </fieldset>
          {agents.agents.length > 0 && <label>Agent <span className="hint">which backend and model runs this card</span>
            <select aria-label="Agent" value={agent} onChange={(event) => setAgent(event.target.value)}>
              <option value="">{defaultAgentLabel(agents, assignee)}</option>
              {agents.agents.map((name) => <option key={name} value={name}>{name}</option>)}
            </select>
          </label>}
          <div className="modal-actions">
            <button className="quiet" onClick={onClose} type="button">Cancel</button>
            <button className="primary" disabled={busy} type="submit">{busy ? 'Saving…' : card ? 'Save card' : 'Create card'}</button>
          </div>
        </form>
        {card && <section className="attachments" aria-labelledby="attachments-title">
          <h3 id="attachments-title">Attachments{attachments.length > 0 ? ` (${attachments.length})` : ''}</h3>
          {attachments.length === 0 ? <p className="muted">No attachments yet.</p> : <ul>{attachments.map((attachment) => <li key={attachment.id}>
            <div><a aria-label={`Download ${attachment.filename}`} href={`${API_BASE}/boards/${board}/cards/${card.id}/attachments/${attachment.id}`}>{attachment.filename}</a><span>{formatBytes(attachment.size)}</span></div>
            {onDeleteAttachment && <button aria-label={`Delete ${attachment.filename}`} className="quiet" disabled={attachmentBusy === attachment.id} onClick={async () => {
              setAttachmentBusy(attachment.id); setAttachmentError('')
              try { await onDeleteAttachment(attachment); setAttachments((current) => current.filter((item) => item.id !== attachment.id)) }
              catch (caught) { setAttachmentError(message(caught)) } finally { setAttachmentBusy('') }
            }} type="button">Delete</button>}
          </li>)}</ul>}
          {attachmentError && <div className="form-error" role="alert">{attachmentError}</div>}
          {onUpload && <form className="attachment-upload" onSubmit={async (event) => {
            event.preventDefault()
            const form = event.currentTarget
            if (!attachmentFile) return
            if (attachmentFile.size > 10 * 1024 * 1024) { setAttachmentError('Attachment must be at most 10 MiB.'); return }
            setAttachmentBusy('upload'); setAttachmentError('')
            try { const uploaded = await onUpload(attachmentFile); setAttachments((current) => [...current, uploaded]); setAttachmentFile(null); form.reset() }
            catch (caught) { setAttachmentError(message(caught)) } finally { setAttachmentBusy('') }
          }}>
            <label>Choose attachment<input aria-label="Choose attachment" disabled={attachments.length >= 10} onChange={(event) => setAttachmentFile(event.target.files?.[0] || null)} type="file" /></label>
            <button className="quiet" disabled={!attachmentFile || attachmentBusy === 'upload' || attachments.length >= 10} type="submit">{attachmentBusy === 'upload' ? 'Uploading…' : 'Upload attachment'}</button>
            <p className="hint">Up to 10 files per card, 10 MiB each.</p>
          </form>}
        </section>}
        {card && <div className="comments"><h3>Comments{card.comments.length > 0 ? ` (${card.comments.length})` : ''}</h3>{card.comments.length === 0 ? <p className="muted">No comments yet.</p> : card.comments.map((item) => <article key={item.id}><strong>{item.author}</strong><time>{new Date(item.createdAt).toLocaleString()}</time><p>{item.body}</p></article>)}{onComment && <form className="comment-form" onSubmit={async (event) => { event.preventDefault(); if (!comment.trim()) return; await onComment(comment); setComment('') }}><textarea aria-label="Add comment" maxLength={2000} placeholder="Add a comment…" rows={3} value={comment} onChange={(event) => setComment(event.target.value)} /><button className="quiet" disabled={busy || !comment.trim()} type="submit">Comment</button></form>}</div>}
        {card && (onDelete || (onMoveBoard && moveBoards.length > 0)) && <section className="editor-section card-actions" aria-labelledby="card-actions-title">
          <h3 id="card-actions-title">Card actions</h3>
          {onMoveBoard && moveBoards.length > 0 && <div className="move-board-row">
            <select aria-label="Destination board" value={destinationBoard} onChange={(event) => setDestinationBoard(event.target.value)}><option value="">Move to another board…</option>{moveBoards.map((item) => <option key={item.name} value={item.name}>{displayName(item.name)}</option>)}</select>
            <button className="quiet" disabled={busy || !destinationBoard} onClick={() => void onMoveBoard(destinationBoard)} type="button">Move card</button>
          </div>}
          {onMoveBoard && moveBoards.length > 0 && <p className="hint">Moving preserves the card ID, content, attachments, and history.</p>}
          {onDelete && <button className="danger-link" disabled={busy} onClick={() => void onDelete()} type="button">Delete card…</button>}
        </section>}
      </section>
    </div>
  )
}

function displayName(name: string) { return name.charAt(0).toUpperCase() + name.slice(1).replaceAll('-', ' ') }
// The done-date uses the browser's local calendar day, matching what the user
// sees; toISOString() would shift the date near midnight for non-UTC users.
function localDateStamp() {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
}
function message(error: unknown) { return error instanceof Error ? error.message : 'Something went wrong' }
function formatBytes(size: number) {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${Math.round(size / 1024)} KB`
  return `${(size / (1024 * 1024)).toFixed(size % (1024 * 1024) === 0 ? 0 : 1)} MB`
}

// A board URL is <mount>/<board>, optionally followed by one label per path
// segment: /kanban/proof/1.0/bug shows only cards carrying both labels.
const BOARD_SUFFIX = /^\/([a-z0-9-]+)(?:\/(.*))?$/

function boardSuffix(pathname: string): RegExpMatchArray | null {
  if (!pathname.startsWith(`${KANBAN_MOUNT}/`)) return null
  return pathname.slice(KANBAN_MOUNT.length).match(BOARD_SUFFIX)
}

export function boardNameFromPath(pathname: string): string | null {
  const match = boardSuffix(pathname)
  return match ? match[1] : null
}

export function labelsFromPath(pathname: string): string[] {
  const match = boardSuffix(pathname)
  if (!match || !match[2]) return []
  const seen = new Set<string>()
  for (const segment of match[2].split('/')) {
    if (!segment) continue
    let decoded = segment
    try { decoded = decodeURIComponent(segment) } catch { /* keep the raw segment */ }
    const label = decoded.trim().toLowerCase()
    // Mirror the server's label rules so a junk URL cannot filter everything out.
    if (label && label.length <= 32) seen.add(label)
  }
  return [...seen]
}

export function boardPath(name: string, labels: string[] = []): string {
  const segments = [encodeURIComponent(name), ...labels.map((label) => encodeURIComponent(label))]
  return `${KANBAN_MOUNT}/${segments.join('/')}`
}

export function safeNextPath(search: string): string | null {
  const value = new URLSearchParams(search).get('next')
  if (!value || !value.startsWith('/') || value.startsWith('//') || value.includes('\\')) return null
  try {
	const resolved = new URL(value, window.location.origin)
	if (resolved.origin !== window.location.origin) return null
	return resolved.pathname + resolved.search + resolved.hash
  } catch {
	return null
  }
}
