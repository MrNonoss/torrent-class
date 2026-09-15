package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"torrent-class/pkg/discovery"
	"torrent-class/pkg/engine"
	"torrent-class/pkg/httpserver"
	"torrent-class/pkg/netutils"
	"torrent-class/pkg/tui"
	"torrent-class/pkg/wizard"

	"github.com/anacrolix/torrent"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/ncruces/zenity"
)

// peerTTL is how long a peer stays in the seen-cache before it can be re-added.
// 10 minutes is long enough to suppress spurious re-adds within a session while
// still allowing a peer that reconnected after a drop to rejoin the mesh (Issue #3).
const peerTTL = 10 * time.Minute

// seenPeer records the last time we added a peer to the torrent.
type seenPeer struct {
	addedAt time.Time
}

func main() {
	var mode, path, ipOverride, seederIP string
	var port, httpPort, maxConns int

	flag.StringVar(&mode, "mode", "download", "Mode: seed or download")
	flag.StringVar(&mode, "m", "download", "Short for --mode")

	flag.StringVar(&path, "path", ".", "Path to file/folder (default: current directory)")
	flag.StringVar(&path, "p", ".", "Short for --path")

	flag.IntVar(&port, "bit-port", 8081, "Port for BitTorrent traffic (default: 8081)")
	flag.IntVar(&port, "b", 8081, "Short for --bit-port")

	flag.IntVar(&httpPort, "http-port", 8000, "Port for the HTTP binary distribution server")
	flag.IntVar(&httpPort, "l", 8000, "Short for --http-port")

	flag.StringVar(&ipOverride, "ip", "", "Manually specify the local IP to broadcast")
	flag.StringVar(&ipOverride, "i", "", "Short for --ip")

	flag.IntVar(&maxConns, "max-conns", 200, "Maximum simultaneous connections for speed (default: 200)")
	flag.IntVar(&maxConns, "c", 200, "Short for --max-conns")

	flag.StringVar(&seederIP, "seeder", "", "Manually specify the seeder's IP address")
	flag.StringVar(&seederIP, "s", "", "Short for --seeder")

	flag.Parse()

	if flag.NFlag() == 0 {
		var err error
		mode, path, ipOverride, seederIP, err = wizard.RunInteractiveSetup()
		if err != nil {
			log.Fatalf("Setup error: %v", err)
		}
	} else {
		pathWasSet := false
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "path" || f.Name == "p" {
				pathWasSet = true
			}
		})

		if mode == "seed" && !pathWasSet {
			fmt.Println("Error: You must specify a path to the file or folder you want to seed.")
			fmt.Println("Usage: torrent-class -m seed -p <path>")
			os.Exit(1)
		}
	}

	if runtime.GOOS == "linux" && os.Getenv("TORRENT_CLASS_RELAUNCHED") == "" {
		if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
			term := findTerminal()
			if term != "" {
				relaunchInTerminal(term, mode, path, ipOverride, seederIP, port, httpPort, maxConns)
				return
			}
		}
	}

	dataDir, err := filepath.Abs(path)
	if err != nil {
		log.Fatalf("Invalid path: %v", err)
	}

	storageDir := dataDir
	if mode == "seed" {
		storageDir = filepath.Dir(dataDir)
	}

	eng, err := engine.NewEngine(storageDir, port, maxConns)
	if err != nil {
		log.Fatalf("Failed to start engine: %v", err)
	}
	defer eng.Close()

	interfaces, _ := netutils.GetValidInterfaces()
	localIP := ipOverride
	if localIP == "" {
		localIP = netutils.GetLocalIP()
	}

	var t *torrent.Torrent

	// actualMagnet is written by the background goroutine and read by the HTTP
	// server goroutine and the discovery listener goroutines — protect it with a
	// mutex to eliminate the data race (Issue #5).
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

	// Root context: cancelled when the TUI exits, which cascades to all
	// background goroutines (Broadcaster, Listener) for clean shutdown (Issues #6, #10).
	ctx, cancel := context.WithCancel(context.Background())

	var httpAddr string
	if mode == "seed" {
		httpAddr = httpserver.StartShareableServer(localIP, httpPort, port, getMagnet)
	}

	// fallbackDeadline is computed once so the UI countdown and the actual
	// time.After in the goroutine share exactly the same deadline (Issue #12).
	fallbackDeadline := time.Now().Add(25 * time.Second)

	m := tui.Model{
		Mode:             mode,
		IP:               localIP,
		Port:             port,
		Magnet:           actualMagnet,
		HTTPAddr:         httpAddr,
		IsHashing:        mode == "seed",
		Progress:         progress.New(progress.WithDefaultGradient()),
		Interfaces:       interfaces,
		FallbackDeadline: fallbackDeadline,
	}

	p := tea.NewProgram(m)

	go func() {
		if mode == "seed" {
			var skipped []string
			t, skipped, err = eng.CreateTorrentFromPathWithProgress(dataDir, func(read, total int64) {
				if total > 0 {
					p.Send(tui.HashingProgressMsg(float64(read) / float64(total)))
				}
			})
			if err != nil {
				log.Printf("Failed to create torrent: %v", err)
				return
			}

			if len(skipped) > 0 {
				p.Send(tui.SkippedFilesMsg(skipped))
			}

			<-t.GotInfo()

			magnetLink := eng.GetMagnetLink(t)
			setMagnet(magnetLink)
			p.Send(tui.TorrentLoadedMsg{
				Torrent: t,
				Magnet:  magnetLink,
			})

			// Continuously listen for new peers to ensure full mesh.
			listener := discovery.NewListener()
			go listener.Listen(ctx)
			go func() {
				seen := make(map[string]seenPeer)
				for {
					select {
					case info, ok := <-listener.Foundchan:
						if !ok {
							return
						}
						if info.Magnet == getMagnet() {
							peerKey := fmt.Sprintf("%s:%d", info.IP, info.Port)
							now := time.Now()
							if entry, exists := seen[peerKey]; !exists || now.Sub(entry.addedAt) >= peerTTL {
								seen[peerKey] = seenPeer{addedAt: now}
								eng.AddPeer(t, info.IP, info.Port)
							}
							// Evict expired entries to bound map size (Issue #3)
							if len(seen) > 200 {
								for k, v := range seen {
									if now.Sub(v.addedAt) >= peerTTL {
										delete(seen, k)
									}
								}
							}
						}
					case <-ctx.Done():
						return
					}
				}
			}()

			broadcaster := discovery.NewBroadcaster(magnetLink, port)
			go broadcaster.Start(ctx)
		} else if mode == "download" {
			foundInfo := make(chan discovery.DiscoveryInfo, 1)

			if seederIP != "" {
				go func() {
					info, err := fetchDiscoveryInfo(seederIP, httpPort)
					if err == nil {
						select {
						case foundInfo <- info:
						default:
						}
					}
				}()
			}

			listener := discovery.NewListener()
			go listener.Listen(ctx)
			// Forward the first UDP announcement to foundInfo.
			// The ctx.Done() arm prevents this goroutine from leaking when
			// foundInfo is already resolved via the HTTP path (Issue #6).
			go func() {
				select {
				case info := <-listener.Foundchan:
					select {
					case foundInfo <- info:
					default:
					}
				case <-ctx.Done():
				}
			}()

			var info discovery.DiscoveryInfo
			select {
			case info = <-foundInfo:
			case <-time.After(time.Until(fallbackDeadline)):
				if getMagnet() == "" {
					for {
						res, err := zenity.Entry("Type in the instructor's IP address:",
							zenity.Title("Torrent Class - Connection Timeout"),
							zenity.EntryText("192.168.x.x"),
							zenity.Width(400),
						)
						if err != nil {
							break
						}
						if wizard.IsValidIPv4(res) {
							info, err = fetchDiscoveryInfo(res, httpPort)
							if err != nil {
								log.Printf("Manual HTTP discovery failed: %v", err)
								zenity.Error(fmt.Sprintf("Failed to connect to seeder at %s: %v", res, err), zenity.Title("Connection Error"))
								continue
							}
							break
						}
						zenity.Error("Invalid IPv4 address. Please enter a valid address (e.g. 192.168.1.10) without port.", zenity.Title("Invalid Input"))
					}
				}
			}

			if info.Magnet != "" {
				setMagnet(info.Magnet)
				t, err = eng.AddTorrentByMagnet(getMagnet())
				if err != nil {
					log.Printf("Failed to add magnet: %v", err)
					return
				}
				eng.AddPeer(t, info.IP, info.Port)

				p.Send(tui.TorrentLoadedMsg{
					Torrent: t,
					Magnet:  getMagnet(),
				})

				broadcaster := discovery.NewBroadcaster(getMagnet(), port)
				go broadcaster.Start(ctx)

				// Continuously listen for new peers to ensure full mesh.
				go func() {
					seen := make(map[string]seenPeer)
					seen[fmt.Sprintf("%s:%d", info.IP, info.Port)] = seenPeer{addedAt: time.Now()}

					for {
						select {
						case newInfo, ok := <-listener.Foundchan:
							if !ok {
								return
							}
							if newInfo.Magnet == getMagnet() {
								peerKey := fmt.Sprintf("%s:%d", newInfo.IP, newInfo.Port)
								now := time.Now()
								if entry, exists := seen[peerKey]; !exists || now.Sub(entry.addedAt) >= peerTTL {
									seen[peerKey] = seenPeer{addedAt: now}
									eng.AddPeer(t, newInfo.IP, newInfo.Port)
								}
								// Evict expired entries to bound map size (Issue #3)
								if len(seen) > 200 {
									for k, v := range seen {
										if now.Sub(v.addedAt) >= peerTTL {
											delete(seen, k)
										}
									}
								}
							}
						case <-ctx.Done():
							return
						}
					}
				}()
			} else if getMagnet() != "" {
				t, err = eng.AddTorrentByMagnet(getMagnet())
				if err != nil {
					log.Printf("Failed to add magnet: %v", err)
					return
				}
				p.Send(tui.TorrentLoadedMsg{
					Torrent: t,
					Magnet:  getMagnet(),
				})
				broadcaster := discovery.NewBroadcaster(getMagnet(), port)
				go broadcaster.Start(ctx)

				// Continuously listen for new peers to ensure full mesh.
				go func() {
					seen := make(map[string]seenPeer)
					for {
						select {
						case newInfo, ok := <-listener.Foundchan:
							if !ok {
								return
							}
							if newInfo.Magnet == getMagnet() {
								peerKey := fmt.Sprintf("%s:%d", newInfo.IP, newInfo.Port)
								now := time.Now()
								if entry, exists := seen[peerKey]; !exists || now.Sub(entry.addedAt) >= peerTTL {
									seen[peerKey] = seenPeer{addedAt: now}
									eng.AddPeer(t, newInfo.IP, newInfo.Port)
								}
								// Evict expired entries to bound map size (Issue #3)
								if len(seen) > 200 {
									for k, v := range seen {
										if now.Sub(v.addedAt) >= peerTTL {
											delete(seen, k)
										}
									}
								}
							}
						case <-ctx.Done():
							return
						}
					}
				}()
			}
		}
	}()

	if _, err := p.Run(); err != nil {
		cancel()
		log.Fatalf("TUI Error: %v", err)
	}
	cancel() // Signal all background goroutines to exit cleanly (Issues #6, #10)

	if os.Getenv("TORRENT_CLASS_RELAUNCHED") == "1" {
		fmt.Println("\nPress Enter to exit...")
		var b [1]byte
		os.Stdin.Read(b[:])
	}
}

