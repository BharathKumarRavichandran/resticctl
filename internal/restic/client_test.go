package restic

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestResticUsesAndRemovesTemporaryPasswordFile(t *testing.T) {
	t.Setenv("GO_WANT_PASSWORD_HELPER", "1")
	logPath := filepath.Join(t.TempDir(), "restic.json")
	config := Config{
		Repository: "local:repository",
		Arguments:  []string{"--no-cache"},
		Environment: map[string]string{
			"GO_WANT_RESTIC_HELPER": "1",
			"RESTIC_HELPER_LOG":     logPath,
		},
		PasswordCommand: []string{os.Args[0], "-test.run=TestPasswordHelper"},
	}
	client := &Client{
		executable:      os.Args[0],
		prefixArguments: []string{"-test.run=TestResticHelper", "--"},
		stdin:           strings.NewReader(""),
		stdout:          io.Discard,
		stderr:          io.Discard,
	}
	if err := client.Run(context.Background(), config, []string{"snapshots"}, ""); err != nil {
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
