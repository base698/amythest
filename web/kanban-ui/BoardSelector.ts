import { createElement } from 'react'
import { boardLabel, canFavoriteBoard, organizeBoards, type BoardMetadata } from './boardOrganization.ts'

export const MAX_FAVORITE_BOARDS = 3

export function BoardSelector<T extends BoardMetadata>({ boards, activeBoard, disabled, onFavorite, onSelect }: {
  boards: readonly T[]
  activeBoard: string
  disabled: boolean
  onFavorite: (name: string, favorite: boolean) => void
  onSelect: (name: string) => void
}) {
  const organized = organizeBoards(boards)
  const selected = boards.find((item) => item.name === activeBoard)
  const canToggleFavorite = Boolean(selected && canFavoriteBoard(boards, selected.name, MAX_FAVORITE_BOARDS))
  const activeOptions = organized.active.map((item) => createElement('option', { key: item.name, value: item.name }, `${item.pinned ? '★ ' : ''}${boardLabel(item)}`))
  const children = []
  if (!selected) children.push(createElement('option', { key: 'placeholder', value: '' }, 'Select board…'))
  children.push(createElement('optgroup', { key: 'active', label: 'Active boards' }, activeOptions))
  if (organized.archived.length > 0) children.push(createElement('optgroup', { key: 'archived', label: 'Archived boards' },
    organized.archived.map((item) => createElement('option', { key: item.name, value: item.name }, boardLabel(item))),
  ))

  return createElement('div', { className: 'board-selector' },
    createElement('select', {
      'aria-label': 'Select board',
      disabled,
      onChange: (event: { target: { value: string } }) => onSelect(event.target.value),
      value: selected?.name || '',
    }, children),
    selected && !selected.archived ? createElement('button', {
      'aria-label': `${selected.pinned ? 'Unfavorite' : 'Favorite'} ${boardLabel(selected)}`,
      'aria-pressed': Boolean(selected.pinned),
      className: 'board-favorite-button',
      disabled: disabled || !canToggleFavorite,
      onClick: () => onFavorite(selected.name, !selected.pinned),
      title: !selected.pinned && !canToggleFavorite ? `You can favorite up to ${MAX_FAVORITE_BOARDS} boards` : undefined,
      type: 'button',
    }, createElement('span', { 'aria-hidden': 'true' }, selected.pinned ? '★' : '☆')) : null,
  )
}
