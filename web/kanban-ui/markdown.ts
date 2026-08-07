// Minimal markdown parser for card descriptions. Parsing to a token tree and
// rendering through React elements (never innerHTML) keeps the output XSS-safe
// without a sanitizer dependency. Task items carry their 0-based source line so
// a checkbox click can rewrite exactly that line in the description text.

export type Inline =
  | { kind: 'text'; text: string }
  | { kind: 'code'; text: string }
  | { kind: 'br' }
  | { kind: 'link'; href: string; children: Inline[] }
  | { kind: 'strong'; children: Inline[] }
  | { kind: 'em'; children: Inline[] }
  | { kind: 'del'; children: Inline[] }

export interface ListItem {
  task?: { checked: boolean; line: number }
  content: Inline[]
  children: Block[]
}

export type Block =
  | { kind: 'heading'; level: number; content: Inline[] }
  | { kind: 'paragraph'; content: Inline[] }
  | { kind: 'code'; lang: string; text: string }
  | { kind: 'quote'; children: Block[] }
  | { kind: 'list'; ordered: boolean; items: ListItem[] }
  | { kind: 'hr' }

interface Line { text: string; line: number }

const HEADING = /^(#{1,6})\s+(.*)$/
const HR = /^(?:-{3,}|\*{3,}|_{3,})\s*$/
const FENCE = /^(```+|~~~+)\s*([\w+-]*)\s*$/
const LIST_ITEM = /^(\s*)([-*+]|\d+[.)])\s+(.*)$/
const TASK_PREFIX = /^\[( |x|X|-)\]\s+(.*)$/s

export function parseMarkdown(text: string): Block[] {
  const lines = text.split('\n').map((raw, index) => ({ text: raw.replace(/\r$/, ''), line: index }))
  return parseBlocks(lines)
}

function parseBlocks(lines: Line[]): Block[] {
  const blocks: Block[] = []
  let index = 0
  while (index < lines.length) {
    const { text } = lines[index]
    if (!text.trim()) { index++; continue }

    const fence = text.match(FENCE)
    if (fence) {
      const body: string[] = []
      let cursor = index + 1
      while (cursor < lines.length && !lines[cursor].text.startsWith(fence[1].slice(0, 3))) {
        body.push(lines[cursor].text)
        cursor++
      }
      blocks.push({ kind: 'code', lang: fence[2], text: body.join('\n') })
      index = cursor < lines.length ? cursor + 1 : cursor
      continue
    }

    const heading = text.match(HEADING)
    if (heading) {
      blocks.push({ kind: 'heading', level: heading[1].length, content: parseInline(heading[2].trim()) })
      index++
      continue
    }

    if (HR.test(text.trim()) && !LIST_ITEM.test(text)) {
      blocks.push({ kind: 'hr' })
      index++
      continue
    }

    if (text.trimStart().startsWith('>')) {
      const inner: Line[] = []
      while (index < lines.length && lines[index].text.trimStart().startsWith('>')) {
        inner.push({ text: lines[index].text.trimStart().replace(/^>\s?/, ''), line: lines[index].line })
        index++
      }
      blocks.push({ kind: 'quote', children: parseBlocks(inner) })
      continue
    }

    if (LIST_ITEM.test(text)) {
      const [block, next] = parseList(lines, index)
      blocks.push(block)
      index = next
      continue
    }

    // Paragraph: consecutive plain lines; single newlines become hard breaks
    // because short card descriptions rely on them for layout.
    const content: Inline[] = []
    while (index < lines.length) {
      const current = lines[index].text
      if (!current.trim() || HEADING.test(current) || FENCE.test(current) ||
        LIST_ITEM.test(current) || current.trimStart().startsWith('>') ||
        (HR.test(current.trim()) && !LIST_ITEM.test(current))) break
      if (content.length > 0) content.push({ kind: 'br' })
      content.push(...parseInline(current.trim()))
      index++
    }
    blocks.push({ kind: 'paragraph', content })
  }
  return blocks
}

function parseList(lines: Line[], start: number): [Block, number] {
  const first = lines[start].text.match(LIST_ITEM) as RegExpMatchArray
  const baseIndent = first[1].length
  const ordered = /\d/.test(first[2][0])
  const items: ListItem[] = []
  let index = start

  while (index < lines.length) {
    const { text, line } = lines[index]
    if (!text.trim()) {
      // A blank line ends the list unless another item at any depth follows.
      if (index + 1 < lines.length && LIST_ITEM.test(lines[index + 1].text)) { index++; continue }
      break
    }
    const match = text.match(LIST_ITEM)
    if (!match) {
      // Indented continuation text joins the previous item.
      if (items.length > 0 && text.startsWith(' '.repeat(baseIndent + 1))) {
        items[items.length - 1].content.push({ kind: 'br' }, ...parseInline(text.trim()))
        index++
        continue
      }
      break
    }
    const indent = match[1].length
    if (indent < baseIndent) break
    if (indent > baseIndent) {
      // Deeper item: parse the nested list into the previous item.
      if (items.length === 0) break
      const [child, next] = parseList(lines, index)
      items[items.length - 1].children.push(child)
      index = next
      continue
    }
    const task = match[3].match(TASK_PREFIX)
    items.push(task
      ? { task: { checked: task[1] !== ' ', line }, content: parseInline(task[2]), children: [] }
      : { content: parseInline(match[3]), children: [] })
    index++
  }
  return [{ kind: 'list', ordered, items }, index]
}

// Inline markup: code spans bind tightest, then links, emphasis, and
// strikethrough. Unmatched markers fall through as literal text.
export function parseInline(text: string): Inline[] {
  const out: Inline[] = []
  let plain = ''
  let index = 0
  const flush = () => { if (plain) { out.push({ kind: 'text', text: plain }); plain = '' } }

  while (index < text.length) {
    const rest = text.slice(index)

    const code = rest.match(/^(`+)([^`]|[^`][\s\S]*?[^`])\1(?!`)/)
    if (code) {
      flush()
      out.push({ kind: 'code', text: code[2].trim() })
      index += code[0].length
      continue
    }

    const link = rest.match(/^\[([^\]]+)\]\(([^)\s]+)\)/)
    if (link && safeHref(link[2])) {
      flush()
      out.push({ kind: 'link', href: link[2], children: parseInline(link[1]) })
      index += link[0].length
      continue
    }

    const auto = rest.match(/^https?:\/\/[^\s<>)\]]+/)
    if (auto) {
      flush()
      out.push({ kind: 'link', href: auto[0], children: [{ kind: 'text', text: auto[0] }] })
      index += auto[0].length
      continue
    }

    const strong = rest.match(/^(\*\*|__)(?!\s)([\s\S]+?)(?<!\s)\1/)
    if (strong) {
      flush()
      out.push({ kind: 'strong', children: parseInline(strong[2]) })
      index += strong[0].length
      continue
    }

    // Underscore emphasis only at word boundaries so snake_case stays literal.
    const em = rest.match(/^([*_])(?![\s*_])([^*_]+?)(?<!\s)\1(?!\w)/)
    if (em && !(em[1] === '_' && index > 0 && /\w/.test(text[index - 1]))) {
      flush()
      out.push({ kind: 'em', children: parseInline(em[2]) })
      index += em[0].length
      continue
    }

    const del = rest.match(/^~~(?!\s)([\s\S]+?)(?<!\s)~~/)
    if (del) {
      flush()
      out.push({ kind: 'del', children: parseInline(del[1]) })
      index += del[0].length
      continue
    }

    plain += text[index]
    index++
  }
  flush()
  return out
}

export function safeHref(href: string): boolean {
  if (/^(https?:|mailto:)/i.test(href)) return true
  if (href.startsWith('//')) return false
  return href.startsWith('/') || href.startsWith('#')
}

const CHECKBOX = /^(\s*[-*+]\s+\[).([\]])/
const DONE_DATE = /\s*✅\s*\d{4}-\d{2}-\d{2}/

// toggleTaskLine mirrors the server-side tasks.ToggleLine convention for the
// non-recurring case: completing sets [x] and appends an Obsidian-style
// "✅ YYYY-MM-DD" done-date, unchecking clears both. Lines that are not task
// checkboxes are returned unchanged.
export function toggleTaskLine(text: string, line: number, done: boolean, today: string): string {
  const lines = text.split('\n')
  if (line < 0 || line >= lines.length || !CHECKBOX.test(lines[line])) return text
  let updated = lines[line].replace(CHECKBOX, `$1${done ? 'x' : ' '}$2`)
  if (done) {
    if (!updated.includes('✅')) updated = `${updated.trimEnd()} ✅ ${today}`
  } else {
    updated = updated.replace(DONE_DATE, '').trimEnd()
  }
  lines[line] = updated
  return lines.join('\n')
}
