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

// Engine handles torrent operations
type Engine struct {
	Client *torrent.Client
	Config *torrent.ClientConfig
}

// NewEngine creates a new torrent engine
func NewEngine(dataDir string, listenPort int, maxConns int) (*Engine, error) {
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.ListenPort = listenPort
	cfg.NoUpload = false
	cfg.NoDHT = true // Disable DHT as requested (local network only)
	cfg.Seed = true
	cfg.NoDefaultPortForwarding = true // Disable UPnP/PMP to avoid errors on some routers/Mac

	// Performance tuning
	if maxConns > 0 {
		cfg.EstablishedConnsPerTorrent = maxConns
		cfg.HalfOpenConnsPerTorrent = maxConns / 2
	}

	client, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create torrent client: %w", err)
	}

	return &Engine{
		Client: client,
		Config: cfg,
	}, nil
}

// CreateTorrentFromPath creates a torrent from a file or directory
func (e *Engine) CreateTorrentFromPath(path string) (*torrent.Torrent, []string, error) {
	return e.CreateTorrentFromPathWithProgress(path, nil)
}

// sequentialFileReader opens and reads files one by one to avoid hitting OS file descriptor limits
type sequentialFileReader struct {
	files        []string
	currentIndex int
	currentFile  *os.File
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

// ProgressReader wraps an io.Reader and reports progress
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
// It returns the torrent, a list of skipped files (due to permissions), and any fatal error.
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

	// Stat the target path up-front; needed both for cache validation and torrent building.
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
			f, err := os.Open(cacheFile)
			if err == nil {
				decodeErr := bencode.NewDecoder(f).Decode(&mi)
				f.Close() // close immediately — don't hold the handle open during hashing (Issue #7)
				if decodeErr == nil {
					// Extra validation: verify file count and total size match so that
					// deleted or renamed files (which don't bump any mtime) invalidate
					// the cache correctly (Issue #9).
					if cachedInfo, infoErr := mi.UnmarshalInfo(); infoErr == nil {
						if cacheMatchesFiles(cachedInfo, fi.IsDir(), files, totalSize) {
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
					// This shouldn't happen as we just stat-ed it in calculateTotalSize,
					// but handle it just in case.
					if os.IsPermission(err) {
						skipped = append(skipped, f)
						continue
					}
					return nil, skipped, err
				}
				// filepath.SplitList splits on the OS PATH separator (';' on Windows),
				// not on path separators. The correct split is on '/' after normalising
				// the relative path (Issue #8).
				info.Files = append(info.Files, metainfo.FileInfo{
					Path:   strings.Split(filepath.ToSlash(rel), "/"),
					Length: ffi.Size(),
				})
			}
		} else {
			info.Length = totalSize
		}

		// Create a sequential reader to avoid opening all files at once
		seqReader := &sequentialFileReader{
			files: files,
		}

		progressReader := &ProgressReader{
			r:          seqReader,
			total:      totalSize,
			onProgress: onProgress,
		}

		// Generate pieces
		info.Pieces, err = metainfo.GeneratePieces(progressReader, info.PieceLength, nil)
		if seqReader.currentFile != nil {
			seqReader.currentFile.Close()
		}
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
			bencode.NewEncoder(f).Encode(mi)
			f.Close()
		}
	} else {
		// Fast progress for UI if loaded from cache
		if onProgress != nil {
			onProgress(totalSize, totalSize)
		}
	}

	// Add to client
	t, err := e.Client.AddTorrent(&mi)
	if err != nil {
		return nil, skipped, fmt.Errorf("failed to add torrent to client: %w", err)
	}

	return t, skipped, nil
}

// AddTorrentByMagnet adds a torrent using a magnet link
func (e *Engine) AddTorrentByMagnet(magnet string) (*torrent.Torrent, error) {
	t, err := e.Client.AddMagnet(magnet)
	if err != nil {
		return nil, fmt.Errorf("failed to add magnet: %w", err)
	}
	return t, nil
}

// AddPeer adds a peer to a torrent
func (e *Engine) AddPeer(t *torrent.Torrent, ip string, port int) error {
	addr := torrent.StringAddr(fmt.Sprintf("%s:%d", ip, port))
	t.AddPeers([]torrent.PeerInfo{
		{
			Addr: addr,
		},
	})
	return nil
}

// Close closes the torrent engine
func (e *Engine) Close() {
	if e.Client != nil {
		e.Client.Close()
	}
}

// calculateTotalSize returns the total size, list of files, and skipped files for a path
func (e *Engine) calculateTotalSize(path string) (int64, []string, []string, error) {
	var totalSize int64
	var files []string
	var skipped []string

	fi, err := os.Stat(path)
	if err != nil {
		if os.IsPermission(err) {
			return 0, nil, []string{path}, nil
		}
		return 0, nil, nil, err
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
			if !info.IsDir() {
				files = append(files, p)
				totalSize += info.Size()
			}
			return nil
		})
		if err != nil {
			return 0, nil, nil, err
		}
	} else {
		totalSize = fi.Size()
		files = []string{path}
	}

	return totalSize, files, skipped, nil
}

// getLatestModTime finds the most recent modification time among a list of files
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

// calculatePieceLength returns an appropriate piece length based on the total size
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

// GetMagnetLink returns the magnet link for a torrent
func (e *Engine) GetMagnetLink(t *torrent.Torrent) string {
	mi := t.Metainfo()
	return mi.Magnet(nil, nil).String()
}

// cacheMatchesFiles validates that a cached MetaInfo still matches the current
// file set. Mtime-only checks miss deleted or renamed files; comparing file
// count and total byte size catches those cases (Issue #9).
func cacheMatchesFiles(info metainfo.Info, isDir bool, files []string, totalSize int64) bool {
	if isDir {
		if len(info.Files) != len(files) {
			return false
		}
		var cachedTotal int64
		for _, f := range info.Files {
			cachedTotal += f.Length
		}
		return cachedTotal == totalSize
	}
	return info.Length == totalSize
}
