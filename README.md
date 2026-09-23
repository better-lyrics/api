# Better Lyrics API

![GitHub top language](https://img.shields.io/github/languages/top/better-lyrics/api)
![GitHub License](https://img.shields.io/github/license/better-lyrics/api)
![CI](https://img.shields.io/github/actions/workflow/status/better-lyrics/api/ci.yml?branch=master&label=build)

The backend for [Better Lyrics](https://betterlyrics.org). It resolves synchronized lyrics for a song, caches them, and serves them over a small REST API.

Parameters, response formats, rate limits and error codes are documented at [docs.betterlyrics.org](https://docs.betterlyrics.org), which also has a playground for trying requests.

> [!IMPORTANT]
> The primary lyrics source is [**lrc.red**](https://lrc.red) by **w4v**. Anything we do fetch from elsewhere is contributed back to lrc.red by ISRC, so the shared catalog keeps growing. Full order below in [Data sources](#data-sources).

> [!NOTE]
> A few upstream endpoints live in `.env` rather than the code. That is a deliberate measure to keep the API from being trivially abused. If you want a similar lyrics API for your own project, [spotify-lyrics-api](https://github.com/akashrchandran/spotify-lyrics-api) is a good starting point. This repo exists to keep the Better Lyrics backend transparent and privacy-respecting.

## Table of Contents

- [Quickstart](#quickstart)
- [API Endpoints](#api-endpoints)
- [Data sources](#data-sources)
- [Deployment](#deployment)
- [Abuse prevention](#abuse-prevention)
- [License](#license)

## Quickstart

To run it locally you need a Go toolchain (1.22+) and a populated `.env`.

```bash
git clone https://github.com/better-lyrics/api.git && cd api
go mod tidy
cp .env.example .env       # fill in upstream API endpoints + credentials
go run main.go             # serves on :8080
```

The server prints request lines as it boots. Once the listener line shows up, hit `http://localhost:8080/health`. For hot reload during development, `./scripts/run.sh` watches the source with `nodemon`.

## API Endpoints

Public:

- `GET /getLyrics?a={artist}&s={song}` - synchronized lyrics for a song
- `GET /artwork?s={song}&a={artist}` - animated album artwork
- `GET /health` - health check
- `GET /stats` - API statistics (requires `Authorization` header)

Every endpoint has a full reference, generated from the OpenAPI spec, starting at [docs.betterlyrics.org/reference/get-lyrics](https://docs.betterlyrics.org/reference/get-lyrics).

Admin and cache endpoints (`/cache/*`, `/revalidate`, `/override`, `/health/mut`, and the rest) are documented live at `GET /cache/help`.

## Data sources

Lyrics for `/getLyrics` (and `/ttml/getLyrics`) resolve in this order, stopping at the first hit:

1. Local cache (Postgres). Entries persist until cleared, so a track is fetched from upstream at most once.
2. [**lrc.red**](https://lrc.red) by w4v, keyed by ISRC. An upstream catalog lookup finds the track and its ISRC, then lrc.red is queried by that ISRC.
3. The upstream lyrics provider, only when lrc.red has nothing for that ISRC.

Lyrics fetched from the upstream provider are contributed back to lrc.red's ingress by ISRC. Lyrics that came from lrc.red are not re-submitted.

`/revalidate` follows the same order. It is not a shortcut straight to the upstream provider: the catalog lookup runs to re-resolve the track and ISRC, then lrc.red is tried before the provider.

## Deployment

Deploys to [Railway](https://railway.com) from the `Dockerfile` (a distroless build of `./cmd/api`), configured in `railway.json`. Railway builds the image, runs the container, and health-checks `/health`; on failure it restarts, up to 10 times. Lyrics and metadata live in a managed Railway Postgres, and schema migrations run on startup.

Configuration is all environment variables. Copy `.env.example` and fill it in. `DATABASE_URL` and the upstream API settings are required. Setting `LRC_RED_INGRESS_KEY` turns on contributing lyrics back to lrc.red; leave it unset and the contribution path stays off.

## Abuse prevention

Fetching from upstream is an expensive path, so the API is built to serve the cache cheaply and make abuse of the fetch path uncomfortable.

- **Per-IP rate limiting.** Every client IP gets a two-tier token bucket. Tier one is the normal limit. When it is exhausted, tier two still serves requests that already have a cached answer, so popular songs keep serving while fresh fetches are throttled. If you exceed both, the API returns `429` with `Retry-After`.
- **API key on the fetch path.** By default, uncached queries on protected paths (`/getLyrics`, `/revalidate`, `/override`, and friends) need a valid `X-API-Key`. Cache hits are public and keyless, so reading known lyrics never needs auth but a fresh upstream fetch does.
- **IP blocking at the edge.** The API sits behind an edge proxy, so an IP or range that keeps hammering the fetch path can be blocked outright before it reaches the app. We can also take similar abuse prevention methods manually such as, but not limited to, IP blacklisting and stricter rate limits.

### The Better Lyrics key

The [Better Lyrics](https://betterlyrics.org) extension carries its own API key through a proxy. A request with that key takes a priority slot, so the extension gets responses even when public traffic is being restricted.

That key is exclusive to Better Lyrics. It is not handed out to other projects and it is not for sale, so please do not ask for one. If you want to run your own instance with your own key instead, get in touch with me through [our Discord](http://discord.gg/UsHE3d5fWF).

## License

This project is licensed under the [GPL v3 License](LICENSE). As long as you attribute me or [Better Lyrics](https://betterlyrics.org) as the original creator and you comply with the rest of the license terms, you can use this project for personal or commercial purposes.
