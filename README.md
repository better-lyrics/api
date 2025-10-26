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

This project includes a `railway.toml` configuration for easy deployment on Railway with persistent cache storage.

**Setup Steps:**

1. Create a new project on [Railway](https://railway.app)
2. Connect your GitHub repository
3. Railway will automatically detect the `railway.toml` and create a volume at `/data`
4. Set the following environment variable in Railway:
   ```
   CACHE_DB_PATH=/data/cache.db
   ```
5. Configure all other required environment variables from `.env.example`
6. Deploy!

**Important:** The volume mount at `/data` ensures your cache database persists across deployments. Without this, the cache will be wiped on every build.

## Contributing

Contributions are welcome! If you find any issues or have suggestions for improvements, please open an issue or submit a pull request.

## License

This project is licensed under the [GPL v3 License](LICENSE). As long as you attribute me or [Better Lyrics](https://better-lyrics.boidu.dev) as the original creator and you comply with the rest of the license terms, you can use this project for personal or commercial purposes.
