#!/usr/bin/env bash
set -Eeuo pipefail

exec 9>"${TMPDIR:-/tmp}/linkshare-provision.lock"
if ! flock -n 9; then
	printf '[linkshare] Another local Linkshare provisioner is already running\n' >&2
	exit 1
fi

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
DEPLOY_CONFIG="${LINKSHARE_DEPLOY_CONFIG:-$ROOT_DIR/.linkshare-deploy.env}"
[[ -f "$DEPLOY_CONFIG" ]] || {
	printf '[linkshare] Missing %s; copy .linkshare-deploy.env.example and configure it\n' "$DEPLOY_CONFIG" >&2
	exit 1
}
# shellcheck source=/dev/null
source "$DEPLOY_CONFIG"

: "${PVE_HOST:?PVE_HOST is required}"
: "${CTID:?CTID is required}"
: "${LAN_CIDR:?LAN_CIDR is required}"
: "${LINKSHARE_PORT:?LINKSHARE_PORT is required}"
: "${OWNER_NAME:?OWNER_NAME is required}"
: "${BACKUP_STORAGE:?BACKUP_STORAGE is required}"
LINK_TTL="${LINK_TTL:-168h}"

DIST_DIR="$ROOT_DIR/dist"
REMOTE_DIR="/tmp/linkshare-deploy-$$"

cleanup() {
	ssh -o BatchMode=yes "$PVE_HOST" "rm -rf '$REMOTE_DIR'" >/dev/null 2>&1 || true
}
trap cleanup EXIT

printf '[linkshare] Running tests\n'
docker run --rm -v "$ROOT_DIR:/src" -w /src golang:1.24-alpine sh -c 'go vet ./... && go test ./...'
node --check "$ROOT_DIR/web/assets/app.js"
node --check "$ROOT_DIR/web/assets/guide.js"
SKILL_VALIDATOR="${CODEX_HOME:-$HOME/.codex}/skills/.system/skill-creator/scripts/quick_validate.py"
if [[ -f "$SKILL_VALIDATOR" ]]; then
	python3 "$SKILL_VALIDATOR" "$ROOT_DIR/skills/linkshare"
fi
python3 -m py_compile "$ROOT_DIR/skills/linkshare/scripts/linkshare.py"

printf '[linkshare] Building static Linux/AMD64 artifact\n'
rm -rf "$DIST_DIR"
docker build --platform linux/amd64 --target artifact --output "type=local,dest=$DIST_DIR" "$ROOT_DIR"
test -x "$DIST_DIR/linkshare"

python3 - "$ROOT_DIR/deploy/linkshare.env.template" "$DIST_DIR/linkshare.env" "$OWNER_NAME" "$LINKSHARE_PORT" "$LINK_TTL" <<'PY'
from pathlib import Path
import sys

template, output, owner, port, link_ttl = sys.argv[1:]
text = Path(template).read_text(encoding="utf-8")
text = text.replace("@OWNER_NAME@", owner).replace("@PORT@", port).replace("@LINK_TTL@", link_ttl)
Path(output).write_text(text, encoding="utf-8")
PY

python3 - "$ROOT_DIR/deploy/nftables.conf.template" "$DIST_DIR/nftables.conf" "$LAN_CIDR" "$LINKSHARE_PORT" <<'PY'
from pathlib import Path
import sys

template, output, cidr, port = sys.argv[1:]
text = Path(template).read_text(encoding="utf-8")
text = text.replace("@LAN_CIDR@", cidr).replace("@PORT@", port)
Path(output).write_text(text, encoding="utf-8")
PY

printf '[linkshare] Uploading deployment bundle to %s\n' "$PVE_HOST"
if ssh -o BatchMode=yes "$PVE_HOST" "pct config '$CTID' >/dev/null 2>&1"; then
	printf '[linkshare] Backing up existing CT %s before migration\n' "$CTID"
	ssh -o BatchMode=yes "$PVE_HOST" "vzdump '$CTID' --storage '$BACKUP_STORAGE' --mode snapshot --compress zstd"
