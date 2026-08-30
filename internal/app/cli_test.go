package app

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAndListCommands(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "config")
	var output, errors bytes.Buffer
	status, err := Run(context.Background(), []string{"--config-dir", directory, "create", "example"}, &output, &errors)
	if err != nil || status != 0 {
		t.Fatalf("create status=%d error=%v stderr=%s", status, err, errors.String())
	}
	output.Reset()
	status, err = Run(context.Background(), []string{"--config-dir", directory, "list"}, &output, &errors)
	if err != nil || status != 0 {
		t.Fatalf("list status=%d error=%v stderr=%s", status, err, errors.String())
	}
	if strings.TrimSpace(output.String()) != "example" {
		t.Fatalf("list output = %q", output.String())
	}
}
