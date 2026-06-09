# infra/

Everything needed to stand up the server lives here. If the Hetzner box dies or you want to move providers, `sudo ./bootstrap.sh` on a fresh Ubuntu 24.04 host rebuilds it in about ten minutes.

## What runs on the box

| Component | Purpose | Reachable at |
|---|---|---|
| `caddy` | Reverse proxy with TLS via Cloudflare DNS-01 | 80/443 |
| `lyrics-api` | The Go API | localhost:8080 (proxied) |
| `lyrics-api@.service` | Per-PR preview environments | localhost:9000+PR |
| `keep` | Self-hosted secrets manager (the prod source of truth) | localhost:4339 (proxied) |
| `keep-agent-lyrics-api-prod.timer` | Pulls prod secrets from keep into `/etc/lyrics-api.env` every 60s and restarts the API on change | n/a |
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
- A Beszel hub somewhere reachable, with an agent KEY/TOKEN pair generated for this host
- `secrets.env` populated next to `bootstrap.sh` (template: `secrets.env.example`)

Two binaries are optional at bootstrap time and installed via deferred-start:

- **`lyrics-api-go`**. Phase 04 installs the systemd unit and waits to start it until both the binary at `/opt/lyrics-api/lyrics-api-go` and `/etc/lyrics-api.env` exist.
- **`keep`**. Phase 05 installs the systemd unit, the data dir, and the `keep` system user. Build keep from the sibling repo (see [`SELF_HOSTING.md`](https://github.com/boidushya/keep/blob/main/SELF_HOSTING.md)), `scp` the binary to `/usr/local/bin/keep`, then `systemctl enable --now keep` to bring it up.

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
- **(Optional) Attach a data Volume.** The CAX21's 80GB root disk fills up once `cache.db` and its daily backups grow. Create a Hetzner Cloud Volume in the same location (50GB+ recommended), attach it to this server with auto-mount enabled (ext4), and set `LYRICS_API_VOLUME_ID` in `secrets.env`. Phase 11 picks it up, migrates any existing `/var/lib/lyrics-api` contents onto it, and adds a fstab bind mount so DB and backup writes land on the volume.
- **DNS records in Cloudflare** for the five hostnames in `secrets.env` (primary, staging, logs, metrics, keep) plus the preview wildcard, all proxied (orange cloud)
- **Beszel hub** running somewhere reachable, with an agent slot for this host. The hub UI hands you the KEY/TOKEN pair for `secrets.env`.
- **keep first-run setup**. After phase 05 puts keep up at `https://$KEEP_DOMAIN`, browse to it and complete `/setup`: pick a master password (save it to a password manager), scan TOTP, save the 8 recovery codes offline. Then create project `lyrics-api`, env `prod`, bulk-import the env via the .env paste UI.
- **keep agent token for lyrics-api**. From keep's UI, mint a token for `lyrics-api/prod` with `OUTPUT=/etc/lyrics-api.env`, `RELOAD_CMD="systemctl restart lyrics-api"`, and `REQUIRED_KEYS` set to every key in your env. Paste the bootstrap install command keep generates in a root shell on the box.
- **Post-reboot unseal**. keep restarts sealed every time the host boots; SSH in and log into `https://$KEEP_DOMAIN` once to unseal, otherwise the keep-agent stays stuck on `503` and `/etc/lyrics-api.env` will not refresh. `lyrics-api` keeps running on the last-good env, so this is a "secrets won't roll until you log in" issue, not an outage.
- **`cache.db` restore** from B2 if you're rebuilding after a loss. Separate process: `rclone copy b2:lyrics-api-backups/daily/<file> /var/lib/lyrics-api/data/cache.db`.

## Deploying the lyrics-api binary

The IaC installs the systemd unit but does not ship the Go binary. Two ways to get it on the box:

1. **From CI** (the path used in prod): GitHub Actions builds, `scp`s to `/opt/lyrics-api/lyrics-api-go`, then `systemctl restart lyrics-api`.
2. **From source on the box**: `git clone`, `go build -o /opt/lyrics-api/lyrics-api-go .`, `chown deploy:deploy`, `systemctl restart lyrics-api`.

Either way, the keep-agent timer writes `/etc/lyrics-api.env` once keep is unsealed and the agent token is valid. That write is what unblocks the first `lyrics-api` start.

## Security model

Prod secrets live in keep's encrypted SQLite (`/var/lib/keep/keep.db`). Each value is age-encrypted under a master key wrapped by your Argon2id-derived master password. keep starts sealed; you unseal it from the web UI with your password + TOTP. After that, the keep-agent on this host pulls `/render`, checks `REQUIRED_KEYS`, atomically swaps `/etc/lyrics-api.env`, and restarts `lyrics-api`.

The other secrets, all kept off the world-readable systemd config:

- `CF_API_TOKEN` is in `/etc/caddy.env` (mode 600, root:caddy)
- `B2_*` is in `/home/deploy/.config/rclone/rclone.conf` (mode 600, deploy:deploy)
- `LOGDY_UI_PASS` is in `/etc/logdy.env` (mode 640, root:deploy)
- The keep agent token sits inside `/usr/local/bin/keep-agent-lyrics-api-prod.sh` (mode 755, root) as a quoted bash variable. Anyone with shell on the box can read the agent token; the blast radius is read-only access to the prod env in keep.

`BESZEL_AGENT_TOKEN` is the one wart: it sits in `Environment=` lines on a mode-644 unit, since that's how the upstream installer ships it. The blast radius if leaked is impersonating the agent to the hub, which sends fake metrics but does not grant credentials back. The same EnvironmentFile pattern Caddy uses would close it; it is not done yet.

`lyrics-api.service` runs as `deploy` with `ProtectSystem=strict`, `ProtectHome=true`, `PrivateTmp=true`, `NoNewPrivileges=true`. `keep.service` runs as a dedicated `keep` user with the same hardening. UFW restricts inbound traffic to 22/80/443. `fail2ban` watches `sshd`.

## Verification after bootstrap

Substitute your own hostnames from `secrets.env` for `$PRIMARY_DOMAIN` and `$LOGS_DOMAIN`.

```bash
systemctl is-active caddy lyrics-api keep keep-agent-lyrics-api-prod.timer beszel-agent logdy fail2ban
ls -l /etc/caddy.env                                # -rw------- root caddy
sudo -u nobody cat /etc/caddy.env                   # permission denied
curl -sI https://$PRIMARY_DOMAIN/health             # 200
curl -sI https://$LOGS_DOMAIN/                      # 200 (then 401 on actual UI without auth)
curl -sI https://$KEEP_DOMAIN/                      # 405 (keep only allows GET on /), proves TLS+proxy
journalctl -u keep-agent-lyrics-api-prod.service -n 20  # cycles every 60s, "[keep-agent] reloaded" only when secrets change
```

## Selective rebuild scenarios

| Scenario | Command |
|---|---|
| Caddy config changed | `sudo ./bootstrap.sh --phase 03` |
| Memory drop-in needs adjusting | edit `files/lyrics-api/memory.conf`, then `sudo ./bootstrap.sh --phase 04` |
| Backup schedule changed | edit `files/backups/crontab.fragment`, then `sudo ./bootstrap.sh --phase 08` |
| Logdy version bump | bump `LOGDY_VERSION` in `secrets.env`, then `sudo ./bootstrap.sh --phase 07` |
| Beszel agent token rotated | update `BESZEL_AGENT_TOKEN` in `secrets.env`, then `sudo ./bootstrap.sh --phase 06` |
| keep binary upgraded | rebuild from sibling repo, scp to `/usr/local/bin/keep`, then `sudo systemctl restart keep` (and unseal via UI) |
| keep agent token rotated | revoke old token + mint new one in keep UI, paste the new install command on the box |

## What's intentionally not here

- **Provisioning** (`hcloud server create`). One command, varies per provider, not worth scripting.
- **DNS records**. Lives in Cloudflare; the UI is fine.
- **The `lyrics-api-go` and `keep` binaries**. Both shipped via build + scp, not infra. Keep is a sibling repo at `github.com/boidushya/keep`.
- **keep first-run setup and token minting**. Master password, TOTP, recovery codes, agent token bootstrap; all interactive in the keep UI by design.
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
4. For keep-agent issues, `journalctl -u keep-agent-lyrics-api-prod.service -n 50` shows curl exit codes and the `[keep-agent]` log line on rewrite. A `503` body in the curl output means keep is sealed and you need to unseal it via the UI.
