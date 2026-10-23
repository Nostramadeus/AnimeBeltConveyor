// Command abc runs AnimeBeltConveyor: the local server, the background loops and
// the embedded web UI at http://127.0.0.1:7700.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/api"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/sched"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
	"github.com/Nostramadeus/AnimeBeltConveyor/web"
)

// version is set at build time: go build -ldflags "-X main.version=v1.1.0".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "abc:", err)
		slog.Error("exit", "err", err)
		if !hasConsole() {
			// abcw.exe (quiet start, no console window): the user would never see stderr.
			fatalBox(err.Error() + "\n\nDetails are in abc.log in the data folder.")
		}
		os.Exit(1)
	}
}

// Data folder sources, for the startup log.
const (
	srcFlag     = "--data flag"
	srcPortable = "portable: data folder next to abc.exe"
	srcUser     = "per-user config folder"
)

// dataDir picks the data folder: the --data flag, else <exe dir>/data when that
// folder exists (portable mode), else os.UserConfigDir()/abc (%APPDATA%\abc on
// Windows, ~/.config/abc on Linux, ~/Library/Application Support/abc on macOS).
func dataDir(flagValue, exeDir string, userConfigDir func() (string, error)) (dir, source string, err error) {
	if flagValue != "" {
		dir, err = filepath.Abs(flagValue)
		return dir, srcFlag, err
	}
	if exeDir != "" {
		p := filepath.Join(exeDir, "data")
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p, srcPortable, nil
		}
	}
	base, err := userConfigDir()
	if err != nil {
		return "", "", fmt.Errorf("no config folder for this user (use --data, or create a data folder next to abc.exe): %w", err)
	}
	return filepath.Join(base, "abc"), srcUser, nil
}

// alreadyRunning reports whether another ABC answers at url (GET /api/state is 200).
func alreadyRunning(url string) bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url + "api/state")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK && resp.Header.Get("Content-Type") != "" && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json")
}

// exeDir is the folder of the running executable ("" when unknown).
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

func run() error {
	dataFlag := flag.String("data", "", "data folder (default: a \"data\" folder next to abc.exe if it exists, else the per-user config folder + /abc)")
	portFlag := flag.Int("port", 0, "HTTP port (default: Settings > Server > port, 7700)")
	noBrowser := flag.Bool("no-browser", false, "do not open the web UI or any folder window")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("abc " + version)
		return nil
	}

	exe := exeDir()
	dir, source, err := dataDir(*dataFlag, exe, os.UserConfigDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "abc.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	// abcw.exe (no console): stderr is not writable there, and io.MultiWriter stops at the
	// first failing writer, which would silence the log file too. So: file only.
	var logOut io.Writer = logFile
	if hasConsole() {
		logOut = io.MultiWriter(os.Stderr, logFile)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(logOut, nil)))
	slog.Info("starting", "version", version, "data", dir, "data_source", source)

	// A second double-click on abc.exe / abcw.exe: show the running instance instead of
	// failing. Checked BEFORE store.Open, because Open migrates the database and a newer
	// build started next to an older running one must not migrate under it.
	showExisting := func(url string) error {
		slog.Info("already running, opening the existing window", "url", url)
		fmt.Println("ABC is already running at " + url)
		if !*noBrowser {
			if err := sched.OpenURL(url); err != nil {
				slog.Warn("could not open the browser", "err", err)
			}
		}
		return nil
	}
	dbPath := filepath.Join(dir, "abc.db")
	probe := *portFlag
	if probe == 0 {
		probe = store.PeekServerPort(dbPath)
	}
	if probe == 0 {
		probe = 7700 // the settings default
	}
	if u := "http://127.0.0.1:" + strconv.Itoa(probe) + "/"; alreadyRunning(u) {
		return showExisting(u)
	}

	db, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer db.Close()
	set, err := settings.NewManager(db)
	if err != nil {
		slog.Warn("settings unreadable, using defaults", "err", err)
	}
	ev := events.New(db)
	sch := sched.New(db, set, ev, nil)

	port := *portFlag
	if port == 0 {
		port = set.Get().Server.Port
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		url := "http://" + addr + "/"
		if alreadyRunning(url) {
			return showExisting(url)
		}
		return fmt.Errorf("cannot listen on %s (another program uses the port; change Settings > Server > port or run with --port): %w", addr, err)
	}

	apiSrv := api.New(db, set, ev, sch, web.FS)
	apiSrv.SetDirs(dir, exe)
	baseCtx, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	srv := &http.Server{
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return baseCtx }, // cancelling ends SSE streams on shutdown
	}
	sch.Start()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	url := "http://" + addr + "/"
	fmt.Println("ABC is running at " + url)
	slog.Info("listening", "url", url)
	if !*noBrowser {
		if err := sched.OpenURL(url); err != nil {
			slog.Warn("could not open the browser", "err", err)
		}
		// Settings > Library "Open the download folder when ABC starts". Never with
		// --no-browser: test and agent runs must not open windows.
		if c := set.Get(); c.Open.FolderOnStart {
			if dl := c.Library.DownloadDir(); dl != "" {
				if err := sch.OpenFolder(dl); err != nil {
					slog.Warn("could not open the download folder", "dir", dl, "err", err)
				}
			}
		}
	}

	sig, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-sig.Done():
		slog.Info("shutting down")
	case <-apiSrv.Quit(): // Settings > Quit ABC (abcw.exe has no console to press Ctrl-C in)
		slog.Info("quit from the web UI, shutting down")
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	cancelBase()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	sch.Stop()
	return nil
}
