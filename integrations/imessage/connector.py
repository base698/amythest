#!/usr/bin/env python3
"""Mac-only iMessage feeder for a central Amythest gateway. Requires imsg 0.4+."""
import argparse
import datetime as dt
import asyncio
import fcntl
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import time
import urllib.parse
import uuid

try:
    from messaging_common import Gateway, Journal
    from stdio_rpc import RPC
except ModuleNotFoundError:
    sys.path.insert(0,str(Path(__file__).resolve().parent.parent/'common'))
    from messaging_common import Gateway, Journal
    from stdio_rpc import RPC


def valid_chat(guid):
    return isinstance(guid,str) and guid.startswith(('iMessage;','SMS;','RCS;','any;')) and len(guid.split(';',2))==3 and all(guid.split(';',2)[1:]) and len(guid)<=300 and not any(c in guid for c in '\r\n\0')


def clipped(text,size):
    return str(text).encode()[:size].decode('utf-8','ignore')


class Source:
    def __init__(self,path,services=("iMessage","SMS","RCS")):
        self.services=tuple(services)
        self.db=sqlite3.connect(Path(path).as_uri()+'?mode=ro',uri=True)
        self.db.row_factory=sqlite3.Row

    def high_watermark(self):
        return self.db.execute('SELECT coalesce(max(ROWID),0) FROM message').fetchone()[0]

    def chats(self):
        placeholders=','.join('?' for _ in self.services)
        return [dict(r) for r in self.db.execute('SELECT ROWID AS id,guid,display_name AS name,chat_identifier AS identifier,service_name AS service FROM chat WHERE service_name IN ('+placeholders+')',self.services)]

    def chat(self,chat_id):
        placeholders=','.join('?' for _ in self.services)
        row=self.db.execute('SELECT ROWID AS id,guid,display_name AS name,chat_identifier AS identifier,service_name AS service FROM chat WHERE ROWID=? AND service_name IN ('+placeholders+')',(chat_id,*self.services)).fetchone()
        return dict(row) if row else None

    def retainable(self,message):
        # Do not turn expiring audio, tapbacks, deleted or service events into
        # permanently retained messages. SQL is read-only and identifiers bound.
        row=self.db.execute('SELECT guid,is_from_me,is_expirable,associated_message_type,is_system_message,is_service_message,date_retracted FROM message WHERE ROWID=?',(message['id'],)).fetchone()
        return bool(row and row['guid']==message['guid'] and bool(row['is_from_me'])==bool(message.get('is_from_me')) and not any(row[k] for k in ('is_expirable','associated_message_type','is_system_message','is_service_message','date_retracted')))


def normalized(message,chat,account):
    if not chat or not valid_chat(chat['guid']):
        return None
    body=message.get('text') or ''
    if message.get('attachments'):
        body+='\n[iMessage attachment · open in Messages on the Mac]'
    if not body:
        return None
    guid=chat['guid']
    return {'conversation':{'id':'imessage/'+hashlib.sha256((account+'\0'+guid).encode()).hexdigest(),
        'service':'Messages','account':account,'recipient':guid,'thread':'','demo':False,
        'name':clipped(chat.get('name') or chat.get('identifier') or 'iMessage',300),
        'route':{'provider':'imessage','account':account,'chat':guid,'thread':''}},
        'message':{'native_id':message['guid'],'sender':clipped(message.get('sender') or 'iMessage',200),
                   'body':clipped(body,20000),'at':message['created_at'],'outgoing':bool(message.get('is_from_me'))}}


