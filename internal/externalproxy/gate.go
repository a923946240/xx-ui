package externalproxy

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

type gate struct {
	listener       net.Listener
	user, password string
	dial           func(context.Context, string) (net.Conn, error)
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.Mutex
	connections    map[net.Conn]struct{}
	closed         bool
	wg             sync.WaitGroup
}

func newGate(user, password string, dial func(context.Context, string) (net.Conn, error)) (*gate, error) {
	ctx, cancel := context.WithCancel(context.Background())
	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", "127.0.0.1:0")
	if err != nil {
		cancel()
		return nil, err
	}
	g := &gate{listener: l, user: user, password: password, dial: dial, ctx: ctx, cancel: cancel, connections: make(map[net.Conn]struct{})}
	g.wg.Add(1)
	go g.accept()
	return g, nil
}

func (g *gate) Address() string { return g.listener.Addr().String() }

func (g *gate) track(c net.Conn) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || len(g.connections) >= 256 {
		return false
	}
	g.connections[c] = struct{}{}
	return true
}

func (g *gate) untrack(c net.Conn) {
	_ = c.Close()
	g.mu.Lock()
	delete(g.connections, c)
	g.mu.Unlock()
}

func (g *gate) accept() {
	defer g.wg.Done()
	for {
		c, err := g.listener.Accept()
		if err != nil {
			return
		}
		if !g.track(c) {
			_ = c.Close()
			continue
		}
		g.wg.Add(1)
		go func() { defer g.wg.Done(); defer g.untrack(c); g.serve(c) }()
	}
}

func (g *gate) Close() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.closed = true
	g.cancel()
	_ = g.listener.Close()
	for c := range g.connections {
		_ = c.Close()
	}
	g.mu.Unlock()
	g.wg.Wait()
}

func (g *gate) authenticate(c net.Conn) bool {
	header := make([]byte, 2)
	if _, err := io.ReadFull(c, header); err != nil || header[0] != 5 {
		return false
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		return false
	}
	passwordMethod := false
	for _, method := range methods {
		passwordMethod = passwordMethod || method == 2
	}
	if !passwordMethod {
		_, _ = c.Write([]byte{5, 255})
		return false
	}
	if _, err := c.Write([]byte{5, 2}); err != nil {
		return false
	}
	if _, err := io.ReadFull(c, header); err != nil || header[0] != 1 {
		return false
	}
	user := make([]byte, int(header[1]))
	if _, err := io.ReadFull(c, user); err != nil {
		return false
	}
	if _, err := io.ReadFull(c, header[:1]); err != nil {
		return false
	}
	password := make([]byte, int(header[0]))
	if _, err := io.ReadFull(c, password); err != nil {
		return false
	}
	valid := subtle.ConstantTimeCompare(user, []byte(g.user)) & subtle.ConstantTimeCompare(password, []byte(g.password))
	if valid != 1 {
		_, _ = c.Write([]byte{1, 1})
		return false
	}
	_, err := c.Write([]byte{1, 0})
	return err == nil
}

func readDestination(c net.Conn, addressType byte) (string, error) {
	var host string
	switch addressType {
	case 1, 4:
		size := 4
		if addressType == 4 {
			size = 16
		}
		ip := make([]byte, size)
		if _, err := io.ReadFull(c, ip); err != nil {
			return "", err
		}
		host = net.IP(ip).String()
	case 3:
		var size [1]byte
		if _, err := io.ReadFull(c, size[:]); err != nil || size[0] == 0 {
			return "", errors.New("invalid SOCKS domain")
		}
		name := make([]byte, int(size[0]))
		if _, err := io.ReadFull(c, name); err != nil {
			return "", err
		}
		host = string(name)
	default:
		return "", errors.New("unsupported SOCKS address")
	}
	var port [2]byte
	if _, err := io.ReadFull(c, port[:]); err != nil {
		return "", err
	}
	if binary.BigEndian.Uint16(port[:]) == 0 {
		return "", errors.New("invalid SOCKS port")
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:])))), nil
}

func socksReply(c net.Conn, status byte) { _, _ = c.Write([]byte{5, status, 0, 1, 127, 0, 0, 1, 0, 0}) }

func (g *gate) serve(c net.Conn) {
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	if !g.authenticate(c) {
		return
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(c, header); err != nil || header[0] != 5 || header[2] != 0 {
		return
	}
	if header[1] != 1 {
		socksReply(c, 7)
		return
	}
	address, err := readDestination(c, header[3])
	if err != nil {
		socksReply(c, 8)
		return
	}
	ctx, cancel := context.WithTimeout(g.ctx, 10*time.Second)
	upstream, err := g.dial(ctx, address)
	cancel()
	if err != nil {
		socksReply(c, 1)
		return
	}
	if !g.track(upstream) {
		_ = upstream.Close()
		socksReply(c, 1)
		return
	}
	defer g.untrack(upstream)
	socksReply(c, 0)
	_ = c.SetDeadline(time.Time{})
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, c)
		if tcp, ok := upstream.(interface{ CloseWrite() error }); ok {
			_ = tcp.CloseWrite()
		}
		close(done)
	}()
	_, _ = io.Copy(c, upstream)
	_ = c.Close()
	_ = upstream.Close()
	<-done
}
