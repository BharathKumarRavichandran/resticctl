//go:build windows

package profile

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
	"resticctl/internal/securefile"
)

func TestEnsureFileSecurityRequiresOwnerOnlyProtectedDACL(t *testing.T) {
	path := t.TempDir() + `\credentials.json`
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := securefile.Protect(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureFileSecurity(info, path, "credentials"); err != nil {
		t.Fatalf("owner-only DACL rejected: %v", err)
	}

	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(everyone),
		},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if err := ensureFileSecurity(info, path, "credentials"); err == nil {
		t.Fatal("credential file accessible by Everyone was accepted")
	}
}
