#!/usr/bin/env python3
"""CLI for the Amythest Kanban API (note-backed boards in the vault's kanban/).

Auth, CSRF, and session caching are handled automatically. Configure with
KANBAN_BASE_URL and KANBAN_USERNAME/KANBAN_PASSWORD. Run with --help, or see
SKILL.md next to this script.
"""
from __future__ import annotations

import argparse
import json
import mimetypes
import os
import secrets
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

try:  # macOS pythons often lack system CAs; prefer certifi when present
    import certifi

    SSL_CONTEXT = ssl.create_default_context(cafile=certifi.where())
except ImportError:
    SSL_CONTEXT = ssl.create_default_context()

BASE_URL = os.environ.get("KANBAN_BASE_URL", "http://127.0.0.1:8639/kanban").rstrip("/")
# Credentials: env vars first, then the first readable env file.
ENV_FILES = [
    Path(p)
    for p in (
        os.environ.get("KANBAN_ENV_FILE"),
        str(Path.home() / ".config" / "amythest" / "env"),
    )
    if p
]
SESSION_FILE = Path(
    os.environ.get("KANBAN_SESSION_FILE", Path.home() / ".cache" / "amythest-kanban" / "session.json")
)
SESSION_COOKIE = "amythest_kanban_session"
STATUSES = ["triage", "backlog", "ready", "in_progress", "verify", "done"]
TIMEOUT = 30


class ApiError(Exception):
    def __init__(self, status: int, message: str):
        super().__init__(f"HTTP {status}: {message}")
        self.status = status
        self.message = message


# --------------------------------------------------------------------------- auth


def _credentials() -> tuple[str, str]:
    user = os.environ.get("KANBAN_USERNAME")
    password = os.environ.get("KANBAN_PASSWORD")
    if user and password:
        return user, password
    env_file = next((p for p in ENV_FILES if p.exists()), None)
    if env_file is None:
        raise SystemExit(
            "no credentials: set KANBAN_USERNAME/KANBAN_PASSWORD or make one of "
            + ", ".join(str(p) for p in ENV_FILES)
            + " readable"
        )
    values = {}
    for line in env_file.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip().strip("'\"")
    user = user or values.get("KANBAN_USERNAME", "")
    password = password or values.get("KANBAN_PASSWORD", "")
    if not user or not password:
        raise SystemExit(f"KANBAN_USERNAME/KANBAN_PASSWORD not found in {env_file}")
    return user, password


def _load_session() -> dict | None:
    try:
        data = json.loads(SESSION_FILE.read_text())
    except (OSError, ValueError):
        return None
    if not data.get("cookie") or not data.get("csrf"):
        return None
    if data.get("exp", 0) < time.time() + 60:
        return None
    if data.get("base") != BASE_URL:
        return None
    return data


def _store_session(cookie: str, csrf: str) -> dict:
    data = {"cookie": cookie, "csrf": csrf, "exp": time.time() + 7 * 3600, "base": BASE_URL}
    SESSION_FILE.parent.mkdir(parents=True, exist_ok=True)
    tmp = SESSION_FILE.with_suffix(".tmp")
    tmp.write_text(json.dumps(data))
    os.chmod(tmp, 0o600)
    tmp.replace(SESSION_FILE)
    return data


def _login() -> dict:
    user, password = _credentials()
    body = json.dumps({"username": user, "password": password}).encode()
    request = urllib.request.Request(
        f"{BASE_URL}/api/login", data=body, headers={"Content-Type": "application/json"}, method="POST"
    )
    try:
        with urllib.request.urlopen(request, timeout=TIMEOUT, context=SSL_CONTEXT) as response:
            payload = json.loads(response.read() or b"{}")
            cookie = ""
            for header in response.headers.get_all("Set-Cookie") or []:
                if header.startswith(SESSION_COOKIE + "="):
                    cookie = header.split(";", 1)[0]
    except urllib.error.HTTPError as error:
        raise ApiError(error.code, _error_message(error)) from None
    if not cookie:
        raise SystemExit("login succeeded but no session cookie was returned")
    return _store_session(cookie, payload.get("csrf", ""))


def _error_message(error: urllib.error.HTTPError) -> str:
    raw = error.read()
    try:
        return json.loads(raw).get("error", raw.decode("utf-8", "replace"))
    except ValueError:
        return raw.decode("utf-8", "replace")[:400]


# --------------------------------------------------------------------------- transport


