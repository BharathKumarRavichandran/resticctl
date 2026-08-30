//go:build !windows

package main

import (
	"os"
	"syscall"
)

func handledSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGHUP, syscall.SIGTERM}
}

func exitCodeForSignal(signal os.Signal) int {
	if value, ok := signal.(syscall.Signal); ok {
		return 128 + int(value)
	}
	return 1
}
