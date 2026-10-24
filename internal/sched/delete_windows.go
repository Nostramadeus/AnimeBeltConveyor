//go:build windows

package sched

import (
	"errors"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const episodeDeletionSupported = true

func unsafeReparse(info os.FileInfo) bool {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return info.Mode()&os.ModeSymlink != 0 || ok && data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

type pinnedDelete struct {
	path    string
	file    *os.File
	parents []*os.File
}

func openPinned(path string, access uint32) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		file.Close()
		return nil, errors.New("reparse point or unreadable handle")
	}
	return file, nil
}

func pinDelete(target deleteTarget) (pinnedDelete, error) {
	pinned := pinnedDelete{path: target.path}
	fail := func(err error) (pinnedDelete, error) { pinned.close(); return pinnedDelete{}, err }
	for _, parent := range target.parents {
		file, err := openPinned(parent.path, windows.FILE_READ_ATTRIBUTES)
		if err != nil {
			return fail(err)
		}
		pinned.parents = append(pinned.parents, file)
		info, err := file.Stat()
		if err != nil || !info.IsDir() || !os.SameFile(info, parent.info) {
			return fail(errors.New("parent identity changed"))
		}
	}
	file, err := openPinned(target.path, windows.DELETE|windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return fail(err)
	}
	pinned.file = file
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(info, target.info) || info.Size() != target.info.Size() || !info.ModTime().Equal(target.info.ModTime()) {
		return fail(errors.New("file identity changed"))
	}
	return pinned, nil
}

func (target pinnedDelete) remove() error {
	disposition := byte(1)
	err := windows.SetFileInformationByHandle(windows.Handle(target.file.Fd()), windows.FileDispositionInfo, &disposition, uint32(unsafe.Sizeof(disposition)))
	if err == nil {
		err = target.file.Close()
	}
	return err
}

func (target pinnedDelete) close() {
	if target.file != nil {
		target.file.Close()
	}
	for index := len(target.parents) - 1; index >= 0; index-- {
		target.parents[index].Close()
	}
}