func findTerminal() string {
	terminals := []string{
		"x-terminal-emulator",
		"gnome-terminal",
		"konsole",
		"xfce4-terminal",
		"lxterminal",
		"terminator",
		"kitty",
		"alacritty",
		"xterm",
	}

	for _, t := range terminals {
		p, err := exec.LookPath(t)
		if err == nil {
			return p
		}
	}
	return ""
}

func relaunchInTerminal(terminalPath string, mode, path, ip, seederIP string, port, httpPort, maxConns int) {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}

	os.Setenv("TORRENT_CLASS_RELAUNCHED", "1")
	base := filepath.Base(terminalPath)

	args := []string{
		"--mode", mode,
		"--path", path,
		"--bit-port", fmt.Sprintf("%d", port),
		"--http-port", fmt.Sprintf("%d", httpPort),
		"--max-conns", fmt.Sprintf("%d", maxConns),
	}
	if ip != "" {
		args = append(args, "--ip", ip)
	}
	if seederIP != "" {
		args = append(args, "--seeder", seederIP)
	}

	var cmdArgs []string
	switch base {
	case "gnome-terminal":
		cmdArgs = append(cmdArgs, "--", exe)
		cmdArgs = append(cmdArgs, args...)
	case "konsole":
		cmdArgs = append(cmdArgs, "-e", exe)
		cmdArgs = append(cmdArgs, args...)
	case "terminator":
		cmdArgs = append(cmdArgs, "-x", exe)
		cmdArgs = append(cmdArgs, args...)
	default:
		cmdArgs = append(cmdArgs, "-e", exe)
		cmdArgs = append(cmdArgs, args...)
	}

	cmd := exec.Command(terminalPath, cmdArgs...)
	_ = cmd.Start()
	os.Exit(0)
}

func fetchDiscoveryInfo(ip string, port int) (discovery.DiscoveryInfo, error) {
	url := fmt.Sprintf("http://%s:%d/info", ip, port)
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return discovery.DiscoveryInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return discovery.DiscoveryInfo{}, fmt.Errorf("bad status: %s", resp.Status)
	}

	var info discovery.DiscoveryInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return discovery.DiscoveryInfo{}, err
	}
	return info, nil
}
