#!/usr/bin/env python3
"""Personal-account Telegram connector. A session belongs to one process/host.

Gateway queues are claimed once. A local journal preserves receipts across HTTP
failures; an interrupted provider call is reported as uncertain, never retried.
"""
import argparse
import asyncio
import datetime as dt
import fcntl
import getpass
import hashlib
import io
import json
import logging
import os
from pathlib import Path
import sqlite3
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

from telethon import TelegramClient, events, errors, types, utils

MAX_FILE = 5 << 20


try:
    from messaging_common import Gateway, Journal
except ModuleNotFoundError:
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent / 'common'))
    from messaging_common import Gateway, Journal


def thread_id(message):
    header = getattr(message, 'reply_to', None)
    if header and header.forum_topic:
        return str(header.reply_to_top_id or header.reply_to_msg_id or '')
    # Ordinary replies remain in their conversation; forum topics have stable IDs.
    if isinstance(getattr(message, 'action', None), types.MessageActionTopicCreate):
        return str(message.id)
    return ''


def normalized(message, account, chat_name, sender_name):
    chat = str(message.chat_id)
    topic = thread_id(message)
    route = {'provider': 'telegram', 'account': str(account), 'chat': chat, 'thread': topic}
    body = message.raw_text or ''
    if message.media:
        file = message.file
        name = (getattr(file, 'name', None) or 'photo' if message.photo else getattr(file, 'name', None) or 'media')
        body += '\n[Telegram attachment: ' + str(name) + ' · open in Telegram]'
    if not body:
        body = '[Telegram service message]'
    # Go's ingress bound is bytes, not Unicode code points.
    body = body.encode()[:20000].decode('utf-8', 'ignore')
    def clipped(value, size):
        return value.encode()[:size].decode('utf-8', 'ignore')
    return {
        'conversation': {'id': 'telegram/' + str(account) + '/' + chat + '/' + topic,
                         'service': 'Telegram', 'account': str(account), 'name': clipped(chat_name, 300),
                         'recipient': chat, 'thread': topic, 'demo': False, 'route': route},
        'message': {'native_id': str(message.id), 'sender': clipped(sender_name, 200), 'body': body,
                    'outgoing': bool(getattr(message, 'out', False)) and chat != str(account),
                    'at': message.date.astimezone(dt.timezone.utc).isoformat().replace('+00:00', 'Z')}
    }


# Only definite provider rejections are retryable. All other exceptions after
# starting a send are uncertain; their messages are not logged or persisted.
DEFINITE_REJECTIONS = {
    'ChatWriteForbiddenError', 'UserIsBlockedError', 'ChatAdminRequiredError',
    'MessageIdInvalidError', 'MsgIdInvalidError', 'PeerIdInvalidError',
    'MessageTooLongError', 'MediaCaptionTooLongError', 'ChatForwardsRestrictedError',
    'FloodWaitError', 'SlowModeWaitError', 'UserBannedInChannelError',
}


