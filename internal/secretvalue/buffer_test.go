package secretvalue

import (
	"strings"
	"testing"
)

func TestBufferMarksOversizedWrites(t *testing.T) {
	var output Buffer
	if _, err := output.Write([]byte(strings.Repeat("x", MaximumBytes+1))); err != nil {
		t.Fatal(err)
	}
	if !output.Exceeded() {
		t.Fatal("oversized write was not marked")
	}
}