def request(method: str, path: str, body: dict | None = None, files: tuple | None = None, raw: bool = False):
    """Call the API. Returns parsed JSON, or (bytes, headers) when raw=True."""
    session = _load_session() or _login()
    for attempt in range(2):
        headers = {"Cookie": session["cookie"], "Accept": "application/json"}
        if method != "GET":
            headers["X-CSRF-Token"] = session["csrf"]
        data = None
        if files is not None:
            data, content_type = _multipart(*files)
            headers["Content-Type"] = content_type
        elif body is not None:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(BASE_URL + path, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=TIMEOUT, context=SSL_CONTEXT) as response:
                payload = response.read()
                if raw:
                    return payload, response.headers
                if not payload:
                    return None
                return json.loads(payload)
        except urllib.error.HTTPError as error:
            if error.code in (401, 403) and attempt == 0:
                session = _login()
                continue
            raise ApiError(error.code, _error_message(error)) from None
        except urllib.error.URLError as error:
            raise SystemExit(f"cannot reach {BASE_URL}: {error.reason}")
    raise SystemExit("authentication failed")


def _multipart(field: str, filename: str, content: bytes, content_type: str) -> tuple[bytes, str]:
    boundary = "----kanban" + secrets.token_hex(16)
    safe = filename.replace('"', "").replace("\r", "").replace("\n", "")
    body = b"".join(
        [
            f"--{boundary}\r\n".encode(),
            f'Content-Disposition: form-data; name="{field}"; filename="{safe}"\r\n'.encode(),
            f"Content-Type: {content_type}\r\n\r\n".encode(),
            content,
            f"\r\n--{boundary}--\r\n".encode(),
        ]
    )
    return body, f"multipart/form-data; boundary={boundary}"


def quote(value: str) -> str:
    return urllib.parse.quote(value, safe="")


# --------------------------------------------------------------------------- rendering


