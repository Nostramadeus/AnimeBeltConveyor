//go:build windows

package sched

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// OpenAt plays file startSec seconds in when ABC knows the player's start flag
// (StartArgs): the player is playerPath, or the program Windows has as the
// default for the file's extension. Otherwise, or when that launch fails, the
// file opens exactly like Open. player is the program name used, skipped
// whether the start flag was passed.
func (l osLauncher) OpenAt(playerPath, file string, startSec int) (<-chan struct{}, string, bool, error) {
	exe := playerPath
	if exe == "" {
		exe = defaultHandlerExe(filepath.Ext(file))
	}
	if startSec > 0 && exe != "" {
		if args, ok := startCommand(exe, file, startSec); ok {
			cmd := exec.Command(exe, args...)
			cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
			if err := cmd.Start(); err == nil {
				done := make(chan struct{})
				go func() { cmd.Wait(); close(done) }()
				return done, PlayerName(exe), true, nil
			}
		}
	}
	done, err := l.Open(playerPath, file)
	return done, PlayerName(exe), false, err
}

func (l osLauncher) OpenPlayback(playerPath, file string, start int, options SubtitleOptions) (<-chan struct{}, string, bool, bool, string, error) {
	exe := playerPath
	if exe == "" {
		exe = defaultHandlerExe(filepath.Ext(file))
	}
	if exe == "" || !options.Requested && !smplayerName(exe) {
		done, player, skipped, err := l.OpenAt(playerPath, file, start)
		return done, player, skipped, false, "", err
	}
	command := playbackArgs(exe, file, start, options)
	cmd := exec.Command(exe, command.args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	var initialForeground uint32
	if smplayerName(exe) {
		initialForeground = foregroundProcess()
	}
	if err := cmd.Start(); err != nil {
		command.cleanup()
		return nil, PlayerName(exe), false, false, command.note, err
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); command.cleanup(); close(done) }()
	if smplayerName(exe) && initialForeground != 0 {
		go foregroundSMPlayer(uint32(cmd.Process.Pid), initialForeground, done, nativeForegroundOperations())
	}
	return done, PlayerName(exe), command.skipped, command.subtitles, command.note, nil
}

// defaultHandlerExe is the program Windows opens files with extension ext
// (".mkv") with: the user's choice under Explorer\FileExts, else the class
// registered for the extension; "" when none or unreadable.
func defaultHandlerExe(ext string) string {
	ext = strings.ToLower(ext)
	if ext == "" {
		return ""
	}
	progID := ""
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\`+ext+`\UserChoice`, registry.QUERY_VALUE); err == nil {
		progID, _, _ = k.GetStringValue("ProgId")
		k.Close()
	}
	if progID == "" {
		if k, err := registry.OpenKey(registry.CLASSES_ROOT, ext, registry.QUERY_VALUE); err == nil {
			progID, _, _ = k.GetStringValue("")
			k.Close()
		}
	}
	if progID == "" {
		return ""
	}
	k, err := registry.OpenKey(registry.CLASSES_ROOT, progID+`\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	cmd, _, err := k.GetStringValue("")
	if err != nil {
		return ""
	}
	return commandExe(cmd)
}

// commandExe extracts the program from a shell command line such as
// `"C:\Program Files\SMPlayer\smplayer.exe" "%1"` or `C:\x\mpv.exe --player-operation-mode=pseudo-gui -- "%1"`.
func commandExe(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	if cmd[0] == '"' {
		if i := strings.IndexByte(cmd[1:], '"'); i >= 0 {
			return cmd[1 : i+1]
		}
		return strings.Trim(cmd, `"`)
	}
	// Unquoted: the program ends at the first space after ".exe" (paths with spaces are normally quoted).
	low := strings.ToLower(cmd)
	if i := strings.Index(low, ".exe"); i >= 0 {
		return cmd[:i+4]
	}
	if i := strings.IndexByte(cmd, ' '); i >= 0 {
		return cmd[:i]
	}
	return cmd
}
