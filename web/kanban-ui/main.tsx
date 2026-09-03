import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'

// Palette + mode chosen on the notes site carry over (same origin). The
// kanban CSP (script-src 'self') bars inline bootstraps in index.html, so
// this runs at bundle start — still before first paint, since nothing
// renders until React mounts below.
const theme = localStorage.getItem('theme')
document.documentElement.dataset.theme =
  theme ?? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
const palette = localStorage.getItem('palette')
if (palette && palette !== 'default') document.documentElement.dataset.palette = palette

createRoot(document.getElementById('root')!).render(<StrictMode><App /></StrictMode>)
