#!/usr/bin/env bash
# Optional user-owned Linux ARM64 runtime; no root/system Java changes.
# Other platforms can install signal-cli normally and configure signal.binary.
set -euo pipefail
if [[ "$(uname -s)/$(uname -m)" != Linux/aarch64 ]]; then
  echo 'This bootstrap supports Linux ARM64. Install signal-cli for your platform and set connectors.signal.binary.' >&2
  exit 2
fi
python3 - <<'PY'
import hashlib
import os
from pathlib import Path
import tarfile
import urllib.request

os.umask(0o077)
root=Path(os.getenv('AMYTHEST_SIGNAL_INSTALL',Path.home()/'.local/share/amythest-signal')).expanduser()
root.mkdir(mode=0o700,parents=True,exist_ok=True)
assets=[
 ('jre.tgz','jre','https://github.com/adoptium/temurin25-binaries/releases/download/jdk-25.0.5%2B8/OpenJDK25U-jre_aarch64_linux_hotspot_25.0.5_8.tar.gz','963931723891302ebb2687178532c3d793d71e2577cd058061c7efb396140a86'),
 ('signal.tgz','runtime','https://github.com/AsamK/signal-cli/releases/download/v0.14.10/signal-cli-0.14.10.tar.gz','706a57d9faae7525cf732936efdabf210146e3c83f9e8ae7e4e62f5946121c33'),
 ('native.tgz','native','https://github.com/exquo/signal-libs-build/releases/download/libsignal_v0.106.1/libsignal_jni.so-v0.106.1-aarch64-unknown-linux-gnu.tar.gz','993a8ff27d4320864f336fe891320bfc3ce39c513a957a3d81b3cd9e426d190e')]
for filename,directory,url,digest in assets:
    archive=root/filename
    if not archive.exists() or hashlib.sha256(archive.read_bytes()).hexdigest()!=digest:
        urllib.request.urlretrieve(url,archive)
    if hashlib.sha256(archive.read_bytes()).hexdigest()!=digest:
        raise RuntimeError('Checksum mismatch: '+filename)
    destination=root/directory
    destination.mkdir(exist_ok=True)
    with tarfile.open(archive) as tar:
        tar.extractall(destination,filter='data')
    print('Verified '+filename,flush=True)
jre=root/'jre/jdk-25.0.5+8-jre'
signal=root/'runtime/signal-cli-0.14.10/bin/signal-cli'
native=next((root/'native').rglob('libsignal_jni.so')).parent
import shlex
wrapper=root/'signal-cli'
wrapper.write_text('#!/bin/sh\nexport JAVA_HOME='+shlex.quote(str(jre))+'\nexport JAVA_OPTS='+shlex.quote('-Djava.library.path='+str(native))+'\nexec '+shlex.quote(str(signal))+' "$@"\n')
wrapper.chmod(0o700)
print('Signal runtime installed at '+str(wrapper))
PY
