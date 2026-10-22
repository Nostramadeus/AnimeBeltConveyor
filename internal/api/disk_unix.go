//go:build unix

package api

import (
	"io/fs"
	"os"
	"path/filepath"
)

// fsRoots is the roots view of GET /api/fs/list: "/" plus $HOME, /mnt, /media,
// /Volumes when they exist.
func fsRoots() []fsDir {
	cands := []string{"/"}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		cands = append(cands, h)
	}
	cands = append(cands, "/mnt", "/media", "/Volumes")
	var out []fsDir
	for _, p := range cands {
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			continue
		}
		out = append(out, fsDir{Name: p, Path: p, FreeBytes: freeOf(p)})
	}
	return out
}

// driveSuggestions: $HOME/Anime instead of the Windows drive rows.
func driveSuggestions(home string) []suggestion {
	if home == "" {
		return nil
	}
	return []suggestion{{Path: filepath.Join(home, "Anime"), Label: "Your home folder"}}
}

func hiddenSystem(fs.FileInfo) bool { return false }

var driveRootSkips []string
