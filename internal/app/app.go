package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/ncruces/zenity"

	"torrent-class/internal/discovery"
	"torrent-class/internal/engine"
	"torrent-class/internal/httpserver"
	"torrent-class/internal/netutils"
	"torrent-class/internal/tui"
	"torrent-class/internal/winmgr"
	"torrent-class/internal/wizard"
)

const (
	// peerTTL controls how long a peer remains in the seen-cache before re-adding.
	peerTTL          = 10 * time.Minute
	fallbackWaitTime = 25 * time.Second
)

var errSeederNotReady = errors.New("seeder is still hashing files")

// Config holds the resolved runtime configuration for a session.
type Config struct {
	Version  string
	Mode     string
	Path     string
	IP       string
	SeederIP string
	BitPort  int
	HTTPPort int
	MaxConns int
}

type seenPeer struct {
	addedAt time.Time
}

// Run starts the torrent engine, discovery/HTTP services, and the TUI.
func Run(cfg Config) error {
	dataDir, err := filepath.Abs(cfg.Path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	storageDir := dataDir
	if cfg.Mode == "seed" {
		storageDir = filepath.Dir(dataDir)
	}

	eng, err := engine.NewEngine(storageDir, cfg.BitPort, cfg.MaxConns)
	if err != nil {
		return fmt.Errorf("failed to start engine: %w", err)
	}
	defer eng.Close()

	interfaces, _ := netutils.GetValidInterfaces()
	localIP := cfg.IP
	if localIP == "" {
		localIP = netutils.GetLocalIP()
	}

	var magnetMu sync.RWMutex
	var actualMagnet string
	getMagnet := func() string {
		magnetMu.RLock()
		defer magnetMu.RUnlock()
		return actualMagnet
	}
	setMagnet := func(m string) {
		magnetMu.Lock()
		defer magnetMu.Unlock()
		actualMagnet = m
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Forward background errors to the TUI rather than printing to stderr over Bubble Tea.
	var p *tea.Program
	sendErr := func(msg string) {
		if p != nil {
			p.Send(tui.ErrorMsg(msg))
		}
	}

	var httpAddr string
	if cfg.Mode == "seed" {
		httpAddr = httpserver.StartShareableServer(ctx, localIP, cfg.HTTPPort, cfg.BitPort, getMagnet, sendErr)
	}

	fallbackDeadline := time.Now().Add(fallbackWaitTime)

	m := tui.Model{
		Version:          cfg.Version,
		Mode:             cfg.Mode,
		IP:               localIP,
		Port:             cfg.BitPort,
		SeederIP:         cfg.SeederIP,
		HTTPAddr:         httpAddr,
		IsHashing:        cfg.Mode == "seed",
		Progress:         progress.New(progress.WithDefaultGradient()),
		Interfaces:       interfaces,
		FallbackDeadline: fallbackDeadline,
	}

	p = tea.NewProgram(m)

	go func() {
		switch cfg.Mode {
		case "seed":
			runSeed(ctx, cfg, dataDir, eng, p, getMagnet, setMagnet, sendErr)
		case "download":
			runDownload(ctx, cfg, eng, p, fallbackDeadline, getMagnet, setMagnet, sendErr)
		}
	}()

	if _, err := p.Run(); err != nil {
		cancel()
		return fmt.Errorf("TUI error: %w", err)
	}
	cancel()
	return nil
}

func runSeed(
	ctx context.Context,
	cfg Config,
	dataDir string,
	eng *engine.Engine,
	p *tea.Program,
	getMagnet func() string,
	setMagnet func(string),
	sendErr func(string),
) {
	t, skipped, err := eng.CreateTorrentFromPathWithProgress(dataDir, func(read, total int64) {
		if total > 0 {
			p.Send(tui.HashingProgressMsg(float64(read) / float64(total)))
		}
	})
	if err != nil {
		sendErr(fmt.Sprintf("Failed to create torrent: %v", err))
		return
	}

	if len(skipped) > 0 {
		p.Send(tui.SkippedFilesMsg(skipped))
	}

	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		return
	}

	magnetLink := eng.GetMagnetLink(t)
	setMagnet(magnetLink)
	p.Send(tui.TorrentLoadedMsg{
		Torrent: t,
		Magnet:  magnetLink,
	})

	listener := discovery.NewListener()
	go func() {
		if err := listener.Listen(ctx); err != nil {
			sendErr(fmt.Sprintf("UDP discovery listener error (port %d): %v", discovery.BroadcastPort, err))
		}
	}()
	go runPeerMesh(ctx, listener, t, eng, getMagnet)

	broadcaster := discovery.NewBroadcaster(magnetLink, cfg.BitPort)
	go func() {
		if err := broadcaster.Start(ctx); err != nil {
			sendErr(fmt.Sprintf("UDP broadcaster error: %v", err))
		}
	}()
}

func runDownload(
	ctx context.Context,
	cfg Config,
	eng *engine.Engine,
	p *tea.Program,
	fallbackDeadline time.Time,
	getMagnet func() string,
	setMagnet func(string),
	sendErr func(string),
) {
	foundInfo := make(chan discovery.DiscoveryInfo, 1)
	deliver := func(info discovery.DiscoveryInfo) {
		if info.Magnet == "" {
			return
		}
		select {
		case foundInfo <- info:
		default:
		}
	}

	if cfg.SeederIP != "" {
		go pollDiscoveryInfo(ctx, cfg.SeederIP, cfg.HTTPPort, deliver)
	}

	listener := discovery.NewListener()
	go func() {
		if err := listener.Listen(ctx); err != nil {
			sendErr(fmt.Sprintf("UDP discovery listener error (port %d): %v", discovery.BroadcastPort, err))
		}
	}()

	// Wait for initial discovery via UDP or explicit HTTP polling.
	// Keep reading listener.Foundchan until an info arrives or ctx closes so a
	// late-starting seeder still connects even after the 25s prompt is cancelled.
	var info discovery.DiscoveryInfo
	dlgCtx, dlgCancel := context.WithCancel(ctx)
	defer dlgCancel()
	promptDone := make(chan struct{})
	promptStarted := false

	timer := time.NewTimer(time.Until(fallbackDeadline))
	defer timer.Stop()

waitLoop:
	for {
		select {
		case <-ctx.Done():
			return
		case info = <-foundInfo:
			break waitLoop
		case udpInfo, ok := <-listener.Foundchan:
			if !ok {
				return
			}
			deliver(udpInfo)
		case <-timer.C:
			if !promptStarted {
				promptStarted = true
				go func() {
					defer close(promptDone)
					if manualInfo, ok := promptManualSeeder(dlgCtx, cfg.HTTPPort, func(ip string) {
						go pollDiscoveryInfo(ctx, ip, cfg.HTTPPort, deliver)
					}); ok {
						deliver(manualInfo)
					}
				}()
			}
		case <-promptDone:
			// User dismissed or completed the manual prompt without a result yet;
			// remain in waitLoop so late UDP broadcasts or HTTP polling still connect.
			promptDone = nil
		}
	}
	dlgCancel()

	setMagnet(info.Magnet)
	t, err := eng.AddTorrentByMagnet(info.Magnet)
	if err != nil {
		sendErr(fmt.Sprintf("Failed to add magnet: %v", err))
		return
	}
	_ = eng.AddPeer(t, info.IP, info.Port)

	p.Send(tui.TorrentLoadedMsg{
		Torrent:  t,
		Magnet:   info.Magnet,
		SeederIP: info.IP,
	})

	broadcaster := discovery.NewBroadcaster(info.Magnet, cfg.BitPort)
	go func() {
		if err := broadcaster.Start(ctx); err != nil {
			sendErr(fmt.Sprintf("UDP broadcaster error: %v", err))
		}
	}()
	go runPeerMesh(ctx, listener, t, eng, getMagnet, fmt.Sprintf("%s:%d", info.IP, info.Port))

	// Start downloading and prioritize the first 5% of pieces once metadata arrives
	// so students can begin re-seeding to classmates as quickly as possible.
	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		return
	}

	t.DownloadAll()
	numPieces := t.NumPieces()
	earlyCount := numPieces / 20
	if earlyCount < 5 {
		earlyCount = 5
	}
	for i := 0; i < earlyCount && i < numPieces; i++ {
		t.Piece(i).SetPriority(torrent.PiecePriorityNow)
	}
}

