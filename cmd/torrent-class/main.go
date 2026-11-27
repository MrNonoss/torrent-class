package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/mattn/go-isatty"

	"torrent-class/internal/app"
	"torrent-class/internal/winmgr"
	"torrent-class/internal/wizard"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "2.6.0"

func main() {
	var mode, path, ipOverride, seederIP string
	var port, httpPort, maxConns int
	var showVersion bool

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

	flag.BoolVar(&showVersion, "version", false, "Print version and exit")
	flag.BoolVar(&showVersion, "v", false, "Short for --version")

	flag.Parse()

	if showVersion {
		fmt.Printf("torrent-class v%s\n", version)
		return
	}

	if runtime.GOOS == "linux" && os.Getenv("TORRENT_CLASS_RELAUNCHED") == "" {
		if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
			if term := findTerminal(); term != "" {
				relaunchInTerminal(term, os.Args[1:])
				return
			}
		}
	}

	if flag.NFlag() == 0 {
		winmgr.PrepareTerminal()
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

	err := app.Run(app.Config{
		Version:  version,
		Mode:     mode,
		Path:     path,
		IP:       ipOverride,
		SeederIP: seederIP,
		BitPort:  port,
		HTTPPort: httpPort,
		MaxConns: maxConns,
	})
	if err != nil {
		log.Fatalf("%v", err)
	}

	if os.Getenv("TORRENT_CLASS_RELAUNCHED") == "1" {
		fmt.Println("\nPress Enter to exit...")
		var b [1]byte
		_, _ = os.Stdin.Read(b[:])
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
		if p, err := exec.LookPath(t); err == nil {
			return p
		}
	}
	return ""
}

func relaunchInTerminal(terminalPath string, args []string) {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}

	_ = os.Setenv("TORRENT_CLASS_RELAUNCHED", "1")
	base := filepath.Base(terminalPath)

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
