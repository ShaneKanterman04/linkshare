---
name: linkshare
description: Share and consume one-time links through the Linkshare homelab service. Use when the user asks Codex to send, save, share, queue, or remember a URL for them; asks an agent to check or process links waiting in Linkshare; or asks to consume or dismiss a Linkshare item.
---

# Linkshare

Use the bundled client for every operation. Do not handcraft requests unless debugging the client.

## Client

Resolve this skill's directory and run:

```sh
python3 scripts/linkshare.py discover
python3 scripts/linkshare.py health
python3 scripts/linkshare.py send URL --title "Title" --note "Why it matters" --actor "agent-name"
python3 scripts/linkshare.py list --target agents
python3 scripts/linkshare.py consume ID
```

The client reads `LINKSHARE_URL` first, then `~/.config/linkshare/config.json`. Treat a nonzero exit as failure. Run `discover` when operations are unclear or the server may be newer than this skill.

## Send to the user

1. Confirm the user asked to share, save, send, queue, or remember the link.
2. Use `send`; its default target is `owner`.
3. Supply a concise agent identifier with `--actor`.
4. Preserve useful context in `--title` and `--note` without inventing claims.
5. Report the created ID and expiry.

Do not post merely because a URL appeared in conversation. Never place credentials, tokens, private keys, or sensitive contents in a URL, title, or note.

## Consume links for agents

1. Run `list --target agents`.
2. Process only links relevant to the current task.
3. Open or inspect the URL.
4. Run `consume ID` only after successful use.
5. Report which IDs were consumed and what was used.

Consumption is permanent. Leave failed, inaccessible, and unrelated links waiting; they expire automatically.

## Failure handling

- Run `health` when the service is unreachable or unexpected.
- Run `discover` when an expected route is missing.
- Retry once for a transient connection failure.
- Correct validation errors before retrying.
- Never silently switch between `owner` and `agents`.

Read [references/api.md](references/api.md) only when debugging, extending the client, or interpreting an unfamiliar response.
