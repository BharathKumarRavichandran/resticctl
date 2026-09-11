package configlock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestWithRejectsConcurrentUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.lock")
	err := With(path, func() error {
		return With(path, func() error {
			t.Fatal("concurrent update callback ran")
			return nil
		})
	})
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("error = %v, want ErrLocked", err)
	}
}
