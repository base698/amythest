export interface BoardSummaryShape {
  name: string
  displayName: string
  description?: string
  workspace?: string
  icon?: string
  color?: string
  sortOrder: number
  pinned: boolean
  archived: boolean
  focusCardId?: string
  dispatchEnabled: boolean
  counts: unknown
}

export interface BoardDataShape extends Omit<BoardSummaryShape, 'counts'> {
  version: number
  cards: unknown[]
}

export function boardSummaryFromData<T extends BoardSummaryShape>(updated: BoardDataShape, counts: T['counts']): T {
  return {
    name: updated.name,
    displayName: updated.displayName,
    description: updated.description,
    workspace: updated.workspace,
    icon: updated.icon,
    color: updated.color,
    sortOrder: updated.sortOrder,
    pinned: updated.pinned,
    archived: updated.archived,
    focusCardId: updated.focusCardId,
    dispatchEnabled: updated.dispatchEnabled,
    counts,
  } as T
}

export function applyFavoriteMutation<S extends BoardSummaryShape, D extends BoardDataShape>(
  summaries: readonly S[],
  currentBoard: D | null,
  targetName: string,
  updated: D,
): { summaries: S[]; board: D | null } {
  return {
    summaries: summaries.map((item) => item.name === targetName ? boardSummaryFromData(updated, item.counts) : item),
    board: currentBoard?.name === targetName ? updated : currentBoard,
  }
}

export function shouldLeaveAfterSettingsSave(wasArchived: boolean, isArchived: boolean): boolean {
  return !wasArchived && isArchived
}
