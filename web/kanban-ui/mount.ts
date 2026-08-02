// The kanban SPA can be served at /kanban directly or behind a reverse proxy
// under a deeper public prefix such as /notes/kanban. Everything URL-shaped —
// API calls, board paths, history pushes, the Notes link — derives from the
// mount detected in the page location so links never escape the prefix.
const MOUNT_PATTERN = /^(.*?\/kanban)(?:\/|$)/

export function kanbanMountFromPathname(pathname: string): string {
  const match = pathname.match(MOUNT_PATTERN)
  return match ? match[1] : '/kanban'
}

export const KANBAN_MOUNT =
  typeof window === 'undefined' ? '/kanban' : kanbanMountFromPathname(window.location.pathname)

// The notes site sits one level above the kanban mount: '/' when the SPA is
// at /kanban, '/notes/' when it is at /notes/kanban.
export const NOTES_BASE = KANBAN_MOUNT.slice(0, KANBAN_MOUNT.length - 'kanban'.length)
