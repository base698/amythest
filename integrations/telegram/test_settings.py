import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from connector import parse_options

class SettingsTests(unittest.TestCase):
    def test_file_cli_and_environment_precedence(self):
        with tempfile.TemporaryDirectory() as root, patch.dict(os.environ, {}, clear=True):
            path = Path(root)/'messages.json'
            path.write_text(json.dumps({'client': {'url': 'http://127.0.0.1:9452'}, 'connectors': {'telegram': {'chats': ['100','-200'], 'send': True, 'state_dir': '~/private'}}}))
            args = parse_options(['watch','--settings',str(path)])
            self.assertEqual(args.chat, ['100','-200'])
            self.assertTrue(args.send)
            self.assertEqual(args.gateway, 'http://127.0.0.1:9452')
            self.assertFalse(args.state.startswith('~'))
            args = parse_options(['watch','--settings',str(path),'--chat','me','--no-send'])
            self.assertEqual(args.chat,['me'])
            self.assertFalse(args.send)
            os.environ['AMYTHEST_MESSAGES_URL']='http://127.0.0.1:9453'
            self.assertEqual(parse_options(['watch','--settings',str(path)]).gateway,os.environ['AMYTHEST_MESSAGES_URL'])
            self.assertEqual(parse_options(['watch','--settings',str(path),'--gateway','http://127.0.0.1:9454']).gateway,'http://127.0.0.1:9454')
            for bad in [{'send':'false'}, {'chats':'100'}, {'chats':[100]}, {'typo':True}, {'initial':300}]:
                path.write_text(json.dumps({'connectors': {'telegram':bad}}))
                with self.assertRaises(ValueError): parse_options(['watch','--settings',str(path)])
