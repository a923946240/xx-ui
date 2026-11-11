package externalproxy

import (
	"context"
	"encoding/binary"
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

func TestGateForwardsAuthenticatedUDPAssociation(t *testing.T) {
	echo, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		b := make([]byte, 2048)
		for {
			n, addr, e := echo.ReadFromUDP(b)
			if e != nil {
				return
			}
			_, _ = echo.WriteToUDP(b[:n], addr)
		}
	}()
	g, err := newGateWithUDP("local", "token", func(context.Context, string) (net.Conn, error) { return nil, net.ErrClosed }, func(context.Context) (net.Conn, error) {
		return net.Dial("udp", echo.LocalAddr().String())
	})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	control, err := net.Dial("tcp", g.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = control.Write([]byte{5, 1, 2})
	negotiated := make([]byte, 2)
	if _, err = io.ReadFull(control, negotiated); err != nil || negotiated[1] != 2 {
		t.Fatalf("negotiation %v %v", negotiated, err)
	}
	_, _ = control.Write(append(append([]byte{1, 5}, []byte("local")...), append([]byte{5}, []byte("token")...)...))
	auth := make([]byte, 2)
	if _, err = io.ReadFull(control, auth); err != nil || auth[1] != 0 {
		t.Fatalf("auth %v %v", auth, err)
	}
	_, _ = control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	reply := make([]byte, 10)
	if _, err = io.ReadFull(control, reply); err != nil || reply[1] != 0 {
		t.Fatalf("associate %v %v", reply, err)
	}
	port := binary.BigEndian.Uint16(reply[8:10])
	relay, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(port)})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	targetPort := echo.LocalAddr().(*net.UDPAddr).Port
	packet := []byte{0, 0, 0, 1, 127, 0, 0, 1, byte(targetPort >> 8), byte(targetPort)}
	packet = append(packet, []byte("udp-through-ix-out")...)
	if _, err = relay.Write(packet); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 256)
	n, err := relay.Read(got)
	if err != nil || string(got[:n]) != string(packet) {
		t.Fatalf("UDP packet %q, %v", got[:n], err)
	}
}

func TestGateUDPPinsClientAndEndsWithControl(t *testing.T) {
	upstreamClosed := make(chan struct{})
	g, err := newGateWithUDP("local", "token", func(context.Context, string) (net.Conn, error) { return nil, net.ErrClosed }, func(context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		go func() {
			buf := make([]byte, 2048)
			for {
				n, e := b.Read(buf)
				if e != nil {
					close(upstreamClosed)
					return
				}
				_, _ = b.Write(buf[:n])
			}
		}()
		return a, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	control, err := net.Dial("tcp", g.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = control.Write([]byte{5, 1, 2})
	_, _ = io.ReadFull(control, make([]byte, 2))
	_, _ = control.Write(append(append([]byte{1, 5}, []byte("local")...), append([]byte{5}, []byte("token")...)...))
	_, _ = io.ReadFull(control, make([]byte, 2))
	_, _ = control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	reply := make([]byte, 10)
	if _, err = io.ReadFull(control, reply); err != nil || reply[1] != 0 {
		t.Fatalf("associate %v %v", reply, err)
	}
	relay := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(binary.BigEndian.Uint16(reply[8:10]))}
	first, _ := net.DialUDP("udp4", nil, relay)
	defer first.Close()
	intruder, _ := net.DialUDP("udp4", nil, relay)
	defer intruder.Close()
	packet := []byte{0, 0, 0, 1, 1, 2, 3, 4, 0, 53, 'x'}
	_, _ = first.Write(packet)
	got := make([]byte, 64)
	_ = first.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, e := first.Read(got); e != nil || string(got[:n]) != string(packet) {
		t.Fatalf("pinned client %q %v", got[:n], e)
	}
	_, _ = intruder.Write(packet)
	_ = intruder.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, e := intruder.Read(got); e == nil {
		t.Fatal("second UDP endpoint was relayed")
	}
	_ = first.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, e := first.Read(got); e == nil {
		t.Fatal("intruder datagram was forwarded upstream")
	}
	_ = control.Close()
	select {
	case <-upstreamClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("closing the control connection left the UDP association open")
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

func TestGateSessionLimitCountsClientSessionsNotForwardedLegs(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, e := echo.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	const limit = 4
	gate, err := newGateLimited("local", "token", func(ctx context.Context, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}, nil, limit)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	d, err := proxy.SOCKS5("tcp", gate.Address(), &proxy.Auth{User: "local", Password: "token"}, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip := func(i int) error {
		c, e := d.Dial("tcp", echo.Addr().String())
		if e != nil {
			return e
		}
		t.Cleanup(func() { _ = c.Close() })
		_ = c.SetDeadline(time.Now().Add(2 * time.Second))
		if _, e = c.Write([]byte{byte(i)}); e != nil {
			return e
		}
		b := make([]byte, 1)
		_, e = io.ReadFull(c, b)
		return e
	}
	for i := range limit {
		if err := roundTrip(i); err != nil {
			t.Fatalf("concurrent session %d of %d was dropped: %v", i+1, limit, err)
		}
	}
	if err := roundTrip(limit); err == nil {
		t.Fatalf("session %d beyond the limit of %d was accepted", limit+1, limit)
	}
}
