//go:build windows

package winmgr

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/ncruces/zenity"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")

	procGetConsoleWindow         = kernel32.NewProc("GetConsoleWindow")
	procGetCurrentThreadId       = kernel32.NewProc("GetCurrentThreadId")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procBringWindowToTop         = user32.NewProc("BringWindowToTop")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsZoomed                 = user32.NewProc("IsZoomed")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procGetWindowLongW           = user32.NewProc("GetWindowLongW")
	procGetParent                = user32.NewProc("GetParent")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procAttachThreadInput        = user32.NewProc("AttachThreadInput")
	procMonitorFromWindow        = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW          = user32.NewProc("GetMonitorInfoW")
	procFindWindowExW            = user32.NewProc("FindWindowExW")
	procSetFocus                 = user32.NewProc("SetFocus")
)

const (
	swpNoSize          = 0x0001
	swpNoZOrder        = 0x0004
	swpNoActivate      = 0x0010
	swpShowWindow      = 0x0040
	hwndTopMost        = ^uintptr(0) // (HWND)-1
	gwlStyle           = ^uintptr(15) // -16
	wsCaption          = 0x00C00000
	monitorDefaultNear = 0x00000002
)

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type monitorInfo struct {
	CbSize    uint32
	RcMonitor rect
	RcWork    rect
	DwFlags   uint32
}

var savedTerminalHWND uintptr

// PrepareTerminal locates the active console or Windows Terminal window and
// centers it in the middle of the current monitor before GUI dialogs open.
func PrepareTerminal() {
	hwnd := findTerminalWindow(500 * time.Millisecond)
	if hwnd == 0 {
		return
	}
	atomic.StoreUintptr(&savedTerminalHWND, hwnd)

	// Do not un-maximize a maximized terminal window.
	if zoomed, _, _ := procIsZoomed.Call(hwnd); zoomed != 0 {
		return
	}

	centerWindowOnMonitor(hwnd, hwnd, swpNoSize|swpNoZOrder|swpNoActivate)
	procSetForegroundWindow.Call(hwnd)
}

// DialogOptions returns platform-specific Zenity options.
// On Windows, StartDialogPromoter handles centering and HWND_TOPMOST directly.
func DialogOptions() []zenity.Option {
	return nil
}

// StartDialogPromoter watches for newly opened Zenity dialog windows belonging
// to the current process, centers them on the terminal's monitor, and places
// them in front of the terminal window (HWND_TOPMOST + foreground focus).
func StartDialogPromoter(ctx context.Context) {
	myPID := uint32(os.Getpid())
	consoleHWND, _, _ := procGetConsoleWindow.Call()

	go func() {
		seen := make(map[uintptr]bool)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var candidates []uintptr
				cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
					if hwnd == consoleHWND {
						return 1
					}
					if vis, _, _ := procIsWindowVisible.Call(hwnd); vis == 0 {
						return 1
					}
					var pid uint32
					procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
					if pid != myPID {
						return 1
					}
					if parent, _, _ := procGetParent.Call(hwnd); parent != 0 {
						return 1
					}
					style, _, _ := procGetWindowLongW.Call(hwnd, gwlStyle)
					if style&wsCaption == 0 {
						return 1
					}
					cls := getWindowClass(hwnd)
					if cls == "#32770" || strings.HasPrefix(cls, "WC_") {
						candidates = append(candidates, hwnd)
					}
					return 1
				})
				procEnumWindows.Call(cb, 0)

				for _, hwnd := range candidates {
					if seen[hwnd] {
						continue
					}
					seen[hwnd] = true
					promoteAndCenterDialog(hwnd)
				}
			}
		}
	}()
}