class Connector:
    def __init__(self,rpc,source,gateway,journal,account,chats=(),all_chats=False,send=False):
        self.rpc,self.source,self.gateway,self.journal,self.account=rpc,source,gateway,journal,account
        self.chats,self.all_chats,self.send=set(chats),all_chats,send
        self.worker=str(uuid.uuid4())
        self.staging=Path(journal.db.execute("PRAGMA database_list").fetchone()[2]).parent/"outgoing"
        journal.bind_account('imessage/'+account)

    def accepts(self,guid):
        return valid_chat(guid) and (self.all_chats or guid in self.chats)

    def capture(self,message,advance=False):
        if self.source.retainable(message):
            chat=self.source.chat(message['chat_id'])
            if chat and self.accepts(chat['guid']):
                event=normalized(message,chat,self.account)
                if event:
                    self.journal.spool(event)
        # Spooling commits before advancing: a crash can replay, never skip.
        if advance:
            self.journal.advance('watch',int(message['id']))

    def notify(self,event):
        if event.get('method')=='error':
            raise ConnectionError('iMessage watch stopped')
        if event.get('method')=='message':
            self.capture(event['params']['message'],advance=True)

    async def heartbeat(self):
        await self.gateway.call('/connector/heartbeat',{'provider':'imessage','account':self.account,
            'worker':self.worker,'chats':sorted(self.chats),'all_chats':self.all_chats,'send':self.send})

    async def backfill(self,initial,days=30):
        # Subscribe before history so messages arriving during the scan survive.
        # A durable marker means interrupted initial scans run again (deduped).
        for chat in self.source.chats():
            marker='initial-with-sent/'+hashlib.sha256(chat['guid'].encode()).hexdigest()
            if not self.accepts(chat['guid']) or initial==0 or self.journal.checkpoint(marker):
                continue
            history=await self.rpc.call('messages.history',{'chat_id':chat['id'],'limit':initial,'attachments':True,'start':(dt.datetime.now(dt.timezone.utc)-dt.timedelta(days=days)).isoformat()})
            for message in history['messages']:
                self.capture(message)
            self.journal.advance(marker,1)

    async def flush(self):
        for receipt in self.journal.pending_receipts():
            await self.gateway.call('/connector/complete',receipt)
            self.journal.receipt(receipt,done=True)
        for key,event in self.journal.pending_inbox()[:100]:
            if self.accepts(event['conversation']['route']['chat']):
                await self.gateway.call('/connector/ingest',event)
                self.journal.acknowledge(key)

    async def deliver(self,job):
        draft=job['draft'];route=draft['target']['route']
        receipt={'provider':'imessage','account':self.account,'draft':draft['id'],'attempt':draft['attempt'],
                 'state':'uncertain','native_id':'','error':'Delivery interrupted. Check Messages before another reply.'}
        self.journal.receipt(receipt)
        started=False
        try:
            if not self.send or route['provider']!='imessage' or route['account']!=self.account or route.get('thread') or not self.accepts(route['chat']):
                raise ValueError('Reply outside connector scope')
            destination=next((c for c in self.source.chats() if c['guid']==route['chat']),None)
            if not destination:
                raise ValueError('Original iMessage conversation is unavailable')
            files=draft.get('attachments',[])
            if len(files)>1:
                raise ValueError('One attachment per iMessage reply is supported')
            transport={'iMessage':'imessage','SMS':'sms','RCS':'auto'}.get(destination.get('service','iMessage'))
            if not transport:
                raise ValueError('Unsupported Messages transport')
            params={'chat_guid':route['chat'],'service':transport,'text':draft['body']}
            if files:
                attachment=files[0]
                query=urllib.parse.urlencode({'account':self.account,'draft':draft['id'],'attempt':draft['attempt'],'id':attachment['id']})
                data=await self.gateway.call('/connector/attachment?'+query,binary=True)
                if hashlib.sha256(attachment['name'].encode()+b'\0'+data).hexdigest()!=attachment['id'] or len(data)!=attachment['size']:
                    raise ValueError('Attachment snapshot mismatch')
                name=Path(attachment['name']).name
                if name in ('','.','..'):
                    raise ValueError('Invalid attachment filename')
                directory=self.staging/attachment['id'];directory.mkdir(mode=0o700,parents=True,exist_ok=True)
                file=directory/name
                if file.exists():
                    if file.read_bytes()!=data:
                        raise ValueError('Staged attachment changed')
                else:
                    with file.open('xb') as stream:
                        stream.write(data)
                    file.chmod(0o600)
                params['file']=str(file)
            await self.gateway.call('/connector/authorize',{'account':self.account,'draft':draft['id'],'attempt':draft['attempt']})
            started=True
            result=await self.rpc.call('send',params)
            if result.get('ok') is not True:
                raise RuntimeError('Messages did not accept the request')
            # AppleScript returns acceptance, not a provider delivery receipt.
            receipt.update(state='submitted',error='Messages accepted the request. Delivery is unconfirmed; check Messages on the Mac.')
        except Exception as error:
            receipt.update(state='uncertain' if started else 'failed',error=('iMessage delivery uncertain: ' if started else 'iMessage reply not sent: ')+type(error).__name__)
        self.journal.receipt(receipt)
        await self.gateway.call('/connector/complete',receipt)
        self.journal.receipt(receipt,done=True)

    async def loop(self):
        while True:
            if self.rpc.reader.done():
                self.rpc.reader.result()
            await self.heartbeat()
            await self.flush()
            job=(await self.gateway.call('/connector/claim',{'account':self.account,'worker':self.worker}))['delivery']
            if job:
                await self.deliver(job)
            await asyncio.sleep(2)


