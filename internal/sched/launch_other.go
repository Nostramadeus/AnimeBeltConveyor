//go:build !windows

package sched

import (
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
)

type osLauncher struct{}

func opener() string {
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}

func (osLauncher) Open(playerPath, file string) (<-chan struct{}, error) {
	if playerPath != "" {
		cmd := exec.Command(playerPath, file)
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		return done, nil
	}
	return nil, startDetached(exec.Command(opener(), file))
}

func (osLauncher) Reveal(file string) error {
	if runtime.GOOS == "darwin" {
		return startDetached(exec.Command("open", "-R", file))
	}
	return startDetached(exec.Command("xdg-open", filepath.Dir(file)))
}

// OpenDir opens a folder in the file manager.
func (osLauncher) OpenDir(dir string) error { return startDetached(exec.Command(opener(), dir)) }

// OpenURL hands a URL (http or magnet:) to the desktop's handler for its scheme.
func (osLauncher) OpenURL(u string) error { return OpenURL(u) }

// OpenURL opens a URL in the default browser (used by main unless --no-browser).
func OpenURL(u string) error { return startDetached(exec.Command(opener(), u)) }

// ErrNoMagnetHandler means no program is registered for magnet: links.
var ErrNoMagnetHandler = errors.New("no app is registered for magnet links")

// osMagnetHandler cannot know the handler outside Windows (xdg-open / open decide).
func osMagnetHandler() (string, error) { return "unknown", nil }

func startDetached(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
