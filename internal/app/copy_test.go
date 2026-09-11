package app

import (
	"context"
	"slices"
	"testing"

	"resticctl/internal/profile"
)

func TestCopyBuildsTwoRepositoryInvocation(t *testing.T) {
	runner := &recordingRunner{}
	configured := profile.Profile{
		Name: "home", Repository: "local:source",
		Credentials: profile.Credentials{Password: profile.PasswordSource{Value: "source"}},
		Copies: map[string]profile.CopyTarget{
			"offsite": {
				Repository:  "local:destination",
				Credentials: profile.RepositoryCredentials{Password: profile.PasswordSource{Value: "destination"}},
				Hosts:       []string{"server"}, Tags: []string{"important"}, Paths: []string{"/home"}, SnapshotIDs: []string{"latest"},
			},
		},
	}
	if err := Copy(context.Background(), runner, configured, "offsite", false); err != nil {
		t.Fatal(err)
	}
	if len(runner.runs) != 1 {
		t.Fatalf("runs = %d", len(runner.runs))
	}
	run := runner.runs[0]
	if run.config.Repository != "local:destination" {
		t.Fatalf("destination = %q", run.config.Repository)
	}
	want := []string{"copy", "--host", "server", "--tag", "profile:home", "--tag", "important", "--path", "/home", "--", "latest"}
	if !slices.Equal(run.arguments, want) {
		t.Fatalf("arguments = %#v, want %#v", run.arguments, want)
	}
}

func TestCopyDryRunDoesNotInvokeUnsupportedResticFlag(t *testing.T) {
	runner := &recordingRunner{}
	configured := profile.Profile{
		Name: "home", Repository: "local:source",
		Copies: map[string]profile.CopyTarget{"offsite": {Repository: "local:destination"}},
	}
	if err := Copy(context.Background(), runner, configured, "offsite", true); err != nil {
		t.Fatal(err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("dry run invoked Restic: %#v", runner.runs)
	}
}
