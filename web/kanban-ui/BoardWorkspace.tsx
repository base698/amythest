import { useContext, useState, type ReactNode } from 'react'
import type { Card } from './BoardView'
import { WorkContext, WorkUnfiled, useProjectSnapshot } from './Work'
import { WorkProjects } from './WorkProject'
import { BOARD_VIEWS, BOARD_VIEW_STORAGE_KEY, boardWorkspaceView, workspaceUnfiled, type BoardWorkspaceView } from './board-workspace'

type WorkspaceBoard = { name: string; displayName?: string; workspace?: string; archived?: boolean; cards: Card[] }
function saved(key: string): string { try { return localStorage.getItem(key) || '' } catch { return '' } }
function remember(key: string, value: string) { try { localStorage.setItem(key, value) } catch { /* Private browsing still works. */ } }
const LABELS = { board: 'Board', project: 'Project', unfiled: 'Unassigned output' }

export function BoardWorkspace({ board, children, onOpen, onSettings }: { board: WorkspaceBoard; children: ReactNode; onOpen: (board: string, card: Card) => void; onSettings: () => void }) {
 const { data: overview, refresh } = useContext(WorkContext)
 const [view, setView] = useState(() => boardWorkspaceView(saved(BOARD_VIEW_STORAGE_KEY)))
 const project = board.workspace || ''
 const workspace = useProjectSnapshot(project)
 if (!overview) return <>{children}</>
 const unfiled = workspaceUnfiled(workspace.data?.artifacts || [], workspace.data?.runs || [])
 const chooseView = (next: BoardWorkspaceView) => { setView(next); remember(BOARD_VIEW_STORAGE_KEY, next) }
 return <section className="board-workspace" aria-label={`${board.displayName || board.name} views`}>
  <div className="board-workspace-toolbar">
   <p className="board-workspace-link">{project ? <><strong>{project}</strong><span>herdr workspace</span></> : <>No linked workspace <button className="quiet" type="button" onClick={onSettings}>Link in board settings</button></>}</p>
   <nav className="board-view-switcher" aria-label="Board views">{BOARD_VIEWS.map(v => {
    const count = v === 'unfiled' && workspace.data ? unfiled.length : undefined
    return <button key={v} type="button" aria-pressed={view === v} aria-controls="board-workspace-content" onClick={() => chooseView(v)}>
     {LABELS[v]}{count !== undefined && count > 0 && <span className="board-view-count">{count}</span>}
    </button>
   })}</nav>
  </div>
  <div id="board-workspace-content" key={`${board.name}:${project}:${view}`}>
   {view === 'board' ? children : !project ? <p className="muted">Link a herdr workspace in Board settings to see its activity here.</p>
    : view === 'project' ? <WorkProjects boards={[board]} workspace={workspace} onOpen={onOpen} />
    : <WorkUnfiled boards={[board]} artifacts={unfiled} loading={!workspace.data} loadError={workspace.error} readOnly={board.archived} onRefresh={async () => { await workspace.refresh(); await refresh() }} />}
  </div>
 </section>
}
