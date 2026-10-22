//go:build windows

package api

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// driveRoots lists every drive letter root (C:\, D:\, ...).
func driveRoots() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var out []string
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) != 0 {
			out = append(out, string(rune('A'+i))+`:\`)
		}
	}
	return out
}

// fsRoots is the roots view of GET /api/fs/list: every drive letter.
func fsRoots() []fsDir {
	var out []fsDir
	for _, r := range driveRoots() {
		out = append(out, fsDir{Name: r, Path: r, FreeBytes: freeOf(r)})
	}
	return out
}

func systemDrive() string {
	v := strings.ToUpper(strings.TrimSpace(os.Getenv("SystemDrive")))
	if len(v) != 2 || v[1] != ':' {
		v = "C:"
	}
	return v
}

// driveSuggestions: <X>:\Anime for every fixed drive other than the system
// drive, then <system drive>:\Anime (SPEC v1.1 section 3, /api/fs/suggest).
func driveSuggestions(string) []suggestion {
	sys := systemDrive()
	var out []suggestion
	for _, r := range driveRoots() {
		letter := r[:2]
		if strings.EqualFold(letter, sys) {
			continue
		}
		ptr, err := windows.UTF16PtrFromString(r)
		if err != nil || windows.GetDriveType(ptr) != windows.DRIVE_FIXED {
			continue
		}
		out = append(out, suggestion{Path: filepath.Join(r, "Anime"), Label: "Drive " + letter})
	}
	return append(out, suggestion{Path: filepath.Join(sys+`\`, "Anime"), Label: "Drive " + sys + " (system drive)"})
}

// hiddenSystem reports entries Explorer hides as protected operating system
// files (hidden + system attributes), e.g. the legacy "Application Data" junctions.
func hiddenSystem(fi fs.FileInfo) bool {
	d, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false
	}
	return d.FileAttributes&windows.FILE_ATTRIBUTE_HIDDEN != 0 && d.FileAttributes&windows.FILE_ATTRIBUTE_SYSTEM != 0
}

// driveRootSkips are skipped when listing a drive root.
var driveRootSkips = []string{"Windows", "Program Files", "Program Files (x86)", "ProgramData", "Recovery", "PerfLogs"}
