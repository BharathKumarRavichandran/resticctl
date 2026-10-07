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
	want := []string{"copy", "--host", "server", "--tag", "profile:home,important", "--path", "/home", "--", "latest"}
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

func TestCopyTagAlternativesRequireProfile(t *testing.T) {
	for _, tags := range [][]string{nil, {"important", "daily,verified"}} {
		args := copyArguments(profile.Profile{Name: "home"}, profile.CopyTarget{Tags: tags})
		want := []string{"--tag", "profile:home"}
		if len(tags) > 0 {
			want = []string{"--tag", "profile:home,important", "--tag", "profile:home,daily,verified"}
		}
		if !slices.Equal(args, want) {
			t.Fatalf("args=%v want=%v", args, want)
		}
	}
}

func TestCopyScopesConfiguredTagArguments(t *testing.T) {
	p := profile.Profile{Name: "home", ResticArgs: []string{"--tag=global"}}
	target := profile.CopyTarget{Args: []string{"--tag", "important", "--tag=daily"}}
	args := copyArguments(p, target)
	want := []string{"--tag", "profile:home,important", "--tag=profile:home,daily"}
	if !slices.Equal(args, want) {
		t.Fatalf("args=%v want=%v", args, want)
	}
	config := copyConfig(p, target)
	if !slices.Equal(config.Destination.Arguments, []string{"--tag=profile:home,global"}) {
		t.Fatalf("global filters=%v", config.Destination.Arguments)
	}
	if !slices.Equal(target.Args, []string{"--tag", "important", "--tag=daily"}) || p.ResticArgs[0] != "--tag=global" {
		t.Fatal("profile arguments mutated")
	}
}

func TestGlobalCopyTagsDoNotAddBroadProfileAlternative(t *testing.T) {
	p := profile.Profile{Name: "home", ResticArgs: []string{"--tag=important"}}
	args := copyArguments(p, profile.CopyTarget{})
	if len(args) != 0 {
		t.Fatalf("global tags broadened by command arguments: %v", args)
	}
	config := copyConfig(p, profile.CopyTarget{})
	if !slices.Equal(config.Destination.Arguments, []string{"--tag=profile:home,important"}) {
		t.Fatalf("tags=%v", config.Destination.Arguments)
	}
}

func TestCopyTagScopingPreservesFlagLookingValues(t *testing.T) {
	for _, option := range []string{"--host", "--path", "--cache-dir", "--option", "-qH", "-vo"} {
		p := profile.Profile{Name: "home"}
		target := profile.CopyTarget{Args: []string{option, "--tag=literal"}}
		args := copyArguments(p, target)
		if !slices.Equal(args, []string{option, "--tag=literal", "--tag", "profile:home"}) {
			t.Errorf("option=%s args=%v", option, args)
		}
	}
}