func promptManualSeeder(
	ctx context.Context,
	httpPort int,
	onHashing func(ip string),
) (discovery.DiscoveryInfo, bool) {
	winmgr.StartDialogPromoter(ctx)
	dlgOpts := append([]zenity.Option{zenity.Context(ctx)}, winmgr.DialogOptions()...)

	for {
		if ctx.Err() != nil {
			return discovery.DiscoveryInfo{}, false
		}
		res, err := zenity.Entry("Type in the instructor's IP address:",
			append([]zenity.Option{
				zenity.Title("Torrent Class - Connection Timeout"),
				zenity.EntryText("192.168.x.x"),
				zenity.Width(400),
			}, dlgOpts...)...,
		)
		if err != nil {
			return discovery.DiscoveryInfo{}, false
		}
		if !wizard.IsValidIPv4(res) {
			_ = zenity.Error("Invalid IPv4 address. Please enter a valid address (e.g. 192.168.1.10) without port.",
				append([]zenity.Option{zenity.Title("Invalid Input")}, dlgOpts...)...)
			continue
		}

		info, err := FetchDiscoveryInfo(res, httpPort)
		if errors.Is(err, errSeederNotReady) {
			// Seeder is reachable and still hashing; poll it in the background
			// and return control to waitLoop so UDP also continues listening.
			if onHashing != nil {
				onHashing(res)
			}
			return discovery.DiscoveryInfo{}, false
		}
		if err != nil {
			_ = zenity.Error(fmt.Sprintf("Failed to connect to seeder at %s: %v", res, err),
				append([]zenity.Option{zenity.Title("Connection Error")}, dlgOpts...)...)
			continue
		}
		return info, true
	}
}

