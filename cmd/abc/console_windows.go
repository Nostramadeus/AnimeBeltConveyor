package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	user32           = syscall.NewLazyDLL("user32.dll")
	getConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	messageBoxW      = user32.NewProc("MessageBoxW")
)

// hasConsole reports whether the process has a console window. abcw.exe is
// built with -H windowsgui and has none, so errors must be shown another way.
func hasConsole() bool {
	h, _, _ := getConsoleWindow.Call()
	return h != 0
}

// fatalBox shows a Windows message box (used only when there is no console).
func fatalBox(msg string) {
	const mbIconError = 0x10
	text, _ := syscall.UTF16PtrFromString(msg)
	title, _ := syscall.UTF16PtrFromString("AnimeBeltConveyor")
	messageBoxW.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbIconError)
}
