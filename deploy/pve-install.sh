#!/usr/bin/env bash
set -Eeuo pipefail

exec 9>/run/lock/linkshare-provision.lock
if ! flock -n 9; then
	printf '[linkshare] Another Linkshare provisioner is already running\n' >&2
	exit 1
fi

DEPLOY_DIR="${DEPLOY_DIR:?DEPLOY_DIR is required}"
DEPLOY_CONFIG="${DEPLOY_CONFIG:?DEPLOY_CONFIG is required}"
# shellcheck source=/dev/null
source "$DEPLOY_CONFIG"

: "${CTID:?CTID is required}"
: "${CT_NAME:?CT_NAME is required}"
: "${TEMPLATE:?TEMPLATE is required}"
: "${STORAGE:?STORAGE is required}"
: "${BRIDGE:?BRIDGE is required}"
: "${GATEWAY:?GATEWAY is required}"
: "${CIDR_PREFIX:?CIDR_PREFIX is required}"
: "${IP_CANDIDATES:?IP_CANDIDATES is required}"
: "${NAMESERVERS:?NAMESERVERS is required}"
: "${SEARCH_DOMAIN:?SEARCH_DOMAIN is required}"
: "${LINKSHARE_PORT:?LINKSHARE_PORT is required}"
created=0
healthy=0

log() { printf '[linkshare] %s\n' "$*"; }

rollback_new_container() {
	status=$?
	if [[ $status -ne 0 && $created -eq 1 && $healthy -eq 0 ]]; then
		log "Provisioning failed before first health check; removing newly-created CT ${CTID}"
		pct stop "$CTID" --skiplock 1 >/dev/null 2>&1 || true
		pct destroy "$CTID" --purge 1 >/dev/null 2>&1 || true
	fi
	exit "$status"
}
trap rollback_new_container EXIT

for file in linkshare linkshare.service linkshare.env nftables.conf; do
	[[ -f "$DEPLOY_DIR/$file" ]] || { log "Missing deployment file: $file"; exit 1; }
done

choose_ip() {
	local candidate config_files
	config_files=$(find /etc/pve/lxc /etc/pve/qemu-server -maxdepth 1 -type f 2>/dev/null || true)
	for candidate in $IP_CANDIDATES; do
		if [[ -n "$config_files" ]] && grep -q "ip=${candidate}/" $config_files 2>/dev/null; then
			continue
		fi
		if ping -c 2 -W 1 "$candidate" >/dev/null 2>&1; then
			continue
		fi
		if ip neigh show "$candidate" | grep -q 'lladdr'; then
			continue
		fi
		printf '%s' "$candidate"
		return 0
	done
	return 1
}

if pct config "$CTID" >/dev/null 2>&1; then
	existing_name=$(pct config "$CTID" | awk -F': ' '$1 == "hostname" {print $2}')
	[[ "$existing_name" == "$CT_NAME" ]] || { log "VMID ${CTID} belongs to ${existing_name}, refusing to overwrite it"; exit 1; }
	IP=$(pct config "$CTID" | sed -nE 's/.*ip=([0-9.]+)\/.*/\1/p' | head -n 1)
	log "Upgrading existing CT ${CTID} at ${IP}"
else
	[[ -f "/var/lib/vz/template/cache/${TEMPLATE##*/}" ]] || { log "Template $TEMPLATE is unavailable"; exit 1; }
	IP=$(choose_ip) || { log "No verified-free address is available"; exit 1; }
	log "Creating unprivileged CT ${CTID} at ${IP}"
	pct create "$CTID" "$TEMPLATE" \
		--arch amd64 \
		--hostname "$CT_NAME" \
		--unprivileged 1 \
		--cores 1 \
		--memory 512 \
		--swap 256 \
		--rootfs "${STORAGE}:4" \
		--net0 "name=eth0,bridge=${BRIDGE},gw=${GATEWAY},ip=${IP}/${CIDR_PREFIX},type=veth" \
		--nameserver "$NAMESERVERS" \
		--searchdomain "$SEARCH_DOMAIN" \
		--onboot 1 \
		--startup "order=4,up=10,down=30" \
		--tags "linkshare;service;lan" \
		--protection 0
	created=1
