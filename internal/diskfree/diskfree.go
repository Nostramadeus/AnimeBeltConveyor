// Package diskfree reads the free space of the volume holding a path
// (SPEC v1.1 section 10). The path does not have to exist yet: the nearest
// existing ancestor decides the volume.
package diskfree

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrNoVolume is returned when neither the path nor any ancestor exists.
var ErrNoVolume = errors.New("no existing folder on the path")

// Free returns the bytes free for the current user on the volume that holds
// path, read at the nearest existing ancestor folder (path itself when it is
// an existing folder; a file's own folder when path is a file).
func Free(path string) (int64, error) {
	dir, err := Existing(path)
	if err != nil {
		return 0, err
	}
	return volumeFree(dir)
}

// Existing returns the nearest existing folder at or above path.
func Existing(path string) (string, error) {
	if path == "" {
		return "", ErrNoVolume
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p, nil
		}
		up := filepath.Dir(p)
		if up == p {
			return "", ErrNoVolume
		}
		p = up
	}
}

// Volume returns a short name for the volume holding path, for messages:
// "F:" on Windows (or the UNC share root), the nearest existing folder elsewhere.
func Volume(path string) string {
	dir, err := Existing(path)
	if err != nil {
		dir = path
	}
	return volumeName(dir)
}
