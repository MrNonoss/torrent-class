package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// Engine wraps the underlying BitTorrent client and configuration.
type Engine struct {
	Client *torrent.Client
	Config *torrent.ClientConfig
}

// DefaultConfig returns the standard local-only client configuration.
func DefaultConfig(dataDir string, listenPort int, maxConns int) *torrent.ClientConfig {
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.ListenPort = listenPort
	cfg.NoUpload = false
	cfg.NoDHT = true
	cfg.Seed = true
	cfg.NoDefaultPortForwarding = true

	if maxConns > 0 {
		cfg.EstablishedConnsPerTorrent = maxConns
		cfg.HalfOpenConnsPerTorrent = maxConns / 2
	}
	return cfg
}

// NewEngine creates a new local-only torrent engine.
func NewEngine(dataDir string, listenPort int, maxConns int) (*Engine, error) {
	return NewEngineWithConfig(DefaultConfig(dataDir, listenPort, maxConns))
}

// NewEngineWithConfig creates a torrent engine using a caller-supplied configuration.
func NewEngineWithConfig(cfg *torrent.ClientConfig) (*Engine, error) {
	client, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create torrent client: %w", err)
	}

	return &Engine{
		Client: client,
		Config: cfg,
	}, nil
}

// CreateTorrentFromPath creates a torrent from a file or directory without progress reporting.
func (e *Engine) CreateTorrentFromPath(path string) (*torrent.Torrent, []string, error) {
	return e.CreateTorrentFromPathWithProgress(path, nil)
}

// sequentialFileReader opens and reads files one by one to avoid hitting OS file descriptor limits.
type sequentialFileReader struct {
	files        []string
	currentIndex int
	currentFile  *os.File
}

func (s *sequentialFileReader) Close() {
	if s.currentFile != nil {
		s.currentFile.Close()
		s.currentFile = nil
	}
}

func (s *sequentialFileReader) Read(p []byte) (int, error) {
	for {
		if s.currentFile == nil {
			if s.currentIndex >= len(s.files) {
				return 0, io.EOF
			}
			var err error
			s.currentFile, err = os.Open(s.files[s.currentIndex])
			if err != nil {
				return 0, err
			}
		}

		n, err := s.currentFile.Read(p)
		if n > 0 {
			return n, nil
		}
		if err == io.EOF {
			s.currentFile.Close()
			s.currentFile = nil
			s.currentIndex++
			continue
		}
		return n, err
	}
}

// ProgressReader wraps an io.Reader and reports cumulative read progress.
type ProgressReader struct {
	r          io.Reader
	total      int64
	read       int64
	onProgress func(int64, int64)
}

func (pr *ProgressReader) Read(p []byte) (n int, err error) {
	n, err = pr.r.Read(p)
	pr.read += int64(n)
	if pr.onProgress != nil {
		pr.onProgress(pr.read, pr.total)
	}
	return
}

// CreateTorrentFromPathWithProgress creates a torrent from a file or directory with hashing progress.
// It returns the torrent, a list of skipped files (unreadable, locked, or non-regular/symlinks), and any fatal error.
func (e *Engine) CreateTorrentFromPathWithProgress(path string, onProgress func(int64, int64)) (*torrent.Torrent, []string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}

	totalSize, files, skipped, err := e.calculateTotalSize(absPath)
	if err != nil {
		return nil, nil, err
	}

	if len(files) == 0 {
		return nil, skipped, fmt.Errorf("no accessible files found in %s", absPath)
	}

	fi, err := os.Stat(absPath)
	if err != nil {
		return nil, skipped, err
	}

	latestModTime, _ := getLatestModTime(files)
	cacheFile := filepath.Join(filepath.Dir(absPath), "."+filepath.Base(absPath)+".torrent-class-cache")

	var mi metainfo.MetaInfo
	loadedFromCache := false

	if cacheInfo, err := os.Stat(cacheFile); err == nil {
		if !cacheInfo.ModTime().Before(latestModTime) {
			if f, err := os.Open(cacheFile); err == nil {
				decodeErr := bencode.NewDecoder(f).Decode(&mi)
				f.Close()
				if decodeErr == nil {
					if cachedInfo, infoErr := mi.UnmarshalInfo(); infoErr == nil {
						if cacheMatchesFiles(cachedInfo, absPath, fi.IsDir(), files, totalSize) {
							loadedFromCache = true
						}
					}
				}
			}
		}
	}

	if !loadedFromCache {
		pieceLength := calculatePieceLength(totalSize)

		info := metainfo.Info{
			PieceLength: pieceLength,
			Name:        filepath.Base(absPath),
		}

		if fi.IsDir() {
			for _, f := range files {
				rel, err := filepath.Rel(absPath, f)
				if err != nil {
					return nil, skipped, err
				}
				ffi, err := os.Stat(f)
				if err != nil {
					if os.IsPermission(err) {
						skipped = append(skipped, f)
						continue
					}
					return nil, skipped, err
				}
				info.Files = append(info.Files, metainfo.FileInfo{
					Path:   strings.Split(filepath.ToSlash(rel), "/"),
					Length: ffi.Size(),
				})
			}
		} else {
			info.Length = totalSize
		}

		seqReader := &sequentialFileReader{
			files: files,
		}
		defer seqReader.Close()

		progressReader := &ProgressReader{
			r:          seqReader,
			total:      totalSize,
			onProgress: onProgress,
		}

		info.Pieces, err = metainfo.GeneratePieces(progressReader, info.PieceLength, nil)
		seqReader.Close()
		if err != nil {
			return nil, skipped, fmt.Errorf("failed to generate pieces: %w", err)
		}

		infoBytes, err := bencode.Marshal(info)
		if err != nil {
			return nil, skipped, fmt.Errorf("failed to marshal info: %w", err)
		}

		mi = metainfo.MetaInfo{
			InfoBytes: infoBytes,
		}

		if f, err := os.Create(cacheFile); err == nil {
			_ = bencode.NewEncoder(f).Encode(mi)
			_ = f.Close()
		}
	} else if onProgress != nil {
		onProgress(totalSize, totalSize)
	}

	t, err := e.Client.AddTorrent(&mi)
	if err != nil {
		return nil, skipped, fmt.Errorf("failed to add torrent to client: %w", err)
	}

	return t, skipped, nil
}

