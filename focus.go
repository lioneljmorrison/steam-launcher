package main

import (
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procIsIconic            = user32.NewProc("IsIconic")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procKeybdEvent          = user32.NewProc("keybd_event")
)

const (
	focusWait   = 90 * time.Second // how long a game may take to open its window
	focusSettle = 10 * time.Second // keep it in front through splash screens
)

// bringToFront waits for a window from a game installed in installDir and
// brings it to the front. Windows only lets the app you're using change the
// foreground window, so games started by another launcher (Epic) or by this
// tray app open behind whatever is on screen.
func bringToFront(installDir string) {
	if installDir == "" {
		return
	}
	prefix := strings.ToLower(strings.TrimRight(installDir, `\`) + `\`)
	go func() {
		var focused time.Time
		for start := time.Now(); time.Since(start) < focusWait; time.Sleep(time.Second) {
			pids := processesUnder(prefix)
			hwnd := mainWindow(pids)
			if hwnd == 0 {
				continue
			}
			if focused.IsZero() {
				focused = time.Now()
			} else if time.Since(focused) > focusSettle {
				return
			}
			var fg uint32
			windows.GetWindowThreadProcessId(windows.GetForegroundWindow(), &fg)
			if !pids[fg] {
				focusWindow(hwnd)
			}
		}
	}()
}

// processesUnder returns the IDs of running processes whose exe is in dir
// (lower case, ending in a backslash).
func processesUnder(dir string) map[uint32]bool {
	pids := map[uint32]bool{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return pids
	}
	defer windows.CloseHandle(snap)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if err != nil {
			continue
		}
		buf := make([]uint16, windows.MAX_LONG_PATH)
		size := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &size) == nil &&
			strings.HasPrefix(strings.ToLower(windows.UTF16ToString(buf[:size])), dir) {
			pids[entry.ProcessID] = true
		}
		windows.CloseHandle(h)
	}
	return pids
}

// mainWindow finds the largest visible top-level window owned by pids.
func mainWindow(pids map[uint32]bool) windows.HWND {
	if len(pids) == 0 {
		return 0
	}
	var best windows.HWND
	var bestArea int32
	cb := syscall.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		var pid uint32
		windows.GetWindowThreadProcessId(hwnd, &pid)
		if pids[pid] && windows.IsWindowVisible(hwnd) {
			var r windows.Rect
			procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&r)))
			if area := (r.Right - r.Left) * (r.Bottom - r.Top); area > bestArea {
				best, bestArea = hwnd, area
			}
		}
		return 1
	})
	windows.EnumWindows(cb, nil)
	return best
}

// focusWindow restores and activates hwnd. A tapped Alt key counts as user
// input, which is what lets a background app take the foreground.
func focusWindow(hwnd windows.HWND) {
	const vkMenu, keyUp = 0x12, 0x2
	if minimized, _, _ := procIsIconic.Call(uintptr(hwnd)); minimized != 0 {
		procShowWindow.Call(uintptr(hwnd), windows.SW_RESTORE)
	}
	procKeybdEvent.Call(vkMenu, 0, 0, 0)
	procKeybdEvent.Call(vkMenu, 0, keyUp, 0)
	procSetForegroundWindow.Call(uintptr(hwnd))
}
