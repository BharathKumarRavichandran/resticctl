package securefile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestTemporaryIsPrivateBeforeWritingAndCleanupIsIdempotent(t *testing.T) {
	file, err := CreateTemp(t.TempDir(), "secret-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Cleanup()
	info, err := os.Stat(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("temporary file mode = %o", info.Mode().Perm())
	}
	if _, err := file.Write([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := file.Cleanup(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(file.Name()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary file remains: %v", err)
	}
}

func TestTemporaryCleanupRemovesFileEvenWhenCloseFails(t *testing.T) {
	file, err := CreateTemp(t.TempDir(), "secret-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Cleanup()
	// Simulate a failed close without making the filesystem unavailable.
	if err := file.file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Cleanup(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("close failure lost: %v", err)
	}
	if _, err := os.Stat(file.Name()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup skipped removal after close failure: %v", err)
	}
}

func TestWriteTemporaryRejectsOversizedDataWithoutCreatingFile(t *testing.T) {
	directory := t.TempDir()
	if _, err := WriteTemporary(directory, "secret-*", []byte("oversized"), 3); err == nil {
		t.Fatal("oversized secret accepted")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed write left files: %v", entries)
	}
	path, err := WriteTemporary(directory, "secret-*", []byte("ok"), 3)
	if err != nil {
		t.Fatal(err)
	}
	defer Remove(path)
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "ok" {
		t.Fatalf("content = %q, error = %v", contents, err)
	}
}

func TestWriteNewPreservesExistingFileAndSymlink(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "secret")
	if err := WriteNew(path, []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := WriteNew(path, []byte("replacement")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing file accepted: %v", err)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(directory, "link")
		if err := os.Symlink(path, link); err != nil {
			t.Fatal(err)
		}
		if err := WriteNew(link, []byte("replacement")); !errors.Is(err, os.ErrExist) {
			t.Fatalf("existing symlink accepted: %v", err)
		}
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "original" {
		t.Fatalf("existing file changed: %q, %v", contents, err)
	}
}

func TestTemporaryCleanupPreservesCloseErrorAndStopsAfterRemoval(t *testing.T) {
	file, err := CreateTemp(t.TempDir(), "secret-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Cleanup()
	if err := file.file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("expected close failure: %v", err)
	}
	if err := file.Cleanup(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("cleanup discarded prior close failure: %v", err)
	}
	if err := os.WriteFile(file.Name(), []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := file.Cleanup(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("repeated cleanup discarded close failure: %v", err)
	}
	contents, err := os.ReadFile(file.Name())
	if err != nil || string(contents) != "replacement" {
		t.Fatalf("repeated cleanup removed a replacement file: %q, %v", contents, err)
	}
}

func TestTemporaryCleanupRetriesFailedRemoval(t *testing.T) {
	file, err := CreateTemp(t.TempDir(), "secret-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Cleanup()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file.Name()); err != nil {
		t.Fatal(err)
	}
	// A non-empty directory reliably forces removal to fail on every platform.
	if err := os.Mkdir(file.Name(), 0o700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(file.Name(), "blocker")
	if err := os.WriteFile(child, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := file.Cleanup(); err == nil {
		t.Fatal("removal failure was discarded")
	}
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	if err := file.Cleanup(); err != nil {
		t.Fatalf("failed removal was not retried: %v", err)
	}
}
