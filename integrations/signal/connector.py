#!/usr/bin/env python3
"""Personal Signal linked-device connector, using signal-cli JSON-RPC over stdio."""
import argparse
import asyncio
import base64
import datetime as dt
import fcntl
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import urllib.parse
import uuid

try:
    from messaging_common import Gateway, Journal
except ModuleNotFoundError:
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent / 'common'))
    from messaging_common import Gateway, Journal


def canonical_uuid(value):
    return str(uuid.UUID(value)) == value


def valid_chat(chat):
    try:
        if chat.startswith('user/'):
            return canonical_uuid(chat[5:])
        if chat.startswith('group/'):
            return len(base64.b64decode(chat[6:], validate=True)) == 32
    except (ValueError, TypeError):
        pass
    return False


def clipped(text, size):
    return str(text).encode()[:size].decode('utf-8', 'ignore')


EXTENSIONS = {'image/jpeg': '.jpg', 'image/png': '.png', 'image/gif': '.gif', 'image/webp': '.webp',
              'image/heic': '.heic', 'video/mp4': '.mp4', 'audio/aac': '.aac', 'application/pdf': '.pdf'}


def attachment_files(attachments, files_dir):
    """The files signal-cli saved for a message: files_dir/<attachment id>."""
    out = []
    for a in attachments or []:
        ident = str(a.get('id') or '')
        if not ident or '/' in ident or ident.startswith('.'):
            continue
        name = Path(str(a.get('filename') or '')).name
        name = ''.join(c for c in name if c.isprintable())[:200]
        if not name:
            name = 'signal-' + ident.split('.')[0][:8] + EXTENSIONS.get(str(a.get('contentType') or ''), '')
        out.append({'path': str(Path(files_dir) / ident), 'name': name})
    return out


def normalized(envelope, account, names=None, files_dir=None):
    sent = (envelope.get('syncMessage') or {}).get('sentMessage')
    outgoing = bool(sent)
    message = sent or envelope.get('dataMessage') or {}
    author = envelope.get('sourceUuid', '')
    # Do not retain disappearing or view-once content in the persistent inbox.
    # Service events, reactions and story replies are omitted.
    if not message or message.get('expiresInSeconds') or message.get('viewOnce') or message.get('storyContext'):
        return None
    if not author or not canonical_uuid(author) or (outgoing and author != account) or (not outgoing and author == account):
        return None
    body = message.get('message') or ''
    files = attachment_files(message.get('attachments'), files_dir) if files_dir else []
    if message.get('attachments') and not files:
        body += '\n[Signal attachment · open in Signal]'
    if not body and not files:
        return None
    group = message.get('groupInfo') or {}
    destination = message.get('destinationUuid') if outgoing else author
    if not group.get('groupId') and (not destination or not canonical_uuid(destination)):
        return None
    chat = 'group/' + group['groupId'] if group.get('groupId') else 'user/' + destination
    if not valid_chat(chat):
        raise ValueError('Invalid native Signal chat')
    timestamp = int(message['timestamp'])
    if timestamp <= 0:
        raise ValueError('Invalid Signal timestamp')
    name = (names or {}).get(chat) or group.get('groupName') or (None if outgoing else envelope.get('sourceName')) or chat
    event = {
        'conversation': {'id': 'signal/' + hashlib.sha256((account+'\0'+chat).encode()).hexdigest(),
                         'service': 'Signal', 'account': account, 'recipient': chat, 'thread': '',
                         'name': clipped(name, 300), 'demo': False,
                         'route': {'provider': 'signal', 'account': account, 'chat': chat, 'thread': ''}},
        'message': {'native_id': str(timestamp)+'/'+author,
                    'sender': 'You' if outgoing else clipped(envelope.get('sourceName') or author, 200),
                    'outgoing': outgoing,
                    'body': clipped(body, 20000),
                    'at': dt.datetime.fromtimestamp(timestamp/1000, dt.timezone.utc).isoformat().replace('+00:00','Z')}
    }
    if files:
        event['files'] = files
    return event


