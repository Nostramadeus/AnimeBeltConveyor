//go:build windows

package player

import (
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const available = true

var (
	user32                   = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows          = user32.NewProc("EnumWindows")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	procIsWindowVisible      = user32.NewProc("IsWindowVisible")

	enumMu     sync.Mutex
	enumTitles []string
	// One callback for the process lifetime: Windows callbacks created with
	// NewCallback are never freed and the runtime caps how many may exist.
	enumCallback = windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		n, _, _ := procGetWindowTextLengthW.Call(hwnd)
		if n == 0 {
			return 1
		}
		buf := make([]uint16, n+1)
		got, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), n+1)
		if got > 0 {
			enumTitles = append(enumTitles, syscall.UTF16ToString(buf[:got]))
		}
		return 1 // continue enumeration
	})
)

// windowTitles lists the titles of visible top-level windows.
func windowTitles() []string {
	if err := user32.Load(); err != nil {
		return nil
	}
	enumMu.Lock()
	defer enumMu.Unlock()
	enumTitles = nil
	procEnumWindows.Call(enumCallback, 0)
	out := enumTitles
	enumTitles = nil
	return out
}
