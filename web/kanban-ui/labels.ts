import { KANBAN_MOUNT } from './mount.ts'

export type FilterMode = 'or' | 'and'

export interface LabelledCard {
  id: string
  labels: string[]
}

export interface LabelOption {
  label: string
  count: number
}

export function buildLabelOptions(
  cards: LabelledCard[],
  included: string[] = [],
  excluded: string[] = [],
  limit = 12,
): LabelOption[] {
  const counts = new Map<string, number>()
  for (const card of cards) {
    for (const label of new Set(card.labels)) counts.set(label, (counts.get(label) || 0) + 1)
  }
  const ordered = [...counts.entries()]
    .map(([label, count]) => ({ label, count }))
    .sort((left, right) => right.count - left.count || left.label.localeCompare(right.label))
  const selected = new Set([...included, ...excluded])
  const visible = ordered.slice(0, Math.max(0, limit))
  const visibleLabels = new Set(visible.map((option) => option.label))
  for (const label of selected) {
    if (!visibleLabels.has(label)) visible.push({ label, count: counts.get(label) || 0 })
  }
  return visible
}

export function allLabelOptions(cards: LabelledCard[]): LabelOption[] {
  return buildLabelOptions(cards, [], [], Number.MAX_SAFE_INTEGER)
}

export function filterCardsByLabels<T extends LabelledCard>(
  cards: T[],
  included: string[],
  mode: FilterMode = 'or',
  excluded: string[] = [],
): T[] {
  return cards.filter((card) => {
    if (excluded.some((label) => card.labels.includes(label))) return false
    if (included.length === 0) return true
    return mode === 'and'
      ? included.every((label) => card.labels.includes(label))
      : included.some((label) => card.labels.includes(label))
  })
}

export function filterSettingsFromSearch(search: string): { mode: FilterMode; excluded: string[] } {
  const params = new URLSearchParams(search)
  const mode: FilterMode = params.get('mode') === 'and' ? 'and' : 'or'
  const excluded = [...new Set(params.getAll('exclude').map(normalizeLabel).filter(Boolean))]
  return { mode, excluded }
}

export function boardFilterURL(
  board: string,
  included: string[] = [],
  mode: FilterMode = 'or',
  excluded: string[] = [],
): string {
  const segments = [encodeURIComponent(board), ...included.map((label) => encodeURIComponent(label))]
  const params = new URLSearchParams()
  if (mode === 'and') params.set('mode', 'and')
  for (const label of excluded) params.append('exclude', label)
  const query = params.toString()
  return `${KANBAN_MOUNT}/${segments.join('/')}${query ? `?${query}` : ''}`
}

function normalizeLabel(value: string): string {
  return value.trim().toLowerCase().slice(0, 32)
}
