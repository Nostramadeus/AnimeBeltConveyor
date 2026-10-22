// Package scan finds video files under the library folders: a full walk, and an
// fsnotify watcher that reports changes in debounced batches. It knows nothing
// about the database; callers turn FileSeen / gone paths into files rows.
package scan

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileSeen is one video file found on disk.
type FileSeen struct {
	Path  string
	Size  int64
	Mtime int64 // unix seconds
}

// ExtSet builds a lookup set from extensions like "mkv" or ".MKV".
func ExtSet(exts []string) map[string]bool {
	m := make(map[string]bool, len(exts))
	for _, e := range exts {
		e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "."))
		if e != "" {
			m[e] = true
		}
	}
	return m
}

// HasExt reports whether path's extension is in set.
func HasExt(path string, set map[string]bool) bool {
	return set[strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))]
}

// SkipDir reports whether a directory name is one the scanner never enters:
// Windows system folders ($RECYCLE.BIN, System Volume Information) and dot
// folders. Watching or walking them only yields access-denied noise.
func SkipDir(name string) bool {
	return strings.HasPrefix(name, "$") || strings.HasPrefix(name, ".") || name == "System Volume Information"
}

// Dir is one folder to scan. MaxDepth is how many folder levels below Path are
// entered: 0 = Path only, 1 = Path and its direct subfolders, -1 = unlimited.
type Dir struct {
	Path     string
	MaxDepth int
}

// allows reports whether files directly inside folder p are within d's depth.
// p must be d.Path or inside it.
func (d Dir) allows(p string) bool {
	return d.MaxDepth < 0 || depthBelow(d.Path, p) <= d.MaxDepth
}

// Covers reports whether the file at path is inside d and no deeper than MaxDepth.
func (d Dir) Covers(path string) bool {
	if !Under(path, d.Path) || filepath.Clean(path) == filepath.Clean(d.Path) {
		return false
	}
	return d.allows(filepath.Dir(filepath.Clean(path)))
}

// Covered reports whether any of dirs covers the file at path.
func Covered(path string, dirs []Dir) bool {
	for _, d := range dirs {
		if d.Covers(path) {
			return true
		}
	}
	return false
}

// Paths returns the Path of every dir, in order.
func Paths(dirs []Dir) []string {
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, d.Path)
	}
	return out
}

// Walk walks every dir down to its MaxDepth and calls fn for each regular file
// with a video extension. Files deeper than MaxDepth are not seen and deeper
// folders are not entered. It returns the paths of the dirs whose top level
// could be read: callers mark files missing only under those (an unplugged
// drive must not mark the whole library missing). Unreadable subfolders are
// skipped and logged.
func Walk(ctx context.Context, dirs []Dir, exts []string, fn func(FileSeen)) (walked []string, err error) {
	set := ExtSet(exts)
	for _, dd := range dirs {
		dir := dd.Path
		if dir == "" {
			continue
		}
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() {
			slog.Warn("scan: folder not available", "dir", dir, "err", err)
			continue
		}
		werr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			if err != nil {
				if p == dir {
					return err
				}
				slog.Warn("scan: skipping", "path", p, "err", err)
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if p != dir && (SkipDir(d.Name()) || !dd.allows(p)) {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || !HasExt(p, set) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			fn(FileSeen{Path: p, Size: info.Size(), Mtime: info.ModTime().Unix()})
			return nil
		})
		if errors.Is(werr, context.Canceled) || errors.Is(werr, context.DeadlineExceeded) {
			return walked, werr
		}
		if werr != nil {
			slog.Warn("scan: walk failed", "dir", dir, "err", werr)
			continue
		}
		walked = append(walked, dir)
	}
	return walked, nil
}

// Watcher watches folders recursively and reports changes in batches after
// Debounce of quiet (at most MaxWait after the first change of a batch).
type Watcher struct {
	Debounce time.Duration
	MaxWait  time.Duration

	exts   map[string]bool
	onSeen func(FileSeen)
	onGone func(path string)
	fw     *fsnotify.Watcher
	roots  []Dir
	done   chan struct{}
	wg     sync.WaitGroup
}

// WatchDepth is the most folder levels below a library folder that get a live
// watch: the folder itself, its show folders and one level inside them. Every
// watched folder costs a kernel buffer (a whole drive would be thousands), and
// the periodic full rescan catches anything deeper. A folder's own scan depth
// lowers it further (watchDepth).
const WatchDepth = 2

// watchDepth is the live watch depth of d: min(MaxDepth, WatchDepth), deep = WatchDepth.
func watchDepth(d Dir) int {
	if d.MaxDepth < 0 || d.MaxDepth > WatchDepth {
		return WatchDepth
	}
	return d.MaxDepth
}

// watchBufferSize is the per-folder buffer on Windows (fsnotify default 64 KB).
const watchBufferSize = 16 * 1024

