import { test } from "node:test"
import assert from "node:assert/strict"
import { flattenSubgraphs } from "./subgraphs.ts"

test("removes subgraph blocks and records their members", () => {
  const { source, groups } = flattenSubgraphs(
    [
      "flowchart LR",
      "  subgraph M1[Each machine]",
      "    direction TB",
      "    H[herdr socket] --> C[collector]",
      "    subgraph inner [Queue side]",
      "      C --> Q[(queue: A B)]",
      "    end",
      "  end",
      "  Q -->|HTTPS| A[API]",
    ].join("\n"),
  )
  assert.equal(source, ["flowchart LR", "    H[herdr socket] --> C[collector]", "      C --> Q[(queue: A B)]", "  Q -->|HTTPS| A[API]"].join("\n"))
  assert.equal(groups.length, 2)
  assert.deepEqual(groups[0], { label: "Queue side", tokens: ["C", "Q"], height: 0 })
  assert.equal(groups[1].label, "Each machine")
  assert.deepEqual(new Set(groups[1].tokens), new Set(["H", "C", "Q"]))
  assert.equal(groups[1].height, 1)
})

test("quoted and bare subgraph titles", () => {
  assert.equal(flattenSubgraphs('graph TD\nsubgraph "Edge tier"\na-->b\nend').groups[0].label, "Edge tier")
  assert.equal(flattenSubgraphs("graph TD\nsubgraph Backend\na-->b\nend").groups[0].label, "Backend")
})
