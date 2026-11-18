import { View, div } from "gpui-kit";
import { checking, hc, run, terminals, panes, closeOverlay, props, theme, focused, spaces } from "herdr";
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
    const context = focused();
    const space = spaces().find(s => s.id === context?.space);
    this.workspace = space?.name || "";
    this.scopeKey = space?.id || "";
    this.title = {workspace: "Amythest", add: "Add task", find: "Find", messages: "Messages"}[command] || "Amythest";
    this.deadline = Date.now() + 15000;
    cx.timer.every(100, (cx) => this.attach(cx));
    cx.spawn(async (cx) => {
      try {
        const scoped = command === "workspace";
        // hc names are limited to 32 lowercase characters. The space ID's
        // random suffix keeps one dashboard per workspace within that limit.
        if (scoped && !this.workspace) throw new Error("Select a Herdr workspace first.");
        const name = scoped ? `amy-workspace-${this.scopeKey.slice(-18)}` : `amythest-ui-${command}`;
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
        const args = [binary, command];
        if ((scoped || command === "add") && this.workspace) args.push("-workspace", this.workspace);
        const labels = scoped ? ["--label", "amythest.workspace=" + this.scopeKey] : [];
        const result = await hc(["run", "--bg", "--no-parent", "--no-notify", "--rm", "--name", name, ...labels, "--", ...args]);
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
      run("tab.rename", { id: pane.tab, name: command === "workspace" ? this.title + " · " + this.workspace : this.title });
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
