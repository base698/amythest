import copy
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch

import connector as c
import slack_worker as w


class SlackWorkerTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / 'context.json'
        self.profile = {'content': [{'type': 'text', 'text': json.dumps({'result': 'User ID: U456\nDisplay Name: Casey\nOrganization Name: Example workspace\nEmail: casey@example.test\nStatus: available'})}]}
        self.config = {'provider': 'slack', 'transport': 'agent', 'account': 'T123:U456', 'workspace': 'Example workspace',
                       'send_tool': 'mcp__slack__slack_send_message', 'profile_tool': 'mcp__slack__slack_read_user_profile',
                       'send_profile': w.profile_fingerprint(self.profile)[0], 'token_file': 'unused',
                       'state_dir': self.tmp.name, 'channels': ['C789'], 'send': True}
        self.arguments = {'channel_id': 'C789', 'message': 'Reviewed', 'thread_ts': '1795100000.123456',
                          'reply_broadcast': False, 'unfurl_app_links': False}
        self.context = {'config': self.config, 'arguments': self.arguments, 'gateway': 'http://localhost:8642',
                        'authorization': {'account': 'T123:U456', 'draft': 'd', 'attempt': 'a'}}
        w.private_json(self.path, self.context)
        self.gateway = Mock()
        self.factory = Mock(return_value=self.gateway)
        self.job = {'draft': {'id': 'd', 'attempt': 'a', 'body': 'Reviewed', 'target': {'route': {
            'provider': 'slack', 'account': 'T123:U456', 'chat': 'C789', 'thread': '1795100000.123456'}}}}

    def event(self, tool, args, kind='PreToolUse', response=None):
        return {'hook_event_name': kind, 'tool_name': tool, 'tool_input': args, 'tool_use_id': 'one', 'tool_response': response}

    def verify(self):
        w.guard(self.path, self.event(self.config['profile_tool'], {}, 'PostToolUse', self.profile), self.factory)

    def send(self, args=None):
        return w.guard(self.path, self.event(self.config['send_tool'], self.arguments if args is None else args), self.factory)

    def test_guard_requires_profile_exact_arguments_and_single_send(self):
        with self.assertRaises(ValueError): self.send()
        self.verify()
        for change in ({'channel_id': 'C000'}, {'message': 'Different'}, {'thread_ts': ''},
                       {'draft_id': 'delete-me'}, {'reply_broadcast': True}, {'reply_broadcast': 0}, {'unfurl_app_links': True}):
            with self.assertRaises(ValueError): self.send(dict(self.arguments, **change))
        self.assertEqual(self.send()['hookSpecificOutput']['permissionDecision'], 'allow')
        self.gateway.request.assert_called_once_with('/connector/authorize', self.context['authorization'])
        with self.assertRaises(ValueError): self.send()

    def test_guard_blocks_unrelated_tools_profiles_and_revoked_grant(self):
        for tool, args in [('Bash', {}), ('mcp__slack__upload', {}), (self.config['profile_tool'], {'user_id': 'U999'})]:
            with self.assertRaises(ValueError): w.guard(self.path, self.event(tool, args), self.factory)
        self.verify(); self.gateway.request.side_effect = RuntimeError('revoked')
        with self.assertRaises(RuntimeError): self.send()
        state = json.loads(self.path.with_name('guard-state.json').read_text())
        self.assertFalse(state.get('attempted'))
        self.context['probe'] = True; w.private_json(self.path, self.context)
        with self.assertRaises(ValueError): self.send()

    def test_wrong_identity_and_profile_status(self):
        other = copy.deepcopy(self.profile)
        other['content'][0]['text'] = other['content'][0]['text'].replace('U456', 'U999')
        with self.assertRaises(ValueError):
            w.guard(self.path, self.event(self.config['profile_tool'], {}, 'PostToolUse', other), self.factory)
        with self.assertRaises(ValueError): self.send()
        status = copy.deepcopy(self.profile)
        status['content'][0]['text'] = status['content'][0]['text'].replace('available', 'away')
        self.assertEqual(w.profile_fingerprint(self.profile)[0], w.profile_fingerprint(status)[0])

    def test_receipts_require_native_destination_and_not_model_prose(self):
        link = 'https://example.slack.com/archives/C789/p1795100002123456'
        for response in ({'result': link}, {'content': [{'type': 'text', 'text': json.dumps({'result': link})}]},
                         [{'type': 'text', 'text': json.dumps({'message_link': link, 'message_context': {'channel_id': 'C789', 'message_ts': 1795100002.123456}})}],
                         {'channel': 'C789', 'ts': '1795100002.123456'}):
            self.assertEqual(w.tool_receipt(response, 'C789'), '1795100002.123456')
        for response in ('Sent successfully', {'result': link.replace('C789', 'C000')}, {'isError': True, 'result': link},
                         {'message_link': link, 'message_context': {'channel_id': 'C000'}},
                         [{'type': 'text', 'text': json.dumps({'ok': False, 'result': link})}]):
            self.assertEqual(w.tool_receipt(response, 'C789'), '')

    def test_sender_uses_hooks_and_marks_timeout_uncertain_without_resending(self):
        def runner(args, prompt, cwd):
            settings = json.loads(args[args.index('--settings') + 1])
            for name in ('PreToolUse', 'PostToolUse'):
                self.assertEqual(settings['hooks'][name][0]['hooks'][0]['onFailure'], 'block')
            self.assertNotIn('--dangerously-skip-permissions', args)
            self.assertNotIn(self.config['send_tool'], args[args.index('--allowedTools') + 1])
            context = Path(cwd) / 'context.json'
            w.guard(context, self.event(self.config['profile_tool'], {}, 'PostToolUse', self.profile), self.factory)
            w.guard(context, self.event(self.config['send_tool'], self.arguments), self.factory)
            raise subprocess.TimeoutExpired('claude', 180)
        with patch.object(subprocess, 'check_output', return_value='2.1.328'):
            result = w.ClaudeSender(self.config, 'http://localhost:8642', runner).invoke(self.job)
        self.assertEqual(result[0], 'uncertain')
        self.assertEqual(self.gateway.request.call_count, 1)

    def test_worker_replays_receipt_after_disconnect_never_provider_send(self):
        journal = c.Journal(Path(self.tmp.name) / 'journal.db'); self.addCleanup(journal.db.close)
        sender = Mock(); sender.invoke.return_value = ('sent', '1795100002.123456', '')
        worker = w.Worker(self.config, self.gateway, journal, sender)
        self.gateway.request.side_effect = RuntimeError('offline')
        with self.assertRaises(RuntimeError): worker.deliver(self.job)
        self.assertEqual(journal.pending_receipts()[0]['state'], 'sent')
        self.gateway.request.side_effect = None
        worker.flush(); worker.flush()
        self.assertEqual(journal.pending_receipts(), [])
        sender.invoke.assert_called_once()

    def test_worker_rejects_invalid_scope_and_preserves_uncertain_crash(self):
        journal = c.Journal(Path(self.tmp.name) / 'journal.db'); self.addCleanup(journal.db.close)
        sender = Mock(); worker = w.Worker(self.config, self.gateway, journal, sender)
        invalid = copy.deepcopy(self.job); invalid['draft']['target']['route']['chat'] = 'C000'
        worker.deliver(invalid)
        sender.invoke.assert_not_called()
        self.assertEqual(self.gateway.request.call_args.args[1]['state'], 'failed')
        sender.invoke.side_effect = RuntimeError('interrupted')
        worker.deliver(self.job)
        self.assertEqual(self.gateway.request.call_args.args[1]['state'], 'uncertain')


