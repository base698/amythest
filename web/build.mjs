import * as esbuild from "esbuild"
import { sassPlugin } from "esbuild-sass-plugin"
import { cp, readdir } from "node:fs/promises"

const watch = process.argv.includes("--watch")

// Notes site bundle.
const site = await esbuild.context({
  entryPoints: ["web/src/main.ts", "web/styles/main.scss"],
  bundle: true,
  minify: !watch,
  sourcemap: watch,
  format: "esm",
  splitting: true, // mermaid loads as a separate chunk on demand
  outdir: "web/dist",
  outbase: "web",
  logLevel: "info",
  plugins: [sassPlugin()],
})

// Kanban React SPA, served under /kanban/.
const kanban = await esbuild.context({
  entryPoints: ["web/kanban-ui/main.tsx"],
  bundle: true,
  minify: !watch,
  sourcemap: watch,
  format: "esm",
  outdir: "web/dist/kanban-ui",
  logLevel: "info",
  jsx: "automatic",
})

// Canvas editor (Excalidraw), served under /static/canvas-ui/. Split so the
// Mermaid converter only loads when something is drawn from Mermaid.
const canvas = await esbuild.context({
  entryPoints: { main: "web/canvas-ui/main.tsx" },
  bundle: true,
  minify: !watch,
  sourcemap: watch,
  format: "esm",
  splitting: true,
  target: "es2022",
  outdir: "web/dist/canvas-ui",
  logLevel: "info",
  jsx: "automatic",
  conditions: [watch ? "development" : "production"],
  define: { "process.env.NODE_ENV": watch ? '"development"' : '"production"', "process.env.IS_PREACT": '"false"' },
  loader: { ".woff2": "file", ".ttf": "file", ".png": "file", ".svg": "file" },
})

// Excalidraw loads its hand-drawn fonts from EXCALIDRAW_ASSET_PATH. Ship them
// so nothing is fetched from a CDN; Xiaolai (12 MB of CJK glyphs) is left out
// and falls back to the system font.
async function copyCanvasFonts() {
  const src = "node_modules/@excalidraw/excalidraw/dist/prod/fonts"
  for (const family of await readdir(src)) {
    if (family === "Xiaolai") continue
    await cp(`${src}/${family}`, `web/dist/canvas-ui/fonts/${family}`, { recursive: true })
  }
}

async function copyStatic() {
  await cp("web/kanban-ui/index.html", "web/dist/kanban-ui/index.html")
  await copyCanvasFonts()
}

if (watch) {
  await site.watch()
  await kanban.watch()
  await canvas.watch()
  await copyStatic()
} else {
  await site.rebuild()
  await kanban.rebuild()
  await canvas.rebuild()
  await copyStatic()
  await site.dispose()
  await kanban.dispose()
  await canvas.dispose()
}
