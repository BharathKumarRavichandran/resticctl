package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"resticctl/internal/profile"
)

func TestMigratePlanRedactsRepositorySecrets(t *testing.T) {
	directory := t.TempDir()
	profiles := profile.Dir(directory)
	writePrivateCLIFile(t, filepath.Join(profiles, "source.credentials.json"), `{"password":{"value":"source"}}`)
	writePrivateCLIFile(t, filepath.Join(profiles, "target.credentials.json"), `{"password":{"value":"target"}}`)
	writePrivateCLIFile(t, filepath.Join(profiles, "home.json"), `{
      "repository":"rest:https://source:secret@example.test/source", "credentials_file":"source.credentials.json",
      "copies":{"offsite":{"repository":"rest:https://target:secret@example.test/target","credentials_file":"target.credentials.json"}}}`)

	var output, stderr bytes.Buffer
	status, err := runForTest(context.Background(), []string{"migrate", "plan", "home", "offsite", "--config-dir", directory}, &output, &stderr)
	if err != nil || status != 0 {
		t.Fatalf("plan status=%d error=%v stderr=%s", status, err, stderr.String())
	}
	if strings.Contains(output.String(), "secret") || !strings.Contains(output.String(), "REDACTED") {
		t.Fatalf("plan output was not redacted: %s", output.String())
	}
}

func TestMigratePlanRejectsConflictingBackendCredentials(t *testing.T) {
	directory := t.TempDir()
	profiles := profile.Dir(directory)
	writePrivateCLIFile(t, filepath.Join(profiles, "source.credentials.json"), `{"environment":{"B2_ACCOUNT_ID":"one"},"password":{"value":"source"}}`)
	writePrivateCLIFile(t, filepath.Join(profiles, "target.credentials.json"), `{"environment":{"B2_ACCOUNT_ID":"two"},"password":{"value":"target"}}`)
	writePrivateCLIFile(t, filepath.Join(profiles, "home.json"), `{
      "repository":"b2:source:/restic", "credentials_file":"source.credentials.json",
      "copies":{"offsite":{"repository":"b2:destination:/restic","credentials_file":"target.credentials.json"}}}`)

	status, err := runForTest(context.Background(), []string{"migrate", "plan", "home", "offsite", "--config-dir", directory}, &bytes.Buffer{}, &bytes.Buffer{})
	if status != 1 || err == nil || !strings.Contains(err.Error(), "B2_ACCOUNT_ID") {
		t.Fatalf("plan status=%d error=%v", status, err)
	}
}
