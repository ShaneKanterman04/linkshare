#!/usr/bin/env python3
"""Best-effort Linkshare operation publisher."""

import json
import os
import sys
import urllib.request
from pathlib import Path


if len(sys.argv) != 4:
    raise SystemExit(2)
state, status, detail = sys.argv[1:]
endpoint = os.environ.get("HOMEBASE_OPERATIONS_URL", "").strip()
token = os.environ.get("HOMEBASE_OPERATIONS_TOKEN", "").strip()
config_path = Path(
    os.environ.get(
        "HOMEBASE_OPERATIONS_CONFIG",
        str(Path.home() / ".config" / "linkshare" / "operations.json"),
    )
)
if not endpoint or not token:
    try:
        config = json.loads(config_path.read_text(encoding="utf-8"))
        endpoint = str(config.get("url", "")).strip()
        token = str(config.get("token", "")).strip()
    except (OSError, ValueError):
        raise SystemExit(0)
if not endpoint or not token:
    raise SystemExit(0)
request = urllib.request.Request(
    endpoint.rstrip("/") + "/api/operations/events",
    data=json.dumps(
        {
            "id": f"provision-{os.environ.get('CTID', 'linkshare')}",
            "kind": "deploy-staging",
            "state": state,
            "title": "Linkshare provision",
            "status": status,
            "detail": detail or None,
            "ttlSeconds": 28800,
        },
        separators=(",", ":"),
    ).encode(),
    method="POST",
    headers={
        "Authorization": f"Bearer {token}",
        "Content-Type": "application/json",
        "User-Agent": "linkshare-operations/1",
    },
)
try:
    with urllib.request.urlopen(request, timeout=5) as response:
        raise SystemExit(0 if response.status in {200, 202} else 1)
except OSError:
    raise SystemExit(1)
