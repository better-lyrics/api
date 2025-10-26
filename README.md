# Better Lyrics API

![GitHub top language](https://img.shields.io/github/languages/top/boidushya/better-lyrics-api)
![GitHub License](https://img.shields.io/github/license/boidushya/better-lyrics-api)
![GitHub Actions Workflow Status](https://img.shields.io/github/actions/workflow/status/boidushya/better-lyrics-api/go.yml)
![Railway](https://img.shields.io/badge/deployement-railway-javascript?logo=railway&logoColor=fff&color=851AE6)

This repository contains the source code for the official Better Lyrics API - primarily serving as the backend for [Better Lyrics](https://better-lyrics.boidu.dev).

> [!NOTE]
> A few endpoints are defined as environment variables in the `.env` file. This is deliberate to prevent abuse of the API and to ensure that the API is used responsibly. If you would like to use a similar API for your own project, consider using something like [spotify-lyrics-api](https://github.com/akashrchandran/spotify-lyrics-api). This repository is intended to address privacy concerns and to provide a more transparent API for users.

## Table of Contents

- [Better Lyrics API](#better-lyrics-api)
  - [Table of Contents](#table-of-contents)
  - [Installation](#installation)
  - [Usage](#usage)
  - [API Endpoints](#api-endpoints)
  - [Deployment](#deployment)
    - [Railway](#railway)
  - [Contributing](#contributing)
  - [License](#license)

## Installation

To install and run the Lyrics API Go, follow these steps:

1. Clone the repository: `git clone https://github.com/boidushya/better-lyrics-api.git`
2. Navigate to the project directory: `cd better-lyrics-api`
3. Install the dependencies: `go mod tidy`
4. Copy the `.env.example` file to `.env` and update the environment variables as needed: `cp .env.example .env`
5. Start the server: `go run main.go`

## Usage

Once the server is running, you can access the API endpoints to retrieve lyrics for songs.

## API Endpoints

- `GET /getLyrics?a={artist}&s={song}`: Retrieves the lyrics for the specified artist and song.

## Deployment

### Railway

This project uses Railway's persistent volumes to maintain the cache database across deployments.

**Setup Steps:**

1. Create a new project on [Railway](https://railway.app) and connect your GitHub repository

2. **Create a Volume (CRITICAL):**
   - Go to your service in Railway dashboard
   - Click **Settings** → **Volumes** tab
   - Click **+ New Volume**
   - **Mount Path:** `/data`
   - Click **Add**

3. **Set Environment Variables:**
   - Go to **Variables** tab
   - Add: `CACHE_DB_PATH=/data/cache.db`
   - Configure all other required variables from `.env.example`

4. **Deploy!**

**Verification:**
After deployment, check your logs for:
```
[Cache] Loaded X entries from disk to memory
```
If you see `Loaded 0 entries` on subsequent deploys (after caching data), the volume isn't persisting.

**Troubleshooting:**
- Ensure the volume mount path is exactly `/data`
- Verify `CACHE_DB_PATH=/data/cache.db` is set in Railway variables
- The volume must be created BEFORE deploying with the updated env var

## Contributing

Contributions are welcome! If you find any issues or have suggestions for improvements, please open an issue or submit a pull request.

## License

This project is licensed under the [GPL v3 License](LICENSE). As long as you attribute me or [Better Lyrics](https://better-lyrics.boidu.dev) as the original creator and you comply with the rest of the license terms, you can use this project for personal or commercial purposes.