// depthBelow returns how many levels p is below root (0 = root itself).
func depthBelow(root, p string) int {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

// remainingDepth returns how many more levels may be watched below p (the most
// any root containing p allows), or -1 when p is not under any root or already
// too deep.
func (w *Watcher) remainingDepth(p string) int {
	best := -1
	for _, r := range w.roots {
		if Under(p, r.Path) {
			best = max(best, watchDepth(r)-depthBelow(r.Path, p))
		}
	}
	return max(best, -1)
}

// allows reports whether files directly inside folder p are within the scan
// depth of a root that contains p.
func (w *Watcher) allows(p string) bool {
	for _, r := range w.roots {
		if Under(p, r.Path) && r.allows(p) {
			return true
		}
	}
	return false
}

// NewWatcher starts watching dirs (missing dirs are skipped), each down to
// min(MaxDepth, WatchDepth) levels. onSeen is called for video files that
// appeared or changed within a dir's MaxDepth (including every such file inside
// a new folder); onGone for paths that vanished (a file or a whole folder).
// Callbacks run on the watcher goroutine, one batch at a time.
func NewWatcher(dirs []Dir, exts []string, debounce time.Duration, onSeen func(FileSeen), onGone func(path string)) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if debounce <= 0 {
		debounce = 3 * time.Second
	}
	w := &Watcher{Debounce: debounce, MaxWait: 10 * debounce, exts: ExtSet(exts), onSeen: onSeen, onGone: onGone, fw: fw, done: make(chan struct{})}
	for _, d := range dirs {
		if d.Path == "" {
			continue
		}
		if fi, err := os.Stat(d.Path); err != nil || !fi.IsDir() {
			continue
		}
		d.Path = filepath.Clean(d.Path)
		w.roots = append(w.roots, d)
		w.addTree(d.Path, watchDepth(d))
	}
	w.wg.Add(1)
	go w.loop()
	return w, nil
}

// Close stops the watcher and waits for the loop to exit.
func (w *Watcher) Close() error {
	select {
	case <-w.done:
	default:
		close(w.done)
	}
	err := w.fw.Close()
	w.wg.Wait()
	return err
}

// addTree watches root and its subfolders down to maxDepth levels below it.
func (w *Watcher) addTree(root string, maxDepth int) {
	if maxDepth < 0 {
		return
	}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != root {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != root && SkipDir(d.Name()) {
				return fs.SkipDir
			}
			if depthBelow(root, p) > maxDepth {
				return fs.SkipDir
			}
			if err := w.fw.AddWith(p, fsnotify.WithBufferSize(watchBufferSize)); err != nil {
				slog.Warn("scan: cannot watch", "dir", p, "err", err)
			}
		}
		return nil
	})
}

func (w *Watcher) loop() {
	defer w.wg.Done()
	pending := map[string]struct{}{}
	var first time.Time
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		select {
		case <-w.done:
			timer.Stop()
			return
		case ev, ok := <-w.fw.Events:
			if !ok {
				return
			}
			if !w.relevant(ev) {
				continue
			}
			if len(pending) == 0 {
				first = time.Now()
			}
			pending[ev.Name] = struct{}{}
			wait := w.Debounce
			if left := w.MaxWait - time.Since(first); left < wait {
				wait = max(left, 0)
			}
			timer.Reset(wait)
		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			slog.Warn("scan: watcher error", "err", err)
		case <-timer.C:
			batch := pending
			pending = map[string]struct{}{}
			w.flush(batch)
		}
	}
}

// relevant filters out events that cannot matter: chmod, and creates/writes of
// regular files without a video extension (e.g. qBittorrent's .!qB part files).
func (w *Watcher) relevant(ev fsnotify.Event) bool {
	if ev.Op == fsnotify.Chmod {
		return false
	}
	if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
		return true
	}
	fi, err := os.Stat(ev.Name)
	if err != nil {
		return true // vanished already: report as gone
	}
	return fi.IsDir() || HasExt(ev.Name, w.exts)
}

func (w *Watcher) flush(batch map[string]struct{}) {
	for p := range batch {
		fi, err := os.Stat(p)
		switch {
		case err != nil:
			if w.onGone != nil {
				w.onGone(p)
			}
		case fi.IsDir():
			w.addTree(p, w.remainingDepth(p))
			filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
				if err == nil && d.IsDir() && ((q != p && SkipDir(d.Name())) || !w.allows(q)) {
					return fs.SkipDir
				}
				if err != nil || d.IsDir() || !d.Type().IsRegular() || !HasExt(q, w.exts) {
					return nil
				}
				if info, err := d.Info(); err == nil && w.onSeen != nil {
					w.onSeen(FileSeen{Path: q, Size: info.Size(), Mtime: info.ModTime().Unix()})
				}
				return nil
			})
		case fi.Mode().IsRegular() && HasExt(p, w.exts) && w.allows(filepath.Dir(p)):
			if w.onSeen != nil {
				w.onSeen(FileSeen{Path: p, Size: fi.Size(), Mtime: fi.ModTime().Unix()})
			}
		}
	}
}

// Under reports whether path is root itself or inside root. filepath.Rel compares
// path elements case-insensitively on Windows.
func Under(path, root string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}
