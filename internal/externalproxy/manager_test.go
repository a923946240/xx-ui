package externalproxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

func TestMissingClientCannotFallBackAndDisableKeepsTagBlocked(t *testing.T) {
	m := NewManager(t.TempDir(), t.TempDir())
	defer m.StopAll()
	s := Spec{ID: 1, Tag: "exit", Protocol: "naive", Host: "proxy.example", Port: 443, Username: "alice", Password: "secret", Enable: true}
	if err := m.Reconcile([]Spec{s}); err != nil {
		t.Fatal(err)
	}
	obs := m.Outbounds()
	if len(obs) != 1 || obs[0]["tag"] != "exit" || obs[0]["protocol"] != "socks" {
		t.Fatalf("outbounds %#v", obs)
	}
	if got := m.Status("exit"); got.State != "missing-binary" {
		t.Fatalf("state %+v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if conn, err := m.DialContext(ctx, "exit", "example.com:443"); err == nil {
		conn.Close()
		t.Fatal("missing client yielded a connection")
	}
	s.Enable = false
	if err := m.Reconcile([]Spec{s}); err != nil {
		t.Fatal(err)
	}
	obs = m.Outbounds()
	if obs[0]["protocol"] != "blackhole" {
		t.Fatalf("disabled tag is not blackhole: %#v", obs)
	}
}

func TestManagedClientStopRevokesActiveConnectionsAndRedactsLogs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process ownership")
	}
	t.Setenv("EXTERNAL_PROXY_HELPER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	t.Chdir(base)
	bin := filepath.Join(base, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "naive"), []byte("#!/bin/sh\nexec '"+exe+"' -test.run=TestExternalProxyHelper -- \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeBin, err := filepath.Rel(cwd, bin)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(t.TempDir(), relativeBin)
	defer m.StopAll()
	s := Spec{ID: 2, Tag: "managed", Protocol: "naive", Host: "proxy.example", Port: 443, Username: "alice", Password: "redaction-secret", Enable: true}
	if err := m.Reconcile([]Spec{s}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for m.Status(s.Tag).State != "running" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := m.Status(s.Tag); got.State != "running" {
		t.Fatalf("client failed to start: %+v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := m.DialContext(ctx, s.Tag, "example.invalid:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = c.Write([]byte("tunnel"))
	b := make([]byte, 6)
	if _, err := io.ReadFull(c, b); err != nil || string(b) != "tunnel" {
		t.Fatalf("payload %q, err %v", b, err)
	}
	if strings.Contains(m.Status(s.Tag).Logs, s.Password) {
		t.Fatal("logs exposed password")
	}
	s.Enable = false
	if err := m.Reconcile([]Spec{s}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(b); err == nil {
		t.Fatal("disabled exit retained an active stream")
	}
	if got := m.Outbounds()[0]["protocol"]; got != "blackhole" {
		t.Fatalf("disabled protocol %v", got)
	}
}

func TestListenerOwnershipRejectsAnotherProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process ownership")
	}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !ownsListener(os.Getpid(), l.Addr().String()) {
		t.Fatal("own listener not recognized")
	}
	if ownsListener(os.Getppid(), l.Addr().String()) {
		t.Fatal("foreign listener accepted")
	}
}

func TestExternalProxyHelper(t *testing.T) {
	if os.Getenv("EXTERNAL_PROXY_HELPER") != "1" {
		return
	}
	b, err := os.ReadFile(os.Args[len(os.Args)-1])
	if err != nil {
		os.Exit(3)
	}
	var cfg map[string]string
	if json.Unmarshal(b, &cfg) != nil {
		os.Exit(4)
	}
	u, _ := url.Parse(cfg["listen"])
	l, err := net.Listen("tcp4", u.Host)
	if err != nil {
		os.Exit(5)
	}
	remote, _ := url.Parse(cfg["proxy"])
	password, _ := remote.User.Password()
	_, _ = os.Stdout.WriteString("started credential=" + password + "\n")
	for {
		c, err := l.Accept()
		if err != nil {
			os.Exit(0)
		}
		go func() {
			defer c.Close()
			header := make([]byte, 2)
			if _, err := io.ReadFull(c, header); err != nil {
				return
			}
			methods := make([]byte, int(header[1]))
			if _, err := io.ReadFull(c, methods); err != nil {
				return
			}
			_, _ = c.Write([]byte{5, 0})
			req := make([]byte, 4)
			if _, err := io.ReadFull(c, req); err != nil {
				return
			}
			if _, err := readDestination(c, req[3]); err != nil {
				return
			}
			socksReply(c, 0)
			_, _ = io.Copy(c, c)
		}()
	}
}

var _ proxy.ContextDialer
