package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"torrent-class/pkg/discovery"
	"torrent-class/pkg/engine"
	"torrent-class/pkg/netutils"
	"torrent-class/pkg/tui"

	"github.com/anacrolix/torrent"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/ncruces/zenity"
)

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

	// If no flags are provided, enter interactive mode via GUI dialogs
	if flag.NFlag() == 0 {
		selectedMode, err := zenity.List(
			"Select operation mode:",
			[]string{"Download (Receive files)", "Seed (Share a file/folder)"},
			zenity.Title("Torrent Class"),
			zenity.DefaultItems("Download (Receive files)"),
		)
		if err != nil {
			if err == zenity.ErrCanceled {
				os.Exit(0)
			}
			log.Fatalf("Error selecting mode: %v", err)
		}

		if selectedMode == "Seed (Share a file/folder)" {
			mode = "seed"
			// Use buttons: Folder (OK), File (Extra), Abort (Cancel/Close)
			err := zenity.Question("What would you like to share?",
				zenity.Title("Torrent Class - Seeding"),
				zenity.OKLabel("          Folder          "),
				zenity.ExtraButton("          File          "),
				zenity.CancelLabel("          Abort          "),
			)

			if err == nil { // "Folder" clicked
				res, err := zenity.SelectFile(
					zenity.Title("Select folder to seed"),
					zenity.Directory(),
				)
				if err != nil {
					if err == zenity.ErrCanceled {
						os.Exit(0)
					}
					log.Fatalf("Error selecting folder: %v", err)
				}
				path = res
			} else if err == zenity.ErrExtraButton { // "File" clicked
				res, err := zenity.SelectFile(
					zenity.Title("Select file to seed"),
				)
				if err != nil {
					if err == zenity.ErrCanceled {
						os.Exit(0)
					}
					log.Fatalf("Error selecting file: %v", err)
				}
				path = res
			} else {
				// "Abort" clicked or window closed
				os.Exit(0)
			}
		} else {
			// Download config
			err := zenity.Question("Where would you like to save the files?",
				zenity.Title("Torrent Class - Download"),
				zenity.OKLabel("          Choose Folder          "),
				zenity.CancelLabel("          Current Directory          "),
			)
			if err == nil {
				res, err := zenity.SelectFile(zenity.Title("Select destination folder"), zenity.Directory())
				if err == nil {
					path = res
				}
			}

			// Discovery config
			discoveryMode, err := zenity.List(
				"Choose discovery mode:",
				[]string{"Automatic (UDP Discovery)", "Manual (Enter Seeder IP)"},
				zenity.Title("Torrent Class - Connectivity"),
				zenity.DefaultItems("Automatic (UDP Discovery)"),
			)
			if err == nil && discoveryMode == "Manual (Enter Seeder IP)" {
				for {
					res, err := zenity.Entry("Enter the Instructor/Seeder IP address:",
						zenity.Title("Manual Connection"),
						zenity.EntryText("192.168.x.x"),
						zenity.Width(400),
					)
					if err != nil { // Canceled
						break
					}
					if isValidIPv4(res) {
						seederIP = res
						break
					}
					zenity.Error("Invalid IPv4 address. Please enter a valid address (e.g. 192.168.1.10) without port.", zenity.Title("Invalid Input"))
				}
			}
		}
	} else {
		// CLI Validation: If mode is seed, path must be provided (not default ".")
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

	// Handle the case where both long and short flags might be provided (last one wins)
	// We use StringVar with pointers so they already overwritten each other if both set in a specific order,
	// but standard 'flag' package doesn't handle aliases perfectly out of the box.
	// We'll trust the user or the last value.

	// TTY Check and Relaunch for Linux (Moved after parameter collection)
	if runtime.GOOS == "linux" && os.Getenv("TORRENT_CLASS_RELAUNCHED") == "" {
		if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
			term := findTerminal()
			if term != "" {
				relaunchInTerminal(term, mode, path, ipOverride, seederIP, port, httpPort, maxConns)
				return // Exit original process
			}
		}
	}

	dataDir, err := filepath.Abs(path)
	if err != nil {
		log.Fatalf("Invalid path: %v", err)
	}

	// Determine storage directory: for seeding, use the parent so the torrent name matches the folder/file
	storageDir := dataDir
	if mode == "seed" {
		storageDir = filepath.Dir(dataDir)
	}

	eng, err := engine.NewEngine(storageDir, port, maxConns)
	if err != nil {
		log.Fatalf("Failed to start engine: %v", err)
	}
	defer eng.Close()

	// Get local IP and all valid interfaces
	interfaces, _ := netutils.GetValidInterfaces()
	localIP := ipOverride
	if localIP == "" {
		localIP = netutils.GetLocalIP()
	}

	var t *torrent.Torrent
	var actualMagnet string

	// Initialize TUI Model
	var httpAddr string
	if mode == "seed" {
		httpAddr = fmt.Sprintf("http://%s:%d", localIP, httpPort)

		// Start HTTP server immediately for binary distribution and metadata discovery
		exePath, _ := os.Executable()
		exeDir := filepath.Dir(exePath)
		mux := http.NewServeMux()
		mux.Handle("/", http.FileServer(http.Dir(exeDir)))

		// Add /info endpoint for HTTP discovery
		mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
			if actualMagnet == "" {
				http.Error(w, "Torrent not ready", http.StatusServiceUnavailable)
				return
			}
			info := discovery.DiscoveryInfo{
				Magnet: actualMagnet,
				IP:     localIP,
				Port:   port,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(info)
		})

		go func() {
			addr := fmt.Sprintf(":%d", httpPort)
			if err := http.ListenAndServe(addr, mux); err != nil {
				log.Printf("HTTP server error: %v", err)
			}
		}()
	}

	m := tui.Model{
		Mode:          mode,
		IP:            localIP,
		Port:          port,
		Magnet:        actualMagnet,
		HTTPAddr:      httpAddr,
		IsHashing:     mode == "seed",
		Progress:      progress.New(progress.WithDefaultGradient()),
		Interfaces:    interfaces,
		FallbackCount: 25,
	}

	p := tea.NewProgram(m)

	// Seeding/Downloading Logic in Background
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

			// Wait for info to be available
			<-t.GotInfo()

			magnetLink := eng.GetMagnetLink(t)
			actualMagnet = magnetLink
			p.Send(tui.TorrentLoadedMsg{
				Torrent: t,
				Magnet:  magnetLink,
			})

			// Start broadcasting
			broadcaster := discovery.NewBroadcaster(magnetLink, port)
			go broadcaster.Start()
		} else if mode == "download" {
			foundInfo := make(chan discovery.DiscoveryInfo, 1)

			// 1. If seeder IP provided, try HTTP immediately
			if seederIP != "" {
				go func() {
					info, err := fetchDiscoveryInfo(seederIP, httpPort)
					if err == nil {
						foundInfo <- info
					}
				}()
			}

			// 2. Start UDP Discovery
			listener := discovery.NewListener()
			go listener.Listen()
			go func() {
				info := <-listener.Foundchan
				foundInfo <- info
			}()

			// 3. Fallback logic: Wait for first discovery (UDP, Manual, or 25s Timeout)
			var info discovery.DiscoveryInfo
			select {
			case info = <-foundInfo:
				// Successfully found via UDP or manual initial IP
			case <-time.After(25 * time.Second):
				if actualMagnet == "" {
					// Trigger popup if still waiting
					for {
						res, err := zenity.Entry("Type in the instructor's IP address:",
							zenity.Title("Torrent Class - Connection Timeout"),
							zenity.EntryText("192.168.x.x"),
							zenity.Width(400),
						)
						if err != nil {
							break
						}
						if isValidIPv4(res) {
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
				actualMagnet = info.Magnet
				t, err = eng.AddTorrentByMagnet(actualMagnet)
				if err != nil {
					log.Printf("Failed to add magnet: %v", err)
					return
				}
				eng.AddPeer(t, info.IP, info.Port)

				p.Send(tui.TorrentLoadedMsg{
					Torrent: t,
					Magnet:  actualMagnet,
				})

				// Viral Seeding
				broadcaster := discovery.NewBroadcaster(actualMagnet, port)
				go broadcaster.Start()
			} else if actualMagnet != "" {
				// We already have a magnet (e.g. from CLI flag)
				t, err = eng.AddTorrentByMagnet(actualMagnet)
				if err != nil {
					log.Printf("Failed to add magnet: %v", err)
					return
				}
				p.Send(tui.TorrentLoadedMsg{
					Torrent: t,
					Magnet:  actualMagnet,
				})
				broadcaster := discovery.NewBroadcaster(actualMagnet, port)
				go broadcaster.Start()
			}
		}
	}()

	if _, err := p.Run(); err != nil {
		log.Fatalf("TUI Error: %v", err)
	}

	// Keep terminal open if we relaunched
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

	// Build relaunch command with collected parameters
	os.Setenv("TORRENT_CLASS_RELAUNCHED", "1")

	base := filepath.Base(terminalPath)

	// Collect arguments to pass to the new process
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
		// Most others support -e command [args]
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

func isValidIPv4(ip string) bool {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}
	// To4 returns non-nil only for IPv4.
	// Also ensure no colons (IPv6 or port)
	return parsedIP.To4() != nil && !strings.Contains(ip, ":")
}
