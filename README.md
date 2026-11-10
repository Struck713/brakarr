# Brakarr

Brakarr shrinks your media library by transcoding H.264 (and other) video to HEVC on your GPU, then swapping the smaller file in place of the original. It is built to sit next to Sonarr and Radarr, and it tells them to rescan when a file changes.

- **Hardware-accelerated.** Encodes with `hevc_vaapi` on Intel and AMD GPUs. Video is re-encoded; audio, subtitles and attachments are copied untouched.
- **Quality-checked.** Before replacing anything, Brakarr checks that the output is valid HEVC, has the same duration, is smaller than the source, and (optionally) scores well on VMAF.
- **Sonarr/Radarr aware.** Replaced files are batched and reported to the apps so they rescan the affected series or movies.
- **Hardlink-safe.** Files with more than one hardlink (for example, still seeding) are deferred until only one link remains, so no extra copy is created.
- **Web UI.** Library, live job progress and settings live in a single web interface.

> **Status:** the final "replace the original" step in `internal/pool/job_pool.go` (`replaceOriginal`) is currently commented out. Brakarr transcodes and verifies files but does not yet overwrite your originals.

## How it works

1. The **crawler** walks the library path on an interval and records new `.mkv` / `.mp4` files in a SQLite database. Files are identified by content hash, so renamed or moved files are not queued twice. Files younger than the minimum age are ignored so in-progress downloads are left alone.
2. The **job pool** picks up pending files and transcodes each one to HEVC into a work directory.
3. The output is **verified**:
   - it is HEVC, with a duration within tolerance of the source;
   - it is smaller than the source (otherwise the file is *skipped*);
   - its lowest VMAF score across evenly spaced samples meets the minimum (otherwise the file is *skipped*).
4. The original is replaced and Sonarr/Radarr are asked to rescan.

Files end up as `pending`, `running`, `complete`, `skipped`, `deferred` or `failed`, all visible in the UI.

## Requirements

- Linux host with a VAAPI-capable GPU (for example `/dev/dri/renderD128`).
- Docker (recommended), or Go 1.26+, `libmediainfo-dev` and an ffmpeg build with VAAPI and `libvmaf` if you build from source.

## Quick start (Docker)

The image is published to GitHub Container Registry:

```
ghcr.io/struck713/brakarr:latest
```

Copy `docker-compose.example.yml` to `docker-compose.yml`, adjust it, and start it:

```yaml
services:
  brakarr:
    image: ghcr.io/struck713/brakarr:latest   # or `build: .` to build locally
    restart: unless-stopped
    user: "1000:1000"
    group_add:
      - render # use the numeric group ID if the name doesn't exist in the image
    devices:
      - /dev/dri/renderD128:/dev/dri/renderD128
    volumes:
      - ./config:/config
      - /path/to/data:/data # match the path your *arr containers use
    ports:
      - "127.0.0.1:1323:1323" # the web UI has no authentication
```

```sh
docker compose up -d
```

Then open <http://localhost:1323>, go to **Settings**, and set the crawl path (for example `/data/media`). Settings are saved to `/config/config.json`. Save, then restart the container: changes to settings take effect on restart.

Mount your media at the **same path** your Sonarr/Radarr containers use. This keeps hardlink detection accurate and avoids needing path mappings.

> **Security:** the web UI has no authentication. Keep it bound to `127.0.0.1`, or put it behind a reverse proxy that handles auth.

## Configuration

Everything is edited on the Settings page and stored in a JSON file (`-config`, default `config.json`). Invalid settings disable transcoding (the UI stays up) until they are fixed and Brakarr is restarted.

| Setting | JSON key | Default | Description |
| --- | --- | --- | --- |
| Crawl path | `crawlPath` | *(required)* | Root directory scanned for media. |
| Extensions | `extensions` | `.mkv`, `.mp4` | Only `.mkv` and `.mp4` are supported. |
| Crawl interval | `crawlInterval` | `10m` | Time between scans. `0` scans once at startup. |
| Minimum file age | `minFileAge` | `30s` | Ignore files modified more recently than this. |
| Work directory | `workDir` | `./work` | Where transcodes are written before replacing the original. Needs space for your largest file. |
| Database path | `dbPath` | `app.db` | SQLite database. |
| VAAPI device | `vaapiDevice` | `/dev/dri/renderD128` | GPU render node. |
| Quality | `quality` | `23` | ICQ quality, 1–51. Lower is higher quality and larger files. |
| Minimum VMAF | `minVmaf` | `94` | Skip files that score below this. `0` disables the check. |
| VMAF samples | `vmafSamples` | `3` | Number of 20-second segments compared (1–10). |
| Workers | `workers` | `1` | Concurrent transcodes. |
| Listen address | `listenAddr` | `127.0.0.1:1323` | Web UI address. |
| Skip hardlinked | `skipHardlinked` | `true` | Defer files that have more than one hardlink. |
| Log level | `logLevel` | `info` | `debug`, `info`, `warn` or `error`. Logs are JSON on stdout. |
| Sonarr | `sonarr.url`, `sonarr.apiKey` | *(none)* | Optional. URL and API key must be set together. |
| Radarr | `radarr.url`, `radarr.apiKey` | *(none)* | Optional. URL and API key must be set together. |
| Path mappings | `arrPathMap` | `[]` | `{ "local": ..., "remote": ... }` pairs for when Brakarr and the apps see different paths. |

Example `config.json`:

```json
{
  "crawlPath": "/data/media",
  "quality": 23,
  "minVmaf": 94,
  "sonarr": { "url": "http://sonarr:8989", "apiKey": "..." },
  "radarr": { "url": "http://radarr:7878", "apiKey": "..." },
  "arrPathMap": [{ "local": "/data/media", "remote": "/media" }]
}
```

### Command-line flags

| Flag | Default | Description |
| --- | --- | --- |
| `-config` | `config.json` | Path to the settings file. Created with defaults if missing. |
| `-listen` | *(from settings)* | Override the listen address. The Docker image uses `0.0.0.0:1323`. |

## Building from source

Requires Go 1.26+, `libmediainfo-dev` (cgo), and `ffmpeg` on your `PATH` with VAAPI and `libvmaf` support. Many distro ffmpeg builds omit `libvmaf`; the Docker image uses a [BtbN](https://github.com/BtbN/FFmpeg-Builds) build that includes it. Alternatively, set the minimum VMAF to `0`.

```sh
go run github.com/a-h/templ/cmd/templ@v0.3.1100 generate
CGO_ENABLED=1 go build -o brakarr ./cmd
./brakarr -config config.json
```

## Project layout

```
cmd/                 entry point
internal/arr         Sonarr/Radarr rescan notifier
internal/config      settings file, validation
internal/crawler     library scanner
internal/database    SQLite store and migrations
internal/media       probing, hashing, hardlink detection
internal/pool        job queue, verification, replacement
internal/transcode   ffmpeg (VAAPI) and VMAF scoring
internal/web         web UI (templ templates, controllers)
```

## Releases

GitHub Actions builds and publishes the Docker image on pushes to `main` (tagged `latest` and by commit SHA) and on `vX.Y.Z` tags (tagged with the version).
