//go:build linux

package externalproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

type backendDialFunc func(context.Context, string, string) (net.Conn, error)

func (f backendDialFunc) DialContext(c context.Context, n, a string) (net.Conn, error) {
	return f(c, n, a)
}

func TestBackendOwnershipIsRecheckedOnEstablishedConnection(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r := &runner{spec: Spec{Enable: true}, ctx: ctx, pid: os.Getpid(), backend: l.Addr().String(), state: "running"}
	if !ownsListener(r.pid, r.backend) {
		t.Fatal("initial listener is not owned")
	}
	peer, connection := net.Pipe()
	defer peer.Close()
	readResult := observeBackendHandshake(peer)
	r.backendDialer = backendDialFunc(func(context.Context, string, string) (net.Conn, error) {
		// The original listener disappears after TCP establishment, before SOCKS.
		_ = l.Close()
		return connection, nil
	})
	if _, err := r.dial(ctx, "example.com:443"); err == nil {
		t.Fatal("accepted a backend whose owner disappeared")
	}
	if leaked := <-readResult; leaked {
		t.Fatal("sent SOCKS data before verifying owner")
	}
}

func TestBackendGenerationChangeRevokesHandshake(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &runner{spec: Spec{Enable: true}, ctx: ctx, pid: os.Getpid(), backend: l.Addr().String(), state: "running"}
	peer, connection := net.Pipe()
	defer peer.Close()
	readResult := observeBackendHandshake(peer)
	r.backendDialer = backendDialFunc(func(context.Context, string, string) (net.Conn, error) {
		r.mu.Lock()
		r.pid++
		r.mu.Unlock()
		return connection, nil
	})
	if _, err := r.dial(ctx, "example.com:443"); err == nil {
		t.Fatal("accepted a previous generation")
	}
	if leaked := <-readResult; leaked {
		t.Fatal("sent SOCKS data after generation change")
	}
}

func observeBackendHandshake(peer net.Conn) <-chan bool {
	result := make(chan bool, 1)
	go func() {
		_ = peer.SetReadDeadline(time.Now().Add(time.Second))
		n, err := peer.Read(make([]byte, 1))
		result <- n != 0 || !errors.Is(err, io.EOF)
		_ = peer.Close()
	}()
	return result
}
