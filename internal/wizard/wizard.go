package wizard

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ncruces/zenity"
	"torrent-class/internal/netutils"
	"torrent-class/internal/winmgr"
)

func IsValidIPv4(ip string) bool {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}
	return parsedIP.To4() != nil && !strings.Contains(ip, ":")
}

// ResolveInteractivePath avoids dumping downloads and .torrent.bolt.db into $HOME
// when the user picks "Current Directory" after double-clicking the binary in
// macOS Finder (which launches Terminal with cwd=$HOME) or a Linux launcher.
func ResolveInteractivePath(path string) string {
	if path != "." {
		return path
	}
	exePath, err := os.Executable()
	if err != nil {
		return path
	}
	exeDir := filepath.Dir(exePath)

	if runtime.GOOS == "darwin" || os.Getenv("TORRENT_CLASS_RELAUNCHED") == "1" {
		cwd, err := os.Getwd()
		home, herr := os.UserHomeDir()
		if err == nil && herr == nil && cwd == home && exeDir != home {
			return exeDir
		}
	}
	return path
}

func withDialogOpts(opts ...zenity.Option) []zenity.Option {
	return append(opts, winmgr.DialogOptions()...)
}

func RunInteractiveSetup() (mode, path, ipOverride, seederIP string, err error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	winmgr.StartDialogPromoter(ctx)

	mode = "download"

	selectedMode, err := zenity.List(
		"Select operation mode:",
		[]string{"Download (Receive files)", "Seed (Share a file/folder)"},
		withDialogOpts(
			zenity.Title("Torrent Class"),
			zenity.DefaultItems("Download (Receive files)"),
		)...,
	)
	if err != nil {
		if err == zenity.ErrCanceled {
			os.Exit(0)
		}
		log.Fatalf("Error selecting mode: %v", err)
	}

	if selectedMode == "Seed (Share a file/folder)" {
		mode = "seed"
		err := zenity.Question("What would you like to share?",
			withDialogOpts(
				zenity.Title("Torrent Class - Seeding"),
				zenity.OKLabel("          Folder          "),
				zenity.ExtraButton("          File          "),
				zenity.CancelLabel("          Abort          "),
			)...,
		)

		if err == nil {
			res, err := zenity.SelectFile(
				withDialogOpts(
					zenity.Title("Select folder to seed"),
					zenity.Directory(),
				)...,
			)
			if err != nil {
				if err == zenity.ErrCanceled {
					os.Exit(0)
				}
				log.Fatalf("Error selecting folder: %v", err)
			}
			path = res
		} else if err == zenity.ErrExtraButton {
			res, err := zenity.SelectFile(
				withDialogOpts(
					zenity.Title("Select file to seed"),
				)...,
			)
			if err != nil {
				if err == zenity.ErrCanceled {
					os.Exit(0)
				}
				log.Fatalf("Error selecting file: %v", err)
			}
			path = res
		} else {
			os.Exit(0)
		}
	} else {
		path = "."
		err := zenity.Question("Where would you like to save the files?",
			withDialogOpts(
				zenity.Title("Torrent Class - Download"),
				zenity.OKLabel("          Choose Folder          "),
				zenity.CancelLabel("          Current Directory          "),
			)...,
		)
		if err == nil {
			res, err := zenity.SelectFile(withDialogOpts(zenity.Title("Select destination folder"), zenity.Directory())...)
			if err == nil && res != "" {
				path = res
			}
		}
		path = ResolveInteractivePath(path)

		discoveryMode, err := zenity.List(
			"Choose discovery mode:",
			[]string{"Automatic (UDP Discovery)", "Manual (Enter Seeder IP)"},
			withDialogOpts(
				zenity.Title("Torrent Class - Connectivity"),
				zenity.DefaultItems("Automatic (UDP Discovery)"),
			)...,
		)
		if err == nil && discoveryMode == "Manual (Enter Seeder IP)" {
			for {
				res, err := zenity.Entry("Enter the Instructor/Seeder IP address:",
					withDialogOpts(
						zenity.Title("Manual Connection"),
						zenity.EntryText("192.168.x.x"),
						zenity.Width(400),
					)...,
				)
				if err != nil {
					break
				}
				if IsValidIPv4(res) {
					seederIP = res
					break
				}
				zenity.Error("Invalid IPv4 address. Please enter a valid address (e.g. 192.168.1.10) without port.",
					withDialogOpts(zenity.Title("Invalid Input"))...)
			}
		}
	}

	if ipOverride == "" {
		ifaces, _ := netutils.GetValidInterfaces()
		if len(ifaces) > 1 {
			var options []string
			for _, iface := range ifaces {
				options = append(options, fmt.Sprintf("%-15s (%s)", iface.IP, iface.Name))
			}

			selected, err := zenity.List(
				"Multiple network adapters found. Select one to use:",
				options,
				withDialogOpts(
					zenity.Title("Torrent Class - Network Adapter"),
					zenity.DefaultItems(options[0]),
				)...,
			)
			if err == nil {
				parts := strings.Fields(selected)
				if len(parts) > 0 {
					ipOverride = parts[0]
				}
			}
		}
	}

	return mode, path, ipOverride, seederIP, nil
}
