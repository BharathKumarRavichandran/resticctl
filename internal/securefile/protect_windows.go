//go:build windows

package securefile

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// Protect installs a protected DACL granting access only to the current user.
func Protect(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("determine current Windows user: %w", err)
	}
	inheritance := uint32(windows.NO_INHERITANCE)
	if info, statErr := os.Stat(path); statErr != nil {
		return statErr
	} else if info.IsDir() {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("create owner-only Windows ACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, user.User.Sid, nil, acl, nil); err != nil {
		return fmt.Errorf("set owner-only Windows ACL: %w", err)
	}
	return nil
}
