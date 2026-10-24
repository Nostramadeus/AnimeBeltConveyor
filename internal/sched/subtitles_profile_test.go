package sched

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
)

func qtFixtureDecode(t *testing.T, value string) string {
	t.Helper()
	var units []uint16
	for index := 0; index < len(value); {
		if value[index] == '%' {
			digits, start := 2, index+1
			if start < len(value) && value[start] == 'U' {
				digits, start = 4, start+1
			}
			if start+digits > len(value) {
				t.Fatal("invalid Qt escape", value)
			}
			unit, err := strconv.ParseUint(value[start:start+digits], 16, 16)
			if err != nil {
				t.Fatal(err)
			}
			units = append(units, uint16(unit))
			index = start + digits
		} else {
			unit := uint16(value[index])
			if unit == '\\' {
				unit = '/'
			}
			units = append(units, unit)
			index++
		}
	}
	return string(utf16.Decode(units))
}

func qtFixtureRead(t *testing.T, body []byte) map[string]string {
	t.Helper()
	values := map[string]string{}
	group := ""
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if line[0] == '[' {
			if !strings.HasSuffix(line, "]") {
				t.Fatal("malformed group", line)
			}
			raw := line[1 : len(line)-1]
			if strings.EqualFold(raw, "General") {
				group = ""
			} else if strings.EqualFold(raw, "%General") {
				group = raw[1:]
			} else {
				group = qtFixtureDecode(t, raw)
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatal("malformed key", line)
		}
		full := qtFixtureDecode(t, strings.TrimSpace(key))
		if group != "" {
			full = group + "/" + full
		}
		values[full] = strings.TrimSpace(value)
	}
	return values
}

func smplayerFixtureFileID(file string, size int64) string {
	var group strings.Builder
	for _, character := range file {
		switch character {
		case '/', '\\', ':', '.', ' ':
			group.WriteByte('_')
		default:
			group.WriteRune(character)
		}
	}
	return group.String() + "_" + strconv.FormatInt(size, 10)
}

func smplayerFixtureDelay(t *testing.T, profile, media []byte, file string, size int64) int {
	t.Helper()
	settings := qtFixtureRead(t, profile)
	if settings["General/remember_media_settings"] != "true" || settings["General/file_settings_method"] != "normal" {
		return 0
	}
	entry := qtFixtureRead(t, media)
	prefix := smplayerFixtureFileID(file, size) + "/"
	if entry[prefix+"saved"] != "true" {
		return 0
	}
	delay, err := strconv.Atoi(entry[prefix+"sub_delay"])
	if err != nil {
		t.Fatal(err)
	}
	return delay
}

func profileFixture(t *testing.T, original string) (string, string, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "smplayer.exe")
	source := filepath.Join(dir, "smplayer.ini")
	engine := filepath.Join(dir, "mpv", "mpv.exe")
	if runtime.GOOS != "windows" {
		engine = filepath.Join(dir, "mpv", "mpv")
	}
	touch(t, engine)
	media := filepath.Join(dir, "[Group] 日本😀;%= episode.mkv")
	touch(t, media)
	if err := os.WriteFile(source, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	return exe, source, media, []byte(original)
}

