import datetime as dt
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import Mock
import connector as c

class ConnectorTests(unittest.TestCase):
    def setUp(self):
        self.now=dt.datetime(2026,11,19,tzinfo=dt.timezone.utc)
        self.config={'provider':'slack','account':'T123:U456','read_tools':['mcp__slack__search','mcp__slack__read_thread'], 'query':'in:engineering after:2026-11-18','state_dir':'unused','token_file':'unused','channels':['C789']}
        self.row={'id':'1795040000.123456','conversation_id':'C789','thread_id':'1795030000.000001','name':'Review','sender':'Morgan','sender_id':'U456','body':'Please review\nverbatim','at':'2026-11-18T12:00:00+00:00','outgoing':False,'url':'https://example.slack.com/archives/C789/p1795040000123456'}
        self.payload={'available':True,'account':'T123:U456','messages':[self.row]}
    def test_config_and_exact_ids(self):
        c.validate_config(self.config)
        event=c.normalize(self.config,self.payload,self.now)[0]
        self.assertEqual(event['message']['native_id'],self.row['id'])
        self.assertEqual(event['message']['body'],self.row['body'])
        self.assertEqual(event['message']['sender'],'Morgan')
        self.assertEqual(event['message']['sender_id'],'U456')
        for change in ({'send':True},{'read_tools':['mcp__slack__*']},{'read_tools':['mcp__slack__send_message']},{'account':'display name'}):
            with self.assertRaises(ValueError):c.validate_config(dict(self.config,**change))
    def test_reject_mismatch_and_malformed_batch(self):
        for bad in (dict(self.payload,account='T999:U456'),dict(self.payload,available=False),dict(self.payload,messages=[dict(self.row,id=1795040000.123456)]),dict(self.payload,messages=[dict(self.row,conversation_id='C000')]),dict(self.payload,messages=[dict(self.row,at='tomorrow')])):
            with self.assertRaises((ValueError,TypeError)):c.normalize(self.config,bad,self.now)
    def test_outlook_hash_matches_native_mail(self):
        cfg=dict(self.config,provider='outlook',account='reader@example.test');row=dict(self.row,id='opaque-message',conversation_id='opaque-conversation',thread_id='',sender_id='')
        event=c.normalize(cfg,dict(self.payload,account=cfg['account'],messages=[row]),self.now)[0]
        self.assertEqual(event['message']['native_id'],c.digest('opaque-message'))
        self.assertEqual(event['conversation']['id'],'outlook/'+c.digest(cfg['account']+'\0'+c.digest('opaque-conversation')))
    def test_journal_survives_failed_ingest_and_replays(self):
        with tempfile.TemporaryDirectory() as tmp:
            j=c.Journal(Path(tmp)/'state.db');gateway=Mock();events=c.normalize(self.config,self.payload,self.now)
            gateway.request.side_effect=[{},RuntimeError('offline')]
            with self.assertRaises(RuntimeError):c.sync(self.config,gateway,j,lambda cfg,now:events)
            self.assertEqual(len(j.pending_inbox()),1)
            gateway.request.side_effect=None;c.sync(self.config,gateway,j,lambda cfg,now:events)
            self.assertEqual(j.pending_inbox(),[])
            self.assertFalse(any('/claim' in call.args[0] or '/send' in call.args[0] for call in gateway.request.call_args_list));j.db.close()
    def test_claude_argv_is_read_only_and_prompt_on_stdin(self):
        def runner(args,**kwargs):
            self.assertIn('--tools',args);self.assertNotIn('--dangerously-skip-permissions',args)
            self.assertNotIn(self.config['query'],args);self.assertIn(self.config['query'],kwargs['input'])
            self.assertIn('Follow search pagination',kwargs['input'])
            self.assertIn('Resolve each distinct author',kwargs['input'])
            kwargs['stdout'].write(json.dumps({'structured_output':self.payload}));return Mock(returncode=0)
        events=c.read_claude(self.config,self.now,runner);self.assertEqual(len(events),1)
    def test_limit_and_sender_identity(self):
        for value in (0,101,True,'20'):
            with self.assertRaises(ValueError):c.validate_config(dict(self.config,limit=value))
        with self.assertRaises(ValueError):c.normalize(dict(self.config,limit=1),dict(self.payload,messages=[self.row,self.row]),self.now)
        with self.assertRaises(ValueError):c.normalize(self.config,dict(self.payload,messages=[dict(self.row,sender_id='Morgan')]),self.now)
