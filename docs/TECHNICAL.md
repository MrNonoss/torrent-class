# Technical Overview

Torrent Class wraps [`anacrolix/torrent`](https://github.com/anacrolix/torrent) with local peer discovery so a group of machines on the same LAN can distribute large files without internet access, trackers, or DHT.

## Architecture

### 1. Torrent Engine (`pkg/engine`)
- **Local-only configuration**: DHT and UPnP/NAT-PMP port forwarding are disabled so traffic stays on the local network and avoids router errors.
- **Dynamic piece sizing**: Piece length scales automatically with payload size—from `256 KiB` (under `512 MiB`) up to `16 MiB` (`16 GiB` and above)—keeping metadata compact even for multi-gigabyte VM images.
- **Sequential hashing**: Files are opened one at a time during hashing so folders containing thousands of files never exhaust OS file descriptors.
- **Metainfo cache**: Generated torrent metadata is cached next to the source path as `.<name>.torrent-class-cache`. On subsequent runs, the cache is reused if no file has a newer modification time and both the file count and total byte size still match.

### 2. Peer Discovery (`pkg/discovery` & `pkg/httpserver`)
Because trackers and DHT are off, peers find each other through two local channels:

- **UDP Broadcast (port `4243`)**: Every 2 seconds, active seeders and downloaders broadcast `TORRENT_DIST:<magnet>|<ip>|<port>` to the subnet broadcast address and `255.255.255.255` (skipping loopback and `169.254.x.x` link-local addresses). Listeners parse incoming packets and add `IP:Port` directly to the torrent client, refreshing seen peers after a 10-minute TTL.
- **HTTP Fallback (port `8000`)**: When seeding, an HTTP server exposes `/info` (returning JSON with the magnet URI, seeder IP, and BitTorrent port) and serves `shareable_YYYY-MM-DD/` at `/`. If a downloader receives no UDP broadcast within 25 seconds—common on client-isolated or filtered Wi-Fi—it prompts for the seeder's IP and fetches `/info` over HTTP.

### 3. Classroom Swarm Behavior
To prevent the instructor's machine from becoming a bottleneck when dozens of students connect at once:
1. Downloaders prioritize the first 5% of pieces (`PiecePriorityNow`) as soon as metadata arrives so they have complete pieces to upload quickly.
2. Downloaders immediately start broadcasting the magnet over UDP so late-joining peers discover and pull from classmates as well as the instructor.

### 4. Setup Dialogs & Terminal UI (`pkg/wizard` & `pkg/tui`)
- Running without CLI flags opens native dialogs via `ncruces/zenity` (Win32 on Windows, `osascript` on macOS, `zenity` on Linux). On Linux desktops, if the binary was double-clicked without a terminal attached, it automatically relaunches itself inside an available terminal emulator.
- Once configured, `bubbletea` renders transfer progress, peer count, and exponentially smoothed (EMA) upload/download speeds.

## Local State Files

- `.torrent.bolt.db`: Created by `anacrolix/torrent` in the working directory to track piece completion across restarts.
- `.<target>.torrent-class-cache`: Cached torrent metainfo next to a seeded file or folder.

Delete either file if you want to force a clean re-hash or reset local transfer state.
