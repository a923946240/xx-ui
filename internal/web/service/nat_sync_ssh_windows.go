package service

import (
	"errors"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func natSyncWindowsKeyACL(directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	flags := ""
	if directory {
		flags = "OICI"
	}
	return windows.SecurityDescriptorFromString("D:P(A;" + flags + ";FA;;;" + user.User.Sid.String() + ")")
}

func secureNATSyncDirectory(path string, create bool) error {
	if create {
		acl, err := natSyncWindowsKeyACL(true)
		if err != nil {
			return err
		}
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		attrs := windows.SecurityAttributes{SecurityDescriptor: acl}
		attrs.Length = uint32(unsafe.Sizeof(attrs))
		if err := windows.CreateDirectory(name, &attrs); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("NAT sync key directory is not private")
	}
	return checkNATSyncWindowsACL(path, true)
}

func checkNATSyncKeyPermissions(path string, _ os.FileInfo) error {
	return checkNATSyncWindowsACL(path, false)
}

func checkNATSyncWindowsACL(path string, directory bool) error {
	want, err := natSyncWindowsKeyACL(directory)
	if err != nil {
		return err
	}
	got, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	control, _, err := got.Control()
	_, gotEntries, gotOK := strings.Cut(got.String(), "(")
	_, wantEntries, wantOK := strings.Cut(want.String(), "(")
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 || !gotOK || !wantOK || gotEntries != wantEntries {
		return errors.New("NAT sync key permissions are not private")
	}
	return nil
}

func protectNATSyncTempKey(path string) error {
	descriptor, err := natSyncWindowsKeyACL(false)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
