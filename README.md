# AnimeBeltConveyor (ABC)

Weekly anime, delivered. A small local app that keeps the belt moving:

```
AniList schedule  ->  nyaa.si search  ->  release rules  ->  qBittorrent
                                                                  |
your media player (mpv / VLC)  <-  Up next  <-  folder scanner  <-+
```

- Tracks what you watch (AniList data, optional AniList / MyAnimeList sync).
- Finds each new episode on nyaa.si, picks one release by your rules (group order, resolution, fallback delay, batch handling, v2 replacement) and hands the magnet to qBittorrent.
- Watches your folders so episodes on disk show as "ready to play".
- Marks episodes watched when your player has had the file open long enough (or when you tick them).
- One Go binary, SQLite file, web UI at `http://127.0.0.1:7700`. No accounts needed. Every rule above is a setting.

## Run

Build (needs only Go; set `GO` to the full path of the go binary when it is not on PATH):

```
.\build.ps1          # Windows PowerShell: abc.exe with icon and version
./build.sh           # macOS / Linux / Git Bash: abc (abc.exe on Windows)
go build ./cmd/abc   # also works; the icon resource is committed
```

Run:

```
abc                  # opens http://127.0.0.1:7700 in your browser
abc --no-browser --port 7700 --data <dir>
abc --version
```

Where ABC keeps its data (database, log), first match wins:

1. `--data <dir>`
2. a folder named `data` next to `abc.exe`. Create a `data` folder next to abc.exe to make it portable (a USB stick, a synced folder).
3. the per-user config folder: `%APPDATA%\abc` on Windows, `~/.config/abc` on Linux, `~/Library/Application Support/abc` on macOS.

First run: the setup guide opens by itself. It asks where episodes go (any number of library folders; one of them is the download folder, the others are only scanned) and which app downloads them:

| Downloader | What ABC does with a chosen release |
|---|---|
| qBittorrent (recommended) | sends it through qBittorrent's Web UI (`Tools > Options... > Web UI`) into `<download folder>\<Show>\` and follows its progress |
| Another torrent app, magnet links | opens the magnet link with the app your system uses for magnet links (Deluge, Transmission, uTorrent, ...) |
| Another torrent app, watch folder | saves the `.torrent` file into the folder your app watches for new torrents |
| By hand | lists the release with a magnet button; nothing downloads on its own |

For the magnet and watch-folder modes, set your torrent app's download folder to ABC's download folder; ABC matches episodes by file name, so any sub-folder layout works.

## Develop

```
go test ./...
go vet ./...
```

The UI is plain HTML/CSS/JS in `web/`, embedded into the binary. Open `web/index.html?mock=1` in a browser to work on it without the server.

See `SPEC.md` for the contract every package follows.

## Licence

MIT.
