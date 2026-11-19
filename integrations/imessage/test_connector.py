import asyncio
import base64
import copy
import hashlib
import json
import os
from pathlib import Path
import selectors
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch, MagicMock
from connector import main
from connector import Connector,Gateway,Journal,Source,normalized,attachment_files,convert

ACCOUNT='00000000-0000-4000-8000-000000000001'
CHAT={'id':1,'guid':'any;+;chat-test','name':'Test group','identifier':'chat-test','service':'iMessage'}
MESSAGE={'id':1,'guid':'NATIVE-GUID-1','chat_id':1,'sender':'Test sender','is_from_me':False,'text':'Can you show the image?','created_at':'2026-11-17T10:00:00Z','attachments':[]}

class FakeSource:
    def chats(self):return [CHAT]
    def chat(self,chat_id):return CHAT if chat_id==1 else None
    def retainable(self,message):return True
class FakeRPC:
    def __init__(self):self.calls=[];self.error=None
    async def call(self,method,params=None):
        value=copy.deepcopy(params)
        if value.get('file'):value['bytes']=Path(value['file']).read_bytes();value['filename']=Path(value['file']).name
        self.calls.append((method,value))
        if self.error:raise self.error
        if method=='messages.history':return {'messages':[MESSAGE]}
        return {'ok':True}
class FakeGateway:
    def __init__(self):self.receipts=[];self.events=[];self.data=b'';self.revoke=False;self.fail_receipt=False
    async def call(self,path,data=None,binary=False):
        if path.startswith('/connector/attachment'):return self.data
        if path=='/connector/authorize' and self.revoke:raise PermissionError('revoked')
        if path=='/connector/complete':
            if self.fail_receipt:raise ConnectionError('lost response')
            self.receipts.append(copy.deepcopy(data))
        if path=='/connector/ingest':self.events.append(data)
        return {}
    async def ingest_async(self,event):
        self.events.append(event);return {}
def job():
    event=normalized(MESSAGE,CHAT,ACCOUNT)
    return {'draft':{'id':'draft','attempt':'attempt','target':event['conversation'],'body':'Literal **text**','attachments':[]},'parent':event['message']}

