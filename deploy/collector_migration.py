"""Migrate the retired collector without changing its credential or stream identity."""
from datetime import datetime, timezone
import fcntl
import os
from pathlib import Path
import shutil
import subprocess
import time


def migrate(root, state, system, run=subprocess.run):
    old = root / '.local/state/corral'
    if old.is_symlink() and old.resolve() == state.resolve():
        return
    if state.exists():
        raise RuntimeError('new collector state already exists; refusing to merge queues')
    if not old.is_dir():
        raise RuntimeError('legacy collector state not found')
    if system == 'Darwin':
        run(['launchctl', 'bootout', f'gui/{os.getuid()}/dev.base698.corral.sync'],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        unit = root / 'Library/LaunchAgents/dev.base698.corral.sync.plist'
    else:
        run(['systemctl', '--user', 'disable', '--now', 'corral-sync'], check=True)
        unit = root / '.config/systemd/user/corral-sync.service'
    # Service shutdown may be asynchronous. The queue's own lock proves that its
    # old writer has stopped before any state is moved or a new writer starts.
    with (old / 'hub.lock').open('a') as lock:
        for _ in range(100):
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                time.sleep(0.1)
        else:
            raise RuntimeError('legacy collector is still writing; migration stopped')
        old.rename(state)
        state.chmod(0o700)
        old.symlink_to(state, target_is_directory=True)
        backup = state / ('migration-' + datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ'))
        backup.mkdir(mode=0o700)
        for source in [root / '.local/bin/corral', unit]:
            if source.exists():
                shutil.move(str(source), backup / source.name)
        if system != 'Darwin':
            run(['systemctl', '--user', 'daemon-reload'], check=True)
