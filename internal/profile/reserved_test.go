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
		{argument: "--repo", reserved: true},
		{argument: "--repo=other", reserved: true},
		{argument: "--repository-file=repository", reserved: true},
		{argument: "--password-file=password", reserved: true},
		{argument: "--password-command=secret-helper", reserved: true},
		{argument: "--", reserved: true},
		{argument: "--read-data-subset=1G", reserved: false},
	}
	for _, test := range tests {
		if got := isReservedOption(test.argument); got != test.reserved {
			t.Errorf("isReservedOption(%q) = %t, want %t", test.argument, got, test.reserved)
		}
	}
}
