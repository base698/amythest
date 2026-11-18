import asyncio
import base64
import copy
from email import policy
from email.parser import BytesParser
import hashlib
import json
import os
from pathlib import Path
import selectors
import subprocess
import tempfile
import unittest
from unittest.mock import patch
from connector import Connector, settings
from messaging_common import Gateway, Journal
from oauth import HTTPFailure, OAuth, private_write
from providers import Gmail, Outlook, conversation, digest, reply_mime

RAW=b'From: Example <sender@example.test>\r\nReply-To: replies@example.test\r\nTo: inbox@example.test\r\nSubject: Question\r\nMessage-ID: <original@example.test>\r\nReferences: <earlier@example.test>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nCan you review?'
MAIL={'id':'abc1','threadId':'def2','labelIds':['INBOX'],'internalDate':'1794990000000','raw':base64.urlsafe_b64encode(RAW).decode()}
ACCOUNT='inbox@example.test'

class FakeGateway:
    def __init__(self): self.receipts=[];self.events=[];self.data=b'';self.revoke=False;self.disconnect=False
    async def call(self,path,data=None,binary=False):
        if path.startswith('/connector/attachment'): return self.data
        if path=='/connector/authorize' and self.revoke: raise PermissionError()
        if path=='/connector/complete':
            if self.disconnect: raise ConnectionError()
            self.receipts.append(copy.deepcopy(data))
        if path=='/connector/ingest': self.events.append(data)
        return {}

class FakeGmail(Gmail):
    def __init__(self,journal):
        super().__init__(None,journal,{'initial_days':30,'initial_limit':10});self.account=ACCOUNT;self.calls=[];self.response={'id':'abc3','threadId':'def2'};self.error=None
    async def call(self,path,data=None,headers=None):
        self.calls.append((path,copy.deepcopy(data)))
        if path=='/messages/send':
            if self.error: raise self.error
            return self.response
        return copy.deepcopy(MAIL)

def job():
    return {'draft':{'id':'draft','attempt':'attempt','target':conversation('gmail',ACCOUNT,'def2','Question'),'body':'Review attached.','attachments':[]},
        'parent':{'native_id':'abc1','outgoing':False}}

