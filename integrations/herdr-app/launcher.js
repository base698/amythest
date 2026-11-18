import { View, div } from "gpui-kit";
import { checking, hc, run, terminals, panes, closeOverlay, props, theme } from "herdr";
import { command, binary } from "./command.js";

// Start locally even when the selected tab belongs to another computer.
// Wait for Herdr's live model before adopting and focusing the terminal.
export default class Launcher extends View {
  init(_props, cx) {
    this.error = "";
    this.started = null;
    this.handedOff = false;
    this.adopting = false;
    if (checking()) return;
    this.deadline = Date.now() + 15000;
    cx.timer.every(100, (cx) => this.attach(cx));
    cx.spawn(async (cx) => {
      try {
        const name = `amythest-ui-${command}`;
        const previous = await hc(["get", name, "--json"]);
        if (previous.code === 0) {
          const terminal = JSON.parse(previous.stdout);
          if (terminal.state === "live") {
            await hc(["kill", terminal.address]);
            // --rm can remove the record before hc kill reads its final status.
            const remaining = await hc(["get", terminal.address, "--json"]);
            if (remaining.code !== 3 && (remaining.code !== 0 || JSON.parse(remaining.stdout).state === "live")) {
              throw new Error("Could not close the existing Amythest terminal.");
            }
            this.toggledOff = true;
            return;
          }
        }
        const result = await hc(["run", "--bg", "--no-parent", "--no-notify", "--rm", "--name", name, "--", binary, command]);
        if (result.code !== 0) throw new Error("Could not start Amythest. Check its configured executable.");
        this.started = JSON.parse(result.stdout).address;
        if (!this.started) throw new Error("Herdr did not return the new terminal address.");
      } catch (error) {
        this.error = String(error.message || "Could not open Amythest");
        cx.notify();
      }
    });
  }
  attach(cx) {
    if (this.toggledOff) {
      closeOverlay(props().overlay);
      return;
    }
    if (!this.started || this.handedOff || this.error) return;
    const pane = panes().find(p => p.terminal === this.started);
    if (pane) {
      this.handedOff = true;
      run("tab.rename", { id: pane.tab, name: command === "find" ? "Find" : "Messages" });
      closeOverlay(props().overlay);
      run("pane.focus", { pane: pane.pane });
    } else if (Date.now() > this.deadline) {
      this.error = "Herdr could not display the terminal. Close this popup and try again.";
      cx.notify();
    } else if (!this.adopting && terminals().some(t => t.address === this.started && t.state === "live")) {
      this.adopting = true;
      // A full tab remains usable even when the previous split is very short.
      run("terminal.keep", { terminal: this.started, place: "tab" });
    }
  }
  async close() {
    if (this.started && !this.handedOff) await hc(["kill", this.started]);
  }
  render() {
    const t = theme();
    return div().size_full().bg(t.surface).text_color(t.foreground).p(16)
      .child(this.error || "Opening Amythest…");
  }
}
