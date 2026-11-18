#!/usr/bin/env python3
"""Install personal Herdr workspace, quick-add, Find, Messages and Canvas shortcuts."""
import argparse
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default=str(Path.home() / "bin/amy"))
    args = parser.parse_args()
    binary = Path(args.binary).expanduser().resolve()
    if not binary.is_file() or not os.access(binary, os.X_OK):
        parser.error("Build/install amy before installing shortcuts")
    root = Path(__file__).resolve().parent.parent
    home = Path(os.getenv("HERDR_APP_HOME", Path.home() / ".config/herdr-app"))
    keys = home / "keys.json"
    value = json.loads(keys.read_text())
    for command in ("find", "messages", "workspace", "add"):
        folder = home / "mods" / ("amythest-" + command)
        folder.mkdir(parents=True, exist_ok=True)
        shutil.copy2(root / "integrations/herdr-app/launcher.js", folder / "main.js")
        helper = folder / "return-to-origin.py"
        shutil.copy2(root / "integrations/herdr-app/return-to-origin.py", helper)
        helper.chmod(0o755)
        (folder / "command.js").write_text(
            "export const command = " + json.dumps(command) + ";\n"
            "export const binary = " + json.dumps(str(binary)) + ";\n"
            "export const helper = " + json.dumps(str(helper)) + ";\n"
        )
        spec = {"place": "none", "title": command.title(),
                "overlay": {"kind": "popup", "width": 480, "height": 90}}
        (folder / "mod.json").write_text(json.dumps(spec, indent=2) + "\n")
        subprocess.run(["herdr-app", "check", str(folder)], check=True)
    # The canvas mod: Cmd+Shift+C picker, and native canvas panes beside terminals.
    canvas = home / "mods" / "amythest-canvas"
    canvas.mkdir(parents=True, exist_ok=True)
    for name in ("main.js", "draw.js", "edit.js", "mod.json"):
        shutil.copy2(root / "integrations/herdr-app/canvas" / name, canvas / name)
    (canvas / "config.js").write_text("export const binary = " + json.dumps(str(binary)) + ";\n")
    subprocess.run(["herdr-app", "check", str(canvas)], check=True)
    shutil.copy2(keys, keys.with_name("keys.json.before-amythest-" + str(time.time_ns())))
    value.setdefault("global", {}).update({
        "cmd-shift-c": "mod.open:amythest-canvas",
        "cmd-shift-f": "mod.open:amythest-find",
        "cmd-shift-m": "mod.open:amythest-messages",
        "cmd-shift-a": "mod.open:amythest-workspace",
    })
    # Preserve the user's prefix commands in a personal clone. Shipped mods
    # can be replaced on upgrade, so never edit the source mod in place.
    prefix = value.get("global", {}).get("ctrl-b", "")
    if isinstance(prefix, str) and prefix.startswith("mod.open:"):
        original = prefix.split(":", 1)[1]
        target = home / "mods/amythest-prefix"
        if original != "amythest-prefix" and not target.exists():
            subprocess.run(["herdr-app", "do", "mod.clone", json.dumps({"name": original, "as": "amythest-prefix"})], check=True)
        source = target / "main.js"
        if source.is_file():
            text = source.read_text()
            if "const KEYS = {" in text and "const SHEET = [" in text:
                for key, mod in (("a", "add"), ("A", "workspace"), ("f", "find")):
                    text = re.sub(r"^  " + key + r":.*\n", "", text, flags=re.M)
                    text = text.replace("const KEYS = {", "const KEYS = {\n  " + key + ': () => run("mod.open", { name: "amythest-' + mod + '" }),', 1)
                text = re.sub(r'^  \["Amythest",.*\n', "", text, flags=re.M)
                text = text.replace("const SHEET = [", 'const SHEET = [\n  ["Amythest", 36, [["a", "add task"], ["A", "workspace"], ["f", "find"]]],', 1)
                source.write_text(text)
                subprocess.run(["herdr-app", "check", str(target)], check=True)
                value["global"]["ctrl-b"] = "mod.open:amythest-prefix"
            else:
                print("Prefix mod uses a different key format; bind its add action to mod.open:amythest-add.")
    temporary = keys.with_name("keys.json.amythest-next")
    temporary.write_text(json.dumps(value, indent=2) + "\n")
    temporary.replace(keys)
    print("Installed Amythest workspace, Find, Messages, Canvas, and quick-add shortcuts.")


if __name__ == "__main__":
    main()
