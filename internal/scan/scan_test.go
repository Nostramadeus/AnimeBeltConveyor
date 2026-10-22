package scan

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func write(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWalk(t *testing.T) {
	root, extra := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "Frieren", "[SubsPlease] Frieren - 01 (1080p).mkv"))
	write(t, filepath.Join(root, "Frieren", "[SubsPlease] Frieren - 02 (1080p).MKV"))
	write(t, filepath.Join(root, "Frieren", "notes.txt"))
	write(t, filepath.Join(root, "Frieren", "part.mkv.!qB"))
	write(t, filepath.Join(extra, "deep", "a", "b", "movie.mp4"))
	missing := filepath.Join(root, "unplugged")

	var got []string
	walked, err := Walk(context.Background(), []Dir{{root, 1}, {extra, -1}, {missing, -1}, {"", -1}}, []string{"mkv", ".mp4"}, func(f FileSeen) {
		if f.Size != 5 || f.Mtime == 0 {
			t.Errorf("bad FileSeen %+v", f)
		}
		got = append(got, filepath.Base(f.Path))
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"[SubsPlease] Frieren - 01 (1080p).mkv", "[SubsPlease] Frieren - 02 (1080p).MKV", "movie.mp4"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("got %v", got)
	}
	if len(walked) != 2 {
		t.Fatalf("walked = %v, want root and extra only", walked)
	}
}

func TestUnder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "lib")
	if !Under(filepath.Join(root, "a", "b.mkv"), root) || !Under(root, root) {
		t.Fatal("inside not detected")
	}
	if Under(filepath.Join(root+"x", "b.mkv"), root) || Under(filepath.Dir(root), root) || Under("x", "") {
		t.Fatal("outside reported as inside")
	}
}

type collector struct {
	mu   sync.Mutex
	seen map[string]int
	gone map[string]int
}

func (c *collector) onSeen(f FileSeen) { c.mu.Lock(); c.seen[f.Path]++; c.mu.Unlock() }
func (c *collector) onGone(p string)   { c.mu.Lock(); c.gone[p]++; c.mu.Unlock() }

func (c *collector) wait(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		ok := cond()
		c.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out; seen=%v gone=%v", c.seen, c.gone)
}

func TestWatcherDebouncedBatches(t *testing.T) {
	root := t.TempDir()
	c := &collector{seen: map[string]int{}, gone: map[string]int{}}
	w, err := NewWatcher([]Dir{{root, -1}, {filepath.Join(root, "nope"), -1}}, []string{"mkv"}, 150*time.Millisecond, c.onSeen, c.onGone)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// New folder with a file inside: the folder is walked on flush and watched afterwards.
	sub := filepath.Join(root, "Show")
	f1 := filepath.Join(sub, "Show - 01.mkv")
	write(t, f1)
	write(t, filepath.Join(sub, "ignore.txt"))
	c.wait(t, func() bool { return c.seen[f1] >= 1 })
	if c.seen[filepath.Join(sub, "ignore.txt")] != 0 {
		t.Fatal("non-video file reported")
	}

	// A file in the now-watched subfolder.
	f2 := filepath.Join(sub, "Show - 02.mkv")
	write(t, f2)
	c.wait(t, func() bool { return c.seen[f2] >= 1 })

	// Removal is reported as gone.
	if err := os.Remove(f2); err != nil {
		t.Fatal(err)
	}
	c.wait(t, func() bool { return c.gone[f2] >= 1 })
}

// depthTree builds root/a.mkv, root/Show/b.mkv, root/Show/S1/c.mkv,
// root/Show/S1/x/d.mkv and a video inside a skipped system folder.
func depthTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "a.mkv"))
	write(t, filepath.Join(root, "Show", "b.mkv"))
	write(t, filepath.Join(root, "Show", "S1", "c.mkv"))
	write(t, filepath.Join(root, "Show", "S1", "x", "d.mkv"))
	write(t, filepath.Join(root, "$RECYCLE.BIN", "e.mkv"))
	write(t, filepath.Join(root, "Show", ".hidden", "f.mkv"))
	return root
}

func walkNames(t *testing.T, dirs []Dir) []string {
	t.Helper()
	var got []string
	walked, err := Walk(context.Background(), dirs, []string{"mkv"}, func(f FileSeen) { got = append(got, filepath.Base(f.Path)) })
	if err != nil {
		t.Fatal(err)
	}
	if len(walked) != len(dirs) {
		t.Fatalf("walked = %v", walked)
	}
	sort.Strings(got)
	return got
}

