package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"sync/atomic"
	"time"

	"resticctl/internal/app"
	"resticctl/internal/cli"
	"resticctl/internal/restic"
	"resticctl/internal/schedule"
)

var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, handledSignals()...)
	defer signal.Stop(signals)
	var signalCode atomic.Int32
	go func() {
		sig := <-signals
		signalCode.Store(int32(exitCodeForSignal(sig)))
		signal.Stop(signals)
		cancel()
	}()

	status, err := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, cli.Dependencies{
		NewRunner:          func() (app.Runner, error) { return restic.New(os.Stdin, os.Stdout, os.Stderr) },
		NewScheduleManager: func() schedule.Manager { return schedule.NewManager() },
		Executable:         os.Executable,
		Now:                time.Now,
		Version:            buildVersion(),
	})
	return finalStatus(status, err, signalCode.Load(), os.Stderr)
}

func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
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
