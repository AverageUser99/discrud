//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"

	"discrud/internal/ui"
	"github.com/lxn/walk"
)

func main() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	demo := flag.Bool("demo", false, "run entirely offline with sample conversations")
	smoke := flag.Bool("smoke", false, "exercise native controls in demo mode, write dist/smoke.json, then exit")
	data := flag.String("data-dir", "", "override settings/cache directory (session remains in Windows Credential Manager)")
	debug := flag.Bool("debug", false, "show network counters in a console and write a sanitized local log")
	debugLog := flag.String("debug-log", "discrud-network-debug.log", "network debug log path (used with --debug; overwritten each run)")
	flag.Parse()
	if *smoke && !*demo {
		fmt.Fprintln(os.Stderr, "--smoke requires --demo; refusing to touch a live account")
		os.Exit(2)
	}
	stopDebug := func() {}
	if *debug {
		stop, err := startNetworkDebug(*debugLog)
		if err != nil {
			walk.MsgBox(nil, "Could not enable network debug", err.Error(), walk.MsgBoxIconError)
			os.Exit(2)
		}
		stopDebug = stop
		defer stopDebug()
	}
	// Register after attaching the debug console: AttachConsole resets handlers.
	interrupt, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()
	if err := ui.Run(ui.Options{Demo: *demo, Smoke: *smoke, DataDir: *data, Shutdown: interrupt.Done()}); err != nil {
		fmt.Fprintln(os.Stderr, "Discrud:", err)
		walk.MsgBox(nil, "Discrud could not start", err.Error(), walk.MsgBoxIconError)
		stopDebug()
		os.Exit(1)
	}
}