func TestSMPlayerQtGeneralAndMediaInitialization(t *testing.T) {
	original := "top_unrelated=kept\n[General]\nmplayer_bin=ignored-top-level\nremember_time_pos=top-level-unchanged\n[%General]\nmplayer_bin=mpv/mpv.exe\nfile_settings_method=hash\nremember_media_settings=false\nremember_time_pos=true\n[advanced]\nmplayer_additional_options=\"--volume=25 --title=\\\"Kept title\\\"\"\n[defaults]\ninitial_sub_scale_ass=1.25\n[custom]\nuntouched=yes\n"
	if runtime.GOOS != "windows" {
		original = strings.Replace(original, "mpv/mpv.exe", "mpv/mpv", 1)
	}
	exe, source, media, before := profileFixture(t, original)
	oldProfile := []byte("remember_media_settings=false\nremember_time_pos=false\n[%General]\nremember_media_settings=true\nfile_settings_method=hash\n[advanced]\nmplayer_additional_options=--sub-delay=0.825\n")
	if got := smplayerFixtureDelay(t, oldProfile, nil, media, 1); got != 0 {
		t.Fatal("old initialization fixture must reproduce zero", got)
	}
	for _, testcase := range []struct {
		delay float64
		want  int
		flag  string
	}{
		{0.8, 800, "0.8"}, {0.825, 825, "0.825"}, {-0.825, -825, "-0.825"}, {0, 0, "0"}, {0.0005, 1, "0.001"}, {-0.0005, -1, "-0.001"}, {0.8256, 826, "0.826"},
	} {
		t.Run(fmt.Sprint(testcase.delay), func(t *testing.T) {
			options := SubtitleOptions{Delay: testcase.delay, Scale: 0.6, Requested: true}
			profile, cleanup, err := smplayerProfileFrom(exe, media, options, []string{source})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			config, err := os.ReadFile(filepath.Join(profile, "smplayer.ini"))
			if err != nil {
				t.Fatal(err)
			}
			entry, err := os.ReadFile(filepath.Join(profile, "smplayer_files.ini"))
			if err != nil {
				t.Fatal(err)
			}
			values := qtFixtureRead(t, config)
			if values["mplayer_bin"] != "ignored-top-level" || values["remember_time_pos"] != "top-level-unchanged" || values["top_unrelated"] != "kept" || values["custom/untouched"] != "yes" {
				t.Fatal("unrelated namespace changed", values)
			}
			if values["General/remember_media_settings"] != "true" || values["General/remember_time_pos"] != "false" || values["General/file_settings_method"] != "normal" {
				t.Fatal("named General incorrect", values)
			}
			if got := smplayerFixtureDelay(t, config, entry, media, 1); got != testcase.want {
				t.Fatalf("SMPlayer media initialization %d want %d", got, testcase.want)
			}
			if !strings.Contains(values["advanced/mplayer_additional_options"], "--sub-delay="+testcase.flag+" ") || !strings.Contains(string(config), `--volume=25 --title=\"Kept title\"`) {
				t.Fatal("options lost or wrong delay", string(config))
			}
			if options.Delay != testcase.delay {
				t.Fatal("preference mutated")
			}
			files, _ := os.ReadDir(profile)
			if len(files) != 2 {
				t.Fatal("copied history or missing file", files)
			}
			for _, body := range [][]byte{config, entry} {
				if strings.Contains(string(body), "\r") || strings.HasPrefix(string(body), "\ufeff") {
					t.Fatal("non-LF/BOM output")
				}
			}
			unchanged, _ := os.ReadFile(source)
			if string(unchanged) != string(before) {
				t.Fatal("source config modified")
			}
			cleanup()
			if _, err := os.Stat(profile); !os.IsNotExist(err) {
				t.Fatal("profile not cleaned")
			}
		})
	}
}