if __name__=='__main__':unittest.main()

class ProfileScopeTests(unittest.TestCase):
    def test_profile_bound_slack_requires_exact_observed_identity(self):
        config={'provider':'slack','account':'profile/'+c.digest('U456'+'\0'+'Example workspace'),'slack_user':'U456','workspace':'Example workspace',
                'read_tools':['mcp__slack__read_user_profile'],'query':'mentions:me','state_dir':'unused','token_file':'unused'}
        c.validate_config(config)
        payload={'account':config['account'],'available':True,'user_id':'U456','workspace':'Example workspace','messages':[]}
        self.assertEqual(c.normalize(config,payload,dt.datetime.now(dt.timezone.utc)),[])
        payload['workspace']='Another workspace'
        with self.assertRaises(ValueError):c.normalize(config,payload,dt.datetime.now(dt.timezone.utc))

class ChannelListTests(unittest.TestCase):
    def setUp(self):
        self.config={'provider':'slack','account':'T123:U456','read_tools':['mcp__slack__search','mcp__slack__slack_list_user_channels','mcp__slack__slack_read_user_profile'],
                     'query':'after:2026-11-18','state_dir':'unused','token_file':'unused'}
        self.payload={'available':True,'account':'T123:U456','user_id':'','workspace':'','channels':[
            {'id':'C111','name':'release','type':'public_channel'},
            {'id':'D222','name':'Morgan  Example','type':'im'},
            {'id':'G333','name':'a, b, c','type':'mpim'},
            {'id':'C111','name':'release','type':'public_channel'}]}
    def test_channels_become_slack_conversations_with_import_ids(self):
        out=c.normalize_channels(self.config,self.payload)
        self.assertEqual([e['recipient'] for e in out],['C111','D222','G333'])
        self.assertEqual(out[0]['id'],'slack/'+c.digest('T123:U456\0C111\0'))
        self.assertEqual(out[1]['name'],'Morgan Example')
        self.assertEqual(out[0]['route'],{'provider':'slack','account':'T123:U456','chat':'C111','thread':''})
        # The same ID the message import gives the channel.
        row={'id':'1795040000.123456','conversation_id':'C111','thread_id':'','name':'release','sender':'Morgan','sender_id':'U456','body':'hi','at':'2026-11-18T12:00:00+00:00','outgoing':False,'url':''}
        event=c.normalize(self.config,{'available':True,'account':'T123:U456','messages':[row]},dt.datetime(2026,11,19,tzinfo=dt.timezone.utc))[0]
        self.assertEqual(event['conversation']['id'],out[0]['id'])
    def test_rejects_bad_lists_and_scopes(self):
        for bad in (dict(self.payload,available=False),dict(self.payload,account='T9:U9'),
                    dict(self.payload,channels=[{'id':'general','name':'x','type':'public_channel'}]),
                    dict(self.payload,channels=[{'id':'C1','name':'x','type':'workspace'}]),
                    dict(self.payload,channels=[{'id':'C1','name':'x'}])):
            with self.assertRaises(ValueError):c.normalize_channels(self.config,bad)
        scoped=c.normalize_channels(dict(self.config,channels=['D222']),self.payload)
        self.assertEqual([e['recipient'] for e in scoped],['D222'])
        with self.assertRaises(ValueError):c.list_tool(dict(self.config,read_tools=['mcp__slack__search']))
    def test_claude_gets_only_the_list_tools_and_sync_registers(self):
        def runner(args,**kwargs):
            allowed=args[args.index('--allowedTools')+1]
            self.assertEqual(allowed,'ToolSearch,mcp__slack__slack_list_user_channels,mcp__slack__slack_read_user_profile')
            self.assertIn('types="public_channel,private_channel,im,mpim"',kwargs['input'])
            kwargs['stdout'].write(json.dumps({'structured_output':self.payload}));return Mock(returncode=0)
        out=c.read_channels(self.config,dt.datetime.now(dt.timezone.utc),runner)
        gateway=Mock();gateway.request.return_value={}
        self.assertEqual(c.sync_channels(self.config,gateway,lambda cfg,now:out),3)
        paths=[call.args[0] for call in gateway.request.call_args_list]
        self.assertEqual(paths,['/connector/heartbeat','/connector/conversations'])
