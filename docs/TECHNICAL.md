# Technical Documentation: Torrent Class

Torrent Class is a specialized P2P file sharing tool designed for high-speed, local-network distribution without requiring internet connectivity or central infrastructure.

## Core Technologies

### 1. BitTorrent Protocol (`anacrolix/torrent`)
The engine is built on the [anacrolix/torrent](https://github.com/anacrolix/torrent) library, a full-featured BitTorrent implementation in Go. 
- **Hashing**: Files are split into pieces (default 256KB) and hashed (SHA-1). This ensures data integrity—if a single bit is flipped during transfer, the client detects it and redownloads that piece.
- **Tit-for-Tat**: The library manages peer connections, piece selection strategies, and bandwidth throttling.

### 2. Local UDP Discovery (`pkg/discovery`)
Since we operate in "offline" mode without Trackers or DHT (Distributed Hash Tables), we use a custom UDP Broadcast protocol:
- **Port**: 4243
- **Mechanism**: The Seeder (and Downloaders in "Viral" mode) sends a UDP packet to the subnet's broadcast address (e.g., `192.168.1.255`) and the global broadcast `255.255.255.255`.
- **Payload**: `TORRENT_DIST:<magnet_link>|<ip>|<port>`
- **Direct Connection**: When a client hears this broadcast, it skips the normal BitTorrent "search" phase and immediately adds the sender's IP:Port as a direct peer.

### 3. Terminal UI (`pkg/tui`)
Built using the **Charmbracelet** stack:
- **Bubble Tea**: A Go framework based on The Elm Architecture (Model-Update-View).
- **Lip Gloss**: Used for terminal styling, borders, and colors.
- **Progress**: A specialized component for rendering the visual progress bar.

## Network Architecture: "Viral Seeding"

In a traditional setup, the instructor (Seeder) would be the bottleneck. Torrent Class implements **Viral Seeding**:
1. **Instructor** starts seeding and broadcasting.
2. **Student A** receives broadcast, starts downloading.
3. **Student A** immediately begins broadcasting the same magnet link.
4. **Student B** can now find the file from either the Teacher OR Student A.
5. **Data Transfer**: Students share pieces with each other as they download, creating a mesh network that scales with the number of participants.

## Storage & Persistence

Torrent Class uses a local database file named `.torrent.bolt.db` (BoltDB format) to store:
- **Metadata Cache**: Stores torrent information so it doesn't need to be re-fetched on every start.
- **Peer History**: Remembers known peer addresses to speed up reconnections.
- **Download State**: Tracks which pieces are already on disk to allow resuming transfers instantly.

> [!TIP]
> If you encounter network errors like "Host is down" or cannot find peers after a network change, deleting this file will clear the peer cache and force a fresh discovery.

## Dependencies

- `github.com/anacrolix/torrent`: Core P2P engine.
- `github.com/charmbracelet/bubbletea`: TUI framework.
- `github.com/charmbracelet/lipgloss`: TUI styling.
