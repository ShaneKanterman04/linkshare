---
name: linkshare
description: Share and consume links through the Linkshare homelab service. Use when the user asks Codex to send, save, share, queue, or remember a URL for them; asks an agent to check or process links waiting in Linkshare; or asks to mark a Linkshare item read, unread, archived, or restored.
---

# Linkshare

Use the bundled client for every operation. Do not handcraft curl commands unless debugging the client itself.

## Client

Resolve this skill's directory and run:

```sh
python3 scripts/linkshare.py discover
python3 scripts/linkshare.py health
python3 scripts/linkshare.py send URL --title "Title" --note "Why it matters" --actor "agent-name"
python3 scripts/linkshare.py list --target agents --state unread
python3 scripts/linkshare.py action ID read --actor "agent-name"
```

The client reads `LINKSHARE_URL` first, then `~/.config/linkshare/config.json`. Treat a nonzero exit as failure and report the error rather than claiming the operation succeeded.

Run `discover` when the available service operations are unclear or the server may be newer than this skill.

## Workflow

### Send a link to the user

1. Confirm the user asked to share, save, send, queue, or remember the link.
2. Use `send` with the default `owner` target.
3. Supply a concise agent identifier with `--actor`.
4. Preserve useful context in `--title` and `--note` without inventing claims about the page.
5. Report the created link ID and title.

Do not post merely because a URL appeared in conversation. Never place credentials, tokens, private keys, or sensitive file contents in a URL, title, or note.

### Consume links intended for agents

1. Run `list --target agents --state unread`.
2. Process only the links relevant to the current task.
3. Open or inspect a link before changing its state.
4. Mark it `read` only after it was actually consumed.
5. Summarize what was used and which link IDs changed.

Leave unrelated links unread. If a link fails to load or cannot be used, report that and leave it unread.

### Change state

- Use `read` after successful consumption.
- Use `unread` when returning an item to the queue.
- Use `archive` only when the user explicitly asks or the current workflow explicitly requires cleanup.
- Use `restore` to return an archived item to its prior read state.

## Failure handling

- Run `health` when the service is unreachable or returns an unexpected response.
- Run `discover` when an expected route is missing or before extending the workflow to a new operation.
- Retry once for a transient connection failure.
- Do not retry validation errors without correcting the request.
- Do not silently switch targets between `owner` and `agents`.

Read [references/api.md](references/api.md) only when debugging, extending the client, or interpreting an unfamiliar API response.
