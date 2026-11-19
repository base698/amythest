import tempfile
import unittest
import urllib.error
from pathlib import Path
from unittest.mock import patch
import messaging_common as mc

class IngestFilesTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        token = self.dir / 'token'; token.write_text('t' * 64)
        self.gw = mc.Gateway('http://127.0.0.1:9', str(token))
        self.calls = []
    def tearDown(self):
        self.tmp.cleanup()
    def fake(self, refuse=(), down=False):
        def request(path, data=None, binary=False):
            self.calls.append((path, data))
            if path == '/connector/attachments':
                if down:
                    raise urllib.error.URLError('refused')
                if data['name'] in refuse:
                    raise urllib.error.HTTPError(path, 409, 'full', {}, None)
                return {'id': 'a' * 64, 'name': data['name'], 'mime': 'image/png', 'size': 3}
            return {'id': 'm'}
        return request
    def event(self, *files):
        return {'conversation': {'id': 'c'}, 'message': {'native_id': 'n', 'body': 'hi'}, 'files': list(files)}
    def test_uploads_attaches_and_removes_staged_copies(self):
        kept = self.dir / 'kept.png'; kept.write_bytes(b'png')
        staged = self.dir / 'staged.jpg'; staged.write_bytes(b'jpg')
        with patch.object(self.gw, 'request', self.fake()):
            self.gw.ingest(self.event({'path': str(kept), 'name': 'kept.png'}, {'path': str(staged), 'name': 'photo.jpg', 'staged': True}))
        paths = [c[0] for c in self.calls]
        self.assertEqual(paths, ['/connector/attachments', '/connector/attachments', '/connector/ingest'])
        sent = self.calls[-1][1]
        self.assertNotIn('files', sent)
        self.assertEqual(len(sent['message']['attachments']), 2)
        self.assertEqual(sent['message']['body'], 'hi')
        self.assertTrue(kept.exists()); self.assertFalse(staged.exists())
    def test_missing_or_refused_files_become_a_note(self):
        refused = self.dir / 'big.png'; refused.write_bytes(b'png')
        with patch.object(self.gw, 'request', self.fake(refuse=('big.png',))):
            self.gw.ingest(self.event({'path': str(self.dir / 'gone.png'), 'name': 'gone.png'}, {'path': str(refused), 'name': 'big.png'}))
        sent = self.calls[-1][1]
        self.assertNotIn('attachments', sent['message'])
        self.assertIn('[not shown here: gone.png, big.png]', sent['message']['body'])
    def test_gateway_outage_keeps_the_message_for_later(self):
        f = self.dir / 'a.png'; f.write_bytes(b'png')
        with patch.object(self.gw, 'request', self.fake(down=True)):
            with self.assertRaises(urllib.error.URLError):
                self.gw.ingest(self.event({'path': str(f), 'name': 'a.png', 'staged': True}))
        self.assertTrue(f.exists())
        self.assertNotIn('/connector/ingest', [c[0] for c in self.calls])

if __name__ == '__main__':
    unittest.main()
