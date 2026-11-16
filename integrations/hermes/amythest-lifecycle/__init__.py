"""Metadata-only Hermes lifecycle capture; never performs network/model calls.

Hermes' on_session_end is the *turn* finalizer, not process/session teardown.
Only on_session_finalize closes a session. No response text, prompts, tool inputs,
outputs, approval commands or credentials are written to the spool.
"""
from __future__ import annotations

from contextvars import ContextVar
from datetime import datetime, timezone
import json
import logging
import os
from pathlib import Path
import tempfile
import time
import uuid

_active: ContextVar[tuple[str, str] | None] = ContextVar("amythest_collector_turn", default=None)
_log = logging.getLogger(__name__)


def _emit(phase: str, session: str, turn: str = "", outcome: str = "", platform: str = "") -> None:
    if os.environ.get("AMYTHEST_COLLECTOR_LIFECYCLE", "1") == "0" or not session:
        return
    try:
        state = Path(os.environ.get("AMYTHEST_COLLECTOR_STATE", str(Path.home() / ".local/state/amythest-collector")))
        spool = state / "hooks"
        spool.mkdir(mode=0o700, parents=True, exist_ok=True)
        identity = uuid.uuid4().hex
        event = {
            "id": identity, "at": datetime.now(timezone.utc).isoformat(),
            "phase": phase, "session_id": session[:200], "turn_id": turn[:200],
            "outcome": outcome, "platform": platform[:100],
            "project": os.environ.get("AMYTHEST_COLLECTOR_PROJECT", "Hermes")[:200],
            # A gateway's process cwd is not a user's working directory. Capture
            # a directory only when explicitly configured for this agent.
            "cwd": os.environ.get("AMYTHEST_COLLECTOR_WORKDIR", "")[:4096],
        }
        destination = spool / f"{time.time_ns():020d}-{identity}.json"
        fd, temporary = tempfile.mkstemp(prefix=".pending-", dir=spool)
        try:
            with os.fdopen(fd, "w") as stream:
                json.dump(event, stream, separators=(",", ":"))
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(temporary, destination)
            directory = os.open(spool, os.O_RDONLY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)
    except Exception:
        # Fail open for the agent, but leave an actionable warning. Never log
        # callback kwargs, which can contain conversation or approval payloads.
        _log.warning("Amythest Collector lifecycle spool unavailable; check collector state directory")


def _start(**kwargs):
    session, turn = str(kwargs.get("session_id") or ""), str(kwargs.get("turn_id") or "")
    if not session or not turn:
        return
    _active.set((session, turn))
    _emit("start", session, turn, platform=str(kwargs.get("platform") or ""))


def _finish(**kwargs):
    session, turn = str(kwargs.get("session_id") or ""), str(kwargs.get("turn_id") or "")
    if not session or not turn:
        return
    outcome = "cancelled" if kwargs.get("interrupted") else "failure" if kwargs.get("failed") else "success" if kwargs.get("completed") else "unknown"
    _emit("finish", session, turn, outcome, str(kwargs.get("platform") or ""))
    if _active.get() == (session, turn):
        _active.set(None)


def _close(**kwargs):
    _emit("close", str(kwargs.get("session_id") or ""))
    _active.set(None)


def _state(phase, **kwargs):
    # Approval hooks may expose session_key rather than the immutable session ID.
    # Use only this execution context; never guess another concurrently active turn.
    current = _active.get()
    if current:
        _emit(phase, *current)


def register(ctx):
    ctx.register_hook("pre_llm_call", _start)
    ctx.register_hook("on_session_end", _finish)
    ctx.register_hook("on_session_finalize", _close)
    ctx.register_hook("pre_approval_request", lambda **kw: _state("blocked", **kw))
    ctx.register_hook("post_approval_response", lambda **kw: _state("working", **kw))
