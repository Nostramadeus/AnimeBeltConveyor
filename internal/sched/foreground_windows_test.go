//go:build windows

package sched

import (
	"testing"
	"time"
)

type foregroundFixture struct {
	clock        time.Time
	done         chan struct{}
	polls        int
	foreground   uint32
	handles      []uintptr
	pids         map[uintptr]uint32
	hidden       map[uintptr]bool
	owners       map[uintptr]bool
	minimized    map[uintptr]bool
	restores     []uintptr
	activations  []uintptr
	flashes      []uintptr
	denied       bool
	onWait       func()
	onProcess    func(uintptr)
	onForeground func()
	onRestore    func()
}

func newForegroundFixture() *foregroundFixture {
	return &foregroundFixture{clock: time.Unix(1, 0), done: make(chan struct{}), foreground: 1, handles: []uintptr{20}, pids: map[uintptr]uint32{20: 2}, hidden: map[uintptr]bool{}, owners: map[uintptr]bool{}, minimized: map[uintptr]bool{}}
}

func (fixture *foregroundFixture) operations() foregroundOperations {
	return foregroundOperations{
		now: func() time.Time { return fixture.clock },
		wait: func(done <-chan struct{}, interval time.Duration) bool {
			if interval != 50*time.Millisecond {
				panic("unexpected poll interval")
			}
			fixture.clock = fixture.clock.Add(interval)
			fixture.polls++
			if fixture.onWait != nil {
				fixture.onWait()
			}
			select {
			case <-done:
				return false
			default:
				return true
			}
		},
		foreground: func() uint32 {
			if fixture.onForeground != nil {
				fixture.onForeground()
			}
			return fixture.foreground
		},
		windows: func() []uintptr { return fixture.handles },
		process: func(handle uintptr) uint32 {
			if fixture.onProcess != nil {
				fixture.onProcess(handle)
			}
			return fixture.pids[handle]
		},
		visible:   func(handle uintptr) bool { return !fixture.hidden[handle] },
		owned:     func(handle uintptr) bool { return fixture.owners[handle] },
		minimized: func(handle uintptr) bool { return fixture.minimized[handle] },
		restore: func(handle uintptr) {
			fixture.restores = append(fixture.restores, handle)
			if fixture.onRestore != nil {
				fixture.onRestore()
			}
		},
		activate: func(handle uintptr) bool {
			fixture.activations = append(fixture.activations, handle)
			return !fixture.denied
		},
		flash: func(handle uintptr) { fixture.flashes = append(fixture.flashes, handle) },
	}
}

func TestSMPlayerForegroundSelection(t *testing.T) {
	for _, scenario := range []string{"normal", "minimized", "delayed", "filter", "denied", "switched", "already", "exited", "deadline", "unknown", "identity", "switch_restore"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newForegroundFixture()
			wantActivate, wantRestore, wantFlash := 1, 0, 0
			switch scenario {
			case "minimized":
				fixture.minimized[20] = true
				wantRestore = 1
			case "delayed":
				fixture.handles = nil
				fixture.onWait = func() {
					if fixture.polls == 3 {
						fixture.handles = []uintptr{20}
					}
				}
			case "filter":
				fixture.handles = []uintptr{10, 11, 12, 20}
				fixture.pids[10] = 9
				fixture.pids[11] = 2
				fixture.pids[12] = 2
				fixture.hidden[11] = true
				fixture.owners[12] = true
			case "denied":
				fixture.denied = true
				wantFlash = 1
			case "switched":
				fixture.handles = nil
				fixture.onWait = func() { fixture.foreground = 9 }
				wantActivate = 0
			case "already":
				fixture.foreground = 2
				wantActivate = 0
			case "exited":
				close(fixture.done)
				wantActivate = 0
			case "deadline":
				fixture.handles = nil
				wantActivate = 0
			case "unknown":
				fixture.foreground = 0
				fixture.onWait = func() {
					if fixture.polls == 2 {
						fixture.foreground = 1
					}
				}
			case "identity":
				calls := 0
				fixture.onProcess = func(handle uintptr) {
					calls++
					if calls == 2 {
						fixture.pids[handle] = 9
					}
				}
				wantActivate = 0
			case "switch_restore":
				fixture.minimized[20] = true
				fixture.onRestore = func() { fixture.foreground = 9 }
				wantActivate = 0
				wantRestore = 1
			}
			foregroundSMPlayer(2, 1, fixture.done, fixture.operations())
			if len(fixture.activations) != wantActivate || len(fixture.restores) != wantRestore || len(fixture.flashes) != wantFlash {
				t.Fatalf("activate=%v restore=%v flash=%v", fixture.activations, fixture.restores, fixture.flashes)
			}
			if fixture.polls > 60 {
				t.Fatal("deadline exceeded", fixture.polls)
			}
			for _, handle := range append(append(fixture.activations, fixture.restores...), fixture.flashes...) {
				if handle != 20 {
					t.Fatal("acted on unrelated window", handle)
				}
			}
		})
	}
}

func TestSMPlayerForegroundCancellation(t *testing.T) {
	for _, scenario := range []string{"initial_unknown", "process_unknown", "exit_wait", "switch_before_activate", "switch_after_denial", "unknown_after_restore"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newForegroundFixture()
			initial := uint32(1)
			process := uint32(2)
			wantActivate := 0
			switch scenario {
			case "initial_unknown":
				initial = 0
			case "process_unknown":
				process = 0
			case "exit_wait":
				fixture.handles = nil
				fixture.onWait = func() { close(fixture.done) }
			case "switch_before_activate":
				calls := 0
				fixture.onForeground = func() {
					calls++
					if calls == 3 {
						fixture.foreground = 9
					}
				}
			case "switch_after_denial":
				fixture.denied = true
				calls := 0
				fixture.onForeground = func() {
					calls++
					if calls == 4 {
						fixture.foreground = 9
					}
				}
				wantActivate = 1
			case "unknown_after_restore":
				fixture.minimized[20] = true
				fixture.onRestore = func() { fixture.foreground = 0 }
				fixture.onWait = func() { fixture.foreground = 9 }
			}
			foregroundSMPlayer(process, initial, fixture.done, fixture.operations())
			if len(fixture.activations) != wantActivate || len(fixture.flashes) != 0 {
				t.Fatal(fixture.activations, fixture.flashes)
			}
		})
	}
}
