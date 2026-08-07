import { createElement, Fragment, type ReactNode } from 'react'
import { parseMarkdown, type Block, type Inline, type ListItem } from './markdown'

// Renders card-description markdown. When onToggleTask is given, task
// checkboxes are live and report the 0-based source line of the clicked task.
export function Markdown({ text, onToggleTask }: {
  text: string
  onToggleTask?: (line: number, done: boolean) => void
}) {
  return <div className="markdown-body">{parseMarkdown(text).map((block, index) => <BlockNode block={block} key={index} onToggleTask={onToggleTask} />)}</div>
}

function BlockNode({ block, onToggleTask }: { block: Block; onToggleTask?: (line: number, done: boolean) => void }) {
  switch (block.kind) {
    case 'heading':
      return createElement(`h${Math.min(block.level + 2, 6)}`, { className: `md-h${block.level}` }, renderInlines(block.content))
    case 'paragraph':
      return <p>{renderInlines(block.content)}</p>
    case 'code':
      return <pre><code>{block.text}</code></pre>
    case 'quote':
      return <blockquote>{block.children.map((child, index) => <BlockNode block={child} key={index} onToggleTask={onToggleTask} />)}</blockquote>
    case 'hr':
      return <hr />
    case 'list': {
      const Tag = block.ordered ? 'ol' : 'ul'
      return <Tag>{block.items.map((item, index) => <ListItemNode item={item} key={index} onToggleTask={onToggleTask} />)}</Tag>
    }
  }
}

function ListItemNode({ item, onToggleTask }: { item: ListItem; onToggleTask?: (line: number, done: boolean) => void }) {
  const children = item.children.map((child, index) => <BlockNode block={child} key={index} onToggleTask={onToggleTask} />)
  if (!item.task) return <li>{renderInlines(item.content)}{children}</li>
  const { checked, line } = item.task
  return <li className={`md-task${checked ? ' done' : ''}`}>
    <label>
      <input
        checked={checked}
        disabled={!onToggleTask}
        onChange={(event) => onToggleTask?.(line, event.target.checked)}
        type="checkbox"
      />
      <span>{renderInlines(item.content)}</span>
    </label>
    {children}
  </li>
}

function renderInlines(inlines: Inline[]): ReactNode {
  return inlines.map((inline, index) => <Fragment key={index}>{renderInline(inline)}</Fragment>)
}

function renderInline(inline: Inline): ReactNode {
  switch (inline.kind) {
    case 'text': return inline.text
    case 'br': return <br />
    case 'code': return <code>{inline.text}</code>
    case 'link': return <a href={inline.href} rel="noreferrer" target="_blank">{renderInlines(inline.children)}</a>
    case 'strong': return <strong>{renderInlines(inline.children)}</strong>
    case 'em': return <em>{renderInlines(inline.children)}</em>
    case 'del': return <del>{renderInlines(inline.children)}</del>
  }
}
