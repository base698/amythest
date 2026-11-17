"""Private HTTP transport and durable connector journal shared by providers."""
import asyncio
import json
from pathlib import Path
import sqlite3
import urllib.request
import urllib.parse
MAX_FILE = 5 << 20

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Gateway:
    def __init__(self, url, token_file):
        parsed = urllib.parse.urlparse(url)
        if parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise ValueError('Invalid gateway URL')
        if parsed.scheme != 'https' and not (parsed.scheme == 'http' and parsed.hostname in ('127.0.0.1', '::1', 'localhost')):
            raise ValueError('Gateway requires HTTPS or a loopback tunnel')
        self.url = url.rstrip('/')
        self.token = Path(token_file).read_text().strip()
        self.opener = urllib.request.build_opener(NoRedirect())

    def request(self, path, data=None, binary=False):
        req = urllib.request.Request(self.url + path, data=None if data is None else json.dumps(data).encode(),
                                     headers={'Authorization': 'Bearer ' + self.token, 'Content-Type': 'application/json'})
        with self.opener.open(req, timeout=15) as res:
            raw = res.read(MAX_FILE + 1 if binary else 8 << 20)
            if binary:
                if len(raw) > MAX_FILE:
                    raise ValueError('Attachment exceeds size limit')
                return raw
            return json.loads(raw)

    async def call(self, path, data=None, binary=False):
        return await asyncio.to_thread(self.request, path, data, binary)


class Journal:
    def __init__(self, path):
        self.db = sqlite3.connect(path)
        self.db.executescript('''
            PRAGMA journal_mode=WAL;
            PRAGMA synchronous=FULL;
            CREATE TABLE IF NOT EXISTS inbox(id TEXT PRIMARY KEY, payload TEXT NOT NULL);
            CREATE TABLE IF NOT EXISTS checkpoints(chat TEXT PRIMARY KEY, message INTEGER NOT NULL);
            CREATE TABLE IF NOT EXISTS deliveries(attempt TEXT PRIMARY KEY, payload TEXT NOT NULL, done INTEGER NOT NULL DEFAULT 0);
            CREATE TABLE IF NOT EXISTS sent_messages(chat TEXT NOT NULL, message TEXT NOT NULL, PRIMARY KEY(chat,message));
            CREATE TABLE IF NOT EXISTS metadata(key TEXT PRIMARY KEY, value TEXT NOT NULL);
        ''')

    def bind_account(self, account):
        with self.db:
            self.db.execute("INSERT OR IGNORE INTO metadata VALUES('account',?)", (str(account),))
        if self.db.execute("SELECT value FROM metadata WHERE key='account'").fetchone()[0] != str(account):
            raise ValueError('This bridge journal belongs to another account; use a separate state directory')

    def spool(self, payload):
        c, m = payload['conversation'], payload['message']
        key = c['id'] + ':' + m['native_id']
        with self.db:
            self.db.execute('INSERT OR IGNORE INTO inbox VALUES(?,?)', (key, json.dumps(payload)))

    def advance(self, chat, message):
        with self.db:
            self.db.execute('INSERT INTO checkpoints VALUES(?,?) ON CONFLICT(chat) DO UPDATE SET message=max(message,excluded.message)',
                            (str(chat), message))

    def pending_inbox(self):
        return [(key, json.loads(raw)) for key, raw in self.db.execute('SELECT id,payload FROM inbox ORDER BY rowid')]

    def acknowledge(self, key):
        with self.db:
            self.db.execute('DELETE FROM inbox WHERE id=?', (key,))

    def receipt(self, value, done=False, chat=None):
        with self.db:
            self.db.execute('INSERT INTO deliveries VALUES(?,?,?) ON CONFLICT(attempt) DO UPDATE SET payload=excluded.payload,done=excluded.done',
                            (value['attempt'], json.dumps(value), int(done)))
            if value['state'] == 'sent' and chat is not None:
                self.db.execute('INSERT OR IGNORE INTO sent_messages VALUES(?,?)', (str(chat), value['native_id']))

    def pending_receipts(self):
        return [json.loads(row[0]) for row in self.db.execute('SELECT payload FROM deliveries WHERE done=0 ORDER BY rowid')]

    def sent_ids(self, chat):
        return {row[0] for row in self.db.execute('SELECT message FROM sent_messages WHERE chat=?', (str(chat),))}

    def checkpoint(self, chat):
        row = self.db.execute('SELECT message FROM checkpoints WHERE chat=?', (str(chat),)).fetchone()
        return row[0] if row else 0
