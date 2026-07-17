#!/usr/bin/env python3
"""Deterministic Linkshare API client for Codex agents."""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode, urlparse
from urllib.request import Request, urlopen


ACTIONS = {
    "read": "mark_read",
    "archive": "archive",
}


class ClientError(RuntimeError):
    pass


def config_path() -> Path:
    configured = os.environ.get("LINKSHARE_CONFIG")
    if configured:
        return Path(configured).expanduser()
    base = Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config"))
    return base / "linkshare" / "config.json"


def load_config() -> dict[str, Any]:
    path = config_path()
    if not path.exists():
        return {}
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ClientError(f"cannot read Linkshare config {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise ClientError(f"Linkshare config {path} must contain a JSON object")
    return value


def resolve_base_url(config: dict[str, Any]) -> str:
    value = str(os.environ.get("LINKSHARE_URL") or config.get("url") or "").strip()
    if not value:
        raise ClientError(
            "Linkshare URL is not configured; set LINKSHARE_URL or create "
            f"{config_path()}"
        )
    parsed = urlparse(value)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise ClientError("Linkshare URL must be a valid http or https origin")
    return value.rstrip("/")


def resolve_actor(value: str | None, config: dict[str, Any]) -> str:
    actor = str(
        value
        or os.environ.get("LINKSHARE_ACTOR")
        or config.get("default_actor")
        or "codex-agent"
    ).strip()
    if not actor:
        raise ClientError("actor must not be empty")
    return actor


def request_json(
    base_url: str,
    method: str,
    path: str,
    payload: dict[str, Any] | None = None,
) -> Any:
    data = None if payload is None else json.dumps(payload).encode("utf-8")
    request = Request(
        f"{base_url}{path}",
        data=data,
        method=method,
        headers={
            "Accept": "application/json",
            "Content-Type": "application/json",
            "User-Agent": "linkshare-skill/1",
        },
    )
    timeout = float(os.environ.get("LINKSHARE_TIMEOUT", "10"))
    try:
        with urlopen(request, timeout=timeout) as response:
            raw = response.read()
    except HTTPError as exc:
        raw = exc.read()
        try:
            detail = json.loads(raw).get("error", {}).get("message")
        except (json.JSONDecodeError, AttributeError):
            detail = None
        raise ClientError(f"Linkshare returned HTTP {exc.code}: {detail or exc.reason}") from exc
    except URLError as exc:
        raise ClientError(f"cannot reach Linkshare: {exc.reason}") from exc
    except TimeoutError as exc:
        raise ClientError("Linkshare request timed out") from exc

    if not raw:
        return None
    try:
        return json.loads(raw)
    except json.JSONDecodeError as exc:
        raise ClientError("Linkshare returned invalid JSON") from exc


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Use the Linkshare homelab service")
    commands = parser.add_subparsers(dest="command", required=True)

    commands.add_parser("discover", help="List available service endpoints")
    commands.add_parser("health", help="Check service health")

    send = commands.add_parser("send", help="Create a link")
    send.add_argument("url")
    send.add_argument("--title", default="")
    send.add_argument("--note", default="")
    send.add_argument("--target", choices=("owner", "agents"), default="owner")
    send.add_argument("--actor")

    list_parser = commands.add_parser("list", help="List links")
    list_parser.add_argument("--target", choices=("owner", "agents"), default="agents")
    list_parser.add_argument(
        "--state",
        choices=("active", "unread", "read", "archived", "all"),
        help=argparse.SUPPRESS,
    )
    list_parser.add_argument("--limit", type=int, choices=range(1, 201), default=50)
    list_parser.add_argument("--before-id", type=int)

    consume = commands.add_parser("consume", help="Permanently remove a used link")
    consume.add_argument("id", type=int)

    action = commands.add_parser("action", help=argparse.SUPPRESS)
    action.add_argument("id", type=int)
    action.add_argument("action", choices=tuple(ACTIONS))
    action.add_argument("--actor")

    return parser


def run(args: argparse.Namespace) -> Any:
    config = load_config()
    base_url = resolve_base_url(config)

    if args.command == "discover":
        return request_json(base_url, "GET", "/api/v1")

    if args.command == "health":
        return request_json(base_url, "GET", "/healthz")

    if args.command == "send":
        parsed = urlparse(args.url)
        if parsed.scheme not in {"http", "https"} or not parsed.netloc:
            raise ClientError("link URL must be a valid http or https address")
        return request_json(
            base_url,
            "POST",
            "/api/v1/links",
            {
                "url": args.url,
                "title": args.title,
                "note": args.note,
                "target": args.target,
                "submitted_by": resolve_actor(args.actor, config),
            },
        )

    if args.command == "list":
        query: dict[str, Any] = {
            "target": args.target,
            "limit": args.limit,
        }
        if args.state is not None:
            query["state"] = args.state
        if args.before_id is not None:
            query["before_id"] = args.before_id
        return request_json(base_url, "GET", f"/api/v1/links?{urlencode(query)}")

    if args.command == "consume":
        if args.id < 1:
            raise ClientError("link id must be positive")
        request_json(base_url, "DELETE", f"/api/v1/links/{args.id}")
        return {"consumed": True, "id": args.id}

    if args.command == "action":
        if args.id < 1:
            raise ClientError("link id must be positive")
        return request_json(
            base_url,
            "PATCH",
            f"/api/v1/links/{args.id}",
            {
                "action": ACTIONS[args.action],
                "actor": resolve_actor(args.actor, config),
            },
        )

    raise ClientError(f"unknown command: {args.command}")


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    try:
        result = run(args)
    except (ClientError, ValueError) as exc:
        print(f"linkshare: {exc}", file=sys.stderr)
        return 1
    json.dump(result, sys.stdout, indent=2, sort_keys=True)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
