package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"

	"resticctl/internal/app"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, handledSignals()...)
	defer signal.Stop(signals)
	var signalCode atomic.Int32
	go func() {
		sig := <-signals
		signalCode.Store(int32(exitCodeForSignal(sig)))
		cancel()
	}()

	status, err := app.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(finalStatus(status, err, signalCode.Load(), os.Stderr))
}

func finalStatus(status int, err error, signalCode int32, stderr io.Writer) int {
	if signalCode != 0 {
		return int(signalCode)
	}
	if err != nil {
		fmt.Fprintf(stderr, "resticctl: %v\n", err)
		if status == 0 {
			return 1
		}
	}
	return status
}
