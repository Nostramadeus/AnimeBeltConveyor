# Player start-offset flags (launch with a file, skip the first N seconds)

Research date: 2026-10-23. Method: official docs, man pages, and upstream source code read over HTTPS. No player was launched or tested. "Source-verified" means the parsing code in the upstream repository was read. "Documented" means an official doc, help string or man page states it.

Example target: start at 10 seconds into `C:\v\ep1.mkv`.

## Summary table

| Player | Windows exe (other OS) | Start flag, 10 s example | Unit / format | Confidence |
|---|---|---|---|---|
| mpv | `mpv.exe` (`mpv.com` console wrapper, unverified; Linux/mac `mpv`) | `mpv.exe --start=10 "C:\v\ep1.mkv"` | seconds, `[+-][[hh:]mm:]ss[.ms]`, `pp%`, `#chapter`; `=` form | documented |
| mpv.net | `mpvnet.exe` | `mpvnet.exe --start=10 "C:\v\ep1.mkv"` | same as mpv (passes mpv options through) | documented (pass-through), flag itself not listed in mpv.net docs |
| SMPlayer | `smplayer.exe` (Linux `smplayer`) | `smplayer.exe -start 10 "C:\v\ep1.mkv"` | seconds, or `m:s`, or `h:m:s`; space separated | source-verified (not in man page) |
| VLC | `vlc.exe` (Linux `vlc`; mac `VLC.app/Contents/MacOS/VLC`) | `vlc.exe --start-time=10 "C:\v\ep1.mkv"` | seconds (float); Windows needs `=` form | documented |
| MPC-HC (original and clsid2 fork) | `mpc-hc.exe` (32-bit), `mpc-hc64.exe` (64-bit) | `mpc-hc64.exe "C:\v\ep1.mkv" /startpos 00:00:10` or `/start 10000` | `/startpos hh:mm:ss[.ms]`; `/start` = milliseconds; space separated | documented (in-app help string) + source-verified |
| MPC-BE | `mpc-be.exe`, `mpc-be64.exe` | `mpc-be64.exe "C:\v\ep1.mkv" /startpos 00:00:10` or `/start 10000` | same as MPC-HC | source-verified (help text not read) |
| PotPlayer | `PotPlayerMini.exe` (32-bit), `PotPlayerMini64.exe` (64-bit) | `PotPlayerMini64.exe "C:\v\ep1.mkv" /seek=10` | seconds or `hh:mm:ss.ms`; `=` required | documented (official usage text, quoted via GitHub issue) |
| Zoom Player | `zplayer.exe` | `zplayer.exe /seek:00:00:10 "C:\v\ep1.mkv"` | `/Seek:HH:MM:SS`; colon separated | documented |
| IINA (macOS) | `iina-cli` (inside `IINA.app/Contents/MacOS/`, path unverified) | `iina-cli --mpv-start=10 /path/ep1.mkv` | mpv syntax via `--mpv-<option>=<value>` | source-verified (iina-cli help text) |
| Celluloid (Linux) | `celluloid` (formerly `gnome-mpv`) | `celluloid --mpv-start=10 /path/ep1.mkv` | mpv syntax via `--mpv-MPVOPTION=VALUE` | documented (man page: generic mpv pass-through) |
| ffplay (FFmpeg) | `ffplay.exe` / `ffplay` | `ffplay.exe -ss 10 "C:\v\ep1.mkv"` | seconds or `[hh:]mm:ss[.ms]` | documented |
| MPlayer (legacy) | `mplayer.exe` / `mplayer` | `mplayer -ss 10 /path/ep1.mkv` | seconds or `[[hh:]mm:]ss[.ms]` | documented (man page; `-ss` page text read only through the -endpos cross-reference) |
| KMPlayer | `KMPlayer.exe`, `KMPlayer64X.exe` (names unverified) | unknown | unknown | not found (forum hint only that a start switch exists) |
| GOM Player | `GOM.exe` / `gom.exe` (seen in a forum path, unverified) | none found | n/a | not found |
| Haruna | `haruna.exe` / `haruna` | none found | n/a | not found |
| Totem / GNOME Videos | `totem` | none usable | n/a | not supported |
| Parole | `parole` | none | n/a | not supported |
| Kodi | `kodi.exe` (Linux `kodi`/`kodi.bin`; mac `Kodi.app/Contents/MacOS/Kodi`) | none | n/a | not supported |
| Windows Media Player legacy | `wmplayer.exe` | none | n/a | not supported |
| Windows Media Player / Films & TV (modern UWP) | `Microsoft.Media.Player.exe` (path under `WindowsApps\Microsoft.ZuneMusic_*`); Films & TV `Video.UI.exe` (unverified) | none found | n/a | not found |
| QuickTime Player (macOS) | `QuickTime Player.app` | none on the command line; AppleScript only | `set current time of document 1 to <seconds>` | forum only |
| 5KPlayer | `5KPlayer.exe` (unverified) | none found | n/a | not found |
| DivX Player | `DivX Player.exe` (unverified) | none found | n/a | not found |

