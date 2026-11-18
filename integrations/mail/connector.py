#!/usr/bin/env python3
"""Private multi-account Gmail/Outlook feeder for the central Amythest gateway."""
import argparse
import asyncio
from contextlib import ExitStack
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import sys
import urllib.parse
import uuid

try:
    from messaging_common import Gateway, Journal, MAX_FILE
except ModuleNotFoundError:
    sys.path.insert(0,str(Path(__file__).resolve().parent.parent/'common'))
    from messaging_common import Gateway, Journal, MAX_FILE
from oauth import OAuth, HTTPFailure, google_login, microsoft_login, private_write
from providers import Gmail, Outlook

PROVIDERS={'gmail':Gmail,'outlook':Outlook}

class Connector:
    def __init__(self, provider, gateway, journal, send=False):
        self.provider,self.gateway,self.journal=provider,gateway,journal
        self.account=provider.account;self.kind='gmail' if isinstance(provider,Gmail) else 'outlook'
        self.worker=str(uuid.uuid4());self.send=send
        journal.bind_account(self.kind+'/'+self.account)
    async def heartbeat(self):
        await self.gateway.call('/connector/heartbeat',{'provider':self.kind,'account':self.account,
            'worker':self.worker,'chats':[],'all_chats':True,'send':self.send})
    async def flush(self):
        for receipt in self.journal.pending_receipts():
            await self.gateway.call('/connector/complete',receipt);self.journal.receipt(receipt,done=True)
        for key,payload in self.journal.pending_inbox():
            await self.gateway.call('/connector/ingest',payload);self.journal.acknowledge(key)
    async def deliver(self, job):
        draft=job['draft'];route=draft['target']['route']
        receipt={'provider':self.kind,'account':self.account,'draft':draft['id'],'attempt':draft['attempt'],
            'state':'failed','native_id':'','error':'Reply preparation failed; nothing sent'}
        attempted=False
        try:
            if not self.send or route['provider']!=self.kind or route['account']!=self.account:
                raise ValueError('Reply is outside this account')
            if job['parent'].get('outgoing'): raise ValueError('Select an incoming email to reply')
            files=[]
            for attachment in draft.get('attachments',[]):
                query=urllib.parse.urlencode({'account':self.account,'draft':draft['id'],'attempt':draft['attempt'],'id':attachment['id']})
                data=await self.gateway.call('/connector/attachment?'+query,binary=True)
                if len(data)>MAX_FILE or len(data)!=attachment['size'] or hashlib.sha256(attachment['name'].encode()+b'\0'+data).hexdigest()!=attachment['id']:
                    raise ValueError('Attachment snapshot changed')
                files.append((attachment,data))
            prepared=await self.provider.prepare(job,files)
            await self.gateway.call('/connector/authorize',{'account':self.account,'draft':draft['id'],'attempt':draft['attempt']})
            receipt.update(state='uncertain',error='Mail provider result is unknown; check Sent before retrying')
            self.journal.receipt(receipt)
            attempted=True
            state,native=await self.provider.send(job,prepared)
            receipt.update(state=state,native_id=native,error='')
        except HTTPFailure as error:
            if not attempted or error.status in (400,401,403,404,413,422,429):
                receipt.update(state='failed',error='Mail provider rejected the request (HTTP '+str(error.status)+'); nothing sent')
        except Exception:
            # A timeout after POST begins is ambiguous. The durable attempt is
            # never sent again, even if receipt submission also disconnects.
            pass
        self.journal.receipt(receipt)
        await self.gateway.call('/connector/complete',receipt)
        self.journal.receipt(receipt,done=True)
    async def pump(self):
        while True:
            await self.flush()
            job=(await self.gateway.call('/connector/claim',{'account':self.account,'worker':self.worker}))['delivery']
            if job: await self.deliver(job)
            await asyncio.sleep(2)
    async def keepalive(self):
        while True:
            await self.heartbeat()
            await asyncio.sleep(10)
    async def sync(self):
        while True:
            await self.provider.sync()
            await asyncio.sleep(30)
    async def watch(self):
        await self.heartbeat()
        tasks=[asyncio.create_task(self.pump()),asyncio.create_task(self.sync()),asyncio.create_task(self.keepalive())]
        try:
            done,_=await asyncio.wait(tasks,return_when=asyncio.FIRST_EXCEPTION)
            for task in done: task.result()
        finally:
            for task in tasks: task.cancel()
            await asyncio.gather(*tasks,return_exceptions=True)

