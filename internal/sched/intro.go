package sched

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// Rips from one streaming service open with a logo animation (measured on a
// 2026 Erai-raws file: black 0-3.1 s, logo 3.1-5.0 s, black until 11.6 s).
// Release groups tag those files in the name: "[1080p NF WEBRip ...]",
// "Netflix", "NFLX". The UI never names the company (the owner, 2026-10-23): it
// says "company logos" and "streaming-service rips".
var startLogoRip = regexp.MustCompile(`(?i)(^|[^a-z0-9])(NF[ ._-]*WEB|NETFLIX|NFLX)`)

// HasStartLogo reports whether the file name is tagged as a rip that opens
// with a service logo.
func HasStartLogo(name string) bool { return startLogoRip.MatchString(filepath.Base(name)) }

// introSkipFor is the number of seconds Play skips for file name of show sh:
// the show's own number when set (0 = never), else the global number for
// tagged files, else 0.
func introSkipFor(c settings.Settings, sh *store.Show, name string) int {
	if sh != nil && sh.IntroSkipSec != nil {
		return max(*sh.IntroSkipSec, 0)
	}
	if HasStartLogo(name) {
		return max(c.Open.IntroSkipSec, 0)
	}
	return 0
}

// playerStart is one player's start-position flag. names are executable base
// names in lower case without the extension. after = the flag goes after the
// file path (MPC and PotPlayer document that order); otherwise before it.
type playerStart struct {
	names  []string
	args   func(sec int) []string
	after  bool
	tested bool // verified on a real machine (SMPlayer, 2026-10-23); the rest come from docs/player-start-flags.md
}

func hms(sec int) string { return fmt.Sprintf("%02d:%02d:%02d", sec/3600, sec/60%60, sec%60) }

var playerStarts = []playerStart{
	{names: []string{"smplayer", "smplayer-portable"}, tested: true, args: func(s int) []string { return []string{"-start", strconv.Itoa(s)} }},
	{names: []string{"mpv", "mpvnet"}, args: func(s int) []string { return []string{"--start=" + strconv.Itoa(s)} }},
	{names: []string{"vlc"}, args: func(s int) []string { return []string{"--start-time=" + strconv.Itoa(s)} }},
	{names: []string{"mpc-hc", "mpc-hc64", "mpc-be", "mpc-be64"}, after: true, args: func(s int) []string { return []string{"/startpos", hms(s)} }}, // a bare "10" is ignored, hh:mm:ss is not
	{names: []string{"potplayer", "potplayer64", "potplayermini", "potplayermini64"}, after: true, args: func(s int) []string { return []string{"/seek=" + hms(s)} }},
	{names: []string{"zplayer"}, args: func(s int) []string { return []string{"/Seek:" + hms(s)} }}, // Zoom Player
	{names: []string{"ffplay", "mplayer"}, args: func(s int) []string { return []string{"-ss", strconv.Itoa(s)} }},
	{names: []string{"celluloid", "iina-cli"}, args: func(s int) []string { return []string{"--mpv-start=" + strconv.Itoa(s)} }}, // mpv pass-through
}

// StartArgs returns the arguments that make the player program exe start sec
// seconds into the file: before go in front of the file path, after behind
// it. tested = the flag was verified on a real machine; ok = false when ABC
// knows no flag for that program.
func StartArgs(exe string, sec int) (before, after []string, tested, ok bool) {
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe)))
	for _, p := range playerStarts {
		for _, n := range p.names {
			if n == base {
				if p.after {
					return nil, p.args(sec), p.tested, true
				}
				return p.args(sec), nil, p.tested, true
			}
		}
	}
	return nil, nil, false, false
}

// startCommand is the full argument list: before, the file, after.
func startCommand(exe, file string, sec int) ([]string, bool) {
	before, after, _, ok := StartArgs(exe, sec)
	if !ok {
		return nil, false
	}
	args := append(append(append([]string{}, before...), file), after...)
	return args, true
}

// PlayerName is the short program name shown in the UI ("smplayer.exe" -> "SMPlayer" style is
// not attempted; the base name without extension is clear enough).
func PlayerName(exe string) string {
	if exe == "" {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe))
}
