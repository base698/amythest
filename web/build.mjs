import * as esbuild from "esbuild"
import { sassPlugin } from "esbuild-sass-plugin"
import { cp } from "node:fs/promises"

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

async function copyStatic() {
  await cp("web/kanban-ui/index.html", "web/dist/kanban-ui/index.html")
}

if (watch) {
  await site.watch()
  await kanban.watch()
  await copyStatic()
} else {
  await site.rebuild()
  await kanban.rebuild()
  await copyStatic()
  await site.dispose()
  await kanban.dispose()
}
