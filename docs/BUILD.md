# Building from Source

Requires **Go 1.25.6** or newer.

## Local Build

Build a binary for your current OS and architecture:

```bash
go build -o torrent-class ./cmd/distributor
```

## Cross-Compile Release Binaries

Build binaries for Windows (`amd64`), Linux (`amd64`), and macOS (`amd64` and `arm64`) into the `releases/` folder:

```bash
go run scripts/build.go
```

## Tests

```bash
go test ./...
```
