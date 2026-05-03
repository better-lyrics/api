# infra/

Everything needed to stand up the server lives here. If the Hetzner box dies or you want to move providers, `sudo ./bootstrap.sh` on a fresh Ubuntu 24.04 host rebuilds it in about ten minutes.

## What runs on the box

| Component | Purpose | Reachable at |
|---|---|---|
| `caddy` | Reverse proxy with TLS via Cloudflare DNS-01 | 80/443 |
| `lyrics-api` | The Go API | localhost:8080 (proxied) |
| `lyrics-api@.service` | Per-PR preview environments | localhost:9000+PR |
| `infisical-agent` | Syncs prod secrets from Infisical Cloud into `/etc/lyrics-api.env` and restarts the API on change | n/a |
| `beszel-agent` | System metrics, reports to a hub | localhost:45876 |
| `logdy` | Browser log viewer for `lyrics-api` journal | localhost:8888 (proxied) |
| Backup scripts | Daily `cache.db` dump, off-site upload to Backblaze B2 | cron |
| `ufw` + `fail2ban` | Firewall + sshd brute-force protection | n/a |

## Prerequisites

On the box:
- Fresh Ubuntu 24.04 (or compatible) with sudo and outbound network
- SSH access for the account that will run `sudo`

Off the box, before you run bootstrap:
- DNS records pre-pointed at the box (see "Manual steps" below)
- A Cloudflare API token scoped to Zone:DNS:Edit on the parent zone of your domains
- A Backblaze B2 application key with read+write on the backups bucket
- An Infisical Cloud machine identity (Universal Auth) with read access to the `prod` env
- A Beszel hub somewhere reachable, with an agent KEY/TOKEN pair generated for this host
- `secrets.env` populated next to `bootstrap.sh` (template: `secrets.env.example`)

The compiled `lyrics-api-go` binary is optional at bootstrap time. Phase 04 installs the systemd unit either way and waits to start it until both the binary and `/etc/lyrics-api.env` exist.

## Running

```bash
# from the repo root on the target box
cp infra/secrets.env.example infra/secrets.env
$EDITOR infra/secrets.env                    # fill every blank
sudo ./infra/bootstrap.sh                    # full bootstrap
sudo ./infra/bootstrap.sh --phase 03         # re-run a single phase (here: Caddy)
```

Logs go to `/var/log/bli-bootstrap.log`. Phases are idempotent: re-running reconciles drift (rewriting systemd units to match repo state, refreshing config files) without breaking anything already in place.

## Manual steps (not automated)

These happen outside the box and stay manual:

- **Provision the Hetzner instance** with `hcloud server create --type cax21 --image ubuntu-24.04 --location hel1 ...`
- **DNS records in Cloudflare** for the four hostnames in `secrets.env` plus the preview wildcard, all proxied (orange cloud)
- **Infisical secrets** in the project's `prod` env. The agent only syncs; it does not create.
- **Beszel hub** running somewhere reachable, with an agent slot for this host. The hub UI hands you the KEY/TOKEN pair for `secrets.env`.
- **`cache.db` restore** from B2 if you're rebuilding after a loss. Separate process: `rclone copy b2:lyrics-api-backups/daily/<file> /var/lib/lyrics-api/data/cache.db`.

## Deploying the lyrics-api binary

The IaC installs the systemd unit but does not ship the Go binary. Two ways to get it on the box:

1. **From CI** (the path used in prod): GitHub Actions builds, `scp`s to `/opt/lyrics-api/lyrics-api-go`, then `systemctl restart lyrics-api`.
2. **From source on the box**: `git clone`, `go build -o /opt/lyrics-api/lyrics-api-go .`, `chown deploy:deploy`, `systemctl restart lyrics-api`.

Either way, `infisical-agent` writes `/etc/lyrics-api.env` once it starts, which is what unblocks the first `lyrics-api` start.

## Security model

Most secrets live in Infisical Cloud and sync read-only to the box. The exceptions, all kept off the world-readable systemd config:

