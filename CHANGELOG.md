# Changelog

## v2.6.0

### Fixes
- **Windows Dialog Promoter Crash**: Allocated the `EnumWindows` callback once at package initialization instead of per poll tick, preventing the Go runtime's 2000-callback limit crash after ~40 seconds in the setup wizard.
- **Centered Terminal & Frontmost Setup Dialogs**: When double-clicked in interactive mode, the terminal centers on the active monitor and setup dialogs appear centered in front of it (without keeping dialogs permanently above other apps like File Explorer).
- **Linux Desktop Launch Order**: Double-clicking on Linux now opens a terminal first before running the setup prompts.
- **Late-Starting Seeders**: Downloaders now keep listening for UDP broadcasts while the 25-second IP prompt is open (auto-closing it if a broadcast arrives) and after dismissing it, and retry `503` responses when a seeder is still hashing.
- **Cache Rename Detection**: Metainfo caching now validates each file's relative path and size in addition to mtimes, catching renamed or moved files.
- **Symlinks & Locked Files**: Non-regular files (symlinks, sockets, pipes) and unreadable/locked files are skipped and reported instead of corrupting pieces or aborting hashing.
- **Reliable Peer IPs & Clean TUI Errors**: Peer discovery uses the actual UDP packet source address or the IP that answered HTTP `/info`, and background errors render cleanly in the TUI instead of corrupting the terminal.

### Improvements
- **macOS Default Download Directory**: Picking "Current Directory" after double-clicking in Finder saves alongside the executable instead of `$HOME`.
- **Completion Prompt & Payload Info**: The TUI now shows the torrent name, size, source IP, version, a keep-seeding banner on completion, and asks for a second `q` press before disconnecting while peers are active.
- **Project Structure & Build**: Moved internal packages under `internal/`, renamed the entrypoint to `cmd/torrent-class`, added `-trimpath` / stripped ldflags and ARM64 targets (`windows/arm64`, `linux/arm64`), and added unit/integration tests plus GitHub Actions CI.

## v2.5.0

- Full-mesh UDP peer discovery and continuous re-broadcasting by downloaders.
- Early prioritization (`PiecePriorityNow`) of the first 5% of pieces.
- Persistent UDP broadcaster sockets and `.torrent-class-cache` metadata caching.
- Sequential file hashing to avoid file-descriptor exhaustion on large folders.
- EMA-smoothed transfer speeds and physical LAN adapter prioritization.
