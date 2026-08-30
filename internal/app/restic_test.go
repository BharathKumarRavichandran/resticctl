package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	fmt.Print("test-password\n")
	os.Exit(0)
}

type helperResult struct {
	Arguments       []string `json:"arguments"`
	Password        string   `json:"password"`
	PasswordExisted bool     `json:"password_existed"`
}

func TestResticHelper(t *testing.T) {
	if os.Getenv("GO_WANT_RESTIC_HELPER") != "1" {
		return
	}
	arguments := os.Args
	for len(arguments) > 0 && arguments[0] != "--" {
		arguments = arguments[1:]
	}
	if len(arguments) > 0 {
		arguments = arguments[1:]
	}
	result := helperResult{Arguments: arguments}
	for index, argument := range arguments {
		if argument == "--password-file" && index+1 < len(arguments) {
			content, err := os.ReadFile(arguments[index+1])
			if err != nil {
				os.Exit(3)
			}
			result.Password = string(content)
			result.PasswordExisted = true
		}
	}
	encoded, _ := json.Marshal(result)
	if err := os.WriteFile(os.Getenv("RESTIC_HELPER_LOG"), encoded, 0o600); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

func TestTemporaryPasswordFileIsPrivateAndRemoved(t *testing.T) {
	t.Setenv("GO_WANT_PASSWORD_HELPER", "1")
	credentials := Credentials{Password: PasswordSource{Command: []string{os.Args[0], "-test.run=TestPasswordHelper"}}}
	path, temporary, err := preparePasswordFile(context.Background(), credentials)
	if err != nil {
		t.Fatal(err)
	}
	if !temporary {
		t.Fatal("password file was not temporary")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "test-password\n" {
		t.Fatalf("password content = %q", content)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordCommandErrorDoesNotRevealArguments(t *testing.T) {
	credentials := Credentials{Password: PasswordSource{Command: []string{"command-that-does-not-exist", "super-secret"}}}
	_, _, err := preparePasswordFile(context.Background(), credentials)
	if err == nil || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("error = %v", err)
	}
}

func TestPasswordCommandErrorDoesNotRevealStderr(t *testing.T) {
	t.Setenv("GO_WANT_PASSWORD_HELPER", "1")
	t.Setenv("PASSWORD_HELPER_FAIL", "1")
	credentials := Credentials{Password: PasswordSource{Command: []string{os.Args[0], "-test.run=TestPasswordHelper"}}}
	_, _, err := preparePasswordFile(context.Background(), credentials)
	if err == nil || strings.Contains(err.Error(), "secret from stderr") {
		t.Fatalf("error = %v", err)
	}
}

func TestResticUsesAndRemovesTemporaryPasswordFile(t *testing.T) {
	t.Setenv("GO_WANT_PASSWORD_HELPER", "1")
	logPath := filepath.Join(t.TempDir(), "restic.json")
	profile := Profile{
		Repository: "local:repository",
		ResticArgs: []string{"--no-cache"},
		Credentials: Credentials{
			Environment: map[string]string{
				"GO_WANT_RESTIC_HELPER": "1",
				"RESTIC_HELPER_LOG":     logPath,
			},
			Password: PasswordSource{Command: []string{os.Args[0], "-test.run=TestPasswordHelper"}},
		},
	}
	restic := &Restic{
		executable:      os.Args[0],
		prefixArguments: []string{"-test.run=TestResticHelper", "--"},
		stdin:           strings.NewReader(""),
		stdout:          io.Discard,
		stderr:          io.Discard,
	}
	if err := restic.Run(context.Background(), profile, []string{"snapshots"}, ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var result helperResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if !result.PasswordExisted || result.Password != "test-password\n" {
		t.Fatalf("helper result = %+v", result)
	}
	if strings.Contains(strings.Join(result.Arguments, " "), "test-password") {
		t.Fatalf("password leaked into arguments: %v", result.Arguments)
	}
	passwordIndex := -1
	for index, argument := range result.Arguments {
		if argument == "--password-file" {
			passwordIndex = index + 1
		}
	}
	if passwordIndex <= 0 || passwordIndex >= len(result.Arguments) {
		t.Fatalf("missing password file argument: %v", result.Arguments)
	}
	if _, err := os.Stat(result.Arguments[passwordIndex]); !os.IsNotExist(err) {
		t.Fatalf("temporary password file still exists: %v", err)
	}
}

func TestMergeEnvironmentDropsResticSelectors(t *testing.T) {
	environment := mergeEnvironment(
		[]string{"PATH=/bin", "RESTIC_PASSWORD=ambient-secret", "RESTIC_REPOSITORY=wrong"},
		map[string]string{"AWS_ACCESS_KEY_ID": "key"},
	)
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "RESTIC_PASSWORD") || strings.Contains(joined, "RESTIC_REPOSITORY") {
		t.Fatalf("managed restic environment leaked through: %v", environment)
	}
	if !strings.Contains(joined, "AWS_ACCESS_KEY_ID=key") {
		t.Fatalf("credential environment missing: %v", environment)
	}
}