fi
ssh -o BatchMode=yes "$PVE_HOST" "mkdir -p '$REMOTE_DIR'"
scp -q \
	"$DIST_DIR/linkshare" \
	"$DIST_DIR/linkshare.env" \
	"$DIST_DIR/nftables.conf" \
	"$DEPLOY_CONFIG" \
	"$ROOT_DIR/deploy/linkshare.service" \
	"$ROOT_DIR/deploy/pve-install.sh" \
	"$PVE_HOST:$REMOTE_DIR/"

config_name=$(basename "$DEPLOY_CONFIG")
ssh -o BatchMode=yes "$PVE_HOST" \
	"chmod 0700 '$REMOTE_DIR/pve-install.sh' && DEPLOY_DIR='$REMOTE_DIR' DEPLOY_CONFIG='$REMOTE_DIR/$config_name' '$REMOTE_DIR/pve-install.sh'"

IP=$(ssh -o BatchMode=yes "$PVE_HOST" "pct config '$CTID'" | sed -nE 's/.*ip=([0-9.]+)\/.*/\1/p' | head -n 1)
URL="http://${IP}:${LINKSHARE_PORT}"
printf '[linkshare] Verifying remote endpoint at %s\n' "$URL"
curl -fsS "$URL/healthz" >/dev/null
curl -fsS "$URL/" >/dev/null
curl -fsS "$URL/guide" >/dev/null

CODEX_HOME_DIR="${CODEX_HOME:-$HOME/.codex}"
SKILL_DEST="$CODEX_HOME_DIR/skills/linkshare"
rm -rf "$SKILL_DEST"
mkdir -p "$(dirname "$SKILL_DEST")"
cp -a "$ROOT_DIR/skills/linkshare" "$SKILL_DEST"
chmod 0755 "$SKILL_DEST/scripts/linkshare.py"

CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/linkshare"
mkdir -p "$CONFIG_DIR"
python3 - "$CONFIG_DIR/config.json" "$URL" <<'PY'
import json
from pathlib import Path
import sys

path, url = sys.argv[1:]
Path(path).write_text(
    json.dumps({"url": url, "default_actor": "codex-agent"}, indent=2) + "\n",
    encoding="utf-8",
)
PY
chmod 0600 "$CONFIG_DIR/config.json"

GUIDANCE_FILE="$CODEX_HOME_DIR/AGENTS.md"
mkdir -p "$CODEX_HOME_DIR"
temporary=$(mktemp)
if [[ -f "$GUIDANCE_FILE" ]]; then
	awk '
		/^<!-- linkshare:start -->$/ {skip=1; next}
		/^<!-- linkshare:end -->$/ {skip=0; next}
		!skip {print}
	' "$GUIDANCE_FILE" > "$temporary"
fi
cat >> "$temporary" <<EOF

<!-- linkshare:start -->
## Linkshare

Use the installed \`\$linkshare\` skill whenever asked to share, save, check, consume, or manage links in Linkshare.

- The trusted-LAN service is configured at \`$URL\`.
- Send links for $OWNER_NAME to the \`owner\` target.
- Check the \`agents\` target for links intended for Codex agents.
- Consume a link only after successfully using it; consumption is permanent.
- Leave failed or unrelated links waiting. They expire automatically.
<!-- linkshare:end -->
EOF
mv "$temporary" "$GUIDANCE_FILE"
chmod 0644 "$GUIDANCE_FILE"

printf '[linkshare] Skill installed at %s\n' "$SKILL_DEST"
printf '[linkshare] Codex guidance installed at %s\n' "$GUIDANCE_FILE"
printf 'LINKSHARE_URL=%s\nLINKSHARE_CTID=%s\n' "$URL" "$CTID"
