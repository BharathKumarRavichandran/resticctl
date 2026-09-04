package process

import (
	"context"
	"errors"
	"os/exec"
)

// Run starts command in an isolated process tree and terminates the whole tree
// when ctx is cancelled.
func Run(ctx context.Context, command *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := prepareTree(command); err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	tree, err := superviseTree(command.Process)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		return errors.Join(err, tree.close())
	case <-ctx.Done():
		killErr := tree.kill()
		<-done
		return errors.Join(ctx.Err(), killErr, tree.close())
	}
}
