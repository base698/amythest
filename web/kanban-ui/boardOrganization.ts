export interface BoardMetadata {
  name: string
  displayName?: string
  sortOrder?: number
  pinned?: boolean
  archived?: boolean
}

export interface FocusableCard { id: string }
export interface RankedFocusCard extends FocusableCard {
  priority?: 'p0' | 'p1' | 'p2' | 'p3'
  status?: 'triage' | 'backlog' | 'ready' | 'in_progress' | 'verify' | 'done'
  blocked?: boolean
  dueDate?: string
  createdAt?: string
}

export function boardLabel(board: Pick<BoardMetadata, 'name' | 'displayName'>): string {
  const explicit = board.displayName?.trim()
  if (explicit) return explicit
  return board.name.charAt(0).toUpperCase() + board.name.slice(1).replaceAll('-', ' ')
}

function compareBoards(left: BoardMetadata, right: BoardMetadata): number {
  if (Boolean(left.pinned) !== Boolean(right.pinned)) return left.pinned ? -1 : 1
  const order = (left.sortOrder || 0) - (right.sortOrder || 0)
  if (order !== 0) return order
  return boardLabel(left).localeCompare(boardLabel(right), undefined, { sensitivity: 'base' })
}

export function organizeBoards<T extends BoardMetadata>(boards: readonly T[]): {
  active: T[]
  pinned: T[]
  archived: T[]
} {
  const active = boards.filter((board) => !board.archived).sort(compareBoards)
  const archived = boards.filter((board) => board.archived).sort(compareBoards)
  return { active, pinned: active.filter((board) => board.pinned), archived }
}

export function selectedFocusCard<T extends FocusableCard>(cards: readonly T[], focusCardId?: string): T | undefined {
  if (!focusCardId) return undefined
  return cards.find((card) => card.id === focusCardId)
}

const priorityRank = { p0: 0, p1: 1, p2: 2, p3: 3 } as const

function fallbackBucket(card: RankedFocusCard): number {
  if (card.status === 'in_progress') return 0
  if (card.status === 'ready' && (card.priority === 'p0' || card.priority === 'p1')) return 1
  if (card.dueDate) return 2
  if (card.status === 'ready') return 3
  if (card.status === 'triage') return 4
  if (card.status === 'verify') return 5
  return 6
}

export function suggestedFocusCard<T extends RankedFocusCard>(cards: readonly T[]): T | undefined {
  return cards
    .filter((card) => !card.blocked && ['triage', 'ready', 'in_progress', 'verify'].includes(card.status || ''))
    .slice()
    .sort((left, right) => {
      const priority = priorityRank[left.priority || 'p2'] - priorityRank[right.priority || 'p2']
      if (priority !== 0) return priority
      const bucket = fallbackBucket(left) - fallbackBucket(right)
      if (bucket !== 0) return bucket
      const leftDue = left.dueDate || '9999-12-31'
      const rightDue = right.dueDate || '9999-12-31'
      if (leftDue !== rightDue) return leftDue.localeCompare(rightDue)
      return (left.createdAt || '').localeCompare(right.createdAt || '')
    })[0]
}

export function boardFocusCandidate<T extends RankedFocusCard>(cards: readonly T[], focusCardId?: string): { card?: T; explicit: boolean } {
  const explicit = selectedFocusCard(cards, focusCardId)
  if (explicit) return { card: explicit, explicit: true }
  return { card: suggestedFocusCard(cards), explicit: false }
}
