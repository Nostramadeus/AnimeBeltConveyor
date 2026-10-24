package sched

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

type SubtitleOptions struct {
	Delay     float64
	Scale     float64
	Requested bool
}

func subtitlesFor(c settings.Settings, show *store.Show) SubtitleOptions {
	options := SubtitleOptions{Delay: c.Open.SubtitleDelaySec, Scale: c.Open.SubtitleScale}
	if show != nil {
		if show.SubtitleDelaySec != nil {
			options.Delay = *show.SubtitleDelaySec
			options.Requested = true
		}
		if show.SubtitleScale != nil {
			options.Scale = *show.SubtitleScale
			options.Requested = true
		}
	}
	options.Requested = options.Requested || options.Delay != 0 || options.Scale != 1
	return options
}

func subtitleArgs(exe string, options SubtitleOptions) ([]string, bool) {
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe)))
	prefix := "--"
	switch name {
	case "mpv", "mpvnet":
	case "celluloid", "iina-cli":
		prefix = "--mpv-"
	default:
		return nil, false
	}
	return []string{prefix + "sub-delay=" + strconv.FormatFloat(options.Delay, 'f', -1, 64), prefix + "sub-scale=" + strconv.FormatFloat(options.Scale, 'f', -1, 64), prefix + "sub-ass-override=scale"}, true
}

func smplayerName(exe string) bool {
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe)))
	return name == "smplayer" || name == "smplayer-portable"
}

type playbackCommand struct {
	args      []string
	cleanup   func()
	skipped   bool
	subtitles bool
	note      string
}

func playbackArgs(exe, file string, start int, options SubtitleOptions) playbackCommand {
	command := playbackCommand{args: []string{file}, cleanup: func() {}}
	before, after, _, supported := StartArgs(exe, start)
	if start > 0 && supported {
		command.args = append(append(before, file), after...)
		command.skipped = true
	}
	if smplayerName(exe) {
		profile, cleanup, err := smplayerProfile(exe, options)
		if err != nil {
			command.note = "Subtitle preferences were not applied: " + err.Error()
			return command
		}
		command.cleanup = cleanup
		command.args = []string{"-config-path", profile}
		if start > 0 {
			command.args = append(command.args, "-start", strconv.Itoa(start))
			command.skipped = true
		}
		command.args = append(command.args, file)
		command.subtitles = true
		return command
	}
	if options.Requested {
		args, ok := subtitleArgs(exe, options)
		if ok {
			command.args = append(args, command.args...)
			command.subtitles = true
		} else {
			command.note = "Subtitle delay and size are not supported for this player."
		}
	}
	return command
}

func iniValue(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "@") {
		return "", fmt.Errorf("unsupported QSettings encoded value")
	}
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, `"`) {
		return strconv.Unquote(raw)
	}
	if strings.ContainsAny(raw, "\",\n\r") {
		return "", fmt.Errorf("unsupported QSettings value")
	}
	return strconv.Unquote(`"` + raw + `"`)
}

func iniKey(lines []string, section, key string) (string, bool) {
	current := "General"
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = line[1 : len(line)-1]
			continue
		}
		if current != section || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(name) == key {
			return value, true
		}
	}
	return "", false
}

func setINI(lines []string, section, key, value string) []string {
	current := "General"
	sectionEnd := -1
	foundSection := section == "General"
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if current == section {
				sectionEnd = index
			}
			current = trimmed[1 : len(trimmed)-1]
			if current == section {
				foundSection = true
				sectionEnd = len(lines)
			}
			continue
		}
		name, _, ok := strings.Cut(trimmed, "=")
		if current == section && ok && strings.TrimSpace(name) == key {
			lines[index] = key + "=" + value
			return lines
		}
	}
	if !foundSection {
		return append(lines, "["+section+"]", key+"="+value)
	}
	if sectionEnd < 0 {
		sectionEnd = len(lines)
	}
	return append(lines[:sectionEnd], append([]string{key + "=" + value}, lines[sectionEnd:]...)...)
}