class Tests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.temp=tempfile.TemporaryDirectory();self.journal=Journal(Path(self.temp.name)/'bridge.db');self.rpc=FakeRPC();self.api=FakeGateway()
        self.connector=Connector(self.rpc,FakeSource(),self.api,self.journal,ACCOUNT,all_chats=True,send=True)
    async def asyncTearDown(self):self.journal.db.close();self.temp.cleanup()
    async def test_startup_failure_releases_lock_even_with_retained_traceback(self):
        source=MagicMock()
        cfg={'state_dir':self.temp.name,'database':'unused','services':['iMessage'],
             'all_chats':True,'chats':[],'amy_binary':'amy'}
        args=SimpleNamespace(command='service',settings='unused')
        errors=[]
        with patch('connector.Source',return_value=source), patch('connector.subprocess.run',side_effect=OSError('offline')):
            for _ in range(2):
                try: await main(args,cfg)
                except OSError as error: errors.append(error)
        self.assertEqual(len(errors),2)
        self.assertEqual(source.db.close.call_count,2)
    async def test_backfill_and_watch_are_durable_deduplicated(self):
        await self.connector.backfill(10);self.connector.capture(MESSAGE,advance=True)
        self.assertEqual(len(self.journal.pending_inbox()),1);self.assertEqual(self.journal.checkpoint('watch'),1)
        await self.connector.backfill(10);self.assertEqual(len(self.rpc.calls),1)
        await self.connector.flush();self.assertEqual(len(self.api.events),1);self.assertEqual(self.journal.pending_inbox(),[])
    async def test_native_replies_are_captured_as_outgoing(self):
        self.connector.capture(dict(MESSAGE,is_from_me=True),advance=True)
        await self.connector.flush()
        self.assertTrue(self.api.events[0]['message']['outgoing'])
    async def test_pinned_guid_attachment_and_unconfirmed_handoff(self):
        value=job();data=b'original image';name='image.png';aid=hashlib.sha256(name.encode()+b'\0'+data).hexdigest();self.api.data=data
        value['draft']['attachments']=[{'id':aid,'name':name,'size':len(data)}]
        await self.connector.deliver(value);method,params=self.rpc.calls[0]
        self.assertEqual(method,'send');self.assertEqual(params['chat_guid'],CHAT['guid']);self.assertEqual(params['service'],'imessage');self.assertNotIn('to',params)
        self.assertEqual(params['bytes'],data);self.assertEqual(params['filename'],name);self.assertEqual(self.api.receipts[-1]['state'],'submitted');self.assertEqual(self.api.receipts[-1]['native_id'],'')
        self.assertTrue(Path(params['file']).exists());self.assertEqual(Path(params['file']).read_bytes(),data)
    async def test_sms_and_rcs_use_the_original_mac_conversation(self):
        for service,transport in [('SMS','sms'),('RCS','auto')]:
            chat=dict(CHAT,service=service)
            class TransportSource(FakeSource):
                def chats(self):return [chat]
            self.connector.source=TransportSource()
            await self.connector.deliver(job())
            self.assertEqual(self.rpc.calls[-1][1]['service'],transport)
            self.assertEqual(self.rpc.calls[-1][1]['chat_guid'],CHAT['guid'])

    async def test_scope_and_revocation_block_send(self):
        self.api.revoke=True;await self.connector.deliver(job());self.assertEqual(self.rpc.calls,[]);self.assertEqual(self.api.receipts[-1]['state'],'failed')
        self.api.revoke=False;self.connector.all_chats=False;await self.connector.deliver(job());self.assertEqual(self.rpc.calls,[])
    async def test_ambiguous_result_and_receipt_replay(self):
        self.rpc.error=TimeoutError();await self.connector.deliver(job());self.assertEqual(self.api.receipts[-1]['state'],'uncertain')
        self.rpc.error=None;self.api.fail_receipt=True
        with self.assertRaises(ConnectionError):await self.connector.deliver(job())
        self.api.fail_receipt=False;await self.connector.flush();self.assertEqual(len(self.rpc.calls),2);self.assertEqual(self.api.receipts[-1]['state'],'submitted')
    async def test_sqlite_source_filters_sensitive_and_non_imessage_events(self):
        path=Path(self.temp.name)/'source.db';db=sqlite3.connect(path)
        db.executescript('CREATE TABLE message(guid TEXT,is_from_me INTEGER,is_expirable INTEGER,associated_message_type INTEGER,is_system_message INTEGER,is_service_message INTEGER,date_retracted INTEGER); CREATE TABLE chat(guid TEXT,display_name TEXT,chat_identifier TEXT,service_name TEXT);')
        db.execute('INSERT INTO message VALUES(?,0,0,0,0,0,0)',(MESSAGE['guid'],));db.execute('INSERT INTO chat VALUES(?,?,?,?)',(CHAT['guid'],'Test','chat-test','iMessage'));db.execute('INSERT INTO chat VALUES(?,?,?,?)',('SMS;+;test','SMS','test','SMS'));db.commit()
        source=Source(path);self.assertEqual(len(source.chats()),2);self.assertTrue(source.retainable(MESSAGE));self.assertEqual(source.chat(2)['service'],'SMS');restricted=Source(path,['iMessage']);self.assertIsNone(restricted.chat(2));restricted.db.close()
        db.execute('UPDATE message SET is_from_me=1');db.commit()
        self.assertTrue(source.retainable(dict(MESSAGE,is_from_me=True)))
        db.execute('UPDATE message SET is_from_me=0');db.commit()
        for flag in ['is_expirable','associated_message_type','is_system_message','is_service_message','date_retracted']:
            db.execute('UPDATE message SET '+flag+'=1');db.commit();self.assertFalse(source.retainable(MESSAGE));db.execute('UPDATE message SET '+flag+'=0');db.commit()
        with self.assertRaises(sqlite3.OperationalError):source.db.execute('DELETE FROM message')
        source.db.close();db.close()

@unittest.skipUnless(os.getenv('AMYTHEST_TEST_BIN'),'requires Go gateway binary')
class FullWire(unittest.IsolatedAsyncioTestCase):
    async def test_central_gateway_submission(self):
        with tempfile.TemporaryDirectory() as directory:
            state=Path(directory);cfg=state/'config.json';cfg.write_text('{}')
            proc=subprocess.Popen([os.environ['AMYTHEST_TEST_BIN'],'messages','serve','-config',str(cfg),'-state',directory,'-listen','127.0.0.1:0','-irc',''],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True);journal=None
            try:
                selector=selectors.DefaultSelector();selector.register(proc.stdout,selectors.EVENT_READ);self.assertTrue(selector.select(15))
                url=proc.stdout.readline().strip().removeprefix('Messaging gateway: ');owner=Gateway(url,state/'owner.token');api=Gateway(url,state/'imessage.token');wrong=Gateway(url,state/'signal.token')
                journal=Journal(state/'bridge.db');rpc=FakeRPC();c=Connector(rpc,FakeSource(),api,journal,ACCOUNT,all_chats=True,send=True)
                await c.heartbeat();c.capture(MESSAGE);await c.flush();items=await owner.call('/items');self.assertEqual(items[0]['conversation']['service'],'Messages')
                # A received photo travels with its message and downloads intact.
                root=state/'Attachments';(root/'x').mkdir(parents=True);photo=root/'x'/'IMG_9.png';photo.write_bytes(b'\x89PNG\r\n\x1a\nphoto')
                c.attachments_root=root
                c.capture(dict(MESSAGE,id=2,guid='NATIVE-GUID-2',text='Look',attachments=[{'original_path':str(photo),'transfer_name':'IMG_9.png','mime_type':'image/png','missing':False,'is_sticker':False}]))
                await c.flush()
                thread=await owner.call('/thread?conversation='+items[0]['conversation']['id'])
                got=[m for m in thread['messages'] if m['native_id']=='NATIVE-GUID-2'][0]['attachments']
                self.assertEqual([(a['name'],a['mime']) for a in got],[('IMG_9.png','image/png')])
                self.assertEqual(await owner.call('/attachment?id='+got[0]['id'],binary=True),photo.read_bytes())
                d=await owner.call('/drafts',{'request_id':'imessage-test','reply_to':items[0]['message']['id'],'body':'Here'})
                await owner.call('/send',{'draft':d['id']})
                with self.assertRaises(Exception):await wrong.call('/connector/claim',{'account':ACCOUNT,'worker':c.worker})
                claimed=(await api.call('/connector/claim',{'account':ACCOUNT,'worker':c.worker}))['delivery'];await c.deliver(claimed)
                drafts=await owner.call('/drafts');self.assertEqual(drafts[0]['state'],'submitted');self.assertEqual(len(await owner.call('/items')),1)
                self.assertEqual((await owner.call('/send',{'draft':d['id']}))['state'],'submitted')
            finally:
                if journal:journal.db.close()
                proc.terminate()
                try:proc.communicate(timeout=5)
                except subprocess.TimeoutExpired:proc.kill();proc.communicate()


