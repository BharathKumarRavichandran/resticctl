package restic

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type helperResult struct {
	Arguments       []string `json:"arguments"`
	InputBytes      int      `json:"input_bytes"`
	Password        string   `json:"password"`
	PasswordExisted bool     `json:"password_existed"`
	StdoutIsPipe    bool     `json:"stdout_is_pipe"`
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
	stdoutInfo, _ := os.Stdout.Stat()
	result := helperResult{Arguments: arguments, StdoutIsPipe: stdoutInfo != nil && stdoutInfo.Mode()&os.ModeNamedPipe != 0}
	input, _ := io.ReadAll(os.Stdin)
	result.InputBytes = len(input)
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
	if logPath := os.Getenv("RESTIC_HELPER_LOG"); logPath != "" {
		if err := os.WriteFile(logPath, encoded, 0o600); err != nil {
			os.Exit(4)
		}
	}
	if os.Getenv("RESTIC_HELPER_LOCKS") == "1" {
		joined := strings.Join(arguments, " ")
		switch {
		case strings.Contains(joined, "list locks"):
			_, _ = io.WriteString(os.Stdout, "abc123\n")
		case strings.Contains(joined, "cat lock abc123"):
			_, _ = io.WriteString(os.Stdout, os.Getenv("RESTIC_HELPER_LOCK_JSON"))
		}
	}
	switch os.Getenv("RESTIC_HELPER_FAILURE") {
	case "missing":
		_, _ = io.WriteString(os.Stdout, "repository-config-must-not-leak")
		_, _ = io.WriteString(os.Stderr, "Fatal: repository does not exist")
		os.Exit(1)
	case "auth":
		_, _ = io.WriteString(os.Stderr, "authentication failed")
		os.Exit(1)
	case "large":
		_, _ = io.WriteString(os.Stderr, strings.Repeat("x", maximumDiagnostic*2))
		os.Exit(1)
	}
	os.Exit(0)
}

func TestRecoverRepositoryLocksChecksAgeActivityAndDryRun(t *testing.T) {
	previous := isProcessActive
	t.Cleanup(func() { isProcessActive = previous })
	isProcessActive = func(int) (bool, error) { return false, nil }
	now := time.Now().UTC()
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	lock, _ := json.Marshal(repositoryLock{Time: now.Add(-2 * time.Hour), PID: 4242, Hostname: host})
	logPath := filepath.Join(t.TempDir(), "restic.json")
	client := &Client{executable: os.Args[0], prefixArguments: []string{"-test.run=TestResticHelper", "--"}, stdout: io.Discard, stderr: io.Discard}
	config := Config{Repository: "local:repository", PasswordValue: "secret", Environment: map[string]string{
		"GO_WANT_RESTIC_HELPER": "1", "RESTIC_HELPER_LOCKS": "1", "RESTIC_HELPER_LOCK_JSON": string(lock), "RESTIC_HELPER_LOG": logPath,
	}}
	if err := client.RecoverRepositoryLocks(context.Background(), config, time.Hour, true, now); err != nil {
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
	if strings.Contains(strings.Join(result.Arguments, " "), "unlock") {
		t.Fatal("dry run invoked unlock")
	}
	if err := client.RecoverRepositoryLocks(context.Background(), config, time.Hour, false, now); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(result.Arguments, " "), "unlock") {
		t.Fatalf("last command = %v", result.Arguments)
	}

	isProcessActive = func(int) (bool, error) { return true, nil }
	if err := client.RecoverRepositoryLocks(context.Background(), config, time.Hour, false, now); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(logPath)
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(result.Arguments, " "), "unlock") {
		t.Fatal("active lock was unlocked")
	}
}

func TestRepositoryExistsIsolatesAndBoundsProbeOutput(t *testing.T) {
	for _, test := range []struct {
		name, failure string
		wantExists    bool
		wantErr       bool
	}{
		{"missing", "missing", false, false},
		{"authentication", "auth", false, true},
		{"large diagnostic", "large", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			client := &Client{executable: os.Args[0], prefixArguments: []string{"-test.run=TestResticHelper", "--"}, stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}
			config := Config{Repository: "local:repository", PasswordValue: "secret", Environment: map[string]string{"GO_WANT_RESTIC_HELPER": "1", "RESTIC_HELPER_FAILURE": test.failure}}
			exists, err := client.RepositoryExists(context.Background(), config)
			if exists != test.wantExists || (err != nil) != test.wantErr {
				t.Fatalf("RepositoryExists = %t, %v", exists, err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("probe leaked stdout: %q", stdout.String())
			}
			if test.failure == "missing" && stderr.Len() != 0 {
				t.Fatalf("expected missing diagnostic was emitted: %q", stderr.String())
			}
			if test.failure == "large" && (stderr.Len() > maximumDiagnostic+64 || !strings.Contains(stderr.String(), "diagnostic truncated")) {
				t.Fatalf("large diagnostic was not bounded: %d bytes", stderr.Len())
			}
		})
	}
}

func TestRunWithInputStreamsLargeInput(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "restic.json")
	client := &Client{executable: os.Args[0], prefixArguments: []string{"-test.run=TestResticHelper", "--"}, stdout: io.Discard, stderr: io.Discard}
	config := Config{Repository: "local:repository", PasswordValue: "secret", Environment: map[string]string{"GO_WANT_RESTIC_HELPER": "1", "RESTIC_HELPER_LOG": logPath}}
	const size = 2 << 20
	if _, err := client.RunWithInput(context.Background(), config, []string{"backup", "--stdin"}, "", strings.NewReader(strings.Repeat("x", size))); err != nil {
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
	if result.InputBytes != size {
		t.Fatalf("input bytes = %d", result.InputBytes)
	}
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

func TestRunPipesStdoutToKeepCommandOutputStable(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "restic.json")
	output, err := os.CreateTemp(t.TempDir(), "output-")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	client := &Client{
		executable:      os.Args[0],
		prefixArguments: []string{"-test.run=TestResticHelper", "--"},
		stdout:          output,
		stderr:          io.Discard,
	}
	config := Config{Repository: "local:repository", PasswordValue: "secret", Environment: map[string]string{
		"GO_WANT_RESTIC_HELPER": "1", "RESTIC_HELPER_LOG": logPath,
	}}
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
	if !result.StdoutIsPipe {
		t.Fatal("restic stdout was attached directly instead of through a pipe")
	}
}

func TestSummaryCaptureIsStreamingAndBounded(t *testing.T) {
	var capture summaryCapture
	_, _ = capture.Write([]byte(`{"message_type":"status","percent_done":0.5}` + "\n"))
	_, _ = capture.Write([]byte(`{"message_type":"summary","files_new":`))
	_, _ = capture.Write([]byte(`7,"data_added":42}` + "\n"))
	if capture.summary == nil || capture.summary.FilesNew != 7 || capture.summary.DataAddedBytes != 42 {
		t.Fatalf("summary = %#v", capture.summary)
	}
	_, _ = capture.Write([]byte(strings.Repeat("x", maximumJSONLine+1) + "\n"))
	if len(capture.line) != 0 || capture.discard {
		t.Fatalf("capture retained oversized line: len=%d discard=%t", len(capture.line), capture.discard)
	}
}