fi

if ! pct status "$CTID" | grep -q running; then
	pct start "$CTID"
fi
for _ in {1..30}; do
	if pct exec "$CTID" -- true >/dev/null 2>&1; then break; fi
	sleep 1
done
pct exec "$CTID" -- true

log "Installing minimal runtime and firewall packages"
pct exec "$CTID" -- bash -lc 'if ! dpkg-query -W ca-certificates curl nftables >/dev/null 2>&1; then export DEBIAN_FRONTEND=noninteractive; apt-get update; apt-get install -y --no-install-recommends ca-certificates curl nftables; apt-get clean; rm -rf /var/lib/apt/lists/*; fi'
pct exec "$CTID" -- bash -lc 'systemctl disable --now ssh.service ssh.socket 2>/dev/null || true; if ! getent group linkshare >/dev/null; then addgroup --system linkshare; fi; if ! getent passwd linkshare >/dev/null; then adduser --system --ingroup linkshare --home /var/lib/linkshare --no-create-home --disabled-login linkshare; fi; install -d -o linkshare -g linkshare -m 0700 /var/lib/linkshare'

if pct exec "$CTID" -- test -x /usr/local/bin/linkshare; then
	pct exec "$CTID" -- cp -a /usr/local/bin/linkshare /usr/local/bin/linkshare.previous
fi
pct push "$CTID" "$DEPLOY_DIR/linkshare" /usr/local/bin/linkshare.new --user root --group root --perms 0755
pct push "$CTID" "$DEPLOY_DIR/linkshare.service" /etc/systemd/system/linkshare.service --user root --group root --perms 0644
pct push "$CTID" "$DEPLOY_DIR/linkshare.env" /etc/linkshare.env --user root --group root --perms 0644
pct push "$CTID" "$DEPLOY_DIR/nftables.conf" /etc/nftables.conf --user root --group root --perms 0644

log "Validating and enabling nftables"
pct exec "$CTID" -- nft -c -f /etc/nftables.conf
pct exec "$CTID" -- systemctl enable nftables.service
pct exec "$CTID" -- systemctl restart nftables.service

log "Validating and starting Linkshare"
pct exec "$CTID" -- systemctl daemon-reload
pct exec "$CTID" -- systemd-analyze verify /etc/systemd/system/linkshare.service
pct exec "$CTID" -- systemctl enable linkshare.service
pct exec "$CTID" -- systemctl stop linkshare.service 2>/dev/null || true
pct exec "$CTID" -- mv -f /usr/local/bin/linkshare.new /usr/local/bin/linkshare
expected_hash=$(sha256sum "$DEPLOY_DIR/linkshare" | awk '{print $1}')
actual_hash=$(pct exec "$CTID" -- sha256sum /usr/local/bin/linkshare | awk '{print $1}')
[[ "$expected_hash" == "$actual_hash" ]] || { log "Deployed binary hash mismatch"; exit 1; }
pct exec "$CTID" -- systemctl start linkshare.service

for _ in {1..30}; do
	if pct exec "$CTID" -- curl -fsS "http://127.0.0.1:${LINKSHARE_PORT}/healthz" >/dev/null 2>&1; then
		healthy=1
		break
	fi
	sleep 1
done
if [[ $healthy -ne 1 ]]; then
	log "Health check failed"
	pct exec "$CTID" -- journalctl -u linkshare.service --no-pager -n 80 || true
	if pct exec "$CTID" -- test -x /usr/local/bin/linkshare.previous; then
		log "Restoring previous binary"
		pct exec "$CTID" -- systemctl stop linkshare.service 2>/dev/null || true
		pct exec "$CTID" -- cp -a /usr/local/bin/linkshare.previous /usr/local/bin/linkshare
		pct exec "$CTID" -- systemctl start linkshare.service
	fi
	exit 1
fi

pct set "$CTID" --protection 1
log "Deployment healthy"
printf 'LINKSHARE_CTID=%s\nLINKSHARE_IP=%s\nLINKSHARE_URL=http://%s:%s\n' "$CTID" "$IP" "$IP" "$LINKSHARE_PORT"
