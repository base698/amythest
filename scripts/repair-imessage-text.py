#!/usr/bin/env python3
"""Repair iMessage text imported with imsg's length-prefix bug.

Messages keeps newer texts only in attributedBody. imsg decoded those with the
string's length bytes left in front: a stray character such as "4", or
unreadable ones for longer texts. The connector now decodes them itself; this
fixes what was already imported.

For each iMessage conversation the gateway holds, it reads every message's
true text from the Messages database. Where the stored text is that text with
a few extra characters in front, it sends a correction through the iMessage
connector's own credential. The gateway changes only that message's text.

Runs on the Mac with the Messages database. Dry run unless --apply.
"""
import argparse
import json
import os
from pathlib import Path
import sqlite3
import sys
import urllib.parse
import urllib.request

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / 'integrations' / 'imessage'))
sys.path.insert(0, str(Path(__file__).resolve().parent.parent / 'integrations' / 'common'))
from connector import attributed_text  # noqa: E402


def call(url, token, path, data=None):
    req = urllib.request.Request(url + path, data=None if data is None else json.dumps(data).encode(),
                                 headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=30) as res:
        return json.load(res)


def repaired(stored, true_text):
    """The stored body with its junk prefix removed, or None if it is fine or
    does not look like this bug."""
    clean = true_text.replace('￼', '').strip()
    if not clean or stored.startswith(clean):
        return None
    i = stored.find(clean)
    if not 0 < i <= 6:
        return None
    return clean + stored[i + len(clean):]


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument('--settings', default=os.getenv('AMYTHEST_MESSAGES_CONFIG', '~/.config/amythest/messages.json'))
    ap.add_argument('--apply', action='store_true', help='send the corrections (default: count only)')
    args = ap.parse_args()
    cfg = json.loads(Path(args.settings).expanduser().read_text())
    url = cfg['client']['url'].rstrip('/')
    owner = Path(cfg['client']['token_file']).expanduser().read_text().strip()
    im = cfg['connectors']['imessage']
    connector = Path(im['token_file']).expanduser().read_text().strip()
    db = sqlite3.connect('file:' + str(Path(im.get('database', '~/Library/Messages/chat.db')).expanduser()) + '?mode=ro', uri=True)
    checked = fixes = sent = 0
    for listing in call(url, owner, '/conversations'):
        conv = listing['conversation']
        if conv.get('route', {}).get('provider') != 'imessage':
            continue
        thread = call(url, owner, '/thread?conversation=' + urllib.parse.quote(conv['id']))
        for m in thread['messages']:
            checked += 1
            row = db.execute('SELECT text, attributedBody FROM message WHERE guid=?', (m['native_id'],)).fetchone()
            if not row:
                continue
            true_text = row[0] if row[0] is not None else attributed_text(row[1])
            body = repaired(m['body'], true_text or '')
            if body is None:
                continue
            fixes += 1
            if args.apply:
                call(url, connector, '/connector/ingest', {
                    'conversation': thread['conversation'], 'correct': True,
                    'message': {'native_id': m['native_id'], 'sender': m['sender'], 'body': body,
                                'at': m['at'], 'outgoing': m['outgoing']}})
                sent += 1
    print(f'Checked {checked} iMessage messages; {fixes} need repair' + (f'; corrected {sent}.' if args.apply else '. Dry run: add --apply.'))


if __name__ == '__main__':
    main()
