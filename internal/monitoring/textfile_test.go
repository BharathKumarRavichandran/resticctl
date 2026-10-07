package monitoring

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"resticctl/internal/configlock"
	"resticctl/internal/runstatus"
)

func TestTextfileRetainsTargetsAndReplacesLatest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.prom")
	backup := runstatus.Status{Profile: "example", Command: "backup", State: "succeeded"}
	local := runstatus.Status{Profile: "example", Command: "copy", TargetType: "copy", TargetName: "local", State: "succeeded", Statistics: &runstatus.Statistics{FilesNew: 1}}
	remote := local
	remote.TargetName, remote.State, remote.Statistics = "remote", "failed", nil
	for _, status := range []runstatus.Status{backup, local, remote} {
		if err := writePrometheus(context.Background(), path, status); err != nil {
			t.Fatal(err)
		}
	}
	local.State, local.Statistics = "failed", nil
	if err := writePrometheus(context.Background(), path, local); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	metrics := string(data)
	for _, status := range []runstatus.Status{backup, local, remote} {
		want := "resticctl_run_success{" + metricLabels(status) + "} "
		if strings.Count(metrics, want) != 1 {
			t.Fatalf("expected one result for %s: %s", status.TargetName, metrics)
		}
		value := "0"
		if status.State == "succeeded" {
			value = "1"
		}
		if !strings.Contains(metrics, want+value+"\n") {
			t.Fatalf("wrong result for %s: %s", status.TargetName, metrics)
		}
	}
	if strings.Contains(metrics, "resticctl_backup_files_new{") {
		t.Fatal("obsolete statistics were retained")
	}
	if strings.Count(metrics, "# TYPE resticctl_run_success gauge") != 1 {
		t.Fatal("duplicate metric metadata")
	}
}

func TestTextfileConcurrentExports(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.prom")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const targets = 12
	results := make(chan error, targets)
	for index := range targets {
		go func() {
			results <- writePrometheus(ctx, path, runstatus.Status{Profile: "example", Command: "copy", TargetType: "copy", TargetName: fmt.Sprintf("target-%d", index)})
		}()
	}
	for range targets {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "resticctl_run_success{"); got != targets {
		t.Fatalf("retained %d targets, want %d", got, targets)
	}
}

func TestTextfileLockWaitRespectsContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.prom")
	err := configlock.With(path+".lock", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		return writePrometheus(ctx, path, runstatus.Status{})
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled export wrote textfile: %v", err)
	}
}

func TestTextfileRemovesLegacyCopySamples(t *testing.T) {
	legacy := runstatus.Status{Profile: "example", Command: "copy", State: "succeeded"}
	current := legacy
	current.TargetType, current.TargetName = "copy", "local"
	metrics := mergePrometheus(prometheus(legacy), current)
	if strings.Contains(metrics, "{"+metricLabels(legacy)+"}") || !strings.Contains(metrics, "{"+metricLabels(current)+"}") {
		t.Fatalf("legacy migration failed: %s", metrics)
	}
}
