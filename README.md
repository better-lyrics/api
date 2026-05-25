# Better Lyrics API

![GitHub top language](https://img.shields.io/github/languages/top/better-lyrics/api)
![GitHub License](https://img.shields.io/github/license/better-lyrics/api)
![GitHub Actions Workflow Status](https://img.shields.io/github/actions/workflow/status/better-lyrics/api/go.yml)

This repository contains the source code for the official Better Lyrics API - primarily serving as the backend for [Better Lyrics](https://better-lyrics.boidu.dev).

> [!NOTE]
> A few endpoints are defined as environment variables in the `.env` file. This is deliberate to prevent abuse of the API and to ensure that the API is used responsibly. If you would like to use a similar API for your own project, consider using something like [spotify-lyrics-api](https://github.com/akashrchandran/spotify-lyrics-api). This repository is intended to address privacy concerns and to provide a more transparent API for users.

## Table of Contents

- [Quickstart](#quickstart)
- [API Endpoints](#api-endpoints)
- [Deployment](#deployment)
- [Contributing](#contributing)
- [License](#license)

## Quickstart

If you just want to run it locally, you need a Go toolchain (1.22+) and a populated `.env`.

```bash
git clone https://github.com/better-lyrics/api.git && cd api
go mod tidy
cp .env.example .env       # fill in upstream API endpoints + credentials
go run main.go             # serves on :8080
```

The server logs request lines as it boots; once you see the listener line, hit `http://localhost:8080/health`. For hot reload during development, `./scripts/run.sh` watches the source via `nodemon`.

## API Endpoints

Public:

- `GET /getLyrics?a={artist}&s={song}` - Retrieves synchronized lyrics for the specified artist and song
- `GET /artwork?s={song}&a={artist}` - Returns animated album artwork
- `GET /health` - Health check
- `GET /stats` - API statistics (requires `Authorization` header)

Admin/cache endpoints (`/cache/*`, `/revalidate`, `/override`, `/health/mut`, etc.) are documented live at `GET /cache/help`.

## Deployment

Production runs on a single Hetzner CAX21 (ARM64, Helsinki). The whole server stack (Caddy, the API, Infisical agent for secrets sync, Beszel agent for metrics, Logdy for log streaming, B2 backups, UFW, fail2ban) lives in [`infra/`](./infra/README.md) as code.

To rebuild from scratch on any Ubuntu 24.04 host:

```bash
cp infra/secrets.env.example infra/secrets.env
$EDITOR infra/secrets.env                    # fill in every value from your password manager
sudo ./infra/bootstrap.sh                    # about 10 minutes, idempotent
```

See [`infra/README.md`](./infra/README.md) for the prerequisites and the manual steps that stay manual (DNS, provisioning, `cache.db` restore).

## Contributing

Contributions are welcome! If you find any issues or have suggestions for improvements, please open an issue or submit a pull request.

## License

This project is licensed under the [GPL v3 License](LICENSE). As long as you attribute me or [Better Lyrics](https://better-lyrics.boidu.dev) as the original creator and you comply with the rest of the license terms, you can use this project for personal or commercial purposes.