func TestWalkDepth(t *testing.T) {
	root := depthTree(t)
	for _, tc := range []struct {
		depth int
		want  string
	}{
		{0, "a.mkv"},
		{1, "a.mkv b.mkv"},
		{2, "a.mkv b.mkv c.mkv"},
		{-1, "a.mkv b.mkv c.mkv d.mkv"},
	} {
		got := strings.Join(walkNames(t, []Dir{{root, tc.depth}}), " ")
		if got != tc.want {
			t.Errorf("depth %d: got %q, want %q", tc.depth, got, tc.want)
		}
	}
	// A nested folder with its own depth adds files the outer folder's depth misses.
	got := strings.Join(walkNames(t, []Dir{{root, 0}, {filepath.Join(root, "Show", "S1"), 0}}), " ")
	if got != "a.mkv c.mkv" {
		t.Errorf("nested: got %q", got)
	}
}

func TestDirCovers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "lib")
	in := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }
	for _, tc := range []struct {
		depth int
		path  string
		want  bool
	}{
		{0, in("a.mkv"), true},
		{0, in("Show", "b.mkv"), false},
		{1, in("Show", "b.mkv"), true},
		{1, in("Show", "S1", "c.mkv"), false},
		{-1, in("Show", "S1", "x", "d.mkv"), true},
		{-1, root, false},
		{-1, filepath.Join(root+"x", "a.mkv"), false},
	} {
		if got := (Dir{root, tc.depth}).Covers(tc.path); got != tc.want {
			t.Errorf("depth %d %s: got %v", tc.depth, tc.path, got)
		}
	}
	if !Covered(in("Show", "S1", "c.mkv"), []Dir{{root, 0}, {in("Show"), 1}}) || Covered(in("Show", "b.mkv"), []Dir{{root, 0}}) {
		t.Fatal("Covered wrong")
	}
}

func TestWatchDepthCap(t *testing.T) {
	for _, tc := range []struct{ max, want int }{{0, 0}, {1, 1}, {2, 2}, {5, 2}, {-1, WatchDepth}} {
		if got := watchDepth(Dir{"x", tc.max}); got != tc.want {
			t.Errorf("watchDepth(%d) = %d, want %d", tc.max, got, tc.want)
		}
	}
}

// watched returns the folders the watcher has a live watch on, relative to root.
func watched(w *Watcher, root string) []string {
	var out []string
	for _, p := range w.fw.WatchList() {
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out
}

// The live watch goes no deeper than min(scan depth, WatchDepth).
func TestWatcherWatchesToScanDepth(t *testing.T) {
	root := depthTree(t)
	for _, tc := range []struct {
		depth int
		want  string
	}{
		{0, "."},
		{1, ". Show"},
		{-1, ". Show Show/S1"},
	} {
		w, err := NewWatcher([]Dir{{root, tc.depth}}, []string{"mkv"}, time.Second, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Join(watched(w, root), " ")
		w.Close()
		if got != tc.want {
			t.Errorf("depth %d: watching %q, want %q", tc.depth, got, tc.want)
		}
	}
}

// New files and folders are reported only within the folder's scan depth.
func TestWatcherRespectsScanDepth(t *testing.T) {
	flat, shows, deep := t.TempDir(), t.TempDir(), t.TempDir()
	c := &collector{seen: map[string]int{}, gone: map[string]int{}}
	w, err := NewWatcher([]Dir{{flat, 0}, {shows, 1}, {deep, -1}}, []string{"mkv"}, 150*time.Millisecond, c.onSeen, c.onGone)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	tooDeepFlat := filepath.Join(flat, "Show", "b.mkv")
	tooDeepShows := filepath.Join(shows, "Show", "S1", "c.mkv")
	write(t, tooDeepFlat)
	write(t, tooDeepShows)
	okFlat := filepath.Join(flat, "a.mkv")
	okShows := filepath.Join(shows, "Show", "b.mkv")
	okDeep := filepath.Join(deep, "Show", "S1", "x", "d.mkv") // below the watch cap: found by walking the new folder
	write(t, okFlat)
	write(t, okShows)
	write(t, okDeep)
	c.wait(t, func() bool { return c.seen[okFlat] >= 1 && c.seen[okShows] >= 1 && c.seen[okDeep] >= 1 })
	// Let a further batch pass so late events for the too-deep files would have arrived.
	marker := filepath.Join(flat, "marker.mkv")
	write(t, marker)
	c.wait(t, func() bool { return c.seen[marker] >= 1 })
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen[tooDeepFlat] != 0 || c.seen[tooDeepShows] != 0 {
		t.Fatalf("files deeper than the scan depth reported: %v", c.seen)
	}
}
