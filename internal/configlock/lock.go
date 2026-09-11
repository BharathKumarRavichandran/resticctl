package configlock

import (
	"errors"
	"fmt"
)

var ErrLocked = errors.New("configuration is being updated by another process")

// With holds an exclusive cross-process lock while update runs.
func With(path string, update func() error) (err error) {
	release, err := acquire(path)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("cannot release configuration lock: %w", releaseErr))
		}
	}()
	return update()
}