- `CF_API_TOKEN` is in `/etc/caddy.env` (mode 600, root:caddy)
- `B2_*` is in `/home/deploy/.config/rclone/rclone.conf` (mode 600, deploy:deploy)
- `LOGDY_UI_PASS` is in `/etc/logdy.env` (mode 640, root:deploy)
- The Infisical `client-secret` is at `/etc/infisical-agent/client-secret` (mode 600, root)

`BESZEL_AGENT_TOKEN` is the one wart: it sits in `Environment=` lines on a mode-644 unit, since that's how the upstream installer ships it. The blast radius if leaked is impersonating the agent to the hub, which sends fake metrics but does not grant credentials back. The same EnvironmentFile pattern Caddy uses would close it; it is not done yet.

`lyrics-api.service` itself runs as `deploy` with `ProtectSystem=strict`, `ProtectHome=true`, `PrivateTmp=true`, `NoNewPrivileges=true`. UFW restricts inbound traffic to 22/80/443. `fail2ban` watches `sshd`.

## Verification after bootstrap

Substitute your own hostnames from `secrets.env` for `$PRIMARY_DOMAIN` and `$LOGS_DOMAIN`.

```bash
systemctl is-active caddy lyrics-api infisical-agent beszel-agent logdy fail2ban
ls -l /etc/caddy.env                                # -rw------- root caddy
sudo -u nobody cat /etc/caddy.env                   # permission denied
curl -sI https://$PRIMARY_DOMAIN/health             # 200
curl -sI https://$LOGS_DOMAIN/                      # 200 (then 401 on actual UI without auth)
journalctl -u infisical-agent -n 20                 # successful auth + sync
```

## Selective rebuild scenarios

| Scenario | Command |
|---|---|
| Caddy config changed | `sudo ./bootstrap.sh --phase 03` |
| Memory drop-in needs adjusting | edit `files/lyrics-api/memory.conf`, then `sudo ./bootstrap.sh --phase 04` |
| Backup schedule changed | edit `files/backups/crontab.fragment`, then `sudo ./bootstrap.sh --phase 08` |
| Logdy version bump | bump `LOGDY_VERSION` in `secrets.env`, then `sudo ./bootstrap.sh --phase 07` |
| Beszel agent token rotated | update `BESZEL_AGENT_TOKEN` in `secrets.env`, then `sudo ./bootstrap.sh --phase 06` |

## What's intentionally not here

- **Provisioning** (`hcloud server create`). One command, varies per provider, not worth scripting.
- **DNS records**. Lives in Cloudflare; the UI is fine.
- **The `lyrics-api-go` binary**. Shipped from CI, not infra.
- **A self-hosted Infisical instance**. 600MB resident is more than the project warrants.
- **`cache.db` restoration**. Separate runbook, depends on which B2 snapshot you want.

## GitHub Actions configuration

Two workflows talk to this box: `.github/workflows/deploy-hetzner.yml` (prod deploys on push to `master`) and `.github/workflows/preview.yml` (per-PR previews). Both need configuration set in the GitHub repo:

Repository **secrets** (Settings > Secrets and variables > Actions > Secrets):
- `APP_ID`, `APP_PRIVATE_KEY` for the GitHub App that posts PR comments
- `SSH_KEY` private key authorised on the box for the deploy user
- `SSH_HOST` the box's IP or DNS name
- `SSH_USER` the SSH login (e.g. `deploy`)

Repository **variables** (Settings > Secrets and variables > Actions > Variables):
- `PREVIEW_DOMAIN` the wildcard zone for PR previews, e.g. `preview.api.example.com`. Must match `PREVIEW_WILDCARD` in `secrets.env` on the box (same value, minus the leading `*.`).

## When something breaks

1. Read `/var/log/bli-bootstrap.log` for the failing phase
2. Re-run just that phase with `--phase NN`
3. If the failure is upstream (apt repo down, a GitHub release moved), the phase script is the source of truth. Open it, fix it, re-run.
4. For infisical-agent issues, `journalctl -t infisical-agent -n 50` shows the reload script's logger output.
