import asyncio
import base64
import copy
import hashlib
import json
import os
from pathlib import Path
import selectors
import subprocess
import tempfile
import unittest
from connector import Connector, Gateway, Journal, normalized

ACCOUNT='00000000-0000-4000-8000-000000000001'
AUTHOR='00000000-0000-4000-8000-000000000002'
GROUP=base64.b64encode(b'g'*32).decode()

def envelope(group=False):
    message={'timestamp':1700000000000,'message':'Can you send the image?','expiresInSeconds':0,'viewOnce':False}
    if group: message['groupInfo']={'groupId':GROUP,'groupName':'Test group'}
    return {'sourceUuid':AUTHOR,'sourceName':'Colleague','dataMessage':message}

def job(group=False):
    event=normalized(envelope(group),ACCOUNT)
    return {'draft':{'id':'draft','attempt':'attempt','target':event['conversation'],'body':'Here is **literal text**','attachments':[]},'parent':event['message']}

class FakeRPC:
    def __init__(self): self.calls=[];self.error=None;self.result={'timestamp':1700000000001,'results':[{'type':'SUCCESS'}]}
    async def call(self,method,params=None):
        params=copy.deepcopy(params)
        if params.get('attachments'): params['file_bytes']=[Path(f).read_bytes() for f in params['attachments']];params['file_names']=[Path(f).name for f in params['attachments']]
        self.calls.append((method,params))
        if self.error: raise self.error
        return self.result
class FakeGateway:
    def __init__(self): self.events=[];self.receipts=[];self.data=b'';self.revoke=False;self.fail_receipt=False
    async def call(self,path,data=None,binary=False):
        if path.startswith('/connector/attachment'): return self.data
        if path=='/connector/authorize' and self.revoke: raise PermissionError('revoked')
        if path=='/connector/complete':
            if self.fail_receipt: raise ConnectionError('gateway unavailable')
            self.receipts.append(copy.deepcopy(data))
        if path=='/connector/ingest': self.events.append(data)
        return {}

class SignalTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.temp=tempfile.TemporaryDirectory();self.journal=Journal(Path(self.temp.name)/'bridge.db')
        self.rpc=FakeRPC();self.gateway=FakeGateway()
        self.connector=Connector(self.rpc,self.gateway,self.journal,ACCOUNT,all_chats=True,send=True)
    async def asyncTearDown(self): self.journal.db.close();self.temp.cleanup()
    async def test_capture_scope_ephemeral_and_replay(self):
        self.connector.capture(envelope());self.connector.capture(envelope());self.assertEqual(len(self.journal.pending_inbox()),1)
        await self.connector.flush();self.assertEqual(len(self.gateway.events),1)
        for field,value in [('expiresInSeconds',60),('viewOnce',True),('storyContext',{'author':AUTHOR})]:
            event=envelope();event['dataMessage'][field]=value;self.assertIsNone(normalized(event,ACCOUNT))
        self.assertIsNone(normalized({'syncMessage':{'sentMessage':envelope()['dataMessage']}},ACCOUNT))
        self.connector.all_chats=False;self.connector.capture(envelope(True));self.assertEqual(self.journal.pending_inbox(),[])
    async def test_group_quote_file_exact_bytes(self):
        value=job(True);data=b'\x89PNG original image';name='wall.png';aid=hashlib.sha256(name.encode()+b'\0'+data).hexdigest()
        value['draft']['attachments']=[{'id':aid,'name':name,'size':len(data)}];self.gateway.data=data
        await self.connector.deliver(value)
        method,params=self.rpc.calls[0];self.assertEqual(method,'send');self.assertEqual(params['groupId'],GROUP)
        self.assertNotIn('recipients',params);self.assertEqual(params['quoteAuthor'],AUTHOR);self.assertEqual(params['quoteTimestamp'],1700000000000)
        self.assertEqual(params['message'],value['draft']['body']);self.assertEqual(params['file_bytes'],[data]);self.assertEqual(params['file_names'],[name])
        self.assertFalse(Path(params['attachments'][0]).exists());self.assertEqual(self.gateway.receipts[-1]['state'],'sent')
    async def test_sent_sync_preserves_destination_and_direction(self):
        for group in (False,True):
            data=envelope(group)['dataMessage'];data['destinationUuid']=AUTHOR
            event={'sourceUuid':ACCOUNT,'syncMessage':{'sentMessage':data}}
            result=normalized(event,ACCOUNT)
            self.assertTrue(result['message']['outgoing'])
            self.assertEqual(result['message']['native_id'],str(data['timestamp'])+'/'+ACCOUNT)
            self.assertEqual(result['conversation']['route']['chat'],'group/'+GROUP if group else 'user/'+AUTHOR)
            data['expiresInSeconds']=60;self.assertIsNone(normalized(event,ACCOUNT))
    async def test_direct_recipient_and_scope(self):
        await self.connector.deliver(job());self.assertEqual(self.rpc.calls[0][1]['recipients'],[AUTHOR])
        self.connector.chats=set();self.connector.all_chats=False;await self.connector.deliver(job());self.assertEqual(len(self.rpc.calls),1);self.assertEqual(self.gateway.receipts[-1]['state'],'failed')
    async def test_last_moment_revocation_blocks_send(self):
        self.gateway.revoke=True;await self.connector.deliver(job());self.assertEqual(self.rpc.calls,[]);self.assertEqual(self.gateway.receipts[-1]['state'],'failed')
    async def test_partial_or_timeout_never_retried(self):
        self.rpc.result['results'].append({'type':'NETWORK_FAILURE'});await self.connector.deliver(job());self.assertEqual(self.gateway.receipts[-1]['state'],'uncertain')
        self.rpc.error=TimeoutError();await self.connector.deliver(job());self.assertEqual(self.gateway.receipts[-1]['state'],'uncertain')
        await self.connector.flush();self.assertEqual(len(self.rpc.calls),2)
    async def test_receipt_replay_without_duplicate_send(self):
        self.gateway.fail_receipt=True
        with self.assertRaises(ConnectionError): await self.connector.deliver(job())
        self.assertEqual(len(self.journal.pending_receipts()),1);self.gateway.fail_receipt=False
        await self.connector.flush();self.assertEqual(len(self.rpc.calls),1);self.assertEqual(self.gateway.receipts[-1]['state'],'sent')

