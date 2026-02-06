package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"path/filepath"

	"torrent-class/pkg/discovery"
	"torrent-class/pkg/engine"
	"torrent-class/pkg/tui"

	"net/http"
	"os"

	"github.com/anacrolix/torrent"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	var mode, path, magnet, ipOverride string
	var port, httpPort int

	flag.StringVar(&mode, "mode", "download", "Mode: seed or download")
	flag.StringVar(&mode, "m", "download", "Short for --mode")

	flag.StringVar(&path, "path", ".", "Path to file/folder (default: current directory)")
	flag.StringVar(&path, "p", ".", "Short for --path")

	flag.IntVar(&port, "port", 4242, "Port for BitTorrent traffic (default: 4242)")
	flag.IntVar(&port, "l", 4242, "Short for --port")

	flag.StringVar(&magnet, "magnet", "", "Magnet link to download (for download mode)")
	flag.StringVar(&magnet, "x", "", "Short for --magnet")

	flag.IntVar(&httpPort, "http-port", 8000, "Port for the HTTP binary distribution server")
	flag.IntVar(&httpPort, "s", 8000, "Short for --http-port")

	flag.StringVar(&ipOverride, "ip", "", "Manually specify the local IP to broadcast")
	flag.StringVar(&ipOverride, "i", "", "Short for --ip")

	flag.Parse()

	// Handle the case where both long and short flags might be provided (last one wins)
	// We use StringVar with pointers so they already overwritten each other if both set in a specific order,
	// but standard 'flag' package doesn't handle aliases perfectly out of the box.
	// We'll trust the user or the last value.

	dataDir, err := filepath.Abs(path)
	if err != nil {
		log.Fatalf("Invalid path: %v", err)
	}

	// Determine storage directory: for seeding, use the parent so the torrent name matches the folder/file
	storageDir := dataDir
	if mode == "seed" {
		storageDir = filepath.Dir(dataDir)
	}

	eng, err := engine.NewEngine(storageDir, port)
	if err != nil {
		log.Fatalf("Failed to start engine: %v", err)
	}
	defer eng.Close()

	// Get local IP
	localIP := ipOverride
	if localIP == "" {
		localIP = getLocalIP()
	}

	var t *torrent.Torrent
	var actualMagnet string

	if mode == "seed" {
		t, err = eng.CreateTorrentFromPath(dataDir)
		if err != nil {
			log.Fatalf("Failed to create torrent: %v", err)
		}

		// Wait for info to be available
		<-t.GotInfo()

		magnetLink := eng.GetMagnetLink(t)
		actualMagnet = magnetLink

		// Start broadcasting
		broadcaster := discovery.NewBroadcaster(magnetLink, port)
		go broadcaster.Start()

		// Start HTTP server for binary distribution
		exePath, _ := os.Executable()
		exeDir := filepath.Dir(exePath)
		go func() {
			addr := fmt.Sprintf(":%d", httpPort)
			http.Handle("/", http.FileServer(http.Dir(exeDir)))
			log.Printf("Starting binary distribution server on %s", addr)
			if err := http.ListenAndServe(addr, nil); err != nil {
				log.Printf("HTTP server error: %v", err)
			}
		}()
	} else if mode == "download" {
		actualMagnet = magnet
		if actualMagnet == "" {
			listener := discovery.NewListener()
			go listener.Listen()
			info := <-listener.Foundchan
			actualMagnet = info.Magnet

			t, err = eng.AddTorrentByMagnet(actualMagnet)
			if err != nil {
				log.Fatalf("Failed to add magnet: %v", err)
			}

			// Add the seeder as a peer immediately
			eng.AddPeer(t, info.IP, info.Port)

			// Viral Seeding: Start broadcasting once we have the magnet link
			broadcaster := discovery.NewBroadcaster(actualMagnet, port)
			go broadcaster.Start()
		} else {
			t, err = eng.AddTorrentByMagnet(actualMagnet)
			if err != nil {
				log.Fatalf("Failed to add magnet: %v", err)
			}

			// Viral Seeding: Start broadcasting the magnet link we provided
			broadcaster := discovery.NewBroadcaster(actualMagnet, port)
			go broadcaster.Start()
		}

	} else {
		log.Fatalf("Unknown mode: %s", mode)
	}

	// Initialize TUI
	var httpAddr string
	if mode == "seed" {
		httpAddr = fmt.Sprintf("http://%s:%d", localIP, httpPort)
	}

	m := tui.Model{
		Mode:     mode,
		IP:       localIP,
		Port:     port,
		Magnet:   actualMagnet,
		HTTPAddr: httpAddr,
		Torrent:  t,
		Progress: progress.New(progress.WithDefaultGradient()),
	}

	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		log.Fatalf("TUI Error: %v", err)
	}
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "Unknown"
	}

	var bestIP string
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}

			// Skip APIPA (169.254.x.x)
			if ip[0] == 169 && ip[1] == 254 {
				continue
			}

			// If it's a private network address, it's a very good candidate
			if isPrivateIP(ip) {
				return ip.String()
			}

			bestIP = ip.String()
		}
	}

	if bestIP != "" {
		return bestIP
	}
	return "127.0.0.1"
}

func isPrivateIP(ip net.IP) bool {
	if ip[0] == 10 {
		return true
	}
	if ip[0] == 172 && (ip[1] >= 16 && ip[1] <= 31) {
		return true
	}
	if ip[0] == 192 && ip[1] == 168 {
		return true
	}
	return false
}
