# Linkshare repository guidance

- Run `go test ./...` and `go vet ./...` after backend changes.
- Run `node --check web/assets/app.js` and `node --check web/assets/guide.js` after frontend changes.
- Validate `skills/linkshare` with the skill-creator `quick_validate.py` and run `python3 -m py_compile skills/linkshare/scripts/linkshare.py` after skill changes.
- Build the deployment artifact with `docker build --platform linux/amd64 --target artifact --output type=local,dest=dist .`.
- Deploy or upgrade with `./deploy/provision-lxc.sh`; it must preserve `/var/lib/linkshare/linkshare.db` on upgrades.
- Keep real homelab values in the ignored `.linkshare-deploy.env`; commit only the example file.
- Do not expose Linkshare outside the trusted LAN without adding authentication.
