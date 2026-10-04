package process

import (
	"reflect"
	"runtime"
	"testing"
)

func TestMergeEnvironmentOverridesFiltersAndSorts(t *testing.T) {
	result := MergeEnvironment([]string{"Z=last", "VALUE=old", "VALUE=latest", "BLOCKED=secret", "malformed"},
		map[string]string{"VALUE": "new=with-equals", "A": "first", "BLOCKED": "other-secret"},
		func(key string) bool { return key == "BLOCKED" })
	expected := []string{"A=first", "VALUE=new=with-equals", "Z=last"}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("environment = %q, want %q", result, expected)
	}
}

func TestMergeEnvironmentRespectsPlatformCaseSemantics(t *testing.T) {
	result := MergeEnvironment([]string{"Path=old"}, map[string]string{"PATH": "new"}, nil)
	expected := []string{"PATH=new", "Path=old"}
	if runtime.GOOS == "windows" {
		expected = []string{"PATH=new"}
	}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("environment = %q, want %q", result, expected)
	}
}

func TestMergeEnvironmentPreservesWindowsDriveDirectories(t *testing.T) {
	result := MergeEnvironment([]string{`=C:=C:\source`, `=D:=D:\data`, "PATH=tools", "=malformed"}, nil, nil)
	expected := []string{`=C:=C:\source`, `=D:=D:\data`, "PATH=tools"}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("drive-directory entries changed: %q, want %q", result, expected)
	}
}