def options(argv=None):
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command',choices=('doctor','chats','watch','service'))
    parser.add_argument('--settings',default=os.getenv('AMYTHEST_MESSAGES_CONFIG') or str(Path(os.getenv('XDG_CONFIG_HOME',Path.home()/'.config'))/'amythest/messages.json'))
    args=parser.parse_args(argv)
    whole=json.loads(Path(args.settings).expanduser().read_text())
    settings=whole.get('connectors',{}).get('imessage',{})
    defaults={'binary':'imsg','amy_binary':str(Path.home()/'bin/amy'),'database':str(Path.home()/'Library/Messages/chat.db'),
        'state_dir':str(Path.home()/'.local/state/amythest-imessage'),'account':'',
        'gateway':whole.get('client',{}).get('url','http://127.0.0.1:8642'),
        'token_file':str(Path.home()/'.local/state/amythest-imessage/imessage.token'),
        'chats':[],'all_chats':False,'send':False,'initial':10,'initial_days':30,'services':['iMessage','SMS','RCS']}
    if set(settings)-set(defaults):
        raise ValueError('Unknown iMessage connector setting')
    defaults.update(settings)
    if not defaults['account'] or str(uuid.UUID(defaults['account']))!=defaults['account']:
        raise ValueError('Configure a stable device UUID in connectors.imessage.account')
    for key in ('all_chats','send'):
        if not isinstance(defaults[key],bool):
            raise ValueError(key+' must be a JSON boolean')
    if not isinstance(defaults['chats'],list) or any(not valid_chat(c) for c in defaults['chats']):
        raise ValueError('chats must contain native iMessage GUIDs')
    if type(defaults['initial']) is not int or not 0<=defaults['initial']<=200:
        raise ValueError('initial must be between 0 and 200')
    if not isinstance(defaults['services'],list) or not defaults['services'] or any(s not in ('iMessage','SMS','RCS') for s in defaults['services']):
        raise ValueError('services must select iMessage, SMS or RCS')
    if type(defaults['initial_days']) is not int or not 1<=defaults['initial_days']<=365:
        raise ValueError('initial_days must be between 1 and 365')
    for key in ('binary','amy_binary','database','state_dir','token_file'):
        defaults[key]=str(Path(defaults[key]).expanduser())
    defaults['gateway']=os.getenv('AMYTHEST_MESSAGES_URL',defaults['gateway'])
    return args,defaults


async def main(args,cfg):
    os.umask(0o077)
    state=Path(cfg['state_dir']);state.mkdir(mode=0o700,parents=True,exist_ok=True)
    lock=(state/'session.lock').open('a')
    try:
        fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
    except BlockingIOError:
        raise ValueError('The iMessage feeder already owns this state directory')
    try:
        source=Source(cfg['database'],cfg['services'])
        source.high_watermark()
    except sqlite3.Error:
        raise ValueError('macOS denied Messages database access. Grant Full Disk Access to the host terminal/core.')
    if args.command=='doctor':
        print('Messages database readable; iMessage conversations available:',len(source.chats()))
        source.db.close();return
    if args.command=='chats':
        for chat in source.chats():
            print(chat['guid']+'\t'+''.join(c for c in (chat['name'] or chat['identifier']) if c.isprintable()))
        source.db.close();return
    if not cfg['all_chats'] and not cfg['chats']:
        raise ValueError('Select chats or enable all_chats in private iMessage settings')
    subprocess.run([cfg['amy_binary'],'messages','tunnel','-config',str(Path(args.settings).expanduser())],check=True)
    journal=Journal(state/'bridge.db')
    connector=Connector(None,source,Gateway(cfg['gateway'],cfg['token_file']),journal,cfg['account'],cfg['chats'],cfg['all_chats'],cfg['send'])
    rpc=RPC([cfg['binary'],'rpc','--db',cfg['database']],connector.notify);connector.rpc=rpc
    await rpc.start()
    try:
        high=source.high_watermark();checkpoint=journal.checkpoint('watch')
        if checkpoint>high:
            raise ValueError('Messages database was replaced; use a new feeder state directory')
        await connector.heartbeat()
        await rpc.call('watch.subscribe',{'since_rowid':checkpoint or high,'attachments':True})
        await connector.backfill(cfg['initial'],cfg['initial_days'])
        if not checkpoint:
            journal.advance('watch',high)
        await connector.flush()
        print('iMessage feeder ready; central gateway connected',flush=True)
        await connector.loop()
    finally:
        await rpc.close();source.db.close();journal.db.close();lock.close()


if __name__=='__main__':
    service='service' in sys.argv[1:2]
    while True:
        try:
            asyncio.run(main(*options()))
            if not service:
                break
        except KeyboardInterrupt:
            break
        except Exception as error:
            print(str(error) if isinstance(error,ValueError) else type(error).__name__+': iMessage feeder stopped',file=sys.stderr,flush=True)
            if not service:
                sys.exit(1)
        time.sleep(10)
