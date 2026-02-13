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
func NewEngine(dataDir string, listenPort int) (*Engine, error) {
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.ListenPort = listenPort
	cfg.NoUpload = false
	cfg.NoDHT = true // Disable DHT as requested (local network only)
	cfg.Seed = true
	cfg.NoDefaultPortForwarding = true // Disable UPnP/PMP to avoid errors on some routers/Mac

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
func (e *Engine) CreateTorrentFromPath(path string) (*torrent.Torrent, error) {
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
func (e *Engine) CreateTorrentFromPathWithProgress(path string, onProgress func(int64, int64)) (*torrent.Torrent, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	info := metainfo.Info{
		PieceLength: 256 * 1024, // 256KB pieces
		Name:        filepath.Base(absPath),
	}

	var files []string
	var totalSize int64

	fi, err := os.Stat(absPath)
	if err != nil {
		return nil, err
	}

	if fi.IsDir() {
		err = filepath.Walk(absPath, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				files = append(files, p)
				totalSize += info.Size()
			}
			return nil
		})
		if err != nil {
			return nil, err
		}

		for _, f := range files {
			rel, err := filepath.Rel(absPath, f)
			if err != nil {
				return nil, err
			}
			ffi, err := os.Stat(f)
			if err != nil {
				return nil, err
			}
			info.Files = append(info.Files, metainfo.FileInfo{
				Path:   filepath.SplitList(rel),
				Length: ffi.Size(),
			})
		}
	} else {
		totalSize = fi.Size()
		info.Length = totalSize
		files = []string{absPath}
	}

	// Create a concatenated reader for all files
	var readers []io.Reader
	for _, f := range files {
		file, err := os.Open(f)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		readers = append(readers, file)
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
		return nil, fmt.Errorf("failed to generate pieces: %w", err)
	}

	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal info: %w", err)
	}

	mi := metainfo.MetaInfo{
		InfoBytes: infoBytes,
	}

	// Add to client
	t, err := e.Client.AddTorrent(&mi)
	if err != nil {
		return nil, fmt.Errorf("failed to add torrent to client: %w", err)
	}

	return t, nil
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

// GetMagnetLink returns the magnet link for a torrent
func (e *Engine) GetMagnetLink(t *torrent.Torrent) string {
	mi := t.Metainfo()
	return mi.Magnet(nil, nil).String()
}
