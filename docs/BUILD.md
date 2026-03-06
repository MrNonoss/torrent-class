# Building Torrent Class

This guide is for developers who want to build the tool from source or cross-compile it for different platforms.

## Prerequisites

- Go 1.25.6 or higher.

## Installation

1. Clone the repository:
   ```bash
   git clone <repository-url>
   cd torrent-class
   ```

2. Install dependencies:
   ```bash
   go mod download
   ```

## Compilation

To build for your current platform:

```bash
go build -o torrent-class ./cmd/distributor
```

## Build Automation (Cross-Compilation)

To cross-compile binaries for the main supported platforms (Windows, Linux, macOS):

```bash
go run scripts/build.go
```

The compiled binaries will be saved in the `releases/` folder.

## Dependencies

- `github.com/anacrolix/torrent`: Core P2P engine.
- `github.com/charmbracelet/bubbletea`: TUI framework.
- `github.com/charmbracelet/bubbles`: TUI component library (Progress Bar).
- `github.com/charmbracelet/lipgloss`: Terminal layout and styling.
- `github.com/ncruces/zenity`: Native GUI dialogs.
- `github.com/mattn/go-isatty`: Terminal capability detection.
