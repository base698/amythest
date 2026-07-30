import type { DragEvent } from 'react'
import { formatCreatedDate, formatDueDate } from './dates'

export type Status = 'triage' | 'backlog' | 'ready' | 'in_progress' | 'verify' | 'done'

export interface Comment {
  id: string
  author: string
  body: string
  createdAt: string
}

export interface Attachment {
  id: string
  filename: string
  size: number
  contentType: string
  createdAt: string
}

export interface Card {
	id: string
	title: string
	description: string
	dueDate?: string
	milestone?: string
	status: Status
  assignee: string
  agent?: string
  blocked?: boolean
  labels: string[]
  comments: Comment[]
  attachments?: Attachment[] | null
  createdAt: string
  updatedAt: string
  doneAt?: string
}

interface BoardViewProps {
  cards: Card[]
  onMove: (card: Card, status: Status, beforeId?: string) => void
  onOpen: (card: Card) => void
  onBlockedChange: (card: Card, blocked: boolean) => void
}

const columns: Array<{ status: Exclude<Status, 'done'>; label: string }> = [
  { status: 'triage', label: 'Triage' },
  { status: 'backlog', label: 'Backlog' },
  { status: 'ready', label: 'Ready' },
  { status: 'in_progress', label: 'In progress' },
  { status: 'verify', label: 'Verify' },
]

const moveOptions: Array<{ status: Status; label: string }> = [
  ...columns,
  { status: 'done', label: 'Done — archive' },
]

export function BoardView({ cards, onMove, onOpen, onBlockedChange }: BoardViewProps) {
  function startDrag(event: DragEvent, card: Card) {
    event.dataTransfer.setData('text/card-id', card.id)
    event.dataTransfer.effectAllowed = 'move'
  }

  function draggedCard(event: DragEvent) {
    return cards.find((candidate) => candidate.id === event.dataTransfer.getData('text/card-id'))
  }

  return (
    <div className="board" aria-label="Kanban board">
      {columns.map(({ status, label }) => {
        const matching = cards.filter((card) => card.status === status)
        return (
          <section
            className="column"
            key={status}
            data-status={status}
            onDragOver={(event) => event.preventDefault()}
            onDrop={(event) => {
              event.preventDefault()
              const card = draggedCard(event)
              if (card) onMove(card, status, undefined)
            }}
          >
            <header className="column-header">
              <h2>{label}</h2>
              <span className="count" aria-label={`${matching.length} cards`}>{matching.length}</span>
            </header>
            <div className="card-list">
              {matching.map((card) => (
                <article
                  className={card.blocked ? 'card blocked' : 'card'}
                  draggable
                  key={card.id}
                  onDragStart={(event) => startDrag(event, card)}
                  onDragOver={(event) => {
                    event.preventDefault()
                    event.stopPropagation()
                  }}
                  onDrop={(event) => {
                    event.preventDefault()
                    event.stopPropagation()
                    const dragged = draggedCard(event)
                    if (dragged && dragged.id !== card.id) onMove(dragged, status, card.id)
                  }}
                >
                  <button className="card-open" aria-label={`Open ${card.title}`} onClick={() => onOpen(card)} type="button">
                    <span className="card-title">{card.blocked && <span className="blocked-flag">Blocked</span>}{card.title}</span>
                    {card.description && <span className="card-description">{card.description}</span>}
                    {card.milestone && <span className="card-milestone">Milestone {card.milestone}</span>}
                    {card.dueDate && <span className="card-due">Due {formatDueDate(card.dueDate)}</span>}
                    {card.labels.length > 0 && (
                      <span className="labels">
                        {card.labels.map((label) => <span className="label" key={label}>{label}</span>)}
                      </span>
                    )}
                    <span className="card-footer">
                      <span>{card.assignee || 'Unassigned'}</span>
                      <time dateTime={card.createdAt}>Created {formatCreatedDate(card.createdAt)}</time>
                      {card.comments.length > 0 && <span>{card.comments.length} comment{card.comments.length === 1 ? '' : 's'}</span>}
                    </span>
                  </button>
                  <div className="card-move">
                    <label className="card-blocked">
                      <input
                        aria-label={`Blocked: ${card.title}`}
                        checked={Boolean(card.blocked)}
                        onChange={(event) => onBlockedChange(card, event.target.checked)}
                        type="checkbox"
                      />
                      <span>Blocked</span>
                    </label>
                    <select
                      aria-label={`Move ${card.title}`}
                      value={card.status}
                      onChange={(event) => {
                        const next = event.target.value as Status
                        if (next !== card.status) onMove(card, next)
                      }}
                    >
                      {moveOptions.map((option) => <option key={option.status} value={option.status}>{option.label}</option>)}
                    </select>
                  </div>
                </article>
              ))}
              {matching.length === 0 && <p className="empty-column">No cards</p>}
            </div>
          </section>
        )
      })}
    </div>
  )
}
