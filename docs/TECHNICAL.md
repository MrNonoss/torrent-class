# Technical Overview

Torrent Class wraps [`anacrolix/torrent`](https://github.com/anacrolix/torrent) with local peer discovery so a group of machines on the same LAN can distribute large files without internet access, trackers, or DHT.

## Architecture

### 1. Torrent Engine (`internal/engine`)
- **Local-only configuration**: DHT and UPnP/NAT-PMP port forwarding are disabled so traffic stays on the local network and avoids router errors.
- **Dynamic piece sizing**: Piece length scales automatically with payload size—from `256 KiB` (under `512 MiB`) up to `16 MiB` (`16 GiB` and above)—keeping metadata compact even for multi-gigabyte VM images.
- **Sequential hashing & safe walking**: Files are checked for readability and opened one at a time during hashing so folders with thousands of files don't exhaust file descriptors, and symlinks, special files, or locked files are skipped rather than corrupting pieces or aborting the seed.
- **Metainfo cache**: Generated torrent metadata is cached next to the source path as `.<name>.torrent-class-cache`. Subsequent runs reuse it only if modification times haven't advanced and the root name, file count, total size, and every file's relative path and size match.

### 2. Peer Discovery (`internal/discovery` & `internal/httpserver`)
Because trackers and DHT are off, peers find each other through two local channels:

- **UDP Broadcast (port `4243`)**: Every 2 seconds, active seeders and downloaders broadcast `TORRENT_DIST:<magnet>|<ip>|<port>` to each subnet's broadcast address and `255.255.255.255` (skipping loopback and `169.254.x.x` link-local addresses). Listeners use the observed UDP source IP (`ReadFromUDP`) and add `IP:Port` directly to the torrent client, refreshing seen peers after a 10-minute TTL.
- **HTTP Fallback (port `8000`)**: When seeding, an HTTP server exposes `/info` (returning JSON with the magnet URI, seeder IP, and BitTorrent port) and serves `shareable_YYYY-MM-DD/` at `/`. If a downloader receives no UDP broadcast within 25 seconds, it prompts for the seeder's IP while continuing to listen for UDP in the background, and polls `/info` if the seeder is still hashing (`503`).

### 3. Classroom Swarm Behavior (`internal/app`)
To prevent the instructor's machine from becoming a bottleneck when dozens of students connect at once:
1. Downloaders prioritize the first 5% of pieces (`PiecePriorityNow`) as soon as metadata arrives so they have complete pieces to upload quickly.
2. Downloaders immediately start broadcasting the magnet over UDP so late-joining peers discover and pull from classmates as well as the instructor.

### 4. Setup Dialogs, Window Placement & TUI (`internal/wizard`, `internal/winmgr`, `internal/tui`)
- Running without CLI flags opens native dialogs via `ncruces/zenity` (Win32 on Windows, `osascript` on macOS, `zenity` on Linux). When launched by double-click, `winmgr` centers the terminal window and keeps setup dialogs centered in front of it. On Linux desktops, double-clicking without a TTY relaunches inside an available terminal emulator first.
- Once configured, `bubbletea` renders payload info, progress, peer count, EMA-smoothed speeds, and any background errors cleanly inside the TUI, and asks for a second `q` press before quitting while actively sharing with peers.

## Local State Files

- `.torrent.bolt.db`: Created by `anacrolix/torrent` in the storage directory to track piece completion across restarts.
- `.<target>.torrent-class-cache`: Cached torrent metainfo next to a seeded file or folder.

Delete either file if you want to force a clean re-hash or reset local transfer state.
