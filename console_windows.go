//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const swHide = 0

var (
	kernel32              = windows.NewLazySystemDLL("kernel32.dll")
	user32                = windows.NewLazySystemDLL("user32.dll")
	getConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
	getConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	showWindow            = user32.NewProc("ShowWindow")
)

func prepareGUIConsole() {
	var processID uint32
	count, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&processID)), 1)
	if count != 1 {
		// A terminal launched us. Never hide the user's PowerShell, CMD, or
		// Windows Terminal window.
		return
	}
	window, _, _ := getConsoleWindow.Call()
	if window != 0 {
		showWindow.Call(window, swHide)
	}
}
