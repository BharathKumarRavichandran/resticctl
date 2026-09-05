package restic

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestPasswordHelper(t *testing.T) {
	if os.Getenv("GO_WANT_PASSWORD_HELPER") != "1" {
		return
	}
	if os.Getenv("PASSWORD_HELPER_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "secret from stderr")
		os.Exit(9)
	}
	if os.Getenv("PASSWORD_HELPER_OVERSIZED") == "1" {
		fmt.Print(strings.Repeat("x", maximumPasswordBytes+1))
		os.Exit(0)
	}
	if os.Getenv("PASSWORD_HELPER_NUL") == "1" {
		fmt.Print("secret\x00value")
		os.Exit(0)
	}
	fmt.Print("test-password\n")
	os.Exit(0)
}

func TestPasswordCommandRejectsOversizedOutput(t *testing.T) {
	t.Setenv("GO_WANT_PASSWORD_HELPER", "1")
	t.Setenv("PASSWORD_HELPER_OVERSIZED", "1")
	config := Config{PasswordCommand: []string{os.Args[0], "-test.run=^TestPasswordHelper$"}}
	_, _, err := preparePasswordFile(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
		t.Fatalf("error = %v", err)
	}
}

func TestPasswordCommandRejectsNUL(t *testing.T) {
	t.Setenv("GO_WANT_PASSWORD_HELPER", "1")
	t.Setenv("PASSWORD_HELPER_NUL", "1")
	config := Config{PasswordCommand: []string{os.Args[0], "-test.run=^TestPasswordHelper$"}}
	_, _, err := preparePasswordFile(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("error = %v", err)
	}
}

func TestPasswordValueRejectsNUL(t *testing.T) {
	_, _, err := preparePasswordFile(context.Background(), Config{PasswordValue: "secret\x00value"})
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("error = %v", err)
	}
}

func TestTemporaryPasswordFileIsPrivate(t *testing.T) {
	t.Setenv("GO_WANT_PASSWORD_HELPER", "1")
	config := Config{PasswordCommand: []string{os.Args[0], "-test.run=TestPasswordHelper"}}
	path, temporary, err := preparePasswordFile(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if !temporary {
		t.Fatal("password file was not temporary")
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("password file mode = %o, want 600", mode)
		}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "test-password\n" {
		t.Fatalf("password content = %q", content)
	}
}

func TestPasswordCommandErrorDoesNotRevealArguments(t *testing.T) {
	config := Config{PasswordCommand: []string{"command-that-does-not-exist", "super-secret"}}
	_, _, err := preparePasswordFile(context.Background(), config)
	if err == nil || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("error = %v", err)
	}
}

func TestPasswordCommandErrorDoesNotRevealStderr(t *testing.T) {
	t.Setenv("GO_WANT_PASSWORD_HELPER", "1")
	t.Setenv("PASSWORD_HELPER_FAIL", "1")
	config := Config{PasswordCommand: []string{os.Args[0], "-test.run=TestPasswordHelper"}}
	_, _, err := preparePasswordFile(context.Background(), config)
	if err == nil || strings.Contains(err.Error(), "secret from stderr") {
		t.Fatalf("error = %v", err)
	}
}
