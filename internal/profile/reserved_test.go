package profile

import "testing"

func TestReservedRepositoryAndPasswordOptions(t *testing.T) {
	tests := []struct {
		argument string
		reserved bool
	}{
		{argument: "-r", reserved: true},
		{argument: "-r=other", reserved: true},
		{argument: "-rother", reserved: true},
		{argument: "-p", reserved: true},
		{argument: "-p=password", reserved: true},
		{argument: "-ppassword", reserved: true},
		{argument: "--repo", reserved: true},
		{argument: "--repo=other", reserved: true},
		{argument: "--repository-file", reserved: true},
		{argument: "--repository-file=repository", reserved: true},
		{argument: "--password-file=password", reserved: true},
		{argument: "--password-command=secret-helper", reserved: true},
		{argument: "--from-repo=source", reserved: true},
		{argument: "--from-password-file=source-password", reserved: true},
		{argument: "--", reserved: true},
		{argument: "--read-data-subset=1G", reserved: false},
	}
	for _, test := range tests {
		if got := IsReservedOption(test.argument); got != test.reserved {
			t.Errorf("IsReservedOption(%q) = %t, want %t", test.argument, got, test.reserved)
		}
	}
}

func TestReservedResticEnvironment(t *testing.T) {
	for _, key := range []string{"RESTIC_REPOSITORY", "restic_password_file", "RESTIC_FROM_REPOSITORY", "restic_from_password_command"} {
		if !IsReservedEnvironment(key) {
			t.Errorf("IsReservedEnvironment(%q) = false", key)
		}
	}
	if IsReservedEnvironment("RESTIC_CACHE_DIR") {
		t.Error("RESTIC_CACHE_DIR is reserved")
	}
}
