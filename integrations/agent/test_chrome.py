import datetime as dt
import unittest
from unittest.mock import Mock
import json
import chrome

class ChromeTests(unittest.TestCase):
    def setUp(self):
        self.now=dt.datetime(2026,11,19,12,tzinfo=dt.timezone.utc)
        self.config={'provider':'outlook-calendar','transport':'chrome','account':'reader@example.test','url':'https://outlook.cloud.microsoft/calendar/view/workweek','timezone':'America/New_York','state_dir':'unused','token_file':'unused'}
        self.item=dict(id='event-123',title='Design review',sender='Morgan',body='Review the design',url='https://outlook.cloud.microsoft/calendar/item/event-123',at='',start='2026-11-20T10:00:00-05:00',end='2026-11-20T11:00:00-05:00',timezone='America/New_York',all_day=False,location='Room A',status='confirmed')
        self.payload={'available':True,'account':'reader@example.test','items':[self.item]}
    def test_calendar_future_times_and_stable_revision(self):
        chrome.validate(self.config);account,events=chrome.normalize(self.config,self.payload,self.now)
        _,again=chrome.normalize(self.config,self.payload,self.now+dt.timedelta(hours=1))
        self.assertEqual(events[0]['message']['native_id'],again[0]['message']['native_id'])
        self.assertIn('2026-11-20T10:00:00-05:00',events[0]['message']['body'])
        revised=dict(self.payload,items=[dict(self.item,location='Room B')]);_,changed=chrome.normalize(self.config,revised,self.now)
        self.assertEqual(events[0]['conversation']['id'],changed[0]['conversation']['id'])
        self.assertNotEqual(events[0]['message']['native_id'],changed[0]['message']['native_id'])
    def test_invalid_account_scope_and_time(self):
        for payload in (dict(self.payload,account='someone@example.test'),dict(self.payload,available=False),dict(self.payload,items=[dict(self.item,end=self.item['start'])]),dict(self.payload,items=[dict(self.item,url='https://example.test/calendar/item/123')])):
            with self.assertRaises(ValueError):chrome.normalize(self.config,payload,self.now)
    def test_missing_native_event_id_is_explicit(self):
        _,events=chrome.normalize(self.config,dict(self.payload,items=[dict(self.item,id='')]),self.now)
        self.assertIn('No native event ID visible',events[0]['message']['body'])
    def test_mail_needs_observed_permalink(self):
        cfg=dict(self.config,provider='outlook-web',url='https://outlook.cloud.microsoft/mail/inbox')
        row=dict(self.item,url='https://outlook.cloud.microsoft/mail/inbox/id/opaque-example',at='2026-11-19T10:00:00+00:00')
        _,events=chrome.normalize(cfg,dict(self.payload,items=[row]),self.now);self.assertEqual(events[0]['conversation']['service'],'Outlook (Chrome)')
        row['url']=cfg['url']
        with self.assertRaises(ValueError):chrome.normalize(cfg,dict(self.payload,items=[row]),self.now)
    def test_browser_tools_only_and_no_navigation(self):
        def runner(args,**kwargs):
            self.assertIn('--chrome',args);allowed=args[args.index('--allowedTools')+1]
            self.assertNotIn('javascript',allowed);self.assertNotIn('computer',allowed);self.assertNotIn('navigate',allowed)
            self.assertIn('createIfEmpty=false',kwargs['input'])
            kwargs['stdout'].write(json.dumps({'structured_output':self.payload}));return Mock(returncode=0)
        account,events=chrome.read(self.config,self.now,runner);self.assertEqual(account,self.config['account']);self.assertEqual(len(events),1)
if __name__=='__main__':unittest.main()
