//go:build !windows

package service

import (
	"errors"
	"os"
	"syscall"
)

func secureNATSyncDirectory(path string, create bool) error {
	if create {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !ownsNATSyncPath(info) {
		return errors.New("NAT sync key directory is not private")
	}
	return nil
}

func checkNATSyncKeyPermissions(_ string, info os.FileInfo) error {
	if info.Mode().Perm() != 0o600 || !ownsNATSyncPath(info) {
		return errors.New("NAT sync key permissions are not private")
	}
	return nil
}

func ownsNATSyncPath(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

// os.CreateTemp already creates private files on POSIX systems.
func protectNATSyncTempKey(_ string) error { return nil }
