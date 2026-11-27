# Building from Source

Requires **Go 1.25.6** or newer.

## Local Build

Build a binary for your current OS and architecture:

```bash
go build -o torrent-class ./cmd/torrent-class
```

## Cross-Compile Release Binaries

Build stripped, static binaries (`CGO_ENABLED=0`, `-trimpath`) for Windows (`amd64`, `arm64`), Linux (`amd64`, `arm64`), and macOS (`amd64`, `arm64`) into `releases/`:

```bash
go run scripts/build.go
```

You can override the embedded version or output directory with `-version` and `-out`:

```bash
go run scripts/build.go -version 2.6.0 -out dist
```

## Tests

```bash
go vet ./...
go test -race ./...
```
