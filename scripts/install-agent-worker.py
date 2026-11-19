#!/usr/bin/env python3
"""Install and start one private Slack delivery worker on macOS or Linux."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import sys


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--name', required=True)
    p.add_argument('--settings', default='~/.config/amythest/messages.json')
    args = p.parse_args()
    os.umask(0o077)
    settings = Path(args.settings).expanduser().resolve()
    runtime = Path.home() / '.local/share/amythest-agent-messages/connector.py'
    if not runtime.is_file() or not shutil.which('claude'):
        p.error('Install amy-agent-messages and sign into Claude first')
    config = json.loads(settings.read_text())
    accounts = [a for a in config.get('connectors', {}).get('agent', {}).get('accounts', []) if a.get('name') == args.name]
    if len(accounts) != 1 or accounts[0].get('provider') != 'slack' or not accounts[0].get('send') or not accounts[0].get('send_profile'):
        p.error('Choose a bound Slack account with send enabled')
    command = [sys.executable, str(runtime), '--settings', str(settings), 'watch', '--name', args.name]
    state = Path(accounts[0]['state_dir']).expanduser().resolve()
    state.mkdir(parents=True, exist_ok=True, mode=0o700)
    label = 'org.amythest.slack.' + hashlib.sha256((str(settings) + '\0' + args.name).encode()).hexdigest()[:12]
    # Keep the actual signed-in CLI and interpreter available to the service.
    path = os.environ.get('PATH', '/usr/local/bin:/usr/bin:/bin')
    if sys.platform == 'darwin':
        target = Path.home() / 'Library/LaunchAgents' / (label + '.plist')
        target.parent.mkdir(parents=True, exist_ok=True)
        data = {'Label': label, 'ProgramArguments': command, 'RunAtLoad': True, 'KeepAlive': True,
                'ThrottleInterval': 30, 'ProcessType': 'Background', 'WorkingDirectory': str(Path.home()),
                'EnvironmentVariables': {'PATH': path}, 'Umask': 0o077,
                'StandardOutPath': str(state / 'worker.log'), 'StandardErrorPath': str(state / 'worker-error.log')}
        target.write_bytes(plistlib.dumps(data)); target.chmod(0o600)
        domain = 'gui/' + str(os.getuid())
        subprocess.run(['launchctl', 'bootout', domain + '/' + label], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        subprocess.run(['launchctl', 'bootstrap', domain, str(target)], check=True)
    elif sys.platform.startswith('linux'):
        target = Path.home() / '.config/systemd/user' / (label + '.service')
        target.parent.mkdir(parents=True, exist_ok=True)
        def quote(value):
            # systemd's ExecStart quoting and specifier escaping, not shell syntax.
            return json.dumps(value.replace('%', '%%').replace('$', '$$'))
        target.write_text('[Unit]\nDescription=Amy queued Slack delivery\nAfter=network-online.target\n\n[Service]\n'
                          + 'ExecStart=' + ' '.join(map(quote, command)) + '\nEnvironment=' + quote('PATH=' + path)
                          + '\nWorkingDirectory=%h\nUMask=0077\nRestart=always\nRestartSec=30\n\n[Install]\nWantedBy=default.target\n')
        target.chmod(0o600)
        subprocess.run(['systemctl', '--user', 'daemon-reload'], check=True)
        subprocess.run(['systemctl', '--user', 'enable', target.name], check=True)
        subprocess.run(['systemctl', '--user', 'restart', target.name], check=True)
    else:
        p.error('Use amy-agent-messages watch on this platform')
    print('Installed and started ' + str(target))
    print('Only explicitly queued Slack messages are sent. The worker does not schedule imports.')


if __name__ == '__main__':
    main()
