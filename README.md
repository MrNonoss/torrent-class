# Torrent Class

A lightweight, local-network peer-to-peer file distribution system built with Go and the BitTorrent protocol.

## Features

- **Double-Click Experience**: Run the program without flags to be guided by native popups.
- **Local Discovery**: Automatic peer discovery on local networks via UDP broadcast.
- **No Dependencies**: Works without central trackers or DHT for local-only operation.
- **Viral Seeding**: Downloaders automatically help share parts with others.
- **Premium TUI**: A sleek terminal user interface for monitoring transfers.

## Previews

| Seeding (Windows) | Downloading (macOS) |
|:---:|:---:|
| <img src="assets/Seeder.png" width="400" alt="Seeding"> | <img src="assets/Downloader.png" width="450" alt="Downloading"> |

## Getting Started

### 1. Download
Grab the latest binary for your platform from the [Releases](https://github.com/MrNonoss/torrent-class/releases) section.

### 2. Usage (Interactive Mode)
Simply **double-click** the binary (or run it without flags). 
- Select **Seed** to share a file or folder.
- Select **Download** to start receiving files broadcasted on the network.

### 3. CLI Mode (Advanced)
For scripts or specific configurations, use command-line flags:

```bash
# Seed a folder
./torrent-class -m seed -p path/to/folder

# Download to a specific folder
./torrent-class -p path/to/destination
```

**Common Flags:**
- `-m`, `--mode`: `seed` or `download` (default: `download`)
- `-p`, `--path`: Path to file/folder to seed or destination path.
- `-i`, `--ip`: Manually specify your local IP/Adapter if needed.
- `-c`, `--max-conns`: Max simultaneous connections (default: 200). Increase for faster transfers in large groups.

## Network Requirements

> [!IMPORTANT]
> **All machines must be on the same Local Subnet.** Peer discovery relies on UDP broadcast, which typically does not cross between different subnets or VLANs (e.g., student Wi-Fi vs. teacher Ethernet).

### Multiple Network Adapters
If your machine has multiple adapters (Wi-Fi, Ethernet, Virtual Machines):
1. **Check the TUI**: The app lists all "Detected Adapters". Ensure the **Local IP** (shown in red) matches the network your students are using.
2. **Override if necessary**: If the app selects the wrong adapter (like a VM interface), use the `-i` flag to force it:
   ```bash
   ./torrent-class -i 192.168.1.15
   ```

## Documentation

- **[Technical Guide](docs/TECHNICAL.md)**: Details about the P2P engine and discovery protocol.
- **[Build Guide](docs/BUILD.md)**: Instructions for developers building from source.

## Dependencies

- `github.com/anacrolix/torrent`: Core P2P engine.
- `github.com/charmbracelet/bubbletea`: TUI framework.
- `github.com/ncruces/zenity`: Native GUI dialogs.
