//go:build windows

package sched

import (
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type foregroundOperations struct {
	now        func() time.Time
	wait       func(<-chan struct{}, time.Duration) bool
	foreground func() uint32
	windows    func() []uintptr
	process    func(uintptr) uint32
	visible    func(uintptr) bool
	owned      func(uintptr) bool
	minimized  func(uintptr) bool
	restore    func(uintptr)
	activate   func(uintptr) bool
	flash      func(uintptr)
}

var (
	foregroundUser32   = windows.NewLazySystemDLL("user32.dll")
	foregroundGetOwner = foregroundUser32.NewProc("GetWindow")
	foregroundIsIconic = foregroundUser32.NewProc("IsIconic")
	foregroundRestore  = foregroundUser32.NewProc("ShowWindowAsync")
	foregroundActivate = foregroundUser32.NewProc("SetForegroundWindow")
	foregroundFlash    = foregroundUser32.NewProc("FlashWindowEx")
	foregroundEnumMu   sync.Mutex
	foregroundHandles  []uintptr
	foregroundCallback = windows.NewCallback(func(handle uintptr, _ uintptr) uintptr {
		foregroundHandles = append(foregroundHandles, handle)
		return 1
	})
)

func foregroundWindowProcess(handle uintptr) uint32 {
	var process uint32
	if _, err := windows.GetWindowThreadProcessId(windows.HWND(handle), &process); err != nil {
		return 0
	}
	return process
}

func foregroundProcess() uint32 {
	return foregroundWindowProcess(uintptr(windows.GetForegroundWindow()))
}

func nativeForegroundOperations() foregroundOperations {
	return foregroundOperations{
		now: time.Now,
		wait: func(done <-chan struct{}, interval time.Duration) bool {
			timer := time.NewTimer(interval)
			defer timer.Stop()
			select {
			case <-done:
				return false
			case <-timer.C:
				return true
			}
		},
		foreground: foregroundProcess,
		windows: func() []uintptr {
			foregroundEnumMu.Lock()
			defer foregroundEnumMu.Unlock()
			foregroundHandles = nil
			windows.EnumWindows(foregroundCallback, nil)
			handles := foregroundHandles
			foregroundHandles = nil
			return handles
		},
		process:   foregroundWindowProcess,
		visible:   func(handle uintptr) bool { return windows.IsWindowVisible(windows.HWND(handle)) },
		owned:     func(handle uintptr) bool { owner, _, _ := foregroundGetOwner.Call(handle, 4); return owner != 0 },
		minimized: func(handle uintptr) bool { iconic, _, _ := foregroundIsIconic.Call(handle); return iconic != 0 },
		restore:   func(handle uintptr) { foregroundRestore.Call(handle, 9) },
		activate:  func(handle uintptr) bool { activated, _, _ := foregroundActivate.Call(handle); return activated != 0 },
		flash: func(handle uintptr) {
			info := struct {
				Size    uint32
				Window  uintptr
				Flags   uint32
				Count   uint32
				Timeout uint32
			}{Window: handle, Flags: 2, Count: 3}
			info.Size = uint32(unsafe.Sizeof(info))
			foregroundFlash.Call(uintptr(unsafe.Pointer(&info)))
		},
	}
}

func foregroundSMPlayer(process, initial uint32, done <-chan struct{}, ops foregroundOperations) {
	if process == 0 || initial == 0 {
		return
	}
	deadline := ops.now().Add(3 * time.Second)
	var target uintptr
	restored := false
	exited := func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	valid := func(handle uintptr) bool {
		return ops.process(handle) == process && ops.visible(handle) && !ops.owned(handle)
	}
	for ops.now().Before(deadline) && !exited() {
		current := ops.foreground()
		if current == process || current != 0 && current != initial {
			return
		}
		if current != 0 {
			if target == 0 {
				for _, handle := range ops.windows() {
					if valid(handle) {
						target = handle
						break
					}
				}
			}
			if target != 0 {
				if !valid(target) || exited() {
					return
				}
				current = ops.foreground()
				if current == process || current != 0 && current != initial {
					return
				}
				if current != 0 {
					if !restored && ops.minimized(target) {
						ops.restore(target)
						restored = true
					}
					current = ops.foreground()
					if current == process || current != 0 && current != initial {
						return
					}
					if current != 0 {
						if !valid(target) || exited() {
							return
						}
						if !ops.activate(target) && !exited() && valid(target) {
							current = ops.foreground()
							if current == initial {
								ops.flash(target)
							}
						}
						return
					}
				}
			}
		}
		if !ops.wait(done, 50*time.Millisecond) {
			return
		}
	}
}
