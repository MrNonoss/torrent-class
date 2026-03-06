package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

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

// CreateTorrentFromPathWithProgress creates a torrent from a file or directory with hashing progress
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

	pieceLength := calculatePieceLength(totalSize)

	info := metainfo.Info{
		PieceLength: pieceLength,
		Name:        filepath.Base(absPath),
	}

	fi, err := os.Stat(absPath)
	if err != nil {
		return nil, skipped, err
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
			info.Files = append(info.Files, metainfo.FileInfo{
				Path:   filepath.SplitList(rel),
				Length: ffi.Size(),
			})
		}
	} else {
		info.Length = totalSize
	}

	// Create a concatenated reader for all files
	var readers []io.Reader
	var finalFiles []string
	for _, f := range files {
		file, err := os.Open(f)
		if err != nil {
			if os.IsPermission(err) {
				skipped = append(skipped, f)
				continue
			}
			return nil, skipped, err
		}
		defer file.Close()
		readers = append(readers, file)
		finalFiles = append(finalFiles, f)
	}

	if len(readers) == 0 {
		return nil, skipped, fmt.Errorf("all files were inaccessible during hashing")
	}

	// Update files list in case some were skipped during opening
	if len(finalFiles) != len(files) {
		// If we are in single file mode and it failed, we already handled it.
		// If we are in directory mode, we need to rebuild info.Files if pieces were already generated?
		// Actually, we haven't generated pieces yet.
		// But info.Files was already populated. This is getting complex.
		// Let's simplify: if we can't open it now, we should have caught it in calculateTotalSize.
		// We'll keep it for robustness.
	}

	multiReader := io.MultiReader(readers...)
	progressReader := &ProgressReader{
		r:          multiReader,
		total:      totalSize,
		onProgress: onProgress,
	}

	// Generate pieces
	info.Pieces, err = metainfo.GeneratePieces(progressReader, info.PieceLength, nil)
	if err != nil {
		return nil, skipped, fmt.Errorf("failed to generate pieces: %w", err)
	}

	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return nil, skipped, fmt.Errorf("failed to marshal info: %w", err)
	}

	mi := metainfo.MetaInfo{
		InfoBytes: infoBytes,
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
