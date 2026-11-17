export const BOARD_VIEWS = ['board', 'project', 'unfiled'] as const
export type BoardWorkspaceView = typeof BOARD_VIEWS[number]
export function boardWorkspaceView(value: string | null): BoardWorkspaceView {
 if (value === 'needs-you') return 'project'
 return BOARD_VIEWS.includes(value as BoardWorkspaceView) ? value as BoardWorkspaceView : 'board'
}
export const BOARD_VIEW_STORAGE_KEY = 'amythest.work.view'

// Output belongs to this workspace but has not been assigned to a card.
export function workspaceUnfiled<T extends { card_id: string; run_id: string; input?: boolean }>(artifacts: T[], runs: { id: string; card_id: string }[]): T[] {
 const unassignedRuns = new Set(runs.filter(r => !r.card_id).map(r => r.id))
 return artifacts.filter(a => !a.input && !a.card_id && unassignedRuns.has(a.run_id))
}
