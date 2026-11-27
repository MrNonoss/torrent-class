//go:build !windows && !darwin

package winmgr

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/ncruces/zenity"
)

// PrepareTerminal centers the active X11 terminal window on screen when xdotool
// and xrandr/xdpyinfo are available. On Wayland, compositors manage placement automatically.
func PrepareTerminal() {
	if os.Getenv("DISPLAY") == "" {
		return
	}
	xdotool, err := exec.LookPath("xdotool")
	if err != nil {
		return
	}

	winBytes, err := exec.Command(xdotool, "getactivewindow").Output()
	if err != nil {
		return
	}
	winID := strings.TrimSpace(string(winBytes))
	if winID == "" {
		return
	}

	geomBytes, err := exec.Command(xdotool, "getwindowgeometry", "--shell", winID).Output()
	if err != nil {
		return
	}
	var winW, winH int
	for _, line := range strings.Split(string(geomBytes), "\n") {
		if v, ok := strings.CutPrefix(line, "WIDTH="); ok {
			winW, _ = strconv.Atoi(strings.TrimSpace(v))
		} else if v, ok := strings.CutPrefix(line, "HEIGHT="); ok {
			winH, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	if winW <= 0 || winH <= 0 {
		return
	}

	dispBytes, err := exec.Command(xdotool, "getdisplaygeometry").Output()
	if err != nil {
		return
	}
	var screenW, screenH int
	if _, err := fmt.Fscan(bytes.NewReader(dispBytes), &screenW, &screenH); err != nil || screenW <= 0 || screenH <= 0 {
		return
	}

	x := (screenW - winW) / 2
	y := (screenH - winH) / 2
	_ = exec.Command(xdotool, "windowmove", winID, strconv.Itoa(x), strconv.Itoa(y)).Run()
}

// DialogOptions passes --modal (and --attach when WINDOWID is set) so X11/Wayland
// window managers center the Zenity dialog over the terminal and keep it in front.
func DialogOptions() []zenity.Option {
	opts := []zenity.Option{zenity.Modal()}
	if widStr := os.Getenv("WINDOWID"); widStr != "" {
		if wid, err := strconv.Atoi(widStr); err == nil && wid > 0 {
			opts = append(opts, zenity.Attach(wid))
		}
	}
	return opts
}

// StartDialogPromoter is a no-op on Unix; --modal/--attach handles z-order via the WM.
func StartDialogPromoter(_ context.Context) {}
