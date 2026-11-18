#!/usr/bin/env python3
"""Install personal Herdr app mods and Find/Messages toggle keys."""
import argparse
import json
import os
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
    for command in ("find", "messages"):
        folder = home / "mods" / ("amythest-" + command)
        folder.mkdir(parents=True, exist_ok=True)
        shutil.copy2(root / "integrations/herdr-app/launcher.js", folder / "main.js")
        (folder / "command.js").write_text(
            "export const command = " + json.dumps(command) + ";\n"
            "export const binary = " + json.dumps(str(binary)) + ";\n"
        )
        spec = {"place": "none", "title": command.title(),
                "overlay": {"kind": "popup", "width": 480, "height": 90}}
        (folder / "mod.json").write_text(json.dumps(spec, indent=2) + "\n")
        subprocess.run(["herdr-app", "check", str(folder)], check=True)
    shutil.copy2(keys, keys.with_name("keys.json.before-amythest-" + str(time.time_ns())))
    value.setdefault("global", {}).update({
        "cmd-shift-f": "mod.open:amythest-find",
        "cmd-shift-m": "mod.open:amythest-messages",
    })
    temporary = keys.with_name("keys.json.amythest-next")
    temporary.write_text(json.dumps(value, indent=2) + "\n")
    temporary.replace(keys)
    print("Installed Cmd+Shift+F and Cmd+Shift+M toggles. Local settings also work over remote tabs.")


if __name__ == "__main__":
    main()