class RPC:
    def __init__(self, binary, directory, account, capture):
        self.binary, self.directory, self.account, self.capture = binary, directory, account, capture
        self.pending = {}

    async def start(self):
        self.proc = await asyncio.create_subprocess_exec(self.binary, '--data-dir', self.directory,
            '-a', self.account, 'jsonRpc', '--receive-mode', 'manual',
            '--ignore-stories', '--ignore-avatars', '--ignore-stickers',
            stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.DEVNULL, limit=8 << 20)
        self.reader = asyncio.create_task(self.read())

    async def read(self):
        try:
            while line := await self.proc.stdout.readline():
                event = json.loads(line)
                if 'id' in event:
                    future = self.pending.get(str(event['id']))
                    if future and not future.done():
                        if 'error' in event:
                            # Provider errors can contain message text or private identifiers.
                            future.set_exception(RuntimeError('Signal RPC rejected operation'))
                        else:
                            future.set_result(event.get('result'))
                elif event.get('method') == 'receive':
                    params = event.get('params') or {}
                    self.capture((params.get('result') or params).get('envelope') or {})
            raise ConnectionError('Signal process stopped')
        finally:
            for future in self.pending.values():
                if not future.done():
                    future.set_exception(ConnectionError('Signal process stopped'))

    async def call(self, method, params=None):
        if self.reader.done():
            self.reader.result()
        request_id = str(uuid.uuid4())
        future = asyncio.get_running_loop().create_future()
        self.pending[request_id] = future
        try:
            self.proc.stdin.write((json.dumps({'jsonrpc':'2.0','id':request_id,'method':method,'params':params or {}})+'\n').encode())
            await self.proc.stdin.drain()
            return await asyncio.wait_for(future, 90)
        finally:
            self.pending.pop(request_id, None)

    async def close(self):
        if self.proc.returncode is None:
            self.proc.terminate()
        try:
            await asyncio.wait_for(self.proc.wait(), 5)
        except asyncio.TimeoutError:
            self.proc.kill()
            await self.proc.wait()
        self.reader.cancel()
        await asyncio.gather(self.reader, return_exceptions=True)