func TestSMPlayerQtKeyEscaping(t *testing.T) {
	for _, testcase := range []struct{ key, want string }{
		{"AZaz09_-. /", `AZaz09_-.%20\`},
		{"[episode];%=é日本😀", "%5Bepisode%5D%3B%25%3D%E9%U65E5%U672C%UD83D%UDE00"},
	} {
		if got := qtINIKey(testcase.key); got != testcase.want {
			t.Fatalf("%q -> %q want %q", testcase.key, got, testcase.want)
		}
		if got := qtFixtureDecode(t, testcase.want); got != testcase.key {
			t.Fatal("independent decoder mismatch", got)
		}
	}
	if got := qtINIKey(smplayerMediaGroup(`Z:/Anime/[Group] 日 本 😀 01.mkv`, 9)); got != "Z__Anime_%5BGroup%5D_%U65E5_%U672C_%UD83D%UDE00_01_mkv_9" {
		t.Fatal(got)
	}
}

func TestSMPlayerProfileMissingGeneralAndNeutral(t *testing.T) {
	exe, source, media, before := profileFixture(t, "unrelated=preserved\n[General]\nremember_time_pos=top-kept\n[advanced]\nmplayer_additional_options=--volume=25\n")
	profile, cleanup, err := smplayerProfileFrom(exe, media, SubtitleOptions{Delay: 0, Scale: 1, Requested: true}, []string{source})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	body, _ := os.ReadFile(filepath.Join(profile, "smplayer.ini"))
	entry, _ := os.ReadFile(filepath.Join(profile, "smplayer_files.ini"))
	values := qtFixtureRead(t, body)
	if !strings.Contains(string(body), "[%General]\n") || values["remember_time_pos"] != "top-kept" || values["General/remember_time_pos"] != "false" || values["defaults/initial_sub_scale_ass"] != "1" {
		t.Fatal(string(body))
	}
	if got := smplayerFixtureDelay(t, body, entry, media, 1); got != 0 {
		t.Fatal("neutral failed", got)
	}
	unchanged, _ := os.ReadFile(source)
	if string(unchanged) != string(before) {
		t.Fatal("source modified")
	}
	for _, path := range []string{filepath.Join(filepath.Dir(media), "missing.mkv"), filepath.Dir(media)} {
		if _, cleanup, err := smplayerProfileFrom(exe, path, SubtitleOptions{Scale: 1}, []string{source}); err == nil {
			cleanup()
			t.Fatal("unsafe media accepted", path)
		}
	}
}

func TestSMPlayerProfileBackendPreservation(t *testing.T) {
	exe, source, media, _ := profileFixture(t, "[%General]\nmplayer_bin=mplayer\n")
	if _, cleanup, err := smplayerProfileFrom(exe, media, SubtitleOptions{Scale: 1}, []string{source}); err == nil {
		cleanup()
		t.Fatal("MPlayer replaced")
	}
	engineDir := filepath.Join(filepath.Dir(exe), "path-engine")
	if err := os.Mkdir(engineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "mpv"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	engine := filepath.Join(engineDir, name)
	if err := os.WriteFile(engine, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", engineDir)
	if err := os.WriteFile(source, []byte("[%General]\nmplayer_bin=mpv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile, cleanup, err := smplayerProfileFrom(exe, media, SubtitleOptions{Scale: 1}, []string{source})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	body, _ := os.ReadFile(filepath.Join(profile, "smplayer.ini"))
	value := qtFixtureRead(t, body)["General/mplayer_bin"]
	decoded, err := strconv.Unquote(value)
	if err != nil || decoded != engine {
		t.Fatal("PATH engine not preserved", decoded, err)
	}
}

func TestSMPlayerQtDuplicateKeys(t *testing.T) {
	lines := strings.Split("[%General]\nmplayer_bin=mplayer\n[advanced]\nmplayer_additional_options=--first\n[%General]\nmplayer_bin=mpv/mpv.exe\n[advanced]\nmplayer_additional_options=--last\n", "\n")
	if value, ok := iniKey(lines, "General", "mplayer_bin"); !ok || value != "mpv/mpv.exe" {
		t.Fatal("Qt must use last backend", value)
	}
	if value, ok := iniKey(lines, "advanced", "mplayer_additional_options"); !ok || value != "--last" {
		t.Fatal("Qt must use last options", value)
	}
	lines = setINI(lines, "General", "remember_time_pos", "false")
	lines = setINI(lines, "General", "mplayer_bin", "absolute-mpv")
	if strings.Count(strings.Join(lines, "\n"), "mplayer_bin=absolute-mpv") != 2 {
		t.Fatal("duplicate values not updated", lines)
	}
	if got := qtFixtureRead(t, []byte(strings.Join(lines, "\n")))["General/mplayer_bin"]; got != "absolute-mpv" {
		t.Fatal(got)
	}
}