## Per-player notes

### mpv
- Source: https://mpv.io/manual/stable/ (option `--start=<relative time>`).
- Format quoted from the manual: `[+|-][[hh:]mm:]ss[.ms]`; `-` counts from the end; `pp%` is a percent; `#c` is a chapter; `none` resets.
- Manual examples: `--start=+56`, `--start=00:56`, `--start=01:10:00`, `--start=50%`.
- Use the `--start=10` form (with `=`) so a value is never mistaken for a file. Options before the file are the documented convention.
- Gotcha: with `--rebase-start-time=no`, a leading `+` means "relative to file start" and a bare number is absolute. Default is yes, so `--start=10` is 10 s from the start.
- Gotcha: mpv's watch-later/resume (`--save-position-on-quit`) can override or combine with `--start` if the user enabled it. Not tested.

### mpv.net
- Source: https://raw.githubusercontent.com/mpvnet-player/mpv.net/main/docs/manual.md
- Manual: `mpvnet [options] [file|URL|PLAYLIST|-]`; "same syntax as mpv". `--start` is not named in that manual, but it is a normal mpv option. Confidence for the pass-through is documented; confidence that mpv.net accepts every mpv option is not tested.
- Gotcha: default `--process-instance=single`. A second launch while mpv.net is running hands the file to the running process, and the start option may not be applied. Add `--process-instance=multi` to force a separate process. The "single" default is documented; the effect on `--start` is NOT verified.
- Executable name: `mpvnet.exe` (manual text).

### SMPlayer
- Official man page lists NO `-start` option: https://man.archlinux.org/man/smplayer.1.en.txt . The option exists in code, so this is source-verified, not documented.
- Source: https://github.com/smplayer-dev/smplayer/blob/master/src/smplayer.cpp (`processArgs`, branch `argument == "-start"`).
- Parser: regex `(\d+):(\d+):(\d+)` for `h:m:s`, else `(\d+):(\d+)` for `m:s`, else integer seconds. Result is whole seconds (no fraction). The value is the next argument (space separated, not `=`).
- Gotcha: if no value follows, SMPlayer prints "expected parameter for -start" and stops.
- Gotcha: when an SMPlayer instance is already running, the code sends `start_second <n>` to it (line ~458), so start time is forwarded. `-disable-server` turns the single-instance server off.
- Linux binary `smplayer`; Windows `smplayer.exe` (name from general knowledge, not fetched).

### VLC
- Sources: https://wiki.videolan.org/Documentation:Command_line/ (`--start-time <integer>` "number of seconds from the beginning") and https://wiki.videolan.org/VLC_command-line_help/ (`--start-time=<float>`: "The stream will start at this position (in seconds)").
- Windows: wiki states Windows users must use the `--option-name="value"` form, not `--option-name value`.
- Gotchas (forum/issue, lower confidence):
  - With "Allow only one instance" on, a second launch reuses the first window and the new start time may be ignored: https://forum.videolan.org/viewtopic.php?p=541845
  - MP3 files ignored `--start-time` on first play: https://code.videolan.org/videolan/vlc/-/issues/3042
  - May not seek on MPEG-TS/PS and some DVD sources: https://forum.videolan.org/viewtopic.php?p=280684
