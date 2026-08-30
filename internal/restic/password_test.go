package restic

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"resticctl/internal/profile"
)

func TestPasswordHelper(t *testing.T) {
	if os.Getenv("GO_WANT_PASSWORD_HELPER") != "1" {
		return
	}
	if os.Getenv("PASSWORD_HELPER_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "secret from stderr")
		os.Exit(9)
	}
	fmt.Print("test-password\n")
	os.Exit(0)
}

func TestTemporaryPasswordFileIsPrivate(t *testing.T) {
	t.Setenv("GO_WANT_PASSWORD_HELPER", "1")
	credentials := profile.Credentials{Password: profile.PasswordSource{Command: []string{os.Args[0], "-test.run=TestPasswordHelper"}}}
	path, temporary, err := preparePasswordFile(context.Background(), credentials)
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
	credentials := profile.Credentials{Password: profile.PasswordSource{Command: []string{"command-that-does-not-exist", "super-secret"}}}
	_, _, err := preparePasswordFile(context.Background(), credentials)
	if err == nil || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("error = %v", err)
	}
}

func TestPasswordCommandErrorDoesNotRevealStderr(t *testing.T) {
	t.Setenv("GO_WANT_PASSWORD_HELPER", "1")
	t.Setenv("PASSWORD_HELPER_FAIL", "1")
	credentials := profile.Credentials{Password: profile.PasswordSource{Command: []string{os.Args[0], "-test.run=TestPasswordHelper"}}}
	_, _, err := preparePasswordFile(context.Background(), credentials)
	if err == nil || strings.Contains(err.Error(), "secret from stderr") {
		t.Fatalf("error = %v", err)
	}
}
