package engine

import (
	"fmt"
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
	info := metainfo.Info{
		PieceLength: 256 * 1024, // 256KB pieces
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	err = info.BuildFromFilePath(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build info from path: %w", err)
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
