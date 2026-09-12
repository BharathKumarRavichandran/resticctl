package secretvalue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileRejectsOversizedSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, make([]byte, MaximumBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(path); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("ReadFile() error = %v, want ErrTooLarge", err)
	}
}

func TestRunCommandRejectsEmptyArguments(t *testing.T) {
	if _, err := RunCommand(context.Background(), nil); err == nil {
		t.Fatal("RunCommand() succeeded with no arguments")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want error
	}{
		{name: "valid", data: []byte("secret\n")},
		{name: "empty", data: []byte("\r\n"), want: ErrEmpty},
		{name: "nul", data: []byte("sec\x00ret"), want: ErrNUL},
		{name: "oversized", data: make([]byte, MaximumBytes+1), want: ErrTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := Validate(test.data); !errors.Is(err, test.want) {
				t.Fatalf("Validate() error = %v, want %v", err, test.want)
			}
		})
	}
}