func smplayerProfile(exe string, options SubtitleOptions) (string, func(), error) {
	if resolved, err := exec.LookPath(exe); err == nil {
		exe = resolved
	}
	home, _ := os.UserHomeDir()
	var userConfig string
	if runtime.GOOS == "windows" {
		userConfig = filepath.Join(home, ".smplayer", "smplayer.ini")
	} else {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		userConfig = filepath.Join(base, "smplayer", "smplayer.ini")
	}
	return smplayerProfileFrom(exe, options, []string{filepath.Join(filepath.Dir(exe), "smplayer.ini"), filepath.Join(filepath.Dir(exe), "config", "smplayer.ini"), userConfig})
}

func smplayerProfileFrom(exe string, options SubtitleOptions, sources []string) (string, func(), error) {
	var body []byte
	for _, source := range sources {
		data, err := os.ReadFile(source)
		if err == nil {
			body = data
			break
		}
		if !os.IsNotExist(err) {
			return "", nil, fmt.Errorf("cannot read SMPlayer settings: %w", err)
		}
	}
	text := strings.TrimPrefix(strings.ReplaceAll(string(body), "\r\n", "\n"), "\ufeff")
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return "", nil, fmt.Errorf("SMPlayer settings must use supported UTF-8 text")
	}
	lines := strings.Split(text, "\n")
	raw, configured := iniKey(lines, "General", "mplayer_bin")
	engine, err := iniValue(raw)
	if err != nil {
		return "", nil, err
	}
	if !configured || engine == "" {
		engine = filepath.Join(filepath.Dir(exe), "mpv", "mpv")
		if runtime.GOOS == "windows" {
			engine += ".exe"
		}
		if info, err := os.Stat(engine); err != nil || !info.Mode().IsRegular() {
			return "", nil, fmt.Errorf("no verified mpv backend in SMPlayer settings")
		}
	}
	if !filepath.IsAbs(engine) {
		if !strings.ContainsAny(engine, `/\`) {
			resolved, err := exec.LookPath(engine)
			if err != nil {
				return "", nil, fmt.Errorf("configured mpv executable cannot be found: %w", err)
			}
			engine = resolved
		} else {
			engine = filepath.Join(filepath.Dir(exe), filepath.FromSlash(engine))
		}
	}
	name := strings.ToLower(filepath.Base(engine))
	if name != "mpv" && name != "mpv.exe" {
		return "", nil, fmt.Errorf("SMPlayer backend %s is not supported; choose mpv in SMPlayer", name)
	}
	if info, err := os.Stat(engine); err != nil || !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("configured SMPlayer mpv executable is missing or not a regular file")
	}
	optionsRaw, _ := iniKey(lines, "advanced", "mplayer_additional_options")
	engineOptions, err := iniValue(optionsRaw)
	if err != nil {
		return "", nil, err
	}
	args, _ := subtitleArgs("mpv", options)
	engineOptions = strings.TrimSpace(engineOptions + " " + strings.Join(args, " "))
	values := []struct{ section, key, value string }{
		{"instances", "single_instance_enabled", "false"},
		{"General", "remember_media_settings", "false"},
		{"General", "remember_time_pos", "false"},
		{"General", "mplayer_bin", strconv.Quote(engine)},
		{"defaults", "initial_sub_scale_ass", strconv.FormatFloat(options.Scale, 'f', -1, 64)},
		{"defaults", "initial_sub_scale", strconv.FormatFloat(5*options.Scale, 'f', -1, 64)},
		{"advanced", "mplayer_additional_options", strconv.Quote(engineOptions)},
	}
	for _, value := range values {
		lines = setINI(lines, value.section, value.key, value.value)
	}
	dir, err := os.MkdirTemp("", "abc-smplayer-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	if err := os.WriteFile(filepath.Join(dir, "smplayer.ini"), []byte(strings.TrimSpace(strings.Join(lines, "\n"))+"\n"), 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}
