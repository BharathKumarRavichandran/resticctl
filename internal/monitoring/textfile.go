package monitoring

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"resticctl/internal/configlock"
	"resticctl/internal/runstatus"
	"resticctl/internal/securefile"
)

// Serialize read/merge/replace across processes sharing a textfile.
func writePrometheus(ctx context.Context, path string, status runstatus.Status) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := configlock.With(path+".lock", func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			previous, err := readBounded(path, 10<<20)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			data := []byte(mergePrometheus(string(previous), status))
			if len(data) > 10<<20 {
				return errors.New("metrics textfile exceeds size limit")
			}
			return securefile.WriteAtomic(path, data)
		})
		if !errors.Is(err, configlock.ErrLocked) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func mergePrometheus(previous string, status runstatus.Status) string {
	labels := "{" + metricLabels(status) + "}"
	legacy := status
	legacy.TargetType, legacy.TargetName = "", ""
	legacyLabels := "{" + metricLabels(legacy) + "}"
	// Replace every sample for this identity, including optional metrics that
	// are absent in the new result. Remove old unlabeled copy samples too.
	var retained strings.Builder
	for _, line := range strings.Split(previous, "\n") {
		if strings.HasPrefix(line, "resticctl_") {
			index := strings.IndexByte(line, '{')
			if index >= 0 && (strings.HasPrefix(line[index:], labels) ||
				(status.TargetType == "copy" && strings.HasPrefix(line[index:], legacyLabels))) {
				continue
			}
		}
		retained.WriteString(line)
		retained.WriteByte('\n')
	}
	var result strings.Builder
	metadata := make(map[string]bool)
	for _, line := range strings.Split(retained.String()+prometheus(status), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if metadata[line] {
				continue
			}
			metadata[line] = true
		}
		result.WriteString(line)
		result.WriteByte('\n')
	}
	return result.String()
}
