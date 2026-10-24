//go:build !windows

package sched

import (
	"errors"
	"os"
)

const episodeDeletionSupported = false

func unsafeReparse(info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }

type pinnedDelete struct {
	path string
}

func pinDelete(target deleteTarget) (pinnedDelete, error) {
	return pinnedDelete{}, errors.New("episode deletion is currently supported only on Windows")
}

func (target pinnedDelete) remove() error {
	return errors.New("episode deletion is currently supported only on Windows")
}

func (target pinnedDelete) close() {}