@unittest.skipUnless(os.getenv('AMYTHEST_TEST_BIN'),'requires Go gateway binary')
class FullWire(unittest.IsolatedAsyncioTestCase):
    async def test_focus_reply_and_provider_isolation(self):
        with tempfile.TemporaryDirectory() as directory:
            state=Path(directory);config=state/'config.json';config.write_text('{}')
            proc=subprocess.Popen([os.environ['AMYTHEST_TEST_BIN'],'messages','serve','-config',str(config),'-state',directory,'-listen','127.0.0.1:0','-irc',''],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
            journal=None
            try:
                selector=selectors.DefaultSelector();selector.register(proc.stdout,selectors.EVENT_READ);self.assertTrue(selector.select(15))
                url=proc.stdout.readline().strip().removeprefix('Messaging gateway: ')
                owner=Gateway(url,state/'owner.token');api=Gateway(url,state/'signal.token');telegram=Gateway(url,state/'telegram.token')
                journal=Journal(state/'journal.db');rpc=FakeRPC();c=Connector(rpc,api,journal,ACCOUNT,all_chats=True,send=True)
                await c.heartbeat();c.capture(envelope(True));await c.flush();items=await owner.call('/items');self.assertEqual(len(items),1)
                body=b'original image';a=await owner.call('/attachments',{'name':'image.png','data':base64.b64encode(body).decode()})
                d=await owner.call('/drafts',{'request_id':'signal-test','reply_to':items[0]['message']['id'],'body':'Here','attachments':[a['id']]})
                await owner.call('/send',{'draft':d['id']})
                with self.assertRaises(Exception): await telegram.call('/connector/claim',{'account':ACCOUNT,'worker':c.worker})
                delivery=(await api.call('/connector/claim',{'account':ACCOUNT,'worker':c.worker}))['delivery']
                await c.deliver(delivery);self.assertEqual(await owner.call('/items'),[])
                drafts=await owner.call('/drafts');self.assertEqual(drafts[0]['state'],'sent');self.assertEqual(rpc.calls[0][1]['file_bytes'],[body])
            finally:
                if journal: journal.db.close()
                proc.terminate()
                try: proc.communicate(timeout=5)
                except subprocess.TimeoutExpired: proc.kill();proc.communicate()

class DirectoryTests(unittest.IsolatedAsyncioTestCase):
    async def test_contacts_publish_without_synthetic_messages(self):
        class DirectoryRPC:
            async def call(self,method,params=None):
                if method=='listGroups':return [{'id':GROUP,'name':'Shared group','isMember':True}]
                return [{'uuid':AUTHOR,'name':'Test contact'},{'uuid':ACCOUNT,'name':'Self'},{'name':'No native identity'}]
        class DirectoryGateway(FakeGateway):
            async def call(self,path,data=None,binary=False):
                if path=='/connector/conversations':self.directory=data
                return await super().call(path,data,binary)
        with tempfile.TemporaryDirectory() as directory:
            journal=Journal(Path(directory)/'journal.db')
            try:
                api=DirectoryGateway();c=Connector(DirectoryRPC(),api,journal,ACCOUNT,all_chats=True)
                await c.refresh_directory()
                self.assertEqual({x['name'] for x in api.directory['conversations']},{'Test contact','Shared group'})
                self.assertEqual(api.events,[]);self.assertEqual(journal.pending_inbox(),[])
                c.all_chats=False;c.chats={'user/'+AUTHOR};await c.refresh_directory()
                self.assertEqual(len(api.directory['conversations']),1)
            finally:journal.db.close()