class Connector:
    def __init__(self, client, gateway, journal, account, chats, all_chats=False, send=False):
        self.client, self.gateway, self.journal = client, gateway, journal
        self.account, self.chats = str(account), set(map(str, chats))
        self.all_chats, self.send = all_chats, send
        self.worker = str(uuid.uuid4())
        self.send_lock = asyncio.Lock()
        journal.bind_account(account)
        self.own_messages = journal.sent_ids(account)
        self.last_health = ''

    def accepts(self, chat):
        return self.all_chats or str(chat) in self.chats

    async def heartbeat(self):
        await self.gateway.call('/connector/heartbeat', {
            'account': self.account, 'worker': self.worker, 'chats': sorted(self.chats),
            'all_chats': self.all_chats, 'send': self.send})

    async def capture(self, message):
        if not self.accepts(message.chat_id):
            return
        saved = str(message.chat_id) == self.account
        # Serialize Saved Messages events against sends to avoid feeding our own
        # replies back into focus before the local receipt has been recorded.
        async with self.send_lock:
            if saved and str(message.id) in self.own_messages:
                return
            chat = await message.get_chat()
            sender = await message.get_sender()
            payload = normalized(message, self.account,
                                 'Saved Messages' if saved else utils.get_display_name(chat),
                                 utils.get_display_name(sender) or 'Telegram')
            self.journal.spool(payload)

    async def flush(self):
        for receipt in self.journal.pending_receipts():
            await self.gateway.call('/connector/complete', receipt)
            self.journal.receipt(receipt, done=True)
        count = 0
        for key, payload in self.journal.pending_inbox():
            if not self.accepts(payload['conversation']['route']['chat']):
                continue
            await self.gateway.call('/connector/ingest', payload)
            self.journal.acknowledge(key)
            count += 1
            if count >= 100:
                break

    async def deliver(self, job):
        draft, parent = job['draft'], job['parent']
        route = draft['target']['route']
        receipt = {'account': self.account, 'draft': draft['id'], 'attempt': draft['attempt'],
                   'state': 'uncertain', 'native_id': '',
                   'error': 'Connector stopped during delivery. Check Telegram before composing another reply.'}
        self.journal.receipt(receipt)
        started = False
        async with self.send_lock:
            try:
                if not self.send or route['provider'] != 'telegram' or route['account'] != self.account or not self.accepts(route['chat']):
                    raise ValueError('Destination is outside connector scope')
                chat, parent_id = int(route['chat']), int(parent['native_id'])
                peer = await self.client.get_input_entity(chat)
                original = await self.client.get_messages(peer, ids=parent_id)
                if not original or int(original.chat_id) != chat or thread_id(original) != route.get('thread', ''):
                    raise ValueError('Original message or topic is no longer available')
                files = draft.get('attachments', [])
                if len(files) > 1:
                    raise ValueError('Only one attachment per Telegram reply is supported')
                file = None
                if files:
                    attachment = files[0]
                    query = urllib.parse.urlencode({'account': self.account, 'draft': draft['id'],
                                                    'attempt': draft['attempt'], 'id': attachment['id']})
                    data = await self.gateway.call('/connector/attachment?' + query, binary=True)
                    digest = hashlib.sha256(attachment['name'].encode() + b'\0' + data).hexdigest()
                    if digest != attachment['id'] or len(data) != attachment['size']:
                        raise ValueError('Attachment snapshot did not match')
                    file = io.BytesIO(data)
                    file.name = attachment['name']
                await self.gateway.call('/connector/authorize', {'account': self.account,
                                        'draft': draft['id'], 'attempt': draft['attempt']})
                started = True
                if file is not None:
                    sent = await asyncio.wait_for(self.client.send_file(peer, file, caption=draft['body'],
                            reply_to=parent_id, force_document=True, parse_mode=None, clear_draft=False), 60)
                else:
                    sent = await asyncio.wait_for(self.client.send_message(peer, draft['body'], reply_to=parent_id,
                            parse_mode=None, link_preview=False, clear_draft=False), 60)
                receipt.update(state='sent', native_id=str(sent.id), error='')
                if str(chat) == self.account:
                    self.own_messages.add(str(sent.id))
            except Exception as exc:
                definite = not started or type(exc).__name__ in DEFINITE_REJECTIONS
                receipt.update(state='failed' if definite else 'uncertain',
                               error=('Telegram rejected the reply: ' if definite else 'Delivery is uncertain: ') + type(exc).__name__)
            # No provider call is ever repeated to recover a lost HTTP response.
            self.journal.receipt(receipt, chat=route['chat'])
        await self.gateway.call('/connector/complete', receipt)
        self.journal.receipt(receipt, done=True)

    async def loop(self):
        while True:
            try:
                if not self.client.is_connected():
                    raise ConnectionError('Telegram is disconnected')
                await self.heartbeat()
                await self.flush()
                job = (await self.gateway.call('/connector/claim', {'account': self.account, 'worker': self.worker}))['delivery']
                if job:
                    await self.deliver(job)
                if self.last_health != 'ready':
                    print('Telegram connector ready; ' + ('replies enabled' if self.send else 'receive-only'), flush=True)
                    self.last_health = 'ready'
            except Exception as exc:
                # Keep credentials, message bodies, usernames and API URLs out of logs.
                health = type(exc).__name__
                if self.last_health != health:
                    print('Connector waiting: ' + health, file=sys.stderr, flush=True)
                    self.last_health = health
            await asyncio.sleep(2)

    async def backfill(self, initial):
        async for dialog in self.client.iter_dialogs():
            if not self.accepts(dialog.id):
                continue
            marker = 'with-sent/' + str(dialog.id)
            checkpoint = self.journal.checkpoint(marker)
            high = max(checkpoint, dialog.message.id if dialog.message else 0)
            if checkpoint:
                iterator = self.client.iter_messages(dialog.input_entity, min_id=checkpoint, max_id=high+1, reverse=True)
            else:
                iterator = self.client.iter_messages(dialog.input_entity, max_id=high+1, limit=initial)
            async for message in iterator:
                await self.capture(message)
            # Only a completed scan advances the cursor. Live events arriving
            # during a backfill cannot skip older messages after a crash.
            self.journal.advance(marker, high)

    async def recover_gaps(self, initial):
        while True:
            await self.backfill(initial)
            await asyncio.sleep(60)


