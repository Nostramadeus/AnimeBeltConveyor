//go:build windows

package diskfree

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

func volumeFree(dir string) (int64, error) {
	ptr, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	return int64(free), nil
}

func volumeName(dir string) string {
	if v := filepath.VolumeName(dir); v != "" {
		return v
	}
	return dir
}
