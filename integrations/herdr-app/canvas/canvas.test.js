import assert from "node:assert/strict";
import { readFile, mkdtemp, writeFile, rm, access } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFileSync } from "node:child_process";
import { test } from "node:test";
import vm from "node:vm";
import { pngDocument } from "./png.js";

// Run with node --experimental-vm-modules --test integrations/herdr-app/canvas/*.test.js
test("picker creates in Herdr by default; browser opening requires an explicit action", async () => {
  const opened = [], panes = [], calls = [], jobs = [];
  const context = vm.createContext({});
  const values = {
    "gpui-kit": { View: class {}, div() {} },
    "gpui-base": { v_flex() {}, h_flex() {}, InputState: {}, Input: {} },
    herdr: {
      exec: async (_bin, args) => { calls.push(args); return { code: 0, stdout: JSON.stringify({ id: "canvas123", url: "https://example.test/canvas/canvas123" }) }; },
      props: () => ({ overlay: "picker" }), theme() {}, appHome: () => "/tmp/test-home",
      checking: () => true, closeOverlay() {}, run: (_command, args) => panes.push(args), spaces() {}, focused() {}, writeTextFile() {},
    },
    "./config.js": { binary: "amy" },
    "./draw.js": { drawElement() {}, fit() {}, bounds() {} },
    "./png.js": { pngDocument },
    "./edit.js": Object.fromEntries(["TOOLS", "toScene", "hit", "shapeAt", "makeShape", "makeArrow", "makeStroke", "makeText", "textSize", "centerLabel", "labelOf", "setPoints", "absPoints", "wire"].map((key) => [key, () => {}])),
  };
  const mod = new vm.SourceTextModule(await readFile(new URL("./main.js", import.meta.url), "utf8"), { context });
  await mod.link((id) => new vm.SyntheticModule(Object.keys(values[id]), function () {
    for (const [name, value] of Object.entries(values[id])) this.setExport(name, value);
  }, { context }));
  await mod.evaluate();
  const canvas = new mod.namespace.default();
  const cx = { spawn: (fn) => jobs.push(fn(cx)), open_url: (url) => opened.push(url), notify() {} };
  canvas.at = 0;
  canvas.space = { name: "Test workspace" };
  canvas.all = false;
  canvas.pick(cx, false);
  canvas.pick(cx, false); // double-click while create is in flight
  await Promise.all(jobs);
  assert.equal(calls.length, 1);
  assert.deepEqual(Array.from(calls[0]), ["canvas", "new", "-json", "-workspace", "Test workspace"]);
  assert.equal(panes.length, 1);
  assert.deepEqual(opened, []);
  canvas.pick(cx, true);
  await Promise.all(jobs);
  assert.equal(opened.length, 1);
  canvas.items = [{ id: "existing", url: "https://example.test/canvas/existing" }];
  canvas.at = 1;
  canvas.pick(cx, false);
  assert.equal(panes.at(-1).props.id, "existing");
  assert.equal(opened.length, 1);
  canvas.pick(cx, true);
  assert.equal(opened.at(-1), canvas.items[0].url);
});

test("PNG includes the entire latest scene, escaping text and excluding deleted elements", () => {
  const snapshot = { elements: [
    { type: "rectangle", x: -500, y: -300, width: 100, height: 60, angle: Math.PI / 4, strokeColor: "#000000" },
    { type: "text", x: 900, y: 600, width: 180, height: 30, text: '<latest & "local">', fontSize: 20 },
    { type: "text", x: 0, y: 0, width: 1, height: 1, text: "deleted", isDeleted: true },
  ] };
  const drawing = pngDocument(snapshot, "/home/test");
  assert.match(drawing.svg, /&lt;latest &amp; &quot;local&quot;&gt;/);
  assert.doesNotMatch(drawing.svg, /deleted/);
  const [, x, y, width, height] = drawing.svg.match(/viewBox="([^ ]+) ([^ ]+) ([^ ]+) ([^"]+)"/).map(Number);
  assert.ok(x < -500 && y < -300 && x + width > 1080 && y + height > 630);
  snapshot.elements[1].text = "later edit";
  assert.doesNotMatch(drawing.svg, /later edit/); // copy snapshot does not drift while the helper runs
  assert.match(pngDocument(snapshot, "/home/test").svg, /later edit/);
  assert.doesNotThrow(() => pngDocument({ elements: [] }, "/home/test"));
  assert.throws(() => pngDocument({ elements: [{ type: "image", fileId: "not-ready" }] }, "/home/test"), /still loading/);
});

test("macOS helper copies a real PNG with embedded images to an isolated pasteboard", { skip: process.platform !== "darwin" }, async () => {
  const dir = await mkdtemp(join(tmpdir(), "canvas-png-test-"));
  const board = `amythest-canvas-test-${process.pid}`;
  try {
    const helper = join(dir, "copy-png");
    execFileSync("xcrun", ["swiftc", "-O", new URL("./copy-png.swift", import.meta.url).pathname, "-o", helper]);
    const fixture = join(dir, "red.svg");
    await writeFile(fixture, '<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><rect width="20" height="20" fill="red"/></svg>');
    const document = pngDocument({ elements: [
      { type: "image", x: 0, y: 0, width: 100, height: 100, fileId: "red" },
      { type: "rectangle", x: 150, y: 0, width: 100, height: 100, backgroundColor: "#0000ff", strokeColor: "#0000ff" },
      { type: "text", x: 0, y: 140, width: 250, height: 30, text: "Native canvas", fontSize: 24 },
    ], files: { red: fixture } }, dir);
    const input = join(dir, "drawing.json");
    await writeFile(input, JSON.stringify(document));
    const result = execFileSync(helper, [input, "--pasteboard", board], { encoding: "utf8" });
    assert.match(result, /PNG copied/);
    await assert.rejects(access(input));
    const reader = join(dir, "verify.swift");
    await writeFile(reader, `import AppKit
let board = NSPasteboard(name: NSPasteboard.Name(CommandLine.arguments[1]))
defer { board.releaseGlobally() }
guard let data = board.data(forType: .png), let image = NSBitmapImageRep(data: data) else { fatalError("No PNG on clipboard") }
var red = 0, blue = 0, dark = 0
for y in 0..<image.pixelsHigh { for x in 0..<image.pixelsWide {
  let c = image.colorAt(x: x, y: y)!.usingColorSpace(.deviceRGB)!
  if c.redComponent > 0.8 && c.greenComponent < 0.2 && c.blueComponent < 0.2 { red += 1 }
  if c.blueComponent > 0.8 && c.redComponent < 0.2 { blue += 1 }
  if c.redComponent < 0.3 && c.greenComponent < 0.3 && c.blueComponent < 0.3 { dark += 1 }
} }
assert(red > 1000, "Embedded image missing")
assert(blue > 1000, "Native shape missing")
assert(dark > 100, "Native text missing")
print("Verified image, shape and text pixels")
`);
    assert.match(execFileSync("swift", [reader, board], { encoding: "utf8" }), /Verified/);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
