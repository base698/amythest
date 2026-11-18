#!/usr/bin/env python3
"""Run a command in its own Herdr tab and, when it ends, go back to where it was launched.

Herdr shows the neighbouring tab when a tab closes, so quitting Find, Messages or the
workspace dashboard used to land on whatever tab sat beside it. This wrapper waits for
the command, then, only if its own tab is still the one on screen (the person did not
jump somewhere else, e.g. to a Find result), focuses the launching pane again.

Usage: return-to-origin.py --origin-pane P --origin-tab T -- command [args...]
"""
import json
import os
import shutil
import signal
import subprocess
import sys


def herdr(*args):
    exe = shutil.which("herdr-app") or os.path.expanduser("~/.local/bin/herdr-app")
    return subprocess.run([exe, *args], capture_output=True, text=True, timeout=5)


def restore(origin):
    me = os.environ.get("HC_TERMINAL_ID")
    if not me or not (origin.get("pane") or origin.get("tab")):
        return
    listing = herdr("ls")
    if listing.returncode != 0:
        return
    for space in json.loads(listing.stdout).get("spaces", []):
        for tab in space.get("tabs", []):
            if not any(t.get("terminal") == me for t in tab.get("terminals", [])):
                continue
            # Somewhere else is on screen: the person moved on, leave them there.
            if not (space.get("active") and tab.get("active")) or origin.get("tab") == tab.get("id"):
                return
            if origin.get("pane") and herdr("do", "pane.focus", "--pane", origin["pane"]).returncode == 0:
                return
            if origin.get("tab"):
                herdr("do", "tab.select", "--id", origin["tab"])
            return


def main():
    argv, origin = sys.argv[1:], {}
    while len(argv) >= 2 and argv[0].startswith("--origin-"):
        if argv[1]:
            origin[argv[0][len("--origin-"):]] = argv[1]
        argv = argv[2:]
    if argv and argv[0] == "--":
        argv = argv[1:]
    if not argv:
        sys.exit("usage: return-to-origin.py --origin-pane P --origin-tab T -- command [args...]")

    # Ctrl+C reaches the command through the terminal; don't let it kill the wrapper first.
    signal.signal(signal.SIGINT, signal.SIG_IGN)
    child = subprocess.Popen(argv)

    def forward(sig, _frame):
        try:
            child.send_signal(sig)
        except ProcessLookupError:
            pass

    # Toggling the shortcut again kills the terminal: still restore after the command stops.
    signal.signal(signal.SIGTERM, forward)
    signal.signal(signal.SIGHUP, forward)
    code = child.wait()
    try:
        restore(origin)
    except Exception:
        pass
    sys.exit(code if code >= 0 else 128 - code)


if __name__ == "__main__":
    main()