class Tests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.temp=tempfile.TemporaryDirectory();self.journal=Journal(Path(self.temp.name)/'bridge.db');self.provider=FakeGmail(self.journal);self.gateway=FakeGateway();self.connector=Connector(self.provider,self.gateway,self.journal,send=True)
    async def asyncTearDown(self): self.journal.db.close();self.temp.cleanup()
    async def test_reply_pins_account_thread_parent_and_snapshot(self):
        value=job();data=b'Original PNG';name='image.png';aid=hashlib.sha256(name.encode()+b'\0'+data).hexdigest();self.gateway.data=data
        value['draft']['attachments']=[{'id':aid,'name':name,'size':len(data),'mime':'image/png'}]
        await self.connector.deliver(value)
        path,sent=self.provider.calls[-1];self.assertEqual(path,'/messages/send');self.assertEqual(sent['threadId'],'def2')
        message=BytesParser(policy=policy.default).parsebytes(base64.urlsafe_b64decode(sent['raw']))
        self.assertEqual(message['From'],ACCOUNT);self.assertEqual(message['To'],'replies@example.test');self.assertEqual(message['In-Reply-To'],'<original@example.test>')
        self.assertEqual(message['Subject'],'Re: Question');self.assertEqual(next(message.iter_attachments()).get_payload(decode=True),data)
        self.assertEqual(self.gateway.receipts[-1]['state'],'sent')
    async def test_cross_account_thread_and_revocation_do_not_send(self):
        for reason in ('account','thread','revoked','outgoing'):
            value=job();self.provider.calls=[];self.gateway.revoke=reason=='revoked'
            if reason=='account': value['draft']['target']['route']['account']='other@example.test'
            if reason=='thread': value['draft']['target']['route']['chat']='bad'
            if reason=='outgoing': value['parent']['outgoing']=True
            await self.connector.deliver(value)
            self.assertFalse(any(path=='/messages/send' for path,_ in self.provider.calls));self.assertEqual(self.gateway.receipts[-1]['state'],'failed')
    async def test_ambiguous_send_is_never_repeated_on_receipt_replay(self):
        self.provider.error=TimeoutError();self.gateway.disconnect=True
        with self.assertRaises(ConnectionError): await self.connector.deliver(job())
        self.gateway.disconnect=False;await self.connector.flush()
        self.assertEqual(self.gateway.receipts[-1]['state'],'uncertain')
        self.assertEqual(sum(path=='/messages/send' for path,_ in self.provider.calls),1)
    async def test_definite_rejection_is_failed(self):
        self.provider.error=HTTPFailure(403);await self.connector.deliver(job());self.assertEqual(self.gateway.receipts[-1]['state'],'failed')
    async def test_wrong_attachment_hash_never_sends(self):
        value=job();value['draft']['attachments']=[{'id':'bad','name':'file','size':0}]
        await self.connector.deliver(value);self.assertEqual(self.provider.calls,[])
    async def test_native_mail_normalization_and_spam_filter(self):
        incoming=self.provider.normalize(MAIL);self.assertFalse(incoming['message']['outgoing'])
        self.assertTrue(self.provider.normalize(dict(MAIL,labelIds=['SENT']))['message']['outgoing'])
        for label in ('SPAM','TRASH','DRAFT'): self.assertIsNone(self.provider.normalize(dict(MAIL,labelIds=[label])))
        self.provider.capture(MAIL);self.provider.capture(MAIL);self.assertEqual(len(self.journal.pending_inbox()),1)
    async def test_history_watermark_commits_only_after_all_pages_spooled(self):
        self.provider.put('gmail/history','100')
        async def call(path,data=None,headers=None):
            if path.startswith('/history'):
                if 'pageToken=' in path: raise ConnectionError()
                return {'history':[{'messagesAdded':[{'message':{'id':'abc1'}}]}],'nextPageToken':'second','historyId':'200'}
            return MAIL
        self.provider.call=call
        with self.assertRaises(ConnectionError): await self.provider.sync()
        self.assertEqual(self.provider.get('gmail/history'),'100');self.assertEqual(len(self.journal.pending_inbox()),1)
    async def test_expired_history_uses_pre_snapshot_cursor(self):
        self.provider.put('gmail/history','old')
        async def call(path,data=None,headers=None):
            if path.startswith('/history'): raise HTTPFailure(404)
            if path=='/profile': return {'historyId':'200'}
            if path.startswith('/messages?'): return {'messages':[{'id':'abc1'}]}
            return MAIL
        self.provider.call=call
        await self.provider.sync();self.assertEqual(self.provider.get('gmail/history'),'200');self.assertEqual(len(self.journal.pending_inbox()),1)
    async def test_outlook_immutable_parent_and_mime_reply(self):
        provider=Outlook(None,self.journal,{'folders':['inbox'],'initial_days':30});provider.account=ACCOUNT;provider.sent='sent-folder'
        original={'id':'immutable-id==','conversationId':'conversation==','subject':'Question','from':{'emailAddress':{'address':'sender@example.test'}},
            'replyTo':[{'emailAddress':{'address':'replies@example.test'}}],'body':{'contentType':'html','content':'<p>Hello</p><script>bad()</script>'},
            'receivedDateTime':'2026-11-18T10:00:00Z','parentFolderId':'inbox','internetMessageId':'<original@example.test>'}
        event=provider.normalize(original);self.assertNotIn('bad()',event['message']['body']);self.assertEqual(event['message']['native_id'],digest(original['id']))
        value=job();value['draft']['target']=event['conversation'];value['parent']=event['message'];calls=[]
        async def call(path,data=None,headers=None):
            calls.append((path,data,headers));return original if data is None else {}
        provider.call=call
        connector=Connector(provider,self.gateway,Journal(Path(self.temp.name)/'outlook.db'),send=True)
        try:
            await connector.deliver(value)
            self.assertIn('/immutable-id%3D%3D/reply',calls[-1][0]);self.assertEqual(calls[-1][2],{'Content-Type':'text/plain'})
            message=BytesParser(policy=policy.default).parsebytes(base64.b64decode(calls[-1][1]));self.assertEqual(message['To'],'replies@example.test')
            self.assertEqual(self.gateway.receipts[-1]['state'],'submitted');self.assertEqual(self.gateway.receipts[-1]['native_id'],'')
        finally: connector.journal.db.close()
    async def test_outlook_delta_page_survives_disconnect(self):
        provider=Outlook(None,self.journal,{'folders':['inbox'],'initial_days':30});provider.account=ACCOUNT;provider.sent='sent-folder'
        next_link=provider.base+'/me/mailFolders/inbox/messages/delta?$skiptoken=second'
        async def call(path,data=None,headers=None):
            if path==next_link: raise ConnectionError()
            return {'value':[],'@odata.nextLink':next_link}
        provider.call=call
        with self.assertRaises(ConnectionError): await provider.sync()
        self.assertEqual(provider.get('outlook/delta/inbox'),next_link)
    async def test_pagination_cannot_leak_bearer_to_other_origin(self):
        for path in ('https://example.test/steal','https://graph.microsoft.com@evil.test/v1.0/me'):
            with self.assertRaises(ValueError): await Gmail(None,self.journal,{}).call(path)
    def test_refresh_rotation_saved_privately_without_losing_refresh_token(self):
        path=Path(self.temp.name)/'oauth.json';private_write(path,{'provider':'gmail','client_id':'test','refresh_token':'private-fixture','expires_at':0})
        with patch('oauth.request',return_value={'access_token':'new-fixture','expires_in':3600}): self.assertEqual(OAuth(path,'gmail').access_token(),'new-fixture')
        value=json.loads(path.read_text());self.assertEqual(value['refresh_token'],'private-fixture');self.assertEqual(path.stat().st_mode&0o777,0o600)
    def test_account_config_rejects_shared_state_and_string_booleans(self):
        path=Path(self.temp.name)/'settings.json';entry={'name':'one','provider':'gmail','oauth_file':'/tmp/one','state_dir':'/tmp/state','token_file':'/tmp/token'}
        for entries in ([dict(entry,send='false')],[entry,dict(entry,name='two',oauth_file='/tmp/two')]):
            path.write_text(json.dumps({'connectors':{'mail':{'accounts':entries}}}))
            with self.assertRaises(ValueError): settings(path)

