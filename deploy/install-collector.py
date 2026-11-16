#!/usr/bin/env python3
"""Install the local Amythest Collector sync service without changing existing watch services."""
import argparse
from datetime import datetime, timezone
import os
from pathlib import Path
import platform
import plistlib
import shutil
import subprocess
import time
from collector_migration import migrate

p=argparse.ArgumentParser()
p.add_argument('--binary',required=True)
p.add_argument('--skip-hermes-panes',action='store_true')
p.add_argument('--capture-only',action='store_true')
p.add_argument('--migrate-corral',action='store_true',help='stop and retire the old sender, preserving its queue and credential')
a=p.parse_args()
root=Path.home();state=root/'.local/state/amythest-collector'
if not Path(a.binary).is_file():p.error('collector binary not found')
if a.migrate_corral:migrate(root,state,platform.system())
state.mkdir(parents=True,exist_ok=True,mode=0o700);state.chmod(0o700)
if not a.capture_only and not (state/'credential.json').exists():p.error('register a collector or provide credential.json first')
backup=state/('install-'+datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ'));backup.mkdir(mode=0o700)
binary=root/'.local/bin/amythest-collector';binary.parent.mkdir(parents=True,exist_ok=True)
if binary.exists():shutil.copy2(binary,backup/'amythest-collector')
shutil.copy2(a.binary,binary.with_suffix('.new'));binary.with_suffix('.new').chmod(0o755);os.replace(binary.with_suffix('.new'),binary)
args=[str(binary),'sync']
if a.skip_hermes_panes:args.append('--skip-hermes-panes')
if a.capture_only:args.append('--capture-only')
if platform.system()=='Darwin':
 label='dev.amythest.collector';unit=root/'Library/LaunchAgents'/f'{label}.plist';unit.parent.mkdir(parents=True,exist_ok=True)
 if unit.exists():shutil.copy2(unit,backup/unit.name)
 logs=root/'Library/Logs';logs.mkdir(exist_ok=True)
 data={'Label':label,'ProgramArguments':args,'RunAtLoad':True,'KeepAlive':True,'ThrottleInterval':10,'EnvironmentVariables':{'PATH':f'{root}/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin','AMYTHEST_COLLECTOR_STATE':str(state)},'StandardOutPath':str(logs/'amythest-collector-sync.log'),'StandardErrorPath':str(logs/'amythest-collector-sync.log')}
 unit.write_bytes(plistlib.dumps(data))
 subprocess.run(['launchctl','bootout',f'gui/{os.getuid()}/{label}'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 # bootout can return before launchd finishes removing a running job.
 for _ in range(50):
  if subprocess.run(['launchctl','print',f'gui/{os.getuid()}/{label}'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode != 0:break
  time.sleep(0.1)
 else:raise RuntimeError('previous Amythest Collector service has not stopped')
 subprocess.run(['launchctl','bootstrap',f'gui/{os.getuid()}',str(unit)],check=True)
else:
 unit=root/'.config/systemd/user/amythest-collector-sync.service';unit.parent.mkdir(parents=True,exist_ok=True)
 if unit.exists():shutil.copy2(unit,backup/unit.name)
 unit.write_text('[Unit]\nDescription=Amythest Collector Work lifecycle collector\nAfter=network-online.target\n\n[Service]\nExecStart='+ ' '.join(args)+'\nEnvironment="PATH='+str(root)+'/.local/bin:/usr/local/bin:/usr/bin:/bin"\nEnvironment="AMYTHEST_COLLECTOR_STATE='+str(state)+'"\nRestart=on-failure\nRestartSec=10\nUMask=0077\n\n[Install]\nWantedBy=default.target\n')
 subprocess.run(['systemctl','--user','daemon-reload'],check=True)
 subprocess.run(['systemctl','--user','enable','--now','amythest-collector-sync'],check=True)
 subprocess.run(['systemctl','--user','restart','amythest-collector-sync'],check=True)
print('Installed Amythest Collector sync service; previous files saved in',backup)