def paths():
    home = Path.home()
    config = Path(os.getenv('XDG_CONFIG_HOME', home / '.config')) / 'amythest/telegram.json'
    state = Path(os.getenv('XDG_STATE_HOME', home / '.local/state'))
    return config, state / 'amythest-telegram', state / 'amythest-messages-poc/telegram.token'


def credentials(path, interactive):
    path = Path(path)
    if not path.exists():
        if not interactive:
            raise ValueError('Telegram credentials missing; run amy-telegram login in a terminal')
        print('Create application credentials at https://my.telegram.org/apps. They stay on this machine.')
        api_id = int(input('Telegram API ID: ').strip())
        api_hash = getpass.getpass('Telegram API hash: ').strip()
        if api_id <= 0 or len(api_hash) != 32 or any(c not in '0123456789abcdef' for c in api_hash):
            raise ValueError('Invalid Telegram application credentials')
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        with path.open('x') as f:
            os.chmod(path, 0o600)
            json.dump({'api_id': api_id, 'api_hash': api_hash}, f)
    if path.stat().st_mode & 0o077:
        raise ValueError('Telegram credential file must have mode 0600')
    value = json.loads(path.read_text())
    return int(value['api_id']), value['api_hash']


async def main(args):
    os.umask(0o077)
    state = Path(args.state)
    state.mkdir(parents=True, exist_ok=True, mode=0o700)
    lock = (state / 'session.lock').open('a')
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        raise ValueError('Another Telegram connector uses this session; stop it before logging in')
    api_id, api_hash = credentials(args.config, args.command == 'login')
    client = TelegramClient(str(state / 'account'), api_id, api_hash, sequential_updates=True,
                            request_retries=3 if args.command == 'login' else 0,
                            raise_last_call_error=True, flood_sleep_threshold=0, catch_up=False,
                            device_model='Amythest messaging', app_version='0.2')
    try:
        if args.command == 'login':
            def phone_number():
                phone = input('Phone number (international format): ').strip()
                if not phone or any(c not in '+0123456789 ()-' for c in phone):
                    raise ValueError('Enter a personal account phone number, not a bot token')
                return phone
            await client.start(phone=phone_number,
                               code_callback=lambda: getpass.getpass('Telegram login code: '),
                               password=lambda: getpass.getpass('Telegram two-step password: '))
            if (await client.get_me()).bot:
                raise ValueError('This session belongs to a bot; use a new --state directory for a personal account')
            print('Telegram session saved. Use amy-telegram chats to choose conversations.')
            return
        await client.connect()
        if not await client.is_user_authorized():
            raise ValueError('Telegram login required; run amy-telegram login in a terminal')
        me = await client.get_me()
        if me.bot:
            raise ValueError('A personal Telegram account is required, not a bot token')
        if args.command == 'chats':
            async for dialog in client.iter_dialogs(limit=200):
                label = ''.join(c for c in dialog.name if c.isprintable())
                print(f'{dialog.id}\t{label}')
            return
        chats = [str(me.id) if c == 'me' else str(int(c)) for c in args.chat]
        if not chats and not args.all_chats:
            raise ValueError('Choose --chat me, one or more --chat IDs, or --all-chats')
        gateway = Gateway(args.gateway, args.token_file)
        connector = Connector(client, gateway, Journal(state / 'bridge.db'), me.id, chats, args.all_chats, args.send)
        async def incoming(event):
            try:
                await connector.capture(event.message)
            except Exception:
                # Restart so backfill recovers anything that couldn't be spooled.
                await client.disconnect()
                raise
        client.add_event_handler(incoming, events.NewMessage)
        await connector.heartbeat()
        await connector.backfill(args.initial)
        await client.catch_up()
        worker = asyncio.create_task(connector.loop())
        recovery = asyncio.create_task(connector.recover_gaps(args.initial))
        try:
            # A failed recovery scan must restart instead of silently losing
            # coverage while the delivery worker continues to advertise health.
            disconnected = asyncio.ensure_future(client.disconnected)
            done, _ = await asyncio.wait([worker, recovery, disconnected], return_when=asyncio.FIRST_COMPLETED)
            for task in done:
                task.result()
        finally:
            worker.cancel()
            recovery.cancel()
            await asyncio.gather(worker, recovery, return_exceptions=True)
        raise ConnectionError('Telegram disconnected; restart to recover missing events')
    finally:
        await client.disconnect()
        lock.close()