def emit(value) -> None:
    json.dump(value, sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")


def short(card: dict) -> str:
    bits = [card["id"], card.get("title", "")]
    if card.get("assignee"):
        bits.append(f"@{card['assignee']}")
    if card.get("labels"):
        bits.append("[" + ",".join(card["labels"]) + "]")
    extras = []
    if card.get("comments"):
        extras.append(f"{len(card['comments'])}c")
    if card.get("attachments"):
        extras.append(f"{len(card['attachments'])}a")
    if extras:
        bits.append("(" + " ".join(extras) + ")")
    return "  " + "  ".join(bits)


def print_board(value: dict, status_filter: str | None) -> None:
    print(f"# {value['name']}  (dispatch: {'on' if value.get('dispatchEnabled') else 'off'})")
    cards = value.get("cards") or []
    for status in STATUSES:
        if status_filter and status != status_filter:
            continue
        group = [card for card in cards if card.get("status") == status]
        if not group and not status_filter:
            continue
        print(f"\n{status} ({len(group)})")
        for card in group:
            print(short(card))
    print()


def resolve_card(board: str, card_id: str) -> dict:
    value = request("GET", f"/api/boards/{quote(board)}")
    for card in value.get("cards") or []:
        if card["id"] == card_id:
            return card
    for card in request("GET", f"/api/boards/{quote(board)}/archive?limit=100") or []:
        if card["id"] == card_id:
            return card
    raise SystemExit(f"card {card_id} not found on board {board} (active or recent archive)")


# --------------------------------------------------------------------------- commands


def read_text_arg(inline: str | None, path: str | None) -> str | None:
    if path:
        return sys.stdin.read() if path == "-" else Path(path).read_text()
    return inline


def cmd_boards(args):
    emit(request("GET", "/api/boards"))


def cmd_board(args):
    value = request("GET", f"/api/boards/{quote(args.board)}")
    if args.status:
        value["cards"] = [c for c in value.get("cards") or [] if c.get("status") == args.status]
    if args.json:
        emit(value)
    else:
        print_board(value, args.status)


def cmd_card(args):
    emit(resolve_card(args.board, args.card))


def cmd_create(args):
    body = {
        "title": args.title,
        "description": read_text_arg(args.description, args.description_file) or "",
        "status": args.status,
        "assignee": args.assignee or "",
        "labels": args.label or [],
    }
    emit(request("POST", f"/api/boards/{quote(args.board)}/cards", body))


def cmd_update(args):
    patch = {}
    if args.title is not None:
        patch["title"] = args.title
    description = read_text_arg(args.description, args.description_file)
    if description is not None:
        patch["description"] = description
    if args.status is not None:
        patch["status"] = args.status
    if args.assignee is not None:
        patch["assignee"] = args.assignee
    if args.labels is not None:
        patch["labels"] = [l for l in (x.strip() for x in args.labels.split(",")) if l]
    if not patch:
        raise SystemExit("nothing to update")
    emit(request("PUT", f"/api/boards/{quote(args.board)}/cards/{quote(args.card)}", patch))


def cmd_move(args):
    body = {"status": args.status, "beforeId": args.before or ""}
    emit(request("POST", f"/api/boards/{quote(args.board)}/cards/{quote(args.card)}/move", body))


def cmd_transfer(args):
    body = {"destinationBoard": args.to, "confirm": True}
    emit(request("POST", f"/api/boards/{quote(args.board)}/cards/{quote(args.card)}/board", body))


def cmd_comment(args):
    body = {"body": read_text_arg(args.body, args.body_file) or ""}
    emit(request("POST", f"/api/boards/{quote(args.board)}/cards/{quote(args.card)}/comments", body))


def cmd_delete(args):
    request("DELETE", f"/api/boards/{quote(args.board)}/cards/{quote(args.card)}")
    print(f"deleted {args.card}")


def cmd_archive(args):
    path = f"/api/boards/{quote(args.board)}/archive?limit={args.limit}"
    if args.query:
        path += "&q=" + quote(args.query)
    cards = request("GET", path) or []
    if args.json:
        emit(cards)
    else:
        for card in cards:
            print(short(card).strip(), "|", (card.get("doneAt") or "")[:10])


def cmd_restore(args):
    body = {"status": args.status}
    emit(request("POST", f"/api/boards/{quote(args.board)}/archive/{quote(args.card)}/restore", body))


def cmd_attach(args):
    path = Path(args.file)
    content = path.read_bytes()
    if len(content) > 10 << 20:
        raise SystemExit("attachment must be at most 10 MiB")
    guessed = mimetypes.guess_type(path.name)[0] or "application/octet-stream"
    emit(
        request(
            "POST",
            f"/api/boards/{quote(args.board)}/cards/{quote(args.card)}/attachments",
            files=("file", path.name, content, guessed),
        )
    )


def cmd_attachments(args):
    emit(request("GET", f"/api/boards/{quote(args.board)}/cards/{quote(args.card)}/attachments"))


def cmd_download(args):
    content, headers = request(
        "GET",
        f"/api/boards/{quote(args.board)}/cards/{quote(args.card)}/attachments/{quote(args.attachment)}",
        raw=True,
    )
    target = Path(args.output) if args.output else Path(args.attachment)
    if args.output is None:
        disposition = headers.get("Content-Disposition", "")
        if "filename=" in disposition:
            target = Path(disposition.split("filename=", 1)[1].strip('"; '))
    target.write_bytes(content)
    print(f"{target}  ({len(content)} bytes)")


def cmd_detach(args):
    request(
        "DELETE",
        f"/api/boards/{quote(args.board)}/cards/{quote(args.card)}/attachments/{quote(args.attachment)}",
    )
    print(f"deleted attachment {args.attachment}")


def cmd_new_board(args):
    emit(request("POST", "/api/boards", {"name": args.name}))


def cmd_settings(args):
    body = {"dispatchEnabled": args.dispatch == "on"}
    emit(request("PUT", f"/api/boards/{quote(args.board)}/settings", body))


def cmd_search(args):
    needle = args.query.lower()
    boards = [args.board] if args.board else [b["name"] for b in request("GET", "/api/boards")]
    hits = []
    for name in boards:
        cards = (request("GET", f"/api/boards/{quote(name)}") or {}).get("cards") or []
        if args.archived:
            cards += request("GET", f"/api/boards/{quote(name)}/archive?limit=100") or []
        for card in cards:
            haystack = " ".join(
                [card.get("title", ""), card.get("description", ""), " ".join(card.get("labels") or []), card.get("assignee", "")]
            ).lower()
            if needle in haystack:
                hits.append((name, card))
    if args.json:
        emit([{"board": name, **card} for name, card in hits])
        return
    for name, card in hits:
        print(f"{name}/{card.get('status')}" + short(card))
    if not hits:
        print("no matches")


def cmd_raw(args):
    body = json.loads(args.data) if args.data else None
    result = request(args.method.upper(), args.path, body)
    if result is None:
        print("(no content)")
    else:
        emit(result)


# --------------------------------------------------------------------------- parser


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="kanban", description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest="command", required=True)

    def add(name, handler, help_text, **kwargs):
        p = sub.add_parser(name, help=help_text, **kwargs)
        p.set_defaults(func=handler)
        return p

    add("boards", cmd_boards, "list boards with per-status counts")

    p = add("board", cmd_board, "show one board")
    p.add_argument("board")
    p.add_argument("--status", choices=STATUSES)
    p.add_argument("--json", action="store_true", help="full JSON instead of a compact listing")

    p = add("card", cmd_card, "show one card as JSON (active, else recent archive)")
    p.add_argument("board")
    p.add_argument("card")

    p = add("create", cmd_create, "create a card")
    p.add_argument("board")
    p.add_argument("--title", required=True)
    p.add_argument("--description", default=None)
    p.add_argument("--description-file", help="read description from a file, or - for stdin")
    p.add_argument("--status", choices=STATUSES, default="triage")
    p.add_argument("--assignee")
    p.add_argument("--label", action="append", help="repeatable, max 10")

    p = add("update", cmd_update, "patch card fields (only the flags you pass)")
    p.add_argument("board")
    p.add_argument("card")
    p.add_argument("--title")
    p.add_argument("--description")
    p.add_argument("--description-file", help="read description from a file, or - for stdin")
    p.add_argument("--status", choices=STATUSES)
    p.add_argument("--assignee", help='pass "" to clear')
    p.add_argument("--labels", help="comma-separated, replaces all labels")

    p = add("move", cmd_move, "move a card to a status (status done archives it)")
    p.add_argument("board")
    p.add_argument("card")
    p.add_argument("--status", choices=STATUSES, required=True)
    p.add_argument("--before", help="card id to insert before, same status only")

    p = add("transfer", cmd_transfer, "move a card to another board, attachments included")
    p.add_argument("board")
    p.add_argument("card")
    p.add_argument("--to", required=True, dest="to")

    p = add("comment", cmd_comment, "append a comment (1-2000 chars)")
    p.add_argument("board")
    p.add_argument("card")
    p.add_argument("--body")
    p.add_argument("--body-file", help="read comment from a file, or - for stdin")

    p = add("delete", cmd_delete, "delete a card permanently")
    p.add_argument("board")
    p.add_argument("card")

    p = add("archive", cmd_archive, "list completed cards from done.md")
    p.add_argument("board")
    p.add_argument("--query", help="fuzzy filter")
    p.add_argument("--limit", type=int, default=100, help="1-100")
    p.add_argument("--json", action="store_true")

    p = add("restore", cmd_restore, "restore an archived card to an active status")
    p.add_argument("board")
    p.add_argument("card")
    p.add_argument("--status", choices=STATUSES[:-1], default="triage")

    p = add("attach", cmd_attach, "upload a file to a card (max 10 MiB, 10 per card)")
    p.add_argument("board")
    p.add_argument("card")
    p.add_argument("file")

    p = add("attachments", cmd_attachments, "list a card's attachments")
    p.add_argument("board")
    p.add_argument("card")

    p = add("download", cmd_download, "download an attachment")
    p.add_argument("board")
    p.add_argument("card")
    p.add_argument("attachment")
    p.add_argument("-o", "--output")

    p = add("detach", cmd_detach, "delete an attachment")
    p.add_argument("board")
    p.add_argument("card")
    p.add_argument("attachment")

    p = add("new-board", cmd_new_board, "create a board (lowercase, digits, hyphens)")
    p.add_argument("name")

    p = add("settings", cmd_settings, "toggle a board's dispatchEnabled flag")
    p.add_argument("board")
    p.add_argument("--dispatch", choices=["on", "off"], required=True)

    p = add("search", cmd_search, "text search across cards")
    p.add_argument("query")
    p.add_argument("--board", help="limit to one board")
    p.add_argument("--archived", action="store_true", help="also search done cards")
    p.add_argument("--json", action="store_true")

    p = add("raw", cmd_raw, "call any API path directly")
    p.add_argument("method")
    p.add_argument("path", help="e.g. /api/boards")
    p.add_argument("--data", help="JSON request body")

    return parser


def main() -> int:
    args = build_parser().parse_args()
    try:
        args.func(args)
    except ApiError as error:
        print(f"error: {error.message} (HTTP {error.status})", file=sys.stderr)
        return 1
    except FileNotFoundError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