class SlackFileTests(unittest.TestCase):
    event = SlackWorkerTests.event
    verify = SlackWorkerTests.verify

    def setUp(self):
        SlackWorkerTests.setUp(self)
        import hashlib
        self.data = b'\x89PNG\r\n\x1a\nchart'
        fid = hashlib.sha256(b'chart.png\0' + self.data).hexdigest()
        self.file = {'id': fid, 'name': 'chart.png', 'size': len(self.data), 'mime': 'image/png'}
        self.config['upload_tool'], self.config['share_tool'] = w.file_tools(self.config['send_tool'])
        self.share_args = {'channel_id': 'C789', 'initial_comment': 'Reviewed', 'thread_ts': '1795100000.123456'}
        self.context.update(file=self.file, upload_arguments={'filename': 'chart.png', 'content_length': len(self.data)},
                            arguments=self.share_args)
        w.private_json(self.path, self.context)
        self.gateway.request.side_effect = lambda path, data=None, binary=False: self.data if path.startswith('/connector/attachment') else {}
        self.posted = []
        self.upload_response = {'content': [{'type': 'text', 'text': json.dumps({'upload_url': 'https://files.slack.com/upload/v1/ABC', 'file_id': 'F0ABC12345'})}]}

    def call(self, tool, args, kind='PreToolUse', response=None):
        return w.guard(self.path, self.event(tool, args, kind, response), self.factory,
                       poster=lambda url, data, mime: self.posted.append((url, data, mime)))

    def upload(self, response=None):
        self.call(self.config['upload_tool'], self.context['upload_arguments'])
        self.call(self.config['upload_tool'], self.context['upload_arguments'], 'PostToolUse', response or self.upload_response)

    def test_file_is_uploaded_by_the_guard_then_shared_once_exactly(self):
        self.assertEqual(w.file_tools('mcp__x__slack_send_message'), ('mcp__x__slack_get_file_upload_url', 'mcp__x__slack_complete_file_upload'))
        with self.assertRaises(ValueError): self.call(self.config['upload_tool'], self.context['upload_arguments'])
        self.verify()
        with self.assertRaises(ValueError): self.call(self.config['upload_tool'], {'filename': 'other.png', 'content_length': 3})
        with self.assertRaises(ValueError): self.call(self.config['send_tool'], self.arguments)
        with self.assertRaises(ValueError): self.call(self.config['share_tool'], dict(self.share_args, file_id='F0ABC12345'))
        self.upload()
        self.assertEqual(self.posted, [('https://files.slack.com/upload/v1/ABC', self.data, 'image/png')])
        for change in ({'file_id': 'F0OTHER9999'}, {'channel_id': 'C000'}, {'initial_comment': 'edited'}, {'title': 'x'}):
            with self.assertRaises(ValueError): self.call(self.config['share_tool'], {**self.share_args, 'file_id': 'F0ABC12345', **change})
        out = self.call(self.config['share_tool'], dict(self.share_args, file_id='F0ABC12345'))
        self.assertEqual(out['hookSpecificOutput']['permissionDecision'], 'allow')
        with self.assertRaises(ValueError): self.call(self.config['share_tool'], dict(self.share_args, file_id='F0ABC12345'))
        with self.assertRaises(ValueError): self.call(self.config['upload_tool'], self.context['upload_arguments'])

    def test_bytes_go_only_to_slack_and_must_match_the_snapshot(self):
        self.verify()
        bad = {'content': [{'type': 'text', 'text': json.dumps({'upload_url': 'https://evil.example/upload', 'file_id': 'F0ABC12345'})}]}
        with self.assertRaises(ValueError): self.upload(bad)
        self.assertEqual(self.posted, [])
        self.assertEqual(w.upload_target('upload_url: https://files.slack.com/upload/v1/X file_id: F0ABC12345'),
                         ('https://files.slack.com/upload/v1/X', 'F0ABC12345'))
        state = self.path.with_name('guard-state.json'); state.unlink()
        self.verify()
        self.gateway.request.side_effect = lambda path, data=None, binary=False: b'changed' if path.startswith('/connector/attachment') else {}
        with self.assertRaises(ValueError): self.upload()
        self.assertEqual(self.posted, [])

    def test_sender_reports_a_share_as_submitted(self):
        job = copy.deepcopy(self.job); job['draft']['attachments'] = [self.file]
        def runner(args, prompt, cwd):
            self.assertIn(self.config['upload_tool'], prompt); self.assertNotIn(self.config['send_tool'], prompt)
            context = Path(cwd) / 'context.json'
            def g(tool, a, kind='PreToolUse', response=None):
                return w.guard(context, self.event(tool, a, kind, response), self.factory, poster=lambda *x: self.posted.append(x))
            g(self.config['profile_tool'], {}, 'PostToolUse', self.profile)
            g(self.config['upload_tool'], {'filename': 'chart.png', 'content_length': len(self.data)})
            g(self.config['upload_tool'], {}, 'PostToolUse', self.upload_response)
            share = dict(self.share_args, file_id='F0ABC12345')
            g(self.config['share_tool'], share)
            g(self.config['share_tool'], share, 'PostToolUse', {'content': [{'type': 'text', 'text': 'Shared file F0ABC12345 "chart.png": https://example.slack.com/files/U456/F0ABC12345/chart.png'}]})
            return 0
        with patch.object(subprocess, 'check_output', return_value='2.1.328'):
            state, native, _ = w.ClaudeSender(self.config, 'http://localhost:8642', runner).invoke(job)
        self.assertEqual((state, native), ('submitted', ''))
        self.assertEqual(len(self.posted), 1)

    def test_worker_allows_one_file_without_text(self):
        journal = c.Journal(Path(self.tmp.name) / 'journal.db'); self.addCleanup(journal.db.close)
        sender = Mock(); sender.invoke.return_value = ('submitted', '', 'ok')
        worker = w.Worker(self.config, self.gateway, journal, sender)
        job = copy.deepcopy(self.job); job['draft']['body'] = ''; job['draft']['attachments'] = [self.file]
        worker.deliver(job); sender.invoke.assert_called_once()
        job['draft']['attachments'] = [self.file, self.file]
        worker.deliver(job); sender.invoke.assert_called_once()


if __name__ == '__main__':
    unittest.main()
