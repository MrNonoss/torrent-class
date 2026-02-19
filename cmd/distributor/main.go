package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

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
	var mode, path, magnet, ipOverride string
	var port, httpPort, maxConns int

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

	flag.IntVar(&maxConns, "max-conns", 200, "Maximum simultaneous connections for speed (default: 200)")
	flag.IntVar(&maxConns, "c", 200, "Short for --max-conns")

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
				zenity.OKLabel("Folder"),
				zenity.ExtraButton("File"),
				zenity.CancelLabel("Abort"),
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
			mode = "download"
			// Use buttons: Choose Folder (OK), Current Directory (Cancel/Close)
			err := zenity.Question("Where would you like to save the files?",
				zenity.Title("Torrent Class - Download"),
				zenity.OKLabel("Choose Folder"),
				zenity.CancelLabel("Current Directory"),
			)

			if err == nil { // "Choose Folder" clicked
				res, err := zenity.SelectFile(
					zenity.Title("Select destination folder"),
					zenity.Directory(),
				)
				if err == nil {
					path = res
				}
			}
			// If ErrCanceled (button or X), path remains "." which is correct
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
				relaunchInTerminal(term, mode, path, ipOverride, port, httpPort)
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

		// Start HTTP server immediately for binary distribution
		exePath, _ := os.Executable()
		exeDir := filepath.Dir(exePath)
		mux := http.NewServeMux()
		mux.Handle("/", http.FileServer(http.Dir(exeDir)))
		go func() {
			addr := fmt.Sprintf(":%d", httpPort)
			if err := http.ListenAndServe(addr, mux); err != nil {
				log.Printf("HTTP server error: %v", err)
			}
		}()
	}

	m := tui.Model{
		Mode:       mode,
		IP:         localIP,
		Port:       port,
		Magnet:     actualMagnet,
		HTTPAddr:   httpAddr,
		IsHashing:  mode == "seed",
		Progress:   progress.New(progress.WithDefaultGradient()),
		Interfaces: interfaces,
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
			p.Send(tui.TorrentLoadedMsg{
				Torrent: t,
				Magnet:  magnetLink,
			})

			// Start broadcasting
			broadcaster := discovery.NewBroadcaster(magnetLink, port)
			go broadcaster.Start()
		} else if mode == "download" {
			if actualMagnet == "" {
				listener := discovery.NewListener()
				go listener.Listen()
				info := <-listener.Foundchan
				actualMagnet = info.Magnet

				t, err = eng.AddTorrentByMagnet(actualMagnet)
				if err != nil {
					log.Printf("Failed to add magnet: %v", err)
					return
				}

				// Add the seeder as a peer immediately
				eng.AddPeer(t, info.IP, info.Port)

				p.Send(tui.TorrentLoadedMsg{
					Torrent: t,
					Magnet:  actualMagnet,
				})

				// Viral Seeding: Start broadcasting once we have the magnet link
				broadcaster := discovery.NewBroadcaster(actualMagnet, port)
				go broadcaster.Start()
			} else {
				t, err = eng.AddTorrentByMagnet(actualMagnet)
				if err != nil {
					log.Printf("Failed to add magnet: %v", err)
					return
				}

				p.Send(tui.TorrentLoadedMsg{
					Torrent: t,
					Magnet:  actualMagnet,
				})

				// Viral Seeding: Start broadcasting the magnet link we provided
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

func relaunchInTerminal(terminalPath string, mode, path, ip string, port, httpPort int) {
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
		"--port", fmt.Sprintf("%d", port),
		"--http-port", fmt.Sprintf("%d", httpPort),
	}
	if ip != "" {
		args = append(args, "--ip", ip)
	}

	var cmdArgs []string
	switch base {
	case "gnome-terminal":
		cmdArgs = append(cmdArgs, "--", exe)
		cmdArgs = append(cmdArgs, args...)
	case "konsole":
		cmdArgs = append(cmdArgs, "-e", exe)
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