def parser(settings=None):
    settings = settings or {}
    config, state, token = paths()
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('command', choices=('login', 'chats', 'watch'))
    p.add_argument('--settings', help='shared messaging settings (default: ~/.config/amythest/messages.json)')
    p.add_argument('--config', default=settings.get('credentials_file', str(config)), help='private Telegram application credentials')
    p.add_argument('--state', default=settings.get('state_dir', str(state)))
    p.add_argument('--gateway', default=os.getenv('AMYTHEST_MESSAGES_URL', settings.get('gateway', 'http://127.0.0.1:8642')))
    p.add_argument('--token-file', default=settings.get('token_file', str(token)))
    p.add_argument('--chat', action='append', default=None, help='numeric chat ID; overrides configured chats; me means Saved Messages')
    p.add_argument('--all-chats', action=argparse.BooleanOptionalAction, default=settings.get('all_chats', False))
    p.add_argument('--send', action=argparse.BooleanOptionalAction, default=settings.get('send', False), help='allow delivery of explicitly queued replies')
    p.add_argument('--initial', type=int, default=settings.get('initial', 30), help='messages per chat on first import')
    return p

def parse_options(argv=None):
    probe = argparse.ArgumentParser(add_help=False)
    probe.add_argument('--settings')
    early, _ = probe.parse_known_args(argv)
    default = Path(os.getenv('XDG_CONFIG_HOME', Path.home()/'.config'))/'amythest/messages.json'
    explicit = early.settings or os.getenv('AMYTHEST_MESSAGES_CONFIG')
    path = Path(explicit or default).expanduser()
    settings = {}
    if path.exists() or explicit:
        whole = json.loads(path.read_text())
        settings = dict(whole.get('connectors', {}).get('telegram', {}))
        allowed = {'credentials_file','state_dir','gateway','token_file','chats','all_chats','send','initial'}
        if set(settings)-allowed:
            raise ValueError('Unknown Telegram connector setting')
        settings.setdefault('gateway', whole.get('client', {}).get('url', 'http://127.0.0.1:8642'))
        for key in ('all_chats','send'):
            if key in settings and not isinstance(settings[key], bool):
                raise ValueError(key+' must be a JSON boolean')
        if 'chats' in settings and (not isinstance(settings['chats'], list) or not all(isinstance(c,str) for c in settings['chats'])):
            raise ValueError('Telegram chats must be an array of strings')
    args = parser(settings).parse_args(argv)
    if args.chat is None:
        args.chat = settings.get('chats', [])
    for key in ('config','state','token_file'):
        setattr(args,key,str(Path(getattr(args,key)).expanduser()))
    if not isinstance(args.initial,int) or not 0 <= args.initial <= 200:
        raise ValueError('--initial must be between 0 and 200')
    return args


if __name__ == '__main__':
    logging.basicConfig(level=logging.CRITICAL)
    try:
        options = parse_options()
        asyncio.run(main(options))
    except KeyboardInterrupt:
        pass
    except Exception as error:
        # Setup errors contain our own actionable text. Provider errors do not.
        print(str(error) if isinstance(error, ValueError) else type(error).__name__ + ': Telegram connector stopped', file=sys.stderr)
        sys.exit(1)
