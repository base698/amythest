export interface SubgraphBox {
  label: string
  tokens: string[]
  height: number // nesting levels below this one
}

// flattenSubgraphs removes subgraph blocks from a Mermaid definition and
// records which identifiers each one held. mermaid-to-excalidraw 2.2.2 cannot
// find subgraph elements in Mermaid 11.16's SVG and falls back to a flat image
// for any diagram that has one; without the blocks the flowchart converts to
// real shapes, and each subgraph is drawn back as a labeled box around them.
export function flattenSubgraphs(def: string): { source: string; groups: SubgraphBox[] } {
  const out: string[] = []
  const stack: SubgraphBox[] = []
  const groups: SubgraphBox[] = []
  for (const line of def.split(/\r?\n/)) {
    const t = line.trim()
    const open = /^subgraph\s+(.+)$/i.exec(t)
    if (open) {
      const spec = open[1].trim()
      const m = /^([\w-]+)\s*\[(.*)\]$/.exec(spec) || /^"(.*)"$/.exec(spec)
      const label = m ? (m.length === 3 ? m[2] : m[1]).replace(/^"|"$/g, "") : spec
      stack.push({ label, tokens: [], height: 0 })
      continue
    }
    if (/^end$/i.test(t) && stack.length) {
      const g = stack.pop()!
      groups.push(g)
      const parent = stack[stack.length - 1]
      if (parent) {
        parent.tokens.push(...g.tokens)
        parent.height = Math.max(parent.height, g.height + 1)
      }
      continue
    }
    if (stack.length && /^direction\s/i.test(t)) continue
    out.push(line)
    if (stack.length) {
      let bare = t.replace(/"[^"]*"/g, " ").replace(/\|[^|]*\|/g, " ")
      for (let prev = ""; prev !== bare; ) {
        prev = bare
        bare = bare.replace(/[\[({][^\[\](){}]*[\])}]/g, " ")
      }
      stack[stack.length - 1].tokens.push(...(bare.match(/[A-Za-z_]\w*/g) || []))
    }
  }
  return { source: out.join("\n"), groups }
}
