import test from "node:test"
import assert from "node:assert/strict"
import { parseSTL } from "./stl.ts"

// One triangle, written the way a binary exporter would.
function binarySTL(triangles: number[][][], normals?: number[][]): ArrayBuffer {
  const buf = new ArrayBuffer(84 + triangles.length * 50)
  const view = new DataView(buf)
  view.setUint32(80, triangles.length, true)
  let o = 84
  triangles.forEach((tri, i) => {
    const n = normals?.[i] ?? [0, 0, 0]
    n.forEach((v, k) => view.setFloat32(o + k * 4, v, true))
    o += 12
    for (const vertex of tri) {
      vertex.forEach((v, k) => view.setFloat32(o + k * 4, v, true))
      o += 12
    }
    o += 2
  })
  return buf
}

test("parses a binary STL", () => {
  const mesh = parseSTL(binarySTL([[[0, 0, 0], [1, 0, 0], [0, 1, 0]]], [[0, 0, 1]]))
  assert.equal(mesh.triangles, 1)
  assert.deepEqual([...mesh.positions], [0, 0, 0, 1, 0, 0, 0, 1, 0])
  assert.deepEqual([...mesh.normals], [0, 0, 1, 0, 0, 1, 0, 0, 1])
})

test("parses an ASCII STL", () => {
  const text = `solid demo
facet normal 0 0 1
  outer loop
    vertex 0 0 0
    vertex 2 0 0
    vertex 0 2 0
  endloop
endfacet
endsolid demo`
  const mesh = parseSTL(new TextEncoder().encode(text).buffer as ArrayBuffer)
  assert.equal(mesh.triangles, 1)
  assert.deepEqual([...mesh.positions], [0, 0, 0, 2, 0, 0, 0, 2, 0])
  assert.deepEqual([...mesh.normals], [0, 0, 1, 0, 0, 1, 0, 0, 1])
})

// A binary file may begin with "solid", so the ASCII sniff must not win on
// the header alone — the triangle-count arithmetic decides.
test("binary file whose header starts with solid is still read as binary", () => {
  const buf = binarySTL([[[0, 0, 0], [1, 0, 0], [0, 1, 0]]], [[0, 0, 1]])
  new Uint8Array(buf).set(new TextEncoder().encode("solid exported-by-something"), 0)
  const mesh = parseSTL(buf)
  assert.equal(mesh.triangles, 1)
  assert.deepEqual([...mesh.positions], [0, 0, 0, 1, 0, 0, 0, 1, 0])
})

// Exporters write zero normals more often than you would hope; a zero
// normal shades as a black facet, so the parser derives one.
test("zero normals are replaced with the geometric normal", () => {
  const mesh = parseSTL(binarySTL([[[0, 0, 0], [1, 0, 0], [0, 1, 0]]], [[0, 0, 0]]))
  assert.deepEqual([...mesh.normals.slice(0, 3)], [0, 0, 1])
})

test("a non-zero normal is left alone", () => {
  const mesh = parseSTL(binarySTL([[[0, 0, 0], [1, 0, 0], [0, 1, 0]]], [[0, 1, 0]]))
  assert.deepEqual([...mesh.normals.slice(0, 3)], [0, 1, 0])
})

test("an empty mesh parses to zero triangles rather than throwing", () => {
  assert.equal(parseSTL(binarySTL([])).triangles, 0)
  assert.equal(parseSTL(new TextEncoder().encode("solid x\nendsolid x").buffer as ArrayBuffer).triangles, 0)
})

// A truncated ASCII facet must not emit a partial triangle.
test("a trailing partial facet is dropped", () => {
  const text = `solid demo
facet normal 0 0 1
  outer loop
    vertex 0 0 0
    vertex 1 0 0
    vertex 0 1 0
  endloop
endfacet
facet normal 0 0 1
  outer loop
    vertex 5 5 5
`
  const mesh = parseSTL(new TextEncoder().encode(text).buffer as ArrayBuffer)
  assert.equal(mesh.triangles, 1)
  assert.equal(mesh.positions.length, 9)
})