func findTerminalWindow(maxWait time.Duration) uintptr {
	// 1. Classic conhost.exe window
	consoleHWND, _, _ := procGetConsoleWindow.Call()
	if consoleHWND != 0 {
		if vis, _, _ := procIsWindowVisible.Call(consoleHWND); vis != 0 {
			return consoleHWND
		}
	}

	// 2. Windows 11 Windows Terminal (ConPTY handoff creates CASCADIA_HOSTING_WINDOW_CLASS asynchronously)
	deadline := time.Now().Add(maxWait)
	waited := false
	for {
		fg, _, _ := procGetForegroundWindow.Call()
		if fg != 0 {
			if vis, _, _ := procIsWindowVisible.Call(fg); vis != 0 {
				cls := getWindowClass(fg)
				if cls == "CASCADIA_HOSTING_WINDOW_CLASS" || cls == "ConsoleWindowClass" {
					if waited {
						// Allow newly spawned Windows Terminal to finish initial placement
						time.Sleep(60 * time.Millisecond)
					}
					return fg
				}
			}
		}
		if time.Now().After(deadline) {
			break
		}
		waited = true
		time.Sleep(25 * time.Millisecond)
	}
	return 0
}

func promoteAndCenterDialog(dlgHWND uintptr) {
	refHWND := atomic.LoadUintptr(&savedTerminalHWND)
	if refHWND == 0 {
		refHWND = dlgHWND
	}

	// Place dialog centered on the monitor and into the HWND_TOPMOST z-order band
	// so the terminal can never cover it.
	centerWindowOnMonitor(dlgHWND, refHWND, swpNoSize|swpNoActivate|swpShowWindow)
	procSetWindowPos.Call(dlgHWND, hwndTopMost, 0, 0, 0, 0, swpNoSize|0x0002|swpNoActivate|swpShowWindow) // 0x0002 = SWP_NOMOVE

	fg, _, _ := procGetForegroundWindow.Call()
	if fg != dlgHWND {
		var fgPID, dlgPID uint32
		fgTID, _, _ := procGetWindowThreadProcessId.Call(fg, uintptr(unsafe.Pointer(&fgPID)))
		dlgTID, _, _ := procGetWindowThreadProcessId.Call(dlgHWND, uintptr(unsafe.Pointer(&dlgPID)))
		curTID, _, _ := procGetCurrentThreadId.Call()

		if fgTID != 0 && fgTID != curTID {
			procAttachThreadInput.Call(curTID, fgTID, 1)
			defer procAttachThreadInput.Call(curTID, fgTID, 0)
		}
		if dlgTID != 0 && dlgTID != curTID {
			procAttachThreadInput.Call(curTID, dlgTID, 1)
			defer procAttachThreadInput.Call(curTID, dlgTID, 0)
		}

		procBringWindowToTop.Call(dlgHWND)
		procSetForegroundWindow.Call(dlgHWND)

		// Preserve child control keyboard focus on custom Zenity List/Entry windows.
		if strings.HasPrefix(getWindowClass(dlgHWND), "WC_") {
			for _, childClass := range []string{"LISTBOX", "EDIT"} {
				clsPtr, _ := syscall.UTF16PtrFromString(childClass)
				if child, _, _ := procFindWindowExW.Call(dlgHWND, 0, uintptr(unsafe.Pointer(clsPtr)), 0); child != 0 {
					procSetFocus.Call(child)
					break
				}
			}
		}
	}
}

func centerWindowOnMonitor(targetHWND, monitorRefHWND uintptr, flags uintptr) {
	var r rect
	if ok, _, _ := procGetWindowRect.Call(targetHWND, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return
	}
	winW := r.Right - r.Left
	winH := r.Bottom - r.Top
	if winW <= 0 || winH <= 0 {
		return
	}

	mon, _, _ := procMonitorFromWindow.Call(monitorRefHWND, monitorDefaultNear)
	if mon == 0 {
		return
	}

	var mi monitorInfo
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if ok, _, _ := procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); ok == 0 {
		return
	}

	workW := mi.RcWork.Right - mi.RcWork.Left
	workH := mi.RcWork.Bottom - mi.RcWork.Top
	x := mi.RcWork.Left + (workW-winW)/2
	y := mi.RcWork.Top + (workH-winH)/2

	procSetWindowPos.Call(targetHWND, 0, uintptr(int64(x)), uintptr(int64(y)), 0, 0, flags)
}

func getWindowClass(hwnd uintptr) string {
	var buf [256]uint16
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}
