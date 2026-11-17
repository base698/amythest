import asyncio
import datetime as dt
import hashlib
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest

from connector import Connector, Journal, normalized, thread_id


class FakeClient:
    def __init__(self, error=None):
        self.error, self.calls = error, []

    async def get_input_entity(self, chat):
        return chat

    async def get_messages(self, chat, ids):
        return SimpleNamespace(id=ids, chat_id=chat, reply_to=None)

    async def send_message(self, chat, body, **kwargs):
        self.calls.append((chat, body, kwargs))
        if self.error:
            raise self.error
        return SimpleNamespace(id=57)

    async def send_file(self, chat, file, **kwargs):
        self.calls.append((chat, file.read(), kwargs))
        if self.error:
            raise self.error
        return SimpleNamespace(id=58)


class FakeGateway:
    def __init__(self):
        self.receipts, self.events = [], []
        self.data, self.fail_receipt, self.revoked = b'', False, False

    async def call(self, path, data=None, binary=False):
        if path.startswith('/connector/attachment'):
            return self.data
        if path == '/connector/authorize' and self.revoked:
            raise PermissionError('revoked')
        if path == '/connector/complete':
            if self.fail_receipt:
                raise ConnectionError('response lost')
            self.receipts.append(dict(data))
        if path == '/connector/ingest':
            self.events.append(data)
        return {'ok': True}


def job(files=None):
    return {'draft': {'id': 'draft', 'attempt': 'attempt', 'body': 'A reply with **literal text**',
                      'target': {'route': {'provider': 'telegram', 'account': '123', 'chat': '-100456', 'thread': ''}},
                      'attachments': files or []}, 'parent': {'native_id': '55'}}


class ConnectorTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.path = Path(self.temp.name) / 'bridge.db'
        self.journal = Journal(self.path)
        self.client, self.gateway = FakeClient(), FakeGateway()
        self.connector = Connector(self.client, self.gateway, self.journal, 123, [-100456], send=True)

    async def asyncTearDown(self):
        self.journal.db.close()
        self.temp.cleanup()

    async def test_original_chat_parent_and_literal_body(self):
        await self.connector.deliver(job())
        chat, body, options = self.client.calls[0]
        self.assertEqual(chat, -100456)
        self.assertEqual(options['reply_to'], 55)
        self.assertIsNone(options['parse_mode'])
        self.assertIn('**literal text**', body)
        self.assertEqual(self.gateway.receipts[0]['state'], 'sent')
        self.assertEqual(self.gateway.receipts[0]['native_id'], '57')

    async def test_file_bytes_and_filename_are_preserved(self):
        data = b'\x89PNG\r\n\x1a\nexact original screenshot'
        name = 'wall image.png'
        attachment = {'name': name, 'id': hashlib.sha256(name.encode()+b'\0'+data).hexdigest(), 'size': len(data)}
        self.gateway.data = data
        await self.connector.deliver(job([attachment]))
        chat, sent, options = self.client.calls[0]
        self.assertEqual(sent, data)
        self.assertEqual(chat, -100456)
        self.assertEqual(options['reply_to'], 55)
        self.assertTrue(options['force_document'])
        self.assertEqual(self.gateway.receipts[0]['state'], 'sent')

    async def test_receipt_survives_restart_without_second_send(self):
        self.gateway.fail_receipt = True
        with self.assertRaises(ConnectionError):
            await self.connector.deliver(job())
        self.journal.db.close()
        self.journal = Journal(self.path)
        restarted = Connector(self.client, self.gateway, self.journal, 123, [-100456], send=True)
        self.gateway.fail_receipt = False
        await restarted.flush()
        await restarted.flush()
        self.assertEqual(len(self.client.calls), 1)
        self.assertEqual(len(self.gateway.receipts), 1)
        self.assertEqual(self.gateway.receipts[0]['state'], 'sent')

    async def test_timeout_is_uncertain_without_automatic_retry(self):
        self.client.error = TimeoutError()
        await self.connector.deliver(job())
        await self.connector.flush()
        self.assertEqual(len(self.client.calls), 1)
        self.assertEqual(self.gateway.receipts[0]['state'], 'uncertain')

    async def test_revocation_at_last_boundary_prevents_send(self):
        self.gateway.revoked = True
        await self.connector.deliver(job())
        self.assertEqual(self.client.calls, [])
        self.assertEqual(self.gateway.receipts[0]['state'], 'failed')

    async def test_wrong_account_or_chat_never_sends(self):
        changed = job()
        changed['draft']['target']['route']['account'] = '999'
        await self.connector.deliver(changed)
        self.assertEqual(self.client.calls, [])
        self.assertEqual(self.gateway.receipts[0]['state'], 'failed')

    async def test_inbox_durable_dedup_and_cursor_not_advanced_by_live_event(self):
        message = SimpleNamespace(chat_id=-100456, id=55, raw_text='Can you reply?', media=None,
                                  date=dt.datetime.now(dt.timezone.utc), reply_to=None)
        event = normalized(message, 123, 'Group', 'Colleague')
        self.journal.spool(event)
        self.journal.spool(event)
        self.assertEqual(len(self.journal.pending_inbox()), 1)
        self.assertEqual(self.journal.checkpoint(-100456), 0)
        self.journal.advance(-100456, 55)
        await self.connector.flush()
        self.assertEqual(self.journal.pending_inbox(), [])
        self.assertEqual(self.journal.checkpoint(-100456), 55)
        with self.assertRaises(ValueError):
            self.journal.bind_account(999)

    async def test_saved_message_ids_are_scoped_to_chat(self):
        self.journal.receipt({'account':'123','attempt':'a','state':'sent','native_id':'15'}, chat=-100456)
        self.assertNotIn('15', self.journal.sent_ids(123))
        self.journal.receipt({'account':'123','attempt':'b','state':'sent','native_id':'16'}, chat=123)
        self.assertIn('16', self.journal.sent_ids(123))

    async def test_forum_topic_and_unicode_names(self):
        message = SimpleNamespace(chat_id=-100456, id=55, raw_text='Question', media=None,
                                  date=dt.datetime.now(dt.timezone.utc),
                                  reply_to=SimpleNamespace(forum_topic=True, reply_to_top_id=7, reply_to_msg_id=20))
        event = normalized(message, 123, '界'*300, '界'*200)
        self.assertEqual(thread_id(message), '7')
        self.assertEqual(event['conversation']['route']['thread'], '7')
        self.assertLessEqual(len(event['conversation']['name'].encode()), 300)
        self.assertLessEqual(len(event['message']['sender'].encode()), 200)
    async def test_native_outgoing_messages_are_spooled(self):
        async def entity():return SimpleNamespace(first_name='Test',last_name='Contact')
        message=SimpleNamespace(chat_id=-100456,id=60,raw_text='My native reply',media=None,
                                date=dt.datetime.now(dt.timezone.utc),reply_to=None,out=True,get_chat=entity,get_sender=entity)
        await self.connector.capture(message)
        await self.connector.flush()
        self.assertTrue(self.gateway.events[0]['message']['outgoing'])


if __name__ == '__main__':
    unittest.main()
