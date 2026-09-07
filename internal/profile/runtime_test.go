package profile

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateRuntimeNormalizesLock(t *testing.T) {
	base := t.TempDir()
	value := Profile{Runtime: Runtime{Lock: &RuntimeLock{Path: "locks/job.lock", Mode: "wait", Wait: "2s", StaleAfter: "1h", Stale: "clear"}}}
	if err := validateRuntime(&value, base); err != nil { t.Fatal(err) }
	if value.Runtime.Lock.Path != filepath.Join(base, "locks", "job.lock") { t.Fatalf("lock path = %q", value.Runtime.Lock.Path) }
}

func TestValidateRuntimeRejectsUnsafePolicies(t *testing.T) {
	tests := []Runtime{
		{Lock: &RuntimeLock{Path: "job.lock", Mode: "wait"}},
		{Lock: &RuntimeLock{Path: "job.lock", Stale: "clear"}},
		{RepositoryLockRecovery: &RepositoryRecovery{Enabled: true}},
	}
	for _, policy := range tests {
		value := Profile{Runtime: policy}
		if err := validateRuntime(&value, t.TempDir()); err == nil { t.Fatalf("accepted %#v", policy) }
	}
	if runtime.GOOS != "linux" {
		value := Profile{Runtime: Runtime{IOPriority: &IOPriority{Class: "idle"}}}
		if err := validateRuntime(&value, t.TempDir()); err == nil || !strings.Contains(err.Error(), "Linux") { t.Fatalf("ionice error = %v", err) }
	}
}