def settings(path):
    whole=json.loads(Path(path).expanduser().read_text())
    mail=whole.get('connectors',{}).get('mail',{})
    if set(mail)-{'accounts'}: raise ValueError('Unknown mail setting')
    entries=mail.get('accounts',[])
    if not isinstance(entries,list): raise ValueError('mail.accounts must be an array')
    result=[];names=set();states=set();oauths=set()
    for entry in entries:
        cfg={'send':False,'initial_days':30,'initial_limit':200,'folders':[],
            'gateway':whole.get('client',{}).get('url','http://127.0.0.1:8642')}
        allowed=set(cfg)|{'name','provider','oauth_file','state_dir','token_file'}
        if not isinstance(entry,dict) or set(entry)-allowed: raise ValueError('Unknown mail account setting')
        cfg.update(entry)
        if not re.fullmatch(r'[a-zA-Z0-9_-]{1,48}',cfg.get('name','')) or cfg['name'] in names: raise ValueError('Mail account names must be unique simple labels')
        if cfg.get('provider') not in PROVIDERS: raise ValueError('Expected gmail or outlook')
        if type(cfg['send']) is not bool: raise ValueError('send must be a JSON boolean')
        if type(cfg['initial_days']) is not int or not 1<=cfg['initial_days']<=365: raise ValueError('initial_days must be 1..365')
        if type(cfg['initial_limit']) is not int or not 1<=cfg['initial_limit']<=2000: raise ValueError('initial_limit must be 1..2000')
        if not isinstance(cfg['folders'],list) or any(not isinstance(x,str) or not x or len(x)>1000 for x in cfg['folders']): raise ValueError('folders must contain Outlook folder identifiers')
        for field in ('oauth_file','state_dir','token_file'):
            if not cfg.get(field): raise ValueError('Mail account needs '+field)
            cfg[field]=str(Path(cfg[field]).expanduser().resolve())
        if cfg['state_dir'] in states or cfg['oauth_file'] in oauths: raise ValueError('Each account needs separate state and OAuth files')
        names.add(cfg['name']);states.add(cfg['state_dir']);oauths.add(cfg['oauth_file']);result.append(cfg)
    return result

async def run_account(cfg, command):
    state=Path(cfg['state_dir']);state.mkdir(mode=0o700,parents=True,exist_ok=True)
    with ExitStack() as resources:
        lock=resources.enter_context((state/'session.lock').open('a'))
        try: fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        except BlockingIOError: raise ValueError('Another mail connector owns this account state')
        journal=Journal(state/'bridge.db');resources.callback(journal.db.close)
        provider=PROVIDERS[cfg['provider']](OAuth(cfg['oauth_file'],cfg['provider']),journal,cfg)
        await provider.identity()
        journal.bind_account(cfg['provider']+'/'+provider.account)
        if command=='doctor':
            print(cfg['name']+': authenticated; account identity verified');return
        if command=='folders':
            if not isinstance(provider,Outlook): raise ValueError('Folder listing is for Outlook')
            for folder in await provider.folders(): print(folder)
            return
        connector=Connector(provider,Gateway(cfg['gateway'],cfg['token_file']),journal,cfg['send'])
        if command=='sync':
            await connector.heartbeat();await provider.sync();await connector.flush();return
        print(cfg['name']+': mail connector ready',flush=True)
        await connector.watch()

async def service_account(cfg):
    while True:
        try: await run_account(cfg,'watch')
        except asyncio.CancelledError: raise
        except Exception as error:
            # Only our label and error class, never provider messages or account data.
            print(cfg['name']+': '+type(error).__name__+'; reconnecting in 15 seconds',file=sys.stderr,flush=True)
        await asyncio.sleep(15)

def options(argv=None):
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--settings',default=os.getenv('AMYTHEST_MESSAGES_CONFIG') or str(Path.home()/'.config/amythest/messages.json'))
    commands=parser.add_subparsers(dest='command',required=True)
    for command in ('doctor','folders','sync','watch'):
        sub=commands.add_parser(command);sub.add_argument('--name')
    auth=commands.add_parser('auth');auth.add_argument('provider',choices=PROVIDERS)
    auth.add_argument('--oauth-file',required=True);auth.add_argument('--credentials');auth.add_argument('--client-id');auth.add_argument('--tenant',default='organizations');auth.add_argument('--no-browser',action='store_true')
    return parser.parse_args(argv)

async def main(args):
    cfgs=settings(args.settings)
    if args.name: cfgs=[cfg for cfg in cfgs if cfg['name']==args.name]
    if not cfgs: raise ValueError('Configure connectors.mail.accounts in private messages.json')
    await asyncio.gather(*(service_account(cfg) if args.command=='watch' else run_account(cfg,args.command) for cfg in cfgs))

if __name__=='__main__':
    os.umask(0o077)
    try:
        args=options()
        if args.command=='auth':
            if args.provider=='gmail':
                if not args.credentials: raise ValueError('Gmail needs --credentials with a private Desktop OAuth client file')
                google_login(args.credentials,args.oauth_file,not args.no_browser)
            else:
                if not args.client_id: raise ValueError('Outlook needs an approved public-client application --client-id')
                microsoft_login(args.client_id,args.tenant,args.oauth_file)
            print('OAuth credentials saved privately. Configure this account, then run amy-mail doctor.')
        else: asyncio.run(main(args))
    except KeyboardInterrupt: pass
    except (ValueError,FileNotFoundError) as error:
        print(str(error),file=sys.stderr);sys.exit(1)
    except Exception as error:
        print(type(error).__name__+': mail connector failed; check sign-in and gateway availability',file=sys.stderr);sys.exit(1)