class IncomingFileTests(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.root=Path(self.tmp.name)/'Attachments';self.root.mkdir()
    def tearDown(self):self.tmp.cleanup()
    def test_only_files_inside_messages_attachments_are_taken(self):
        photo=self.root/'ab'/'IMG_1.HEIC';photo.parent.mkdir();photo.write_bytes(b'x')
        outside=Path(self.tmp.name)/'secret.txt';outside.write_bytes(b'x')
        attachments=[{'original_path':str(photo),'transfer_name':'IMG_1.HEIC','mime_type':'image/heic','missing':False,'is_sticker':False},
                     {'original_path':str(outside),'transfer_name':'secret.txt','missing':False,'is_sticker':False},
                     {'original_path':str(self.root/'ab'/'..'/'..'/'secret.txt'),'transfer_name':'x','missing':False},
                     {'original_path':str(photo),'missing':True},{'original_path':str(photo),'is_sticker':True},
                     {'original_path':str(self.root/'ab'/'1.pluginPayloadAttachment'),'transfer_name':'1.pluginPayloadAttachment','missing':False}]
        files,left=attachment_files(attachments,self.root)
        self.assertEqual(files,[{'path':str(photo),'name':'IMG_1.HEIC','mime':'image/heic'}]);self.assertEqual(left,3)
        message=dict(MESSAGE,text='',attachments=attachments)
        event=normalized(message,CHAT,ACCOUNT,self.root)
        self.assertEqual(len(event['files']),1);self.assertIn('open in Messages',event['message']['body'])
        self.assertIn('open in Messages',normalized(message,CHAT,ACCOUNT)['message']['body'])
    def test_heic_becomes_jpeg_and_other_files_pass(self):
        calls=[]
        def runner(args,**kw):
            calls.append(args);Path(args[-1]).write_bytes(b'jpeg');return subprocess.CompletedProcess(args,0)
        heic=self.root/'IMG_2.heic';heic.write_bytes(b'x')
        out=convert({'path':str(heic),'name':'IMG_2.heic','mime':'image/heic'},self.root/'in',runner)
        self.assertTrue(out['staged']);self.assertEqual(out['name'],'IMG_2.jpg');self.assertTrue(Path(out['path']).is_file())
        self.assertEqual(calls[0][:9],['sips','-s','format','jpeg','-s','formatOptions','80','-Z','2048'])
        pdf={'path':str(heic),'name':'plan.pdf','mime':'application/pdf'};heic.rename(self.root/'plan.pdf');pdf['path']=str(self.root/'plan.pdf')
        self.assertIs(convert(pdf,self.root/'in',runner),pdf)
        def failing(args,**kw):return subprocess.CompletedProcess(args,1)
        heic.write_bytes(b'x');original={'path':str(heic),'name':'IMG_2.heic','mime':'image/heic'}
        self.assertIs(convert(original,self.root/'in2',failing),original)
    @unittest.skipUnless(sys.platform=='darwin' and shutil.which('sips'),'needs macOS sips')
    def test_real_sips_round_trip(self):
        import struct,zlib
        def chunk(t,d):return struct.pack('>I',len(d))+t+d+struct.pack('>I',zlib.crc32(t+d)&0xffffffff)
        raw=b''.join(b'\x00'+bytes([x*8%256,80,200])*1 for x in range(1))*1
        png=b'\x89PNG\r\n\x1a\n'+chunk(b'IHDR',struct.pack('>IIBBBBB',1,1,8,2,0,0,0))+chunk(b'IDAT',zlib.compress(raw))+chunk(b'IEND',b'')
        src=self.root/'p.png';src.write_bytes(png);heic=self.root/'p.heic'
        if subprocess.run(['sips','-s','format','heic',str(src),'--out',str(heic)],capture_output=True).returncode or not heic.exists():
            self.skipTest('this Mac cannot write HEIC')
        out=convert({'path':str(heic),'name':'p.heic','mime':'image/heic'},self.root/'in')
        self.assertEqual(Path(out['path']).read_bytes()[:3],b'\xff\xd8\xff')
