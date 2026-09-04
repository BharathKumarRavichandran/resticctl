//go:build windows

package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func platformConfigDir() string {
	if value := os.Getenv("APPDATA"); value != "" {
		return filepath.Join(value, "resticctl")
	}
	return ""
}

func ensureFileSecurity(_ os.FileInfo, path, label string) error {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("cannot inspect %s file ACL %s: %w", label, path, err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("cannot inspect owner of %s file %s: %w", label, path, err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("cannot determine current Windows user: %w", err)
	}
	if !windows.EqualSid(owner, user.User.Sid) {
		return fmt.Errorf("%s file is not owned by the current user: %s", label, path)
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("%s file ACL must not inherit access: %s", label, path)
	}
	dacl, defaulted, err := descriptor.DACL()
	if err != nil || dacl == nil || defaulted || dacl.AceCount != 1 {
		return fmt.Errorf("%s file must grant access only to its owner: %s", label, path)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		return fmt.Errorf("%s file must have an owner-only allow ACL: %s", label, path)
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !windows.EqualSid(aceSID, user.User.Sid) {
		return fmt.Errorf("%s file is accessible by another Windows principal: %s", label, path)
	}
	return nil
}
