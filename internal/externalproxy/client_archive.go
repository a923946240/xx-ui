package externalproxy

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"debug/elf"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
)

func extractClientBinary(ctx context.Context, archive, name, protocol, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	var input io.Reader
	var cmd *exec.Cmd
	if strings.HasSuffix(name, ".tar.xz") {
		cmd = exec.CommandContext(ctx, "xz", "-dc", archive)
		pipe, e := cmd.StdoutPipe()
		if e != nil {
			return e
		}
		cmd.Stderr = io.Discard
		if e = cmd.Start(); e != nil {
			return errors.New("xz is required to extract the NaiveProxy archive")
		}
		input = pipe
		defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	} else {
		z, e := gzip.NewReader(f)
		if e != nil {
			return errors.New("invalid gzip client archive")
		}
		defer z.Close()
		input = z
	}
	stream := &io.LimitedReader{R: input, N: (128 << 20) + 1}
	reader := tar.NewReader(stream)
	wanted := protocol
	if protocol == "trusttunnel" {
		wanted = "trusttunnel_client"
	}
	found := false
	for {
		header, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return errors.New("invalid or oversized client archive")
		}
		if stream.N <= 0 {
			return errors.New("expanded client archive exceeds 128 MiB")
		}
		if path.Base(header.Name) != wanted {
			continue
		}
		if found || header.Typeflag != tar.TypeReg || path.IsAbs(header.Name) || strings.Contains(header.Name, "\\") || strings.HasPrefix(path.Clean(header.Name), "../") || header.Size <= 0 || header.Size > 64<<20 {
			return errors.New("unsafe or duplicate client executable in archive")
		}
		file, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if e != nil {
			return e
		}
		_, copyErr := io.Copy(file, reader)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		found = true
	}
	if stream.N <= 0 {
		return errors.New("expanded client archive exceeds 128 MiB")
	}
	if !found {
		return errors.New("client executable is missing from archive")
	}
	return nil
}

func validateClientELF(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return errors.New("client is not a valid Linux ELF executable")
	}
	defer f.Close()
	wanted := elf.EM_X86_64
	if runtime.GOARCH == "arm64" {
		wanted = elf.EM_AARCH64
	}
	if f.Class != elf.ELFCLASS64 || f.Machine != wanted || (f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN) {
		return errors.New("client executable architecture does not match this host")
	}
	return nil
}
