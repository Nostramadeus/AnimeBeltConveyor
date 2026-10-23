//go:build windows

package sched

import (
	"strings"
	"testing"
)

func TestCommandExe(t *testing.T) {
	cases := map[string]string{
		`"C:\Program Files\SMPlayer\smplayer.exe" "%1"`:                    `C:\Program Files\SMPlayer\smplayer.exe`,
		`"C:\Program Files\VideoLAN\VLC\vlc.exe" --started-from-file "%1"`: `C:\Program Files\VideoLAN\VLC\vlc.exe`,
		`C:\mpv\mpv.exe --player-operation-mode=pseudo-gui -- "%1"`:        `C:\mpv\mpv.exe`,
		`C:\mpv\mpv.exe`:   `C:\mpv\mpv.exe`,
		`  "C:\x\a.exe"  `: `C:\x\a.exe`,
		``:                 ``,
	}
	for in, want := range cases {
		if got := commandExe(in); got != want {
			t.Errorf("commandExe(%q) = %q, want %q", in, got, want)
		}
	}
}

// The registry walk returns a program for a common video extension on a
// machine with a player installed; elsewhere it must just return "" quietly.
func TestDefaultHandlerExe(t *testing.T) {
	exe := defaultHandlerExe(".mkv")
	if exe != "" && !strings.HasSuffix(strings.ToLower(exe), ".exe") {
		t.Fatalf("default handler %q is not an .exe", exe)
	}
	if defaultHandlerExe("") != "" || defaultHandlerExe(".no-such-extension-abc") != "" {
		t.Fatal("unknown extensions must give no handler")
	}
	t.Logf(".mkv opens with %q", exe)
}
