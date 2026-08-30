package restic

import (
	"strings"
	"testing"
)

func TestMergeEnvironmentDropsResticSelectors(t *testing.T) {
	environment := mergeEnvironment(
		[]string{"PATH=/bin", "RESTIC_PASSWORD=ambient-secret", "RESTIC_REPOSITORY=wrong"},
		map[string]string{"AWS_ACCESS_KEY_ID": "key"},
	)
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "RESTIC_PASSWORD") || strings.Contains(joined, "RESTIC_REPOSITORY") {
		t.Fatalf("reserved restic environment leaked through: %v", environment)
	}
	if !strings.Contains(joined, "AWS_ACCESS_KEY_ID=key") {
		t.Fatalf("credential environment missing: %v", environment)
	}
}
