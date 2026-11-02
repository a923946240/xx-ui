package externalproxy

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

func TestGateAuthenticatesConnectAndClosesActiveTraffic(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		c, e := echo.Accept()
		if e == nil {
			defer c.Close()
			_, _ = io.Copy(c, c)
		}
	}()
	gate, err := newGate("local", "token", func(ctx context.Context, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	d, err := proxy.SOCKS5("tcp", gate.Address(), &proxy.Auth{User: "local", Password: "token"}, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.Dial("tcp", echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = c.Write([]byte("through-tunnel")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 14)
	if _, err = io.ReadFull(c, b); err != nil || string(b) != "through-tunnel" {
		t.Fatalf("payload %q, error %v", b, err)
	}
	gate.Close()
	if _, err = c.Read(make([]byte, 1)); err == nil {
		t.Fatal("gate shutdown left a live session")
	}
}

func TestGateRejectsBadAuthenticationAndUDP(t *testing.T) {
	gate, err := newGate("local", "token", func(context.Context, string) (net.Conn, error) {
		t.Error("invalid request reached the tunnel dialer")
		return nil, net.ErrClosed
	})
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	for _, cmd := range []byte{1, 3} {
		c, e := net.DialTimeout("tcp", gate.Address(), time.Second)
		if e != nil {
			t.Fatal(e)
		}
		_ = c.SetDeadline(time.Now().Add(time.Second))
		_, _ = c.Write([]byte{5, 1, 2})
		got := make([]byte, 2)
		_, _ = io.ReadFull(c, got)
		if got[0] != 5 || got[1] != 2 {
			t.Fatalf("authentication method %v", got)
		}
		password := "wrong"
		if cmd == 3 {
			password = "token"
		}
		_, _ = c.Write(append(append([]byte{1, 5}, []byte("local")...), append([]byte{byte(len(password))}, []byte(password)...)...))
		_, _ = io.ReadFull(c, got)
		if cmd == 1 && got[1] != 1 {
			t.Fatalf("bad credentials accepted: %v", got)
		}
		if cmd == 3 {
			if got[1] != 0 {
				t.Fatalf("valid credentials rejected: %v", got)
			}
			_, _ = c.Write([]byte{5, 3, 0, 1, 127, 0, 0, 1, 0, 53})
			_, _ = io.ReadFull(c, got)
			if got[1] != 7 {
				t.Fatalf("UDP was not explicitly rejected: %v", got)
			}
		}
		_ = c.Close()
	}
}
