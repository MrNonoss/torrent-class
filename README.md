# Torrent Class

A local-network file distribution tool built with Go and BitTorrent. It lets an instructor share large files or folders across a classroom without internet access, a central tracker, or a single-server bottleneck—as students download pieces, they automatically share them with the rest of the room.

Only a proper LAN is required.

## Usage

Grab the binary for your OS from [Releases](https://github.com/MrNonoss/torrent-class/releases).

### Interactive Mode

Double-click the binary (or run it without flags) and follow the prompts. If your machine has multiple network interfaces (e.g., Wi-Fi plus VMware/VirtualBox/VPN adapters), select the one connected to the classroom network.

#### 1. Seeding (Instructor)

Select **Seed**, then choose the file or folder you want to distribute. When seeding starts, the tool also opens a small HTTP server (shown next to `Deploy:` in the terminal, e.g. `http://192.168.1.10:8000`) serving a `shareable_YYYY-MM-DD` folder with a copy of the binary—you can drop small extra files there if students need to grab them from a browser.

> **Mixed-OS classrooms**: By default, the `shareable_YYYY-MM-DD` folder only includes a copy of the instructor's own OS binary so users don't have to keep every platform's binary on disk. If your classroom mixes Windows, macOS, and Linux, either have students grab their OS binary from [Releases](https://github.com/MrNonoss/torrent-class/releases) or drop the extra binaries into `shareable_YYYY-MM-DD/` before starting.
>
> **Tip — Speed up large transfers with extra seeders**: If you have 2 or 3 laptops available with the same file or folder already copied onto them, start **one** in **Seed** mode and the others in **Download** mode pointed at the folder containing those existing files. Because they already have all the data on disk, they will verify the pieces locally and immediately act as extra full seeders for the room, greatly speeding the process.

![Seeding walkthrough](assets/Seed.gif)

#### 2. Downloading (Student)

Select **Download**, pick where to save the files, and let automatic UDP discovery connect to the swarm (or enter the instructor's IP manually if UDP is blocked).

![Downloading walkthrough](assets/Download.gif)

### Command Line

```bash
# Share a file or folder
./torrent-class -m seed -p /path/to/folder

# Download into a folder (auto-discovers the seeder via UDP)
./torrent-class -p /path/to/destination

# Download directly from a known seeder IP (skips UDP discovery)
./torrent-class -p /path/to/destination -s 192.168.1.10
```

| Flag | Default | Description |
| :--- | :--- | :--- |
| `-m`, `--mode` | `download` | `seed` or `download` |
| `-p`, `--path` | `.` | File/folder to seed, or download destination |
| `-s`, `--seeder` | *(auto)* | Seeder IPv4 address (uses HTTP discovery directly) |
| `-i`, `--ip` | *(auto)* | Local IPv4 interface to bind/advertise |
| `-b`, `--bit-port` | `8081` | BitTorrent port |
| `-l`, `--http-port` | `8000` | HTTP server port (used when seeding or fetching `/info`) |
| `-c`, `--max-conns` | `200` | Max peer connections per torrent |
| `-v`, `--version` | | Print version and exit |

## First-Time OS Setup & Troubleshooting

- **Windows Firewall**: On first launch, Windows Defender Firewall will prompt for network access—allow **Private networks** (or Domain if on a corporate LAN) so inbound BitTorrent (`8081`), UDP discovery (`4243`), and HTTP (`8000`) traffic aren't blocked.
- **macOS Gatekeeper**: Unsigned binaries downloaded over HTTP or from a browser are quarantined by macOS. Before running, mark the binary executable and clear the quarantine attribute:
  ```bash
  chmod +x torrent-class-darwin-*
  xattr -d com.apple.quarantine torrent-class-darwin-*
  ```
- **Linux permissions**: After downloading over HTTP or from Releases, make the binary executable:
  ```bash
  chmod +x torrent-class-linux-*
  ```
- **Same subnet required**: All machines need to be able to reach each other directly on the local network.
- **UDP blocked on corporate/campus Wi-Fi**: Peer discovery uses UDP broadcast (`4243`) first. If no broadcast arrives within 25 seconds, the app prompts for the instructor's IP and fetches the torrent metadata over HTTP (`8000`) while continuing to listen for UDP. You can skip waiting by choosing **Manual** mode at startup or passing `-s <ip>`.
- **Wrong IP shown in the terminal**: Check `Local IP` at the top of the TUI. If a virtual adapter (Docker, WSL, VMware, VPN) was selected, restart and pick the LAN/Wi-Fi adapter—or pass `-i <your-lan-ip>`.

## Further Reading

- [Technical Overview](docs/TECHNICAL.md)
- [Building from Source](docs/BUILD.md)
- [Changelog](CHANGELOG.md)
