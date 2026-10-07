package profile

import "testing"

func TestReservedRepositoryAndPasswordOptions(t *testing.T) {
	tests := []struct {
		argument string
		reserved bool
	}{
		{argument: "-r", reserved: true},
		{argument: "-qr", reserved: true},
		{argument: "-cqr", reserved: true},
		{argument: "-xfr", reserved: true},
		{argument: "-lqp", reserved: true},
		{argument: "-qpsecret", reserved: true},
		{argument: "-vqr=other", reserved: true},
		{argument: "-n=true", reserved: false},
		{argument: "-Hserver", reserved: false},
		{argument: "-ooption=password", reserved: false},
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

func TestDryRunOptions(t *testing.T) {
	for _, argument := range []string{"--dry-run", "-n", "--dry-run=true", "--dry-run=TRUE", "--dry-run=1", "-n=t"} {
		if !IsDryRunOption(argument) {
			t.Errorf("IsDryRunOption(%q) = false", argument)
		}
	}
	for _, argument := range []string{"--dry-run=false", "-n=0", "--dry-run=invalid", "--other=true"} {
		if IsDryRunOption(argument) {
			t.Errorf("IsDryRunOption(%q) = true", argument)
		}
	}
}

func TestDryRunUsesFinalBooleanValue(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		enabled   bool
	}{
		{[]string{"--dry-run", "--dry-run=false"}, false},
		{[]string{"-n=false", "--dry-run"}, true},
		{[]string{"--dry-run=true", "-n=0"}, false},
		{[]string{"--dry-run=false", "-n=1"}, true},
		{[]string{"--", "--dry-run"}, false},
		{[]string{"--dry-run", "--", "--dry-run=false"}, true},
	} {
		if got := DryRunEnabled(test.arguments); got != test.enabled {
			t.Errorf("DryRunEnabled(%q)=%t; want %t", test.arguments, got, test.enabled)
		}
	}
}

func TestReservedShorthandsRespectCommandValues(t *testing.T) {
	for _, test := range []struct {
		command, argument string
		reserved          bool
	}{
		{"restore", "-iprivate", false}, {"restore", "-qiprivate", false},
		{"restore", "-qpsecret", true}, {"find", "-iprivate", true},
		{"find", "-iqrrepository", true}, {"backup", "-xfrrepository", true},
		{"forget", "-lunlimited", false}, {"forget", "-qrrepository", true},
	} {
		if got := IsReservedCommandOption(test.argument, test.command); got != test.reserved {
			t.Errorf("%s %s: reserved=%v want=%v", test.command, test.argument, got, test.reserved)
		}
	}
}

func TestDryRunShortBundles(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		want      bool
	}{
		{[]string{"-qn"}, true}, {[]string{"-qvn=true"}, true},
		{[]string{"-qn=false"}, false}, {[]string{"-qn", "--dry-run=false"}, false},
		{[]string{"--dry-run=false", "-qn"}, true}, {[]string{"-qnn=false"}, false},
		{[]string{"-xnf"}, true}, {[]string{"-Hname"}, false},
	} {
		if got := DryRunEnabled(test.arguments); got != test.want {
			t.Errorf("DryRunEnabled(%v)=%v want=%v", test.arguments, got, test.want)
		}
	}
	if !IsDryRunOption("-qn") {
		t.Fatal("bundled dry run was accepted by backup validation")
	}
}