class Connector:
    def __init__(self, rpc, gateway, journal, account, chats=(), all_chats=False, send=False, files_dir=None):
        self.rpc, self.gateway, self.journal, self.account = rpc, gateway, journal, account
        self.files_dir = files_dir
        self.chats, self.all_chats, self.send = set(chats), all_chats, send
        self.worker, self.names = str(uuid.uuid4()), {}
        self.directory_at = 0
        journal.bind_account('signal/'+account)

    def accepts(self, chat):
        return self.all_chats or chat in self.chats

    def capture(self, envelope):
        event = normalized(envelope, self.account, self.names, self.files_dir)
        if event and self.accepts(event['conversation']['route']['chat']):
            self.journal.spool(event)

    async def heartbeat(self):
        await self.gateway.call('/connector/heartbeat', {'provider':'signal', 'account':self.account,
            'worker':self.worker, 'chats':sorted(self.chats), 'all_chats':self.all_chats, 'send':self.send})

    async def refresh_directory(self, publish=True):
        names = {}
        for group in await self.rpc.call('listGroups'):
            if group.get('isMember', True) and not group.get('isBlocked', False):
                names['group/'+group['id']] = group.get('name') or 'Signal group'
        for contact in await self.rpc.call('listContacts'):
            identity = contact.get('uuid')
            if not identity or not canonical_uuid(identity) or identity == self.account or contact.get('blocked') or contact.get('unregistered'):
                continue
            profile = contact.get('profile') or {}
            name = contact.get('name') or contact.get('nickName') or ' '.join(filter(None,[profile.get('givenName'),profile.get('familyName')])) or 'Signal contact'
            names['user/'+identity] = name
        self.names = {chat:name for chat,name in names.items() if valid_chat(chat) and self.accepts(chat)}
        if publish:
            entries = []
            for chat,name in self.names.items():
                entries.append({'id':'signal/'+hashlib.sha256((self.account+'\0'+chat).encode()).hexdigest(),
                    'service':'Signal','account':self.account,'recipient':chat,'thread':'','name':clipped(name,300),'demo':False,
                    'route':{'provider':'signal','account':self.account,'chat':chat,'thread':''}})
            for offset in range(0,len(entries),1000):
                await self.gateway.call('/connector/conversations',{'account':self.account,'conversations':entries[offset:offset+1000]})
        self.directory_at = asyncio.get_running_loop().time()

    async def flush(self):
        for receipt in self.journal.pending_receipts():
            await self.gateway.call('/connector/complete', receipt)
            self.journal.receipt(receipt, done=True)
        for key, event in self.journal.pending_inbox()[:100]:
            if self.accepts(event['conversation']['route']['chat']):
                await self.gateway.ingest_async(event)
                self.journal.acknowledge(key)

    async def deliver(self, job):
        draft, parent = job['draft'], job['parent']
        route = draft['target']['route']
        receipt = {'provider':'signal', 'account':self.account, 'draft':draft['id'],
                   'attempt':draft['attempt'], 'state':'uncertain', 'native_id':'',
                   'error':'Delivery interrupted. Check Signal before composing another reply.'}
        self.journal.receipt(receipt)
        started = False
        try:
            if not self.send or route['provider'] != 'signal' or route['account'] != self.account or not self.accepts(route['chat']) or route.get('thread'):
                raise ValueError('Reply outside connector scope')
            chat = route['chat']
            if not valid_chat(chat):
                raise ValueError('Invalid Signal destination')
            timestamp, author = parent['native_id'].split('/')
            if int(timestamp) <= 0 or not canonical_uuid(author) or chat.startswith('user/') and chat[5:] != author:
                raise ValueError('Invalid Signal reply parent')
            params = {'message':draft['body'], 'quoteTimestamp':int(timestamp),
                      'quoteAuthor':author, 'quoteMessage':parent['body']}
            if chat.startswith('group/'):
                params['groupId'] = chat[6:]
            else:
                params['recipients'] = [chat[5:]]
            with tempfile.TemporaryDirectory(prefix='amythest-signal-send-') as stage:
                files = []
                for index, attachment in enumerate(draft.get('attachments', [])):
                    query = urllib.parse.urlencode({'account':self.account, 'draft':draft['id'],
                                                   'attempt':draft['attempt'], 'id':attachment['id']})
                    data = await self.gateway.call('/connector/attachment?'+query, binary=True)
                    digest = hashlib.sha256(attachment['name'].encode()+b'\0'+data).hexdigest()
                    if digest != attachment['id'] or len(data) != attachment['size']:
                        raise ValueError('Attachment snapshot mismatch')
                    directory = Path(stage)/str(index)
                    directory.mkdir(mode=0o700)
                    name = Path(attachment['name']).name
                    if name in ('','.','..'):
                        raise ValueError('Invalid attachment filename')
                    file = directory/name
                    file.write_bytes(data)
                    file.chmod(0o600)
                    files.append(str(file))
                if files:
                    params['attachments'] = files
                await self.gateway.call('/connector/authorize', {'account':self.account, 'draft':draft['id'], 'attempt':draft['attempt']})
                started = True
                result = await self.rpc.call('send', params)
                # A group can partially succeed: do not report delivery or retry it.
                results = result.get('results') or []
                if not results or any(r.get('type') != 'SUCCESS' for r in results) or int(result.get('timestamp',0)) <= 0:
                    raise RuntimeError('Signal did not confirm every recipient')
                receipt.update(state='sent', native_id=str(result['timestamp']), error='')
        except Exception as error:
            receipt.update(state='uncertain' if started else 'failed',
                           error=('Signal delivery uncertain: ' if started else 'Signal reply not sent: ')+type(error).__name__)
        self.journal.receipt(receipt)
        await self.gateway.call('/connector/complete', receipt)
        self.journal.receipt(receipt, done=True)

    async def loop(self):
        previous = ''
        while True:
            if self.rpc.reader.done():
                self.rpc.reader.result()
            try:
                await self.heartbeat()
                if asyncio.get_running_loop().time()-self.directory_at>30:
                    await self.refresh_directory()
                await self.flush()
                job = (await self.gateway.call('/connector/claim', {'account':self.account,'worker':self.worker}))['delivery']
                if job:
                    await self.deliver(job)
                health = 'ready'
            except Exception as error:
                health = type(error).__name__
            if health != previous:
                print('Signal connector: '+health, flush=True)
                previous = health
            await asyncio.sleep(2)


