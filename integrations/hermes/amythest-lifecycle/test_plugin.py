import contextvars
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("amythest_lifecycle", Path(__file__).with_name("__init__.py"))
plugin = importlib.util.module_from_spec(spec)
spec.loader.exec_module(plugin)


class LifecycleTests(unittest.TestCase):
    def test_exact_turn_outcomes_and_private_payloads(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {"AMYTHEST_COLLECTOR_STATE": directory}):
            plugin._start(session_id="session", turn_id="turn", platform="telegram", user_message="SECRET PROMPT")
            plugin._state("blocked", command="SECRET COMMAND")
            plugin._finish(session_id="session", turn_id="turn", failed=True, response_text="SECRET RESPONSE")
            plugin._close(session_id="session")
            paths = sorted((Path(directory)/"hooks").glob("*.json"))
            events = [json.loads(p.read_text()) for p in paths]
            self.assertEqual([e["phase"] for e in events], ["start", "blocked", "finish", "close"])
            self.assertEqual(events[2]["outcome"], "failure")
            self.assertNotIn("SECRET", "".join(p.read_text() for p in paths))
            self.assertTrue(all(p.stat().st_mode & 0o777 == 0o600 for p in paths))

    def test_parallel_contexts_cannot_cross_attribute_approval(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {"AMYTHEST_COLLECTOR_STATE": directory}):
            a, b = contextvars.Context(), contextvars.Context()
            a.run(plugin._start, session_id="a", turn_id="1")
            b.run(plugin._start, session_id="b", turn_id="2")
            a.run(plugin._state, "blocked")
            b.run(plugin._finish, session_id="b", turn_id="2", interrupted=True)
            events = [json.loads(p.read_text()) for p in sorted((Path(directory)/"hooks").glob("*.json"))]
            self.assertEqual(events[2]["session_id"], "a")
            self.assertEqual(events[3]["outcome"], "cancelled")

    def test_missing_turn_and_disabled_capture(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {"AMYTHEST_COLLECTOR_STATE": directory, "AMYTHEST_COLLECTOR_LIFECYCLE": "0"}):
            plugin._start(session_id="session")
            plugin._start(session_id="session", turn_id="turn")
            self.assertFalse((Path(directory)/"hooks").exists())

if __name__ == "__main__":
    unittest.main()
