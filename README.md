# Better Lyrics API

![GitHub top language](https://img.shields.io/github/languages/top/better-lyrics/api)
![GitHub License](https://img.shields.io/github/license/better-lyrics/api)
![GitHub Actions Workflow Status](https://img.shields.io/github/actions/workflow/status/better-lyrics/api/go.yml)

This repository contains the source code for the official Better Lyrics API - primarily serving as the backend for [Better Lyrics](https://betterlyrics.org).

> [!NOTE]
> A few endpoints are defined as environment variables in the `.env` file. This is deliberate to prevent abuse of the API and to ensure that the API is used responsibly. If you would like to use a similar API for your own project, consider using something like [spotify-lyrics-api](https://github.com/akashrchandran/spotify-lyrics-api). This repository is intended to address privacy concerns and to provide a more transparent API for users.

## Table of Contents

- [Quickstart](#quickstart)
- [API Endpoints](#api-endpoints)
- [Data sources](#data-sources)
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

## Data sources

Lyrics for `/getLyrics` (and `/ttml/getLyrics`) resolve in this order, stopping at the first hit:

1. Local cache (Postgres). Entries persist until cleared, so a track is fetched from upstream at most once.
2. [lrc.red](https://lrc.red) by ISRC. An upstream catalog lookup finds the track and its ISRC, then lrc.red is queried by that ISRC.
3. The upstream lyrics provider, only when lrc.red has no entry for that ISRC.

Lyrics fetched from the upstream provider are contributed back to lrc.red's ingress, keyed by ISRC; lyrics that came from lrc.red are not.

Upstream lyrics source: [lrc.red](https://lrc.red) by w4v.

## Deployment

Deploys to [Railway](https://railway.com) from the `Dockerfile` (a distroless build of `./cmd/api`), configured in `railway.json`. Railway builds the image, runs the container, and health-checks `/health`; on failure it restarts (up to 10 times). Lyrics and metadata live in a managed Railway Postgres, and schema migrations run on startup.

Configuration is all environment variables. Copy `.env.example` and fill it in; `DATABASE_URL` and the upstream API settings are required, and `LRC_RED_INGRESS_KEY` turns on contributing lyrics back to lrc.red (the contribution path stays off when it is unset).

## Contributing

Contributions are welcome! If you find any issues or have suggestions for improvements, please open an issue or submit a pull request.

## License

This project is licensed under the [GPL v3 License](LICENSE). As long as you attribute me or [Better Lyrics](https://betterlyrics.org) as the original creator and you comply with the rest of the license terms, you can use this project for personal or commercial purposes.
