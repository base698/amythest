// Amythest client entrypoint. Feature islands re-run after SPA navigation.
import { setupDarkmode } from "./darkmode"
import { setupExplorer } from "./explorer"
import { setupSearch } from "./search"
import { setupGraph } from "./graph"
import { setupPopovers } from "./popover"
import { setupShare } from "./share"
import { setupTaskToggles, setupTaskTriage } from "./tasks"
import { setupSPA } from "./spa"

function init() {
  setupDarkmode()
  setupExplorer()
  setupSearch()
  setupGraph()
  setupPopovers()
  setupTaskToggles()
  setupTaskTriage()
  setupShare()
  if (document.querySelector("pre.mermaid")) {
    void import("./mermaid").then((m) => m.renderMermaid())
  }
}

init()
setupSPA(init)