// AddTorrentByMagnet adds a torrent using a magnet link.
func (e *Engine) AddTorrentByMagnet(magnet string) (*torrent.Torrent, error) {
	t, err := e.Client.AddMagnet(magnet)
	if err != nil {
		return nil, fmt.Errorf("failed to add magnet: %w", err)
	}
	return t, nil
}

// AddPeer adds a peer to a torrent.
func (e *Engine) AddPeer(t *torrent.Torrent, ip string, port int) error {
	addr := torrent.StringAddr(fmt.Sprintf("%s:%d", ip, port))
	t.AddPeers([]torrent.PeerInfo{
		{
			Addr: addr,
		},
	})
	return nil
}

// Close closes the torrent engine.
func (e *Engine) Close() {
	if e.Client != nil {
		e.Client.Close()
	}
}

// calculateTotalSize walks path and collects accessible regular files, skipping
// symlinks/special files and locked/unreadable files rather than aborting hashing.
func (e *Engine) calculateTotalSize(path string) (int64, []string, []string, error) {
	var totalSize int64
	var files []string
	var skipped []string

	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsPermission(err) {
			return 0, nil, []string{path}, nil
		}
		return 0, nil, nil, err
	}

	// Allow the root path itself to be a symlink to a directory or file by following it once.
	if fi.Mode()&os.ModeSymlink != 0 {
		fi, err = os.Stat(path)
		if err != nil {
			return 0, nil, nil, err
		}
	}

	if fi.IsDir() {
		err = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				if os.IsPermission(err) {
					skipped = append(skipped, p)
					if info != nil && info.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				return err
			}
			if info.IsDir() {
				return nil
			}
			// Skip symlinks, sockets, pipes, and device files so Lstat size never
			// mismatches the bytes read during piece generation.
			if !info.Mode().IsRegular() {
				skipped = append(skipped, p)
				return nil
			}
			if !canOpenForRead(p) {
				skipped = append(skipped, p)
				return nil
			}
			files = append(files, p)
			totalSize += info.Size()
			return nil
		})
		if err != nil {
			return 0, nil, nil, err
		}
	} else {
		if !fi.Mode().IsRegular() || !canOpenForRead(path) {
			return 0, nil, []string{path}, nil
		}
		totalSize = fi.Size()
		files = []string{path}
	}

	return totalSize, files, skipped, nil
}

func canOpenForRead(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func getLatestModTime(files []string) (time.Time, error) {
	var latest time.Time
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			if os.IsPermission(err) {
				continue
			}
			return time.Time{}, err
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest, nil
}

func calculatePieceLength(totalSize int64) int64 {
	const (
		KiB = 1024
		MiB = 1024 * KiB
		GiB = 1024 * MiB
	)

	switch {
	case totalSize < 512*MiB:
		return 256 * KiB
	case totalSize < 1*GiB:
		return 512 * KiB
	case totalSize < 2*GiB:
		return 1 * MiB
	case totalSize < 4*GiB:
		return 2 * MiB
	case totalSize < 8*GiB:
		return 4 * MiB
	case totalSize < 16*GiB:
		return 8 * MiB
	default:
		return 16 * MiB
	}
}

// GetMagnetLink returns a magnet URI including the display name (dn=) when available.
func (e *Engine) GetMagnetLink(t *torrent.Torrent) string {
	mi := t.Metainfo()
	if m, err := mi.MagnetV2(); err == nil {
		return m.String()
	}
	return mi.Magnet(nil, t.Info()).String()
}

// cacheMatchesFiles validates that a cached MetaInfo still matches the current
// root name, file count, total byte size, and every file's relative path and size.
// Checking relative paths catches renames and moves that preserve count, size, and mtime.
func cacheMatchesFiles(info metainfo.Info, rootPath string, isDir bool, files []string, totalSize int64) bool {
	if info.Name != filepath.Base(rootPath) {
		return false
	}
	if !isDir {
		return len(info.Files) == 0 && info.Length == totalSize
	}
	if len(info.Files) != len(files) {
		return false
	}

	expected := make(map[string]int64, len(info.Files))
	var cachedTotal int64
	for _, f := range info.Files {
		rel := strings.Join(f.Path, "/")
		expected[rel] = f.Length
		cachedTotal += f.Length
	}
	if cachedTotal != totalSize {
		return false
	}

	for _, full := range files {
		rel, err := filepath.Rel(rootPath, full)
		if err != nil {
			return false
		}
		fi, err := os.Stat(full)
		if err != nil {
			return false
		}
		expSize, ok := expected[filepath.ToSlash(rel)]
		if !ok || expSize != fi.Size() {
			return false
		}
	}
	return true
}
