//go:build linux

package externalproxy

import (
	"context"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

// fakeMieruSOCKS mimics the Mieru client: no-auth SOCKS5, UDP relay bound on
// 0.0.0.0 and a reply carrying 0.0.0.0:<port>. Datagrams are echoed back.
func fakeMieruSOCKS(t *testing.T) (string, chan net.Conn) {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	controls := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				b := make([]byte, 10)
				if _, err := io.ReadFull(c, b[:3]); err != nil {
					_ = c.Close()
					return
				}
				_, _ = c.Write([]byte{5, 0})
				if _, err := io.ReadFull(c, b); err != nil || b[1] != 3 {
					_ = c.Close()
					return
				}
				relay, err := net.ListenUDP("udp4", nil)
				if err != nil {
					_ = c.Close()
					return
				}
				p := relay.LocalAddr().(*net.UDPAddr).Port
				_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, byte(p >> 8), byte(p)})
				controls <- c
				go func() { _, _ = io.Copy(io.Discard, c); _ = relay.Close() }()
				buf := make([]byte, 2048)
				for {
					n, addr, err := relay.ReadFromUDP(buf)
					if err != nil {
						return
					}
					_, _ = relay.WriteToUDP(buf[:n], addr)
				}
			}()
		}
	}()
	return l.Addr().String(), controls
}

func TestMieruUDPAssociationUsesLoopbackRelayAndEndsWithControl(t *testing.T) {
	backend, controls := fakeMieruSOCKS(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &runner{spec: Spec{Protocol: "mieru", Enable: true}, state: "running", pid: os.Getpid(), backend: backend, ctx: ctx, cancel: cancel}
	packet := []byte{0, 0, 0, 1, 1, 2, 3, 4, 0, 53, 'h', 'i'}
	for _, stop := range []string{"mieru-control-closed", "runner-stopped"} {
		up, err := r.associateUDP(context.Background())
		if err != nil {
			t.Fatalf("%s: associate: %v", stop, err)
		}
		if remote := up.RemoteAddr().(*net.UDPAddr); !remote.IP.IsLoopback() {
			t.Fatalf("relay is not loopback: %v", remote)
		}
		control := <-controls
		_ = up.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err = up.Write(packet); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, 64)
		if n, err := up.Read(got); err != nil || string(got[:n]) != string(packet) {
			t.Fatalf("echo %q %v", got[:n], err)
		}
		if stop == "mieru-control-closed" {
			_ = control.Close()
		} else {
			cancel()
		}
		_ = up.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err = up.Read(got); err == nil || isTimeout(err) {
			t.Fatalf("%s: association stayed open: %v", stop, err)
		}
		_ = up.Close()
	}
	if _, err := r.associateUDP(context.Background()); err == nil {
		t.Fatal("stopped runner opened a UDP association")
	}
}

func TestMieruUDPAssociationRejectsUnownedBackend(t *testing.T) {
	backend, _ := fakeMieruSOCKS(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// pid 1 does not own the fake listener: setup must fail closed.
	r := &runner{spec: Spec{Protocol: "mieru", Enable: true}, state: "running", pid: 1, backend: backend, ctx: ctx, cancel: cancel}
	if up, err := r.associateUDP(context.Background()); err == nil {
		_ = up.Close()
		t.Fatal("UDP association accepted a backend not owned by the client")
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}
