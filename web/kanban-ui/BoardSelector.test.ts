import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'

import { BoardSelector } from './BoardSelector.ts'

const boards = [
  { name: 'later', displayName: 'Later', sortOrder: 20, pinned: true, archived: false },
  { name: 'ideas', displayName: 'Ideas', sortOrder: 1, pinned: false, archived: false },
  { name: 'home', displayName: 'Home base', sortOrder: 5, pinned: true, archived: false },
  { name: 'archive', displayName: 'Old work', sortOrder: 0, pinned: false, archived: true },
]

test('board selector lists favorites first and exposes an accessible favorite toggle', () => {
  const html = renderToStaticMarkup(createElement(BoardSelector, {
    boards,
    activeBoard: 'ideas',
    disabled: false,
    onFavorite: () => undefined,
    onSelect: () => undefined,
  }))

  assert.match(html, /aria-label="Select board"/)
  assert.ok(html.indexOf('Home base') < html.indexOf('Ideas'))
  assert.ok(html.indexOf('Later') < html.indexOf('Ideas'))
  assert.match(html, /<optgroup label="Archived boards"><option value="archive">Old work<\/option><\/optgroup>/)
  assert.match(html, /aria-label="Favorite Ideas"/)
  assert.match(html, /aria-pressed="false"/)
})

test('selector stays touch-accessible while mobile navigation yields space', () => {
  const css = readFileSync(new URL('./styles.css', import.meta.url), 'utf8')
  assert.match(css, /\.board-selector\s*\{[^}]*display:flex/)
  assert.match(css, /\.board-favorite-button\s*\{[^}]*min-width:38px/)
  assert.match(css, /@media \(max-width: 650px\)[\s\S]*\.board-selector\s*\{[^}]*flex:1/)
  assert.match(css, /@media \(max-width: 650px\)[\s\S]*\.board-tabs button:not\(\.focus-tab\)/)
})

test('favorite limit explanation remains exposed to assistive technology', () => {
  const limitedBoards = [
    ...boards,
    { name: 'third', displayName: 'Third', sortOrder: 30, pinned: true, archived: false },
  ]
  const html = renderToStaticMarkup(createElement(BoardSelector, {
    boards: limitedBoards,
    activeBoard: 'ideas',
    disabled: false,
    onFavorite: () => undefined,
    onSelect: () => undefined,
  }))

  assert.match(html, /aria-describedby="favorite-limit-help"/)
  assert.match(html, /id="favorite-limit-help"[^>]*>You can favorite up to 3 boards/)
  assert.doesNotMatch(html, /title="You can favorite/)
})

test('320px header uses bounded grid geometry and hides only the Notes link', () => {
  const css = readFileSync(new URL('./styles.css', import.meta.url), 'utf8')
  const mobile = css.match(/@media \(max-width: 540px\) \{([\s\S]*)\}\s*$/)?.[1] || ''
  assert.match(mobile, /\.topbar\s*\{[^}]*display:\s*grid[^}]*grid-template-columns:\s*28px minmax\(0, 1fr\) auto[^}]*max-width:\s*100vw[^}]*overflow-x:\s*clip/s)
  assert.match(mobile, /\.board-selector\s*\{[^}]*min-width:\s*0[^}]*width:\s*100%/s)
  assert.match(mobile, /\.top-actions \.notes-home-link\s*\{[^}]*display:\s*none/s)
  assert.doesNotMatch(mobile, /\.top-actions a\s*\{[^}]*display:\s*none/s)
})

test('board create and settings forms honor the three-favorite limit', () => {
  const source = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8')
  assert.match(source, /<NewBoardDialog[^>]*canPin=/s)
  assert.match(source, /<BoardSettingsDialog[^>]*canPin=/s)
  assert.match(source, /disabled=\{!canPin && !pinned\}/)
})

test('app persists favorite changes through the existing board settings API', () => {
  const source = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8')
  assert.match(source, /<BoardSelector/)
  assert.match(source, /settings`, 'PATCH', \{ pinned: favorite \}/)
  assert.match(source, /setBoards\(\(current\) => applyFavoriteMutation/)
  assert.match(source, /setBoard\(\(current\) => applyFavoriteMutation/)
  assert.doesNotMatch(source, /\{ \.\.\.item, \.\.\.updated, counts:/)
})
