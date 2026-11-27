//go:build windows

package winmgr

import (
	"context"
	"os"
	"strings"
	"sync"
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
	procGetConsoleProcessList    = kernel32.NewProc("GetConsoleProcessList")
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
	swpNoMove          = 0x0002
	swpNoZOrder        = 0x0004
	swpNoActivate      = 0x0010
	swpShowWindow      = 0x0040
	hwndTopMost        = ^uintptr(0)  // (HWND)-1
	hwndNoTopMost      = ^uintptr(1)  // (HWND)-2
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

// Package-level EnumWindows callback and guarded scan state.
// Go's runtime caps syscall.NewCallback at 2000 slots per process and never
// frees them, so the callback must be allocated exactly once.
var (
	enumMu         sync.Mutex
	enumTargetPID  uint32
	enumSkipHWND   uintptr
	enumCandidates []uintptr
	enumWindowsCB  = syscall.NewCallback(enumWindowsProc)
)

func enumWindowsProc(hwnd uintptr, _ uintptr) uintptr {
	if hwnd == enumSkipHWND {
		return 1
	}
	if vis, _, _ := procIsWindowVisible.Call(hwnd); vis == 0 {
		return 1
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != enumTargetPID {
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
		enumCandidates = append(enumCandidates, hwnd)
	}
	return 1
}

// ownsConsole reports whether this process is the sole owner attached to the
// console (i.e. double-clicked in Explorer rather than run inside an existing shell).
func ownsConsole() bool {
	var pids [4]uint32
	count, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return count <= 1
}

// PrepareTerminal locates the active console or Windows Terminal window and
// centers it in the middle of the current monitor before GUI dialogs open,
// provided the program was launched in its own console rather than an existing shell.
func PrepareTerminal() {
	if !ownsConsole() {
		return
	}

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
// On Windows, StartDialogPromoter handles centering and z-order directly.
func DialogOptions() []zenity.Option {
	return nil
}

// StartDialogPromoter watches for newly opened Zenity dialog windows belonging
// to the current process, centers them on the terminal's monitor, and brings
// them in front of the terminal window.
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
				enumMu.Lock()
				enumTargetPID = myPID
				enumSkipHWND = consoleHWND
				enumCandidates = enumCandidates[:0]
				procEnumWindows.Call(enumWindowsCB, 0)
				candidates := append([]uintptr(nil), enumCandidates...)
				enumMu.Unlock()

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

	// Temporarily enter HWND_TOPMOST to jump ahead of the terminal, activate,
	// and immediately drop back to HWND_NOTOPMOST so other apps (e.g. Explorer)
	// aren't covered if the user switches away.
	centerWindowOnMonitor(dlgHWND, refHWND, swpNoSize|swpNoActivate|swpShowWindow)
	procSetWindowPos.Call(dlgHWND, hwndTopMost, 0, 0, 0, 0, swpNoSize|swpNoMove|swpNoActivate|swpShowWindow)

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

	procSetWindowPos.Call(dlgHWND, hwndNoTopMost, 0, 0, 0, 0, swpNoSize|swpNoMove|swpNoActivate)
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