// runPeerMesh listens for UDP discovery packets matching our magnet and feeds
// new or expired peers into the torrent client.
func runPeerMesh(
	ctx context.Context,
	listener *discovery.Listener,
	t *torrent.Torrent,
	eng *engine.Engine,
	getMagnet func() string,
	initialPeers ...string,
) {
	seen := make(map[string]seenPeer)
	now := time.Now()
	for _, key := range initialPeers {
		if key != "" {
			seen[key] = seenPeer{addedAt: now}
		}
	}

	for {
		select {
		case info, ok := <-listener.Foundchan:
			if !ok {
				return
			}
			if info.Magnet != getMagnet() {
				continue
			}

			peerKey := fmt.Sprintf("%s:%d", info.IP, info.Port)
			now = time.Now()
			if entry, exists := seen[peerKey]; !exists || now.Sub(entry.addedAt) >= peerTTL {
				seen[peerKey] = seenPeer{addedAt: now}
				_ = eng.AddPeer(t, info.IP, info.Port)
			}

			if len(seen) > 200 {
				for k, v := range seen {
					if now.Sub(v.addedAt) >= peerTTL {
						delete(seen, k)
					}
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// pollDiscoveryInfo queries the seeder's HTTP /info endpoint with retry backoff
// so a student running -s before the seeder finishes hashing connects automatically.
func pollDiscoveryInfo(ctx context.Context, ip string, port int, onFound func(discovery.DiscoveryInfo)) {
	for {
		info, err := FetchDiscoveryInfo(ip, port)
		if err == nil {
			onFound(info)
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// FetchDiscoveryInfo retrieves torrent discovery metadata from a seeder's HTTP
// endpoint and pins the returned IP to the address that actually answered.
func FetchDiscoveryInfo(ip string, port int) (discovery.DiscoveryInfo, error) {
	url := fmt.Sprintf("http://%s:%d/info", ip, port)
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return discovery.DiscoveryInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusServiceUnavailable {
		return discovery.DiscoveryInfo{}, errSeederNotReady
	}
	if resp.StatusCode != http.StatusOK {
		return discovery.DiscoveryInfo{}, fmt.Errorf("bad status: %s", resp.Status)
	}

	var info discovery.DiscoveryInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return discovery.DiscoveryInfo{}, err
	}
	// Trust the IP we successfully reached over HTTP rather than a possibly virtual IP reported by the seeder.
	info.IP = ip
	return info, nil
}
