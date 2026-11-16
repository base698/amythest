import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock
from collector_migration import migrate


class MigrationTests(unittest.TestCase):
    def test_preserves_queue_credentials_and_pending_hooks(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d); old = root / '.local/state/corral'
            old.mkdir(parents=True); (old/'hooks').mkdir()
            original = {'Generation': 'same-stream', 'Acked': 7, 'Next': 9, 'Events': [{'seq': 8}]}
            (old/'hub.json').write_text(json.dumps(original))
            (old/'credential.json').write_text('test-credential')
            (old/'hooks/pending.json').write_text('pending')
            binary = root/'.local/bin/corral'; binary.parent.mkdir(); binary.write_text('old binary')
            unit = root/'Library/LaunchAgents/dev.base698.corral.sync.plist'; unit.parent.mkdir(parents=True); unit.write_text('old service')
            state = root/'.local/state/amythest-collector'
            run = Mock(); migrate(root, state, 'Darwin', run)
            self.assertEqual(json.loads((state/'hub.json').read_text()), original)
            self.assertEqual((state/'credential.json').read_text(), 'test-credential')
            self.assertEqual((old/'hooks/pending.json').read_text(), 'pending')
            self.assertTrue(old.is_symlink()); self.assertFalse(binary.exists()); self.assertFalse(unit.exists())
            migrate(root, state, 'Darwin', run)  # safe rerun, no state replacement
            self.assertEqual(run.call_count, 1)

    def test_refuses_existing_destination_before_stopping_anything(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d); state = root/'new'; state.mkdir()
            run = Mock()
            with self.assertRaises(RuntimeError): migrate(root,state,'Darwin',run)
            run.assert_not_called()

if __name__ == '__main__': unittest.main()