def options(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('login','chats','watch'))
    parser.add_argument('--settings', default=os.getenv('AMYTHEST_MESSAGES_CONFIG') or str(Path(os.getenv('XDG_CONFIG_HOME',Path.home()/'.config'))/'amythest/messages.json'))
    args = parser.parse_args(argv)
    path = Path(args.settings).expanduser()
    whole = json.loads(path.read_text()) if path.exists() else {}
    settings = whole.get('connectors',{}).get('signal',{})
    known = {'binary','state_dir','account','gateway','token_file','chats','all_chats','send'}
    if set(settings)-known:
        raise ValueError('Unknown Signal connector setting')
    defaults = {'binary':'~/.local/share/amythest-signal/signal-cli', 'state_dir':'~/.local/state/amythest-signal',
                'gateway':whole.get('client',{}).get('url','http://127.0.0.1:8642'),
                'token_file':'~/.local/state/amythest-messages-poc/signal.token',
                'chats':[], 'all_chats':False, 'send':False, 'account':''}
    defaults.update(settings)
    defaults['gateway'] = os.getenv('AMYTHEST_MESSAGES_URL',defaults['gateway'])
    for key in ('send','all_chats'):
        if not isinstance(defaults[key],bool):
            raise ValueError(key+' must be a JSON boolean')
    if not isinstance(defaults['chats'],list) or any(not isinstance(c,str) or not valid_chat(c) for c in defaults['chats']):
        raise ValueError('Signal chats must contain user/UUID or group/BASE64 identifiers')
    for key in ('binary','state_dir','token_file'):
        defaults[key] = str(Path(defaults[key]).expanduser())
    return args, path, whole, defaults


async def main(args, config_path, whole, cfg):
    os.umask(0o077)
    state = Path(cfg['state_dir'])
    state.mkdir(mode=0o700,parents=True,exist_ok=True)
    lock = (state/'session.lock').open('a')
    try:
        fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
    except BlockingIOError:
        raise ValueError('Stop the Signal connector before login or listing chats')
    base = [cfg['binary'],'--data-dir',str(state/'account')]
    if args.command == 'login':
        async def accounts_on_disk():
            result = await asyncio.to_thread(subprocess.run,base+['-o','json','listAccounts'],capture_output=True,text=True,check=True)
            return json.loads(result.stdout)
        accounts = await accounts_on_disk()
        if not accounts:
            print('In Signal on your phone: Settings → Linked devices → Link new device. Scan the QR code.',flush=True)
            result = await asyncio.to_thread(subprocess.run,base+['link','-n','Amythest'])
            if result.returncode:
                raise ValueError('Signal linking did not finish; retry login')
            accounts = await accounts_on_disk()
        if len(accounts)!=1 or not canonical_uuid(accounts[0]['aci']):
            raise ValueError('Select an account ACI in the private messaging configuration')
        whole = json.loads(config_path.read_text()) if config_path.exists() else {}
        whole.setdefault('connectors',{}).setdefault('signal',{})['account']=accounts[0]['aci']
        config_path.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
        temporary=config_path.with_suffix('.json.next')
        temporary.write_text(json.dumps(whole,indent=2)+'\n');temporary.chmod(0o600)
        os.replace(temporary,config_path)
        print('Signal linked. Start amythest-signal.service to receive messages.',flush=True)
        return
    if not cfg['account'] or not canonical_uuid(cfg['account']):
        raise ValueError('Link Signal first with amy-signal login')
    journal = Journal(state/'bridge.db')
    # signal-cli saves received files under its data directory.
    connector = Connector(None,Gateway(cfg['gateway'],cfg['token_file']),journal,cfg['account'],cfg['chats'],cfg['all_chats'],cfg['send'],
                          files_dir=str(state/'account'/'attachments'))
    rpc = RPC(cfg['binary'],str(state/'account'),cfg['account'],connector.capture)
    connector.rpc = rpc
    await rpc.start()
    try:
        if args.command == 'chats':
            await connector.refresh_directory(publish=False)
            for chat,name in sorted(connector.names.items()):
                print(chat+'\t'+''.join(c for c in name if c.isprintable()))
            return
        if not cfg['all_chats'] and not cfg['chats']:
            raise ValueError('Select Signal chats or set all_chats in private settings')
        await connector.heartbeat()
        await rpc.call('subscribeReceive')
        await connector.loop()
    finally:
        await rpc.close()
        journal.db.close()
        lock.close()


if __name__ == '__main__':
    try:
        asyncio.run(main(*options()))
    except KeyboardInterrupt:
        pass
    except Exception as error:
        print(str(error) if isinstance(error,ValueError) else type(error).__name__+': Signal connector stopped',file=sys.stderr)
        sys.exit(1)
