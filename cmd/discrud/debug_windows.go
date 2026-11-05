//go:build windows

package main

import (
	"io"
	"os"

	"discrud/internal/netdebug"
	"golang.org/x/sys/windows"
)

// GUI-subsystem executables have no console by default. Attach to the launching
// terminal when possible, otherwise allocate a dedicated diagnostics console.
func startNetworkDebug(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return nil, err
	}
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	attached, _, _ := kernel.NewProc("AttachConsole").Call(uintptr(0xffffffff))
	if attached == 0 {
		kernel.NewProc("AllocConsole").Call()
	}
	var out io.Writer = file
	console, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err == nil {
		out = io.MultiWriter(file, console)
	}
	stop := netdebug.Enable(out)
	return func() {
		stop()
		if console != nil {
			console.Close()
		}
		file.Close()
	}, nil
}
