//go:build darwin

package winmgr

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/ncruces/zenity"
)

// PrepareTerminal centers the frontmost Terminal.app or iTerm2 window on the
// main screen before opening GUI setup dialogs.
func PrepareTerminal() {
	termProg := os.Getenv("TERM_PROGRAM")
	var script string

	switch termProg {
	case "Apple_Terminal":
		script = `
ObjC.import('AppKit');
const f = $.NSScreen.mainScreen.visibleFrame;
const h = $.NSScreen.screens.objectAtIndex(0).frame.size.height;
const t = Application('Terminal');
if (t.running() && t.windows.length > 0) {
  const w = t.windows[0];
  const b = w.bounds();
  const x = Math.round(f.origin.x + (f.size.width - b.width) / 2);
  const topY = h - (f.origin.y + f.size.height);
  const y = Math.round(topY + (f.size.height - b.height) / 2);
  w.bounds = {x: x, y: y, width: b.width, height: b.height};
}`
	case "iTerm.app":
		script = `
ObjC.import('AppKit');
const f = $.NSScreen.mainScreen.visibleFrame;
const h = $.NSScreen.screens.objectAtIndex(0).frame.size.height;
const t = Application('iTerm2');
if (t.running() && t.windows.length > 0) {
  const w = t.currentWindow();
  const b = w.bounds();
  const x = Math.round(f.origin.x + (f.size.width - b.width) / 2);
  const topY = h - (f.origin.y + f.size.height);
  const y = Math.round(topY + (f.size.height - b.height) / 2);
  w.bounds = {x: x, y: y, width: b.width, height: b.height};
}`
	default:
		return
	}

	cmd := exec.Command("osascript", "-l", "JavaScript")
	cmd.Stdin = strings.NewReader(script)
	_ = cmd.Run()
}

// DialogOptions attaches Zenity's AppleScript dialogs to the active terminal
// (or System Events) so macOS presents the dialog centered and in front of the terminal.
func DialogOptions() []zenity.Option {
	switch os.Getenv("TERM_PROGRAM") {
	case "Apple_Terminal":
		return []zenity.Option{zenity.Attach("Terminal")}
	case "iTerm.app":
		return []zenity.Option{zenity.Attach("iTerm2")}
	default:
		return []zenity.Option{zenity.Attach("System Events")}
	}
}

// StartDialogPromoter is a no-op on macOS; Attach() activates the host app in front.
func StartDialogPromoter(_ context.Context) {}
