package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestFinalStatusPreservesUsageErrors(t *testing.T) {
	var stderr bytes.Buffer
	status := finalStatus(2, errors.New("bad arguments"), 0, &stderr)
	if status != 2 {
		t.Fatalf("status = %d, want 2", status)
	}
	if !strings.Contains(stderr.String(), "bad arguments") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestFinalStatusPrefersSignalCode(t *testing.T) {
	if status := finalStatus(1, errors.New("cancelled"), 143, &bytes.Buffer{}); status != 143 {
		t.Fatalf("status = %d, want 143", status)
	}
}
