package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"sync/atomic"
	"time"

	"resticctl/internal/app"
	"resticctl/internal/cli"
	"resticctl/internal/process"
	"resticctl/internal/profile"
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
		NewRunner:          newRunner,
		NewScheduleManager: func() schedule.Manager { return schedule.NewManager() },
		Executable:         os.Executable,
		Now:                time.Now,
		Version:            buildVersion(),
	})
	return finalStatus(status, err, signalCode.Load(), os.Stderr)
}

type applicationRunner struct {
	*restic.Client
	*process.Executor
}

func (runner *applicationRunner) RunStream(ctx context.Context, config restic.Config, arguments []string, cwd string, producer []string) (restic.Result, error) {
	if len(producer) == 0 {
		return runner.Client.RunWithInput(ctx, config, arguments, cwd, os.Stdin)
	}
	var result restic.Result
	producerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var producerErr, consumerErr error
	err := process.Pipe(
		func(output io.Writer) error {
			producerErr = runner.Executor.RunProducer(producerCtx, producer, output)
			return producerErr
		},
		func(input io.Reader) error {
			defer cancel()
			result, consumerErr = runner.Client.RunWithInput(ctx, config, arguments, cwd, input)
			return consumerErr
		},
	)
	if consumerErr != nil && ctx.Err() == nil {
		return result, errors.Join(consumerErr, withoutStreamCancellation(producerErr))
	}
	return result, err
}

// Internal producer cancellation must not turn a Restic failure into a cancelled run.
func withoutStreamCancellation(err error) error {
	if err == context.Canceled {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		remaining := make([]error, len(causes))
		for index, cause := range causes {
			remaining[index] = withoutStreamCancellation(cause)
		}
		return errors.Join(remaining...)
	}
	return err
}

func newRunner() (app.Runner, error) {
	client, err := restic.New(os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		return nil, err
	}
	return &applicationRunner{
		Client:   client,
		Executor: process.NewExecutor(os.Stdin, os.Stdout, os.Stderr, profile.IsReservedEnvironment),
	}, nil
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
