import test from 'node:test'
import assert from 'node:assert/strict'
import { parseInline, parseMarkdown, safeHref, toggleTaskLine, type Block } from './markdown.ts'

test('headings, paragraphs, and rules', () => {
  const blocks = parseMarkdown('# Title\n\nFirst line\nsecond line\n\n---\n\nAfter')
  assert.equal(blocks.length, 4)
  assert.deepEqual(blocks[0], { kind: 'heading', level: 1, content: [{ kind: 'text', text: 'Title' }] })
  assert.equal(blocks[1].kind, 'paragraph')
  // Single newlines inside a paragraph become hard breaks.
  assert.deepEqual((blocks[1] as Extract<Block, { kind: 'paragraph' }>).content, [
    { kind: 'text', text: 'First line' }, { kind: 'br' }, { kind: 'text', text: 'second line' },
  ])
  assert.equal(blocks[2].kind, 'hr')
  assert.equal(blocks[3].kind, 'paragraph')
})

test('task items carry their 0-based source line', () => {
  const blocks = parseMarkdown('Checklist:\n\n- [ ] water the ferns\n- [x] done already ✅ 2026-08-01\n- plain item')
  const list = blocks[1] as Extract<Block, { kind: 'list' }>
  assert.equal(list.kind, 'list')
  assert.equal(list.items.length, 3)
  assert.deepEqual(list.items[0].task, { checked: false, line: 2 })
  assert.deepEqual(list.items[1].task, { checked: true, line: 3 })
  assert.equal(list.items[2].task, undefined)
})

test('nested lists attach to the parent item', () => {
  const blocks = parseMarkdown('- parent\n  - [ ] child task\n- sibling')
  const list = blocks[0] as Extract<Block, { kind: 'list' }>
  assert.equal(list.items.length, 2)
  const child = list.items[0].children[0] as Extract<Block, { kind: 'list' }>
  assert.equal(child.kind, 'list')
  assert.deepEqual(child.items[0].task, { checked: false, line: 1 })
})

test('ordered lists and code fences', () => {
  const blocks = parseMarkdown('1. one\n2. two\n\n```sh\nls -la\n```')
  assert.equal((blocks[0] as Extract<Block, { kind: 'list' }>).ordered, true)
  assert.deepEqual(blocks[1], { kind: 'code', lang: 'sh', text: 'ls -la' })
})

test('blockquotes parse their contents', () => {
  const blocks = parseMarkdown('> quoted **bold**')
  const quote = blocks[0] as Extract<Block, { kind: 'quote' }>
  assert.equal(quote.kind, 'quote')
  assert.equal(quote.children[0].kind, 'paragraph')
})

test('inline markup', () => {
  assert.deepEqual(parseInline('**bold** and *em* and `code`'), [
    { kind: 'strong', children: [{ kind: 'text', text: 'bold' }] },
    { kind: 'text', text: ' and ' },
    { kind: 'em', children: [{ kind: 'text', text: 'em' }] },
    { kind: 'text', text: ' and ' },
    { kind: 'code', text: 'code' },
  ])
  assert.deepEqual(parseInline('~~gone~~'), [{ kind: 'del', children: [{ kind: 'text', text: 'gone' }] }])
})

test('links: markdown, bare URLs, and unsafe schemes', () => {
  assert.deepEqual(parseInline('[docs](https://example.com/a)'), [
    { kind: 'link', href: 'https://example.com/a', children: [{ kind: 'text', text: 'docs' }] },
  ])
  assert.deepEqual(parseInline('see https://example.com now'), [
    { kind: 'text', text: 'see ' },
    { kind: 'link', href: 'https://example.com', children: [{ kind: 'text', text: 'https://example.com' }] },
    { kind: 'text', text: ' now' },
  ])
  // javascript: links render as literal text, not anchors.
  const unsafe = parseInline('[x](javascript:alert(1))')
  assert.ok(unsafe.every((node) => node.kind !== 'link'))
})

test('safeHref accepts http(s), mailto, relative; rejects the rest', () => {
  assert.ok(safeHref('https://a.example'))
  assert.ok(safeHref('http://a.example'))
  assert.ok(safeHref('mailto:a@example.com'))
  assert.ok(safeHref('/notes/foo'))
  assert.ok(safeHref('#anchor'))
  assert.ok(!safeHref('javascript:alert(1)'))
  assert.ok(!safeHref('data:text/html,x'))
  assert.ok(!safeHref('//evil.example'))
})

test('unmatched markers stay literal', () => {
  assert.deepEqual(parseInline('2 * 3 * 4'), [{ kind: 'text', text: '2 * 3 * 4' }])
  assert.deepEqual(parseInline('snake_case_name'), [{ kind: 'text', text: 'snake_case_name' }])
})

test('toggleTaskLine completes with a done-date', () => {
  const body = 'Steps:\n- [ ] water the ferns\n- [ ] second'
  assert.equal(
    toggleTaskLine(body, 1, true, '2026-08-07'),
    'Steps:\n- [x] water the ferns ✅ 2026-08-07\n- [ ] second',
  )
})

test('toggleTaskLine does not duplicate an existing done-date', () => {
  const body = '- [ ] item ✅ 2026-08-01'
  assert.equal(toggleTaskLine(body, 0, true, '2026-08-07'), '- [x] item ✅ 2026-08-01')
})

test('toggleTaskLine unchecks and strips the done-date', () => {
  const body = '- [x] item ✅ 2026-08-01'
  assert.equal(toggleTaskLine(body, 0, false, '2026-08-07'), '- [ ] item')
})

test('toggleTaskLine leaves non-task lines untouched', () => {
  assert.equal(toggleTaskLine('just text', 0, true, '2026-08-07'), 'just text')
  assert.equal(toggleTaskLine('- [ ] fine', 5, true, '2026-08-07'), '- [ ] fine')
})

test('indented tasks keep the correct source line', () => {
  const body = '# Card\n\n- [ ] top\n  - [ ] nested\n- [ ] last'
  assert.equal(
    toggleTaskLine(body, 3, true, '2026-08-07'),
    '# Card\n\n- [ ] top\n  - [x] nested ✅ 2026-08-07\n- [ ] last',
  )
})
