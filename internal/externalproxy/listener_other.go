//go:build !linux

package externalproxy

import "os/exec"

func configureChild(_ *exec.Cmd)        {}
func stopChild(cmd *exec.Cmd)           { _ = cmd.Process.Kill() }
func ownsListener(_ int, _ string) bool { return false }