- Per-item alternative is the `:start-time=10` item option (not checked in this pass).
- Executable names: `vlc.exe` (Windows), `vlc` (Linux). Not fetched from an official page.

### MPC-HC (original) and clsid2 fork
- Source (in-app help strings, clsid2 fork): https://github.com/clsid2/mpc-hc/blob/master/src/mpc-hc/mpc-hc.rc
  - `/start ms` = "Start playing at ms (= milliseconds)"
  - `/startpos hh:mm:ss` = "Start playing at position hh:mm:ss"
- Parser: https://github.com/clsid2/mpc-hc/blob/master/src/mpc-hc/AppSettings.cpp (`sw == _T("start")` uses `_tcstol`, ms x 10000; `sw == _T("startpos")` calls `ConvertTimeToMSec`).
- Switches accept a `-` or `/` prefix. They are read from the whole argument list, so order relative to the file does not matter. Each switch takes the next argument as its value.
- Gotcha (important): `ConvertTimeToMSec` returns 0 when the string is shorter than 4 characters. `/startpos 10` is silently ignored. Use `/startpos 00:00:10` (full `hh:mm:ss`), or `/start 10000`.
- Gotcha: `.` fractions are accepted (`00:00:10.5`). Any non-digit/non-colon/non-dot character zeroes the result.
- Gotcha: single-instance. The whole command line is sent to a running instance via `WM_COPYDATA` unless multi-instance is allowed or `/new` is given (mplayerc.cpp, `SendCommandLine`). So the flag is re-parsed by the running window. Not tested.
- Gotcha: `/play` starts playback automatically; without it the file may only be opened. Typical: `mpc-hc64.exe "file" /play /startpos 00:00:10`. `/open` opens without autoplay.
- Executables: `mpc-hc.exe` (32-bit) and `mpc-hc64.exe` (64-bit), from the project TargetName `$(ProjectName)64` for x64 (https://github.com/clsid2/mpc-hc/blob/master/src/mpc-hc/mpc-hc.vcxproj). A forum command line also shows `mpc-hc64.exe` (https://www.pteavstudio.com/forums/topic/19328-command-line-switches/). No separate "mini" exe found. Portable builds use the same exe names with an `.ini` next to them.
- Original mpc-hc (pre-fork) help text not fetched in this pass; the `/start` and `/startpos` strings are long-standing. Treat original as same-syntax but unverified.

### MPC-BE
- Source: https://github.com/Aleksoid1978/MPC-BE/blob/master/src/apps/mplayerc/AppSettings.cpp . Same code as MPC-HC: `sw == L"start"` (ms) and `sw == L"startpos"` (`ConvertTimeToMSec`, returns 0 if string shorter than 4 characters). `-` or `/` prefix.
- Help-dialog strings for MPC-BE were not read (the .rc fetch failed), hence "source-verified", not "documented".
- Same gotchas as MPC-HC: use `00:00:10`, not `10`.
- Executables: `mpc-be.exe` and `mpc-be64.exe` (project `mpc-be.vcxproj`, x64 TargetName `$(ProjectName)64`).

### PotPlayer
- Official usage text (PotPlayer `CmdLine.txt` content), quoted in https://github.com/red217/EmbyExternalPlayerLauncher/issues/2 :
  - `/seek=time : Starts playback of the specified/last played content from the specified time point. -time format is: hh:mm:ss.ms (OR specify seconds only e.g. /seek=1800 to start at 30th min)`
  - `/new` = new instance; `/current` = existing instance (both ignore the "Multiple instances" setting); `/autoplay` = play last played file.
- Real-world example with the file first: `PotPlayerMini64.exe "C:\Video output\2022-11-12\video.mp4" /seek=14:02:06` (https://www.mrexcel.com/board/threads/how-do-i-create-a-bat-file-for-each-row.1225635/ , forum only).
- Gotcha: the `=` form is the documented one; a space-separated `/seek 10` is not documented.
- Gotcha: the docs say the `/new` and `/current` switches override the multi-instance setting. With neither, behaviour depends on the user's "Multiple instances" setting. Not tested whether `/seek` is applied when handing off to a running instance.
- Official site (potplayer.daum.net) did not load during research. Exe names `PotPlayerMini.exe` / `PotPlayerMini64.exe` appear in the GitHub issue above and in the usage text example (`PotPlayerMini.exe -`).

### Zoom Player
- Source: https://inmatrix.com/zplayer/parameters.shtml
- Text: "/Seek:[HH]:[MM]:[SS] Play a media file in a specified position ... Example: "zplayer.exe" /seek:01:15:32". Colon after `/Seek`, no `=`.
- Exe: `zplayer.exe` (shown in the page examples). Switch order: page example `"zplayer.exe" /ar:0 "myvideo.avi"` puts switches before the file.
- Gotcha: `/MultInst` allows a new instance (otherwise a running instance is reused); seek behaviour when reusing is not documented.

### IINA
- Source: https://github.com/iina/iina/blob/develop/iina-cli/main.swift (help text of `iina-cli`): "`--mpv-*`: All mpv options are supported here, except those starting with `--no-`. Example: `--mpv-volume=20`". Also a form after `--`: `iina-cli file -- --start=10`.
- So the start flag is `--mpv-start=10`. Inferred from the generic pass-through, not a dedicated doc line for `start`.
- Gotcha: `iina-cli` quits immediately after launching IINA unless `--keep-running`. If IINA is already open the file is sent to it and mpv options may not apply (not documented).
- Binary location inside the bundle was not verified (`iina-cli` is the tool name in the source).

### Celluloid
- Source: https://github.com/celluloid-player/celluloid/blob/master/docs/celluloid.1 : "`--mpv-MPVOPTION=VALUE` Set mpv option MPVOPTION to VALUE ... equivalent to passing `--vf=vflip` to mpv".
- So `celluloid --mpv-start=10 file`. Inference from the generic rule.
- Gotcha: `--new-window` creates a new window if an instance is running; `--enqueue` appends to the running instance. Without them a second launch reuses the running instance, and mpv options may not apply (not documented).
- Binary: `celluloid` (`gnome-mpv` on old versions).

### ffplay and MPlayer (extra)
- ffplay: https://ffmpeg.org/ffplay.html : "`-ss pos` Seek to pos ... pos must be a time duration specification". Example `ffplay -ss 10 file`. Seeks to the nearest seek point.
- MPlayer: https://manpages.debian.org/testing/mplayer/mplayer.1.en.html (the `-ss` entry is referenced by `-endpos`). Syntax `-ss <time>`, seconds or `[[hh:]mm:]ss[.ms]`. Not read at the `-ss` entry itself in this pass.

### KMPlayer
- No documented start flag found. A 2009 VideoHelp thread says KMPlayer lists command-line switches under Options > About > Command Line Switches and that a start switch exists but gives no syntax (https://forum.videohelp.com/threads/300361-A-B-repeat-in-command-line , read through a search snippet only; page did not load). Confidence: unknown.
- The `/ADD` switch is used with `KMPlayer.exe` (https://www.xyplorer.com/xyfc/viewtopic.php?t=11982).
- Do not guess a syntax. A Go program should treat KMPlayer as "no start offset" unless someone reads the in-app switch list and records it.

### GOM Player
- No command-line start option found in GOM Lab guides or forums. Only `/fullscreen` and `/maximize` are mentioned (https://www.neowin.net/news/gom-player-2103390-beta/ , old beta note).
- A forum shows `C:\Program Files (x86)\GRETECH\GomPlayer\gom.exe /Fullscreen` (https://yabb.jriver.com/interact/index.php/topic,56077.0.html). Treat the exe name as unverified for current builds.

### Haruna
- Source read: https://invent.kde.org/multimedia/haruna/-/raw/master/src/main.cpp . It only processes positional arguments (the first file) and forwards that file to a running instance through `KDSingleApplication` when "use single instance" is set. No start-time option in this file. Other code could add one; this pass did not search it.
- Confidence: not found.

### Totem / GNOME Videos
- Source: https://gitlab.gnome.org/GNOME/totem/-/raw/master/src/totem-options.c . A hidden `--seek` (int64) option is declared, but the field `optionstate.seek` is never read in `totem-options.c`, `totem-object.c` or `totem.c` in the current master. So it does nothing. Only `--seek-fwd` / `--seek-bwd` (relative step on a running instance) work.
- Confidence: not supported.

### Parole
- Man page (https://manpages.debian.org/testing/parole/parole.1.en.html): only `-f/--seek-f` and `-b/--seek-b` (step forward/back on a running instance). No absolute start time.
- Confidence: not supported.

### Kodi
- Man page (https://dyn.manpages.debian.org/trixie/kodi/kodi.1.en.html): options `-fs`, `--standalone`, `-p`/`--portable`, `--version`, `--test`, `--settings=`, `--windowing=`. No start offset. Kodi's resume/offset is available through its JSON-RPC / `PlayMedia` builtin, not the launch command line.
- Confidence: not supported on the command line.

### Windows Media Player legacy (`wmplayer.exe`)
- Source: https://learn.microsoft.com/en-us/windows/win32/wmp/command-line-parameters . Parameters: file path, `/fullscreen`, `/Device:`, `/Task`, `/Service`, `/Playlist`, `/Schema`. No seek or start position.
- Confidence: not supported.

### Windows Media Player (modern) and Films & TV
- No command-line documentation found. An ElevenForum thread (https://www.elevenforum.com/t/command-line-parameters-for-media-player-app.20023/ , search snippet only) shows the exe path `...\Microsoft.ZuneMusic_*\Microsoft.Media.Player.exe` and no start-position parameter.
- These are packaged apps launched through protocol/AppX activation; no seek argument is known. Confidence: not found.

### QuickTime Player (macOS)
- No command-line start option. The workable route is AppleScript: open the file, then set `current time of document 1` (a MacScripter thread, https://www.macscripter.net/t/quicktime-player-set-player-position-to-specified-timecode/46127 , lower confidence; targets QuickTime 7). A Go program would need `osascript`. Not tested on current macOS.

### 5KPlayer and DivX Player
- No command-line documentation found for either. Exe names above are unverified. Confidence: not found.

## Quick reference for a Go launcher

Flags that were verified in docs or source and can be built mechanically (seconds `n`):

| Player key | Args (file last unless noted) | Notes |
|---|---|---|
| mpv, mpvnet | `--start=<n>` then file | `<n>` can be seconds or `hh:mm:ss` |
| smplayer | `-start <n>` then file | integer seconds |
| vlc | `--start-time=<n>` then file | `=` form required on Windows |
| mpc-hc, mpc-be | file, `/startpos hh:mm:ss` | zero-pad to full `hh:mm:ss`; or `/start <n*1000>` |
| potplayer | file, `/seek=<n>` | `=` form |
| zplayer | `/seek:hh:mm:ss` then file | colon after switch name |
| iina-cli | `--mpv-start=<n>` then file | macOS only |
| celluloid | `--mpv-start=<n>` then file | Linux only |
| ffplay | `-ss <n>` then file | |

Players with no usable flag (treat as "cannot skip"): KMPlayer (unknown), GOM, Haruna, Totem, Parole, Kodi, WMP legacy, Media Player (modern), Films & TV, QuickTime, 5KPlayer, DivX Player.

Cross-cutting gotcha: single-instance mode. Several players (VLC, mpv.net, PotPlayer, Celluloid, IINA, MPC) forward a second launch to the already running window. Only SMPlayer (source) and MPC-HC/BE (source) were shown to forward the start position. For the others the flag may be ignored when the player is already open; this was not tested because nothing was launched.
