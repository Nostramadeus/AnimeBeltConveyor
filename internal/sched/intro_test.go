package sched

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// OpenAt records the start position asked for; the fake knows every player.
func (f *fakeLauncher) OpenAt(player, file string, startSec int) (<-chan struct{}, string, bool, error) {
	f.mu.Lock()
	f.openedAt = append(f.openedAt, startSec)
	f.mu.Unlock()
	done, err := f.Open(player, file)
	return done, "fake", startSec > 0, err
}

func TestHasStartLogo(t *testing.T) {
	yes := []string{
		`[Erai-raws] JoJo no Kimyou na Bouken - Steel Ball Run - 06 [1080p NF WEBRip HEVC AAC][MultiSub][01519664].mkv`,
		`Show.S01E03.1080p.NF.WEB-DL.DDP2.0.H.264-GROUP.mkv`,
		`[Group] Show - 03 (Netflix 1080p).mkv`,
		`Show - 03 [NFLX].mkv`,
		`C:\Users\x\Downloads\Show\[Erai-raws] Show - 01 [1080p NF WEBRip].mkv`,
	}
	no := []string{
		`[Trix] The Laid-Off Cheat-Granting Mage S01E03 [WEBRip 1080p AV1] (Dual Audio, Multi Subs).mkv`,
		`[SubsPlease] Frieren - 01 (1080p) [ABCDEF12].mkv`,
		`Show.S01E03.1080p.AMZN.WEB-DL.mkv`,
		`Infinity - 01 [1080p].mkv`, // "nf" inside a word
		`Show - 01 [CR WEBRip].mkv`,
	}
	for _, n := range yes {
		if !HasStartLogo(n) {
			t.Errorf("%q: want tagged", n)
		}
	}
	for _, n := range no {
		if HasStartLogo(n) {
			t.Errorf("%q: want untagged", n)
		}
	}
}

func TestStartArgs(t *testing.T) {
	cases := []struct {
		exe    string
		want   []string // full argument list for "F" at 10 s
		tested bool
	}{
		{"C:/Program Files/SMPlayer/smplayer.exe", []string{"-start", "10", "F"}, true},
		{"C:/mpv/mpv.exe", []string{"--start=10", "F"}, false},
		{"/usr/bin/mpv", []string{"--start=10", "F"}, false},
		{"C:/x/mpvnet.exe", []string{"--start=10", "F"}, false},
		{"C:/Program Files/VideoLAN/VLC/vlc.exe", []string{"--start-time=10", "F"}, false},
		{"C:/Program Files/MPC-HC/mpc-hc64.exe", []string{"F", "/startpos", "00:00:10"}, false},
		{"C:/Program Files/MPC-BE x64/mpc-be64.exe", []string{"F", "/startpos", "00:00:10"}, false},
		{"C:/Program Files/DAUM/PotPlayer/PotPlayerMini64.exe", []string{"F", "/seek=00:00:10"}, false},
		{"C:/Program Files/Zoom Player/zplayer.exe", []string{"/Seek:00:00:10", "F"}, false},
		{"/usr/bin/ffplay", []string{"-ss", "10", "F"}, false},
		{"/usr/bin/mplayer", []string{"-ss", "10", "F"}, false},
		{"/usr/bin/celluloid", []string{"--mpv-start=10", "F"}, false},
		{"/Applications/IINA.app/Contents/MacOS/iina-cli", []string{"--mpv-start=10", "F"}, false},
	}
	for _, c := range cases {
		args, ok := startCommand(c.exe, "F", 10)
		_, _, tested, _ := StartArgs(c.exe, 10)
		if !ok || tested != c.tested || strings.Join(args, " ") != strings.Join(c.want, " ") {
			t.Fatalf("%s: args %v tested %v ok %v, want %v %v", c.exe, args, tested, ok, c.want, c.tested)
		}
	}
	if _, ok := startCommand("C:/Program Files/Unknown/player.exe", "F", 10); ok {
		t.Fatal("unknown player must not get a flag")
	}
	if got := hms(3725); got != "01:02:05" {
		t.Fatalf("hms = %s", got)
	}
}

// Play skips the global number for tagged files, nothing for other files; the
// show's own number wins either way (0 = never).
func TestOpenFileAtSkipsLogos(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sh := e.show(t, store.Show{TitleRomaji: "Steel Ball Run", Status: "RELEASING"})
	other := e.show(t, store.Show{TitleRomaji: "Frieren", Status: "RELEASING"})
	mk := func(show store.Show, name string) store.File {
		p := filepath.Join(e.root, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := e.db.UpsertFiles(ctx, []store.File{{Path: p, ShowID: &show.ID, Episode: store.Ptr(1), Present: true}}); err != nil {
			t.Fatal(err)
		}
		files, _ := e.db.FilesForShow(ctx, show.ID)
		for _, f := range files {
			if f.Path == p {
				return f
			}
		}
		t.Fatalf("planted file %s not stored", p)
		return store.File{}
	}
	tagged := mk(sh, "[Erai-raws] Steel Ball Run - 01 [1080p NF WEBRip HEVC AAC].mkv")
	plain := mk(other, "[SubsPlease] Frieren - 01 (1080p).mkv")
	launch := e.s.launch.(*fakeLauncher)
	last := func() int { launch.mu.Lock(); defer launch.mu.Unlock(); return launch.openedAt[len(launch.openedAt)-1] }

	res, err := e.s.OpenFileAt(ctx, tagged.ID)
	if err != nil || res.SkippedSec != 10 || res.WantedSec != 10 || last() != 10 {
		t.Fatalf("tagged file, default 10: %+v %v, launched with %d", res, err, last())
	}
	res, err = e.s.OpenFileAt(ctx, plain.ID)
	if err != nil || res.SkippedSec != 0 || res.WantedSec != 0 || last() != 0 {
		t.Fatalf("plain file: %+v %v, launched with %d", res, err, last())
	}

	// Global 0 = off.
	e.update(t, func(c *settings.Settings) { c.Open.IntroSkipSec = 0 })
	if res, _ = e.s.OpenFileAt(ctx, tagged.ID); res.SkippedSec != 0 || last() != 0 {
		t.Fatalf("global off: %+v, launched with %d", res, last())
	}

	// The show's own number applies to every file of the show, tagged or not.
	other.IntroSkipSec = store.Ptr(7)
	if err := e.db.UpdateShowPrefs(ctx, &other); err != nil {
		t.Fatal(err)
	}
	if res, _ = e.s.OpenFileAt(ctx, plain.ID); res.SkippedSec != 7 || last() != 7 {
		t.Fatalf("show override 7 on a plain file: %+v, launched with %d", res, last())
	}
	e.update(t, func(c *settings.Settings) { c.Open.IntroSkipSec = 12 })
	sh.IntroSkipSec = store.Ptr(0)
	if err := e.db.UpdateShowPrefs(ctx, &sh); err != nil {
		t.Fatal(err)
	}
	if res, _ = e.s.OpenFileAt(ctx, tagged.ID); res.SkippedSec != 0 || last() != 0 {
		t.Fatalf("show override 0 on a tagged file: %+v, launched with %d", res, last())
	}
}