@unittest.skipUnless(os.getenv('AMYTHEST_TEST_BIN'),'requires Go gateway binary')
class FullWire(unittest.IsolatedAsyncioTestCase):
    async def test_gateway_ingest_claim_reply_and_provider_isolation(self):
        with tempfile.TemporaryDirectory() as directory:
            state=Path(directory);cfg=state/'config.json';cfg.write_text('{}')
            proc=subprocess.Popen([os.environ['AMYTHEST_TEST_BIN'],'messages','serve','-config',str(cfg),'-state',directory,'-listen','127.0.0.1:0','-irc',''],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
            journal=Journal(state/'bridge.db')
            try:
                selector=selectors.DefaultSelector();selector.register(proc.stdout,selectors.EVENT_READ);self.assertTrue(selector.select(15))
                url=proc.stdout.readline().strip().removeprefix('Messaging gateway: ')
                owner=Gateway(url,state/'owner.token');api=Gateway(url,state/'gmail.token');wrong=Gateway(url,state/'outlook.token')
                provider=FakeGmail(journal);connector=Connector(provider,api,journal,send=True)
                await connector.heartbeat();provider.capture(MAIL);await connector.flush()
                items=await owner.call('/items');self.assertEqual(items[0]['conversation']['service'],'Gmail')
                draft=await owner.call('/drafts',{'request_id':'mail-test','reply_to':items[0]['message']['id'],'body':'Reviewed'})
                await owner.call('/send',{'draft':draft['id']})
                with self.assertRaises(Exception): await wrong.call('/connector/claim',{'account':ACCOUNT,'worker':connector.worker})
                claimed=(await api.call('/connector/claim',{'account':ACCOUNT,'worker':connector.worker}))['delivery']
                await connector.deliver(claimed)
                drafts=await owner.call('/drafts');self.assertEqual(drafts[0]['state'],'sent');self.assertEqual(await owner.call('/items'),[])
                self.assertEqual((await owner.call('/send',{'draft':draft['id']}))['state'],'sent')
                self.assertEqual(sum(path=='/messages/send' for path,_ in provider.calls),1)
            finally:
                journal.db.close();proc.terminate()
                try: proc.communicate(timeout=5)
                except subprocess.TimeoutExpired: proc.kill();proc.communicate()

if __name__=='__main__': unittest.main()
