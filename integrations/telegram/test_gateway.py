"""Optional full-wire test: AMYTHEST_TEST_BIN=/absolute/path/bin/amy python -m unittest ..."""
import base64
import datetime as dt
import os
from pathlib import Path
import selectors
import signal
import subprocess
import tempfile
from types import SimpleNamespace
import unittest

from connector import Connector, Gateway, Journal, normalized
from test_connector import FakeClient


@unittest.skipUnless(os.getenv('AMYTHEST_TEST_BIN'), 'set AMYTHEST_TEST_BIN to run against the actual Go gateway')
class GatewayIntegration(unittest.IsolatedAsyncioTestCase):
    async def test_incoming_focus_attachment_live_receipt(self):
        with tempfile.TemporaryDirectory() as temp:
            state = Path(temp)
            proc = subprocess.Popen([os.environ['AMYTHEST_TEST_BIN'], 'messages', 'serve',
                                     '-state', temp, '-listen', '127.0.0.1:0', '-irc', ''],
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            journal = None
            try:
                selector = selectors.DefaultSelector()
                selector.register(proc.stdout, selectors.EVENT_READ)
                self.assertTrue(selector.select(15), 'gateway failed to start')
                line = proc.stdout.readline().strip()
                self.assertTrue(line.startswith('Messaging gateway: http://'), line)
                url = line.removeprefix('Messaging gateway: ')
                owner = Gateway(url, state/'owner.token')
                connector_api = Gateway(url, state/'telegram.token')
                journal = Journal(state/'journal.db')
                fake = FakeClient()
                connector = Connector(fake, connector_api, journal, 123, [-100456], send=True)
                await connector.heartbeat()
                source = SimpleNamespace(chat_id=-100456, id=55, raw_text='Can you show the image?', media=None,
                                         date=dt.datetime.now(dt.timezone.utc), reply_to=None)
                journal.spool(normalized(source, 123, 'Integration test group', 'Colleague'))
                await connector.flush()
                focus = await owner.call('/items')
                self.assertEqual(len(focus), 1)
                self.assertEqual(focus[0]['message']['category'], 'Reply')
                payload = b'\x89PNG\r\n\x1a\noriginal image bytes'
                attachment = await owner.call('/attachments', {'name': 'wall.png', 'data': base64.b64encode(payload).decode()})
                draft = await owner.call('/drafts', {'request_id': 'full-wire', 'reply_to': focus[0]['message']['id'],
                                                    'body': 'Here is the wall image.', 'attachments': [attachment['id']]})
                queued = await owner.call('/send', {'draft': draft['id']})
                self.assertEqual(queued['state'], 'queued')
                job = (await connector_api.call('/connector/claim', {'account': '123', 'worker': connector.worker}))['delivery']
                await connector.deliver(job)
                records = await owner.call('/drafts')
                self.assertEqual(records[0]['state'], 'sent')
                self.assertEqual(records[0]['native_id'], '58')
                self.assertEqual(fake.calls[0][0], -100456)
                self.assertEqual(fake.calls[0][1], payload)
                self.assertEqual(fake.calls[0][2]['reply_to'], 55)
                self.assertEqual(await owner.call('/items'), [])
                again = await owner.call('/send', {'draft': draft['id']})
                self.assertEqual(again['state'], 'sent')
                self.assertIsNone((await connector_api.call('/connector/claim', {'account': '123', 'worker': connector.worker}))['delivery'])
                self.assertEqual(len(fake.calls), 1)
            finally:
                if journal:
                    journal.db.close()
                proc.send_signal(signal.SIGTERM)
                try:
                    proc.communicate(timeout=5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.communicate()
