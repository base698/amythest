#!/usr/bin/env python3
"""Opt one existing Codex chat into Amythest Collector's metadata-only lifecycle reader."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import tempfile

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--rollout', required=True, type=Path)
p.add_argument('--session', required=True)
p.add_argument('--project', default='')
p.add_argument('--title', default='Codex session')
a = p.parse_args()
path = a.rollout.expanduser().resolve(strict=True)
with path.open() as f:
    meta = json.loads(f.readline(65536))
if meta.get('type') != 'session_meta' or meta.get('payload', {}).get('id') != a.session:
    p.error('rollout identity does not match the requested session')
if not a.session or len(a.session.encode()) > 200 or len(a.title.encode()) > 100 or len(a.project.encode()) > 200:
    p.error('session, title or project exceeds the collector limit')
root = Path(os.environ.get('AMYTHEST_COLLECTOR_STATE', Path.home() / '.local/state/amythest-collector'))
root.mkdir(mode=0o700, parents=True, exist_ok=True)
config = root / 'codex.json'
with (root / 'codex-config.lock').open('a') as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    watches = json.loads(config.read_text()) if config.exists() else []
    watches = [w for w in watches if w['session_id'] != a.session]
    watches.append({'session_id': a.session, 'path': str(path), 'project': a.project, 'title': a.title})
    fd, temporary = tempfile.mkstemp(dir=root, prefix='.codex-')
    try:
        with os.fdopen(fd, 'w') as f:
            json.dump(watches, f, indent=2)
            f.flush()
            os.fsync(f.fileno())
        os.replace(temporary, config)
        directory = os.open(root, os.O_RDONLY)
        try: os.fsync(directory)
        finally: os.close(directory)
    finally:
        if os.path.exists(temporary): os.unlink(temporary)
print('Codex session enabled. The running Amythest Collector sync service reloads this list automatically.')
