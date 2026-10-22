//go:build windows

package sched

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// createNoWindow keeps console programs from opening a console window. It does
// not touch GUI windows. TODO(spec): the brief says HideWindow; HideWindow sets
// SW_HIDE, which rundll32's FileProtocolHandler forwards to ShellExecute and can
// start the player invisible, so CREATE_NO_WINDOW is used instead (same goal:
// no console flash).
const createNoWindow = 0x08000000

type osLauncher struct{}

func (osLauncher) Open(playerPath, file string) (<-chan struct{}, error) {
	if playerPath != "" {
		cmd := exec.Command(playerPath, file)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		return done, nil
	}
	return nil, startDetached(exec.Command("rundll32", "url.dll,FileProtocolHandler", file))
}

func (osLauncher) Reveal(file string) error {
	cmd := exec.Command("explorer")
	// explorer wants exactly /select,"path"; Go's own quoting would wrap the whole argument.
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer /select,"` + filepath.Clean(file) + `"`, CreationFlags: createNoWindow}
	return startDetached(cmd)
}

// OpenDir opens a folder in Explorer.
func (osLauncher) OpenDir(dir string) error {
	cmd := exec.Command("explorer")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: "explorer " + explorerArg(dir), CreationFlags: createNoWindow}
	return startDetached(cmd)
}

// explorerArg quotes a folder for explorer's command line. A trailing backslash
// (drive roots like C:\) would escape the closing quote, so such paths get a
// trailing "." inside the quotes instead.
func explorerArg(dir string) string {
	dir = filepath.Clean(dir)
	if strings.HasSuffix(dir, `\`) {
		dir += "."
	}
	return `"` + dir + `"`
}

// OpenURL hands a URL (http or magnet:) to the handler Windows has registered for its scheme.
func (osLauncher) OpenURL(u string) error { return OpenURL(u) }

// OpenURL opens a URL in the default browser (used by main unless --no-browser).
func OpenURL(u string) error {
	return startDetached(exec.Command("rundll32", "url.dll,FileProtocolHandler", u))
}

// ErrNoMagnetHandler means no program is registered for magnet: links.
var ErrNoMagnetHandler = errors.New("no app is registered for magnet links")

// osMagnetHandler reads HKEY_CLASSES_ROOT\magnet\shell\open\command (default value).
func osMagnetHandler() (string, error) {
	k, err := registry.OpenKey(registry.CLASSES_ROOT, `magnet\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return "", ErrNoMagnetHandler
	}
	defer k.Close()
	v, _, err := k.GetStringValue("")
	if err != nil || strings.TrimSpace(v) == "" {
		return "", ErrNoMagnetHandler
	}
	return strings.TrimSpace(v), nil
}

func startDetached(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap; explorer exits 1 even on success
	return nil
}
