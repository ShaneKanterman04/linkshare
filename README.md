# Linkshare

A lightweight two-way link inbox for a person and their coding agents. Linkshare combines a responsive web UI, a small JSON API, SQLite persistence, and a reusable Codex skill.

## Features

- **For me** and **For agents** inboxes
- Optional titles and notes
- Read receipts, archive, and restore actions
- Agent-friendly JSON API and built-in `/guide`
- Single static Go binary with embedded frontend assets
- Direct unprivileged Proxmox LXC deployment
- Reusable `linkshare` Codex skill with a deterministic client

## Proxmox deployment

Docker is used only to build the static binary. The production LXC runs the binary directly under systemd.

Copy and configure the local deployment file:

```sh
cp .linkshare-deploy.env.example .linkshare-deploy.env
$EDITOR .linkshare-deploy.env
./deploy/provision-lxc.sh
```

The ignored `.linkshare-deploy.env` controls the Proxmox host, VMID, template, storage, network, owner label, and backup destination. The provisioner:

- Creates or upgrades an unprivileged Debian LXC
- Selects the first available configured address
- Installs a hardened systemd service and nftables policy
- Disables guest SSH in favor of `pct exec`
- Preserves SQLite data and retains the previous binary for rollback
- Verifies the deployed binary hash and health endpoint
- Installs the Codex skill and endpoint configuration locally

Administration uses the Proxmox host:

```sh
source .linkshare-deploy.env
ssh "$PVE_HOST" "pct exec $CTID -- systemctl status linkshare"
ssh "$PVE_HOST" "pct exec $CTID -- journalctl -u linkshare -f"
```

> Linkshare intentionally has no authentication. Restrict it to a trusted network or add authentication before broader exposure.

## Codex skill

The versioned skill lives in [`skills/linkshare`](skills/linkshare). Provisioning installs it into `${CODEX_HOME:-~/.codex}/skills/linkshare` and writes the actual endpoint to `~/.config/linkshare/config.json`.

Agents can then use `$linkshare` for requests such as:

- “Share this link with me.”
- “Check the links I left for agents.”
- “Mark Linkshare item 12 unread.”
- “Archive the link after you finish with it.”

The deterministic client can also be run directly:

```sh
python3 skills/linkshare/scripts/linkshare.py health
python3 skills/linkshare/scripts/linkshare.py send https://example.com --title "Example" --actor codex-agent
python3 skills/linkshare/scripts/linkshare.py list --target agents --state unread
python3 skills/linkshare/scripts/linkshare.py action 12 read --actor codex-agent
```

Set `LINKSHARE_URL` to override the configured endpoint.

## API

```sh
# Send a link to the owner
curl -X POST http://LINKSHARE_HOST:8080/api/v1/links \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com","title":"Example","note":"Worth reading","target":"owner","submitted_by":"codex-agent"}'

# Get links waiting for agents
curl 'http://LINKSHARE_HOST:8080/api/v1/links?target=agents&state=unread'

# Mark one consumed
curl -X PATCH http://LINKSHARE_HOST:8080/api/v1/links/1 \
  -H 'Content-Type: application/json' \
  -d '{"action":"mark_read","actor":"codex-agent"}'
```

List states are `active`, `unread`, `read`, `archived`, and `all`. Results are newest-first. `limit` defaults to 50 and accepts up to 200.

## Development

```sh
docker run --rm -v "$PWD:/src" -w /src golang:1.24-alpine go test ./...
node --check web/assets/app.js
node --check web/assets/guide.js
bash -n deploy/provision-lxc.sh deploy/pve-install.sh
docker build --platform linux/amd64 --target artifact --output type=local,dest=dist .
```

Runtime configuration:

| Variable | Default | Purpose |
| --- | --- | --- |
| `LINKSHARE_ADDR` | `:8080` | Listen address |
| `LINKSHARE_DB` | `./linkshare.db` | SQLite database path |
| `LINKSHARE_OWNER_NAME` | `Me` | Browser actor label |
