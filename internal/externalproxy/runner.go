package externalproxy

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

type runner struct {
	spec                       Spec
	binary, dir, localPassword string
	gate                       *gate
	ctx                        context.Context
	cancel                     context.CancelFunc
	done                       chan struct{}
	restart                    chan chan struct{}
	logs                       *logBuffer
	mu                         sync.Mutex
	state, backend             string
	pid                        int
	backendDialer              proxy.ContextDialer
}

func executableExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func (r *runner) setState(s string) { r.mu.Lock(); r.state = s; r.mu.Unlock() }
func (r *runner) getState() string  { r.mu.Lock(); defer r.mu.Unlock(); return r.state }

func (r *runner) dial(ctx context.Context, address string) (net.Conn, error) {
	if r.spec.Protocol == "trusttunnel" {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(strings.ToLower(host), ".")
		ip := net.ParseIP(host)
		if name == "localhost" || strings.HasSuffix(name, ".localhost") || ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
			return nil, errors.New("TrustTunnel local bypass target is blocked")
		}
	}
	r.mu.Lock()
	pid, backend, state := r.pid, r.backend, r.state
	r.mu.Unlock()
	if !r.spec.Enable || state != "running" || r.ctx.Err() != nil {
		return nil, errors.New("external tunnel client is unavailable")
	}
	dialCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	connector := r.backendDialer
	if connector == nil {
		connector = &net.Dialer{Timeout: 5 * time.Second}
	}
	c, err := connector.DialContext(dialCtx, "tcp", backend)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	valid := r.pid == pid && r.backend == backend && r.state == "running" && r.ctx.Err() == nil
	r.mu.Unlock()
	if !valid || !ownsListener(pid, backend) {
		_ = c.Close()
		return nil, errors.New("external tunnel client changed during connection")
	}
	// Handshake on this verified connection; never redial a reusable port.
	d, err := proxy.SOCKS5("tcp", backend, nil, &connectedBackend{conn: c})
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	result, err := d.(proxy.ContextDialer).DialContext(dialCtx, "tcp", address)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	if r.ctx.Err() != nil {
		_ = result.Close()
		return nil, errors.New("external tunnel client stopped")
	}
	return result, nil
}

func (r *runner) associateUDP(ctx context.Context) (net.Conn, error) {
	if r.spec.Protocol != "mieru" {
		return nil, errors.New("UDP association is unavailable for this client")
	}
	r.mu.Lock()
	pid, backend, state := r.pid, r.backend, r.state
	r.mu.Unlock()
	if !r.spec.Enable || state != "running" || r.ctx.Err() != nil {
		return nil, errors.New("external tunnel client is unavailable")
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", backend)
	if err != nil {
		return nil, err
	}
	stopOnCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopOnCancel()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	valid := func() bool {
		r.mu.Lock()
		same := r.pid == pid && r.backend == backend && r.state == "running" && r.ctx.Err() == nil
		r.mu.Unlock()
		return same && ownsListener(pid, backend)
	}
	if !valid() {
		_ = conn.Close()
		return nil, errors.New("external tunnel client changed during UDP setup")
	}
	if _, err = conn.Write([]byte{5, 1, 0}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	var greeting [2]byte
	if _, err = io.ReadFull(conn, greeting[:]); err != nil || greeting != [2]byte{5, 0} {
		_ = conn.Close()
		return nil, errors.New("Mieru SOCKS method negotiation failed")
	}
	if _, err = conn.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	header := make([]byte, 4)
	if _, err = io.ReadFull(conn, header); err != nil || header[0] != 5 || header[1] != 0 || header[2] != 0 {
		_ = conn.Close()
		return nil, errors.New("Mieru SOCKS UDP association failed")
	}
	relayPort, err := readSOCKSRelayPort(conn, header[3])
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	// Mieru binds its relay on 0.0.0.0 and only rewrites the port in the reply,
	// so the replied host is not meaningful. Always use the loopback host of the
	// verified backend, and require that the relay socket belongs to the client.
	host, _, err := net.SplitHostPort(backend)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !valid() || !ownsUDPRelay(pid, relayPort) {
		_ = conn.Close()
		return nil, errors.New("external tunnel client changed during UDP setup")
	}
	udp, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "udp4", net.JoinHostPort(host, strconv.Itoa(relayPort)))
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	associationCtx, cancel := context.WithCancel(ctx)
	stopRunner := context.AfterFunc(r.ctx, cancel)
	stopAssociation := context.AfterFunc(associationCtx, func() { _ = conn.Close(); _ = udp.Close() })
	// The Mieru association ends when its control connection ends (client
	// restart, crash or association teardown); stop relaying immediately.
	go func() { _, _ = io.Copy(io.Discard, conn); cancel() }()
	return &udpAssociationConn{Conn: udp, control: conn, cancel: cancel, stopRunner: stopRunner, stopAssociation: stopAssociation}, nil
}

func readSOCKSRelayPort(r io.Reader, typ byte) (int, error) {
	size := 0
	switch typ {
	case 1:
		size = 4
	case 4:
		size = 16
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(r, n[:]); err != nil || n[0] == 0 {
			return 0, errors.New("invalid Mieru SOCKS relay address")
		}
		size = int(n[0])
	default:
		return 0, errors.New("invalid Mieru SOCKS relay address type")
	}
	if _, err := io.ReadFull(r, make([]byte, size)); err != nil {
		return 0, err
	}
	var port [2]byte
	if _, err := io.ReadFull(r, port[:]); err != nil {
		return 0, err
	}
	n := int(binary.BigEndian.Uint16(port[:]))
	if n == 0 {
		return 0, errors.New("invalid Mieru SOCKS relay port")
	}
	return n, nil
}

type udpAssociationConn struct {
	net.Conn
	control         net.Conn
	cancel          context.CancelFunc
	stopRunner      func() bool
	stopAssociation func() bool
}

func (c *udpAssociationConn) Close() error {
	c.stopRunner()
	c.stopAssociation()
	c.cancel()
	err := c.Conn.Close()
	_ = c.control.Close()
	return err
}

type connectedBackend struct{ conn net.Conn }

func (d *connectedBackend) Dial(_, _ string) (net.Conn, error) { return d.take() }
func (d *connectedBackend) DialContext(context.Context, string, string) (net.Conn, error) {
	return d.take()
}

func (d *connectedBackend) take() (net.Conn, error) {
	if d.conn == nil {
		return nil, errors.New("backend connection already used")
	}
	c := d.conn
	d.conn = nil
	return c, nil
}

func (r *runner) stop() {
	if r.cancel != nil {
		r.cancel()
	}
	if r.gate != nil {
		r.gate.Close()
	}
	if r.done != nil {
		<-r.done
	}
	r.setState("stopped")
}

func (r *runner) command() (*exec.Cmd, error) {
	l, err := (&net.ListenConfig{}).Listen(r.ctx, "tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	backend := l.Addr().String()
	_, rawPort, _ := net.SplitHostPort(backend)
	port, _ := strconv.Atoi(rawPort)
	config, err := r.spec.Render(port)
	if err != nil {
		_ = l.Close()
		return nil, err
	}
	if err = os.MkdirAll(r.dir, 0o700); err != nil {
		_ = l.Close()
		return nil, err
	}
	if err = os.Chmod(r.dir, 0o700); err != nil {
		_ = l.Close()
		return nil, err
	}
	file := filepath.Join(r.dir, "client.json")
	if r.spec.Protocol == "trusttunnel" {
		file = filepath.Join(r.dir, "client.toml")
	}
	if err = os.WriteFile(file+".tmp", config, 0o600); err == nil {
		err = os.Rename(file+".tmp", file)
	}
	if err != nil {
		_ = l.Close()
		return nil, err
	}
	args := []string{file}
	if r.spec.Protocol == "mieru" {
		args = []string{"run"}
	}
	if r.spec.Protocol == "trusttunnel" {
		args = []string{"--config", file}
	}
	cmd := exec.CommandContext(r.ctx, r.binary, args...)
	cmd.Dir = r.dir
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if key == "MIERU_CONFIG_FILE" || key == "MIERU_CONFIG_JSON_FILE" || key == "HOME" || strings.HasSuffix(strings.ToUpper(key), "_PROXY") {
			continue
		}
		cmd.Env = append(cmd.Env, value)
	}
	cmd.Env = append(cmd.Env, "HOME="+r.dir, "MIERU_CONFIG_JSON_FILE="+file)
	cmd.Stdout, cmd.Stderr = r.logs, r.logs
	configureChild(cmd)
	cmd.Cancel = func() error { stopChild(cmd); return nil }
	r.mu.Lock()
	r.backend = backend
	r.mu.Unlock()
	_ = l.Close()
	return cmd, nil
}

func (r *runner) supervise() {
	defer close(r.done)
	delay := time.Second
	for r.ctx.Err() == nil {
		immediate := false
		if !executableExists(r.binary) {
			r.setState("missing-binary")
		} else {
			r.setState("starting")
			cmd, err := r.command()
			if err == nil {
				err = cmd.Start()
			}
			if err != nil {
				r.logs.add("Client could not start; verify executable and private configuration")
			} else {
				r.mu.Lock()
				r.pid = cmd.Process.Pid
				backend := r.backend
				r.mu.Unlock()
				exited := make(chan struct{})
				go func() { _ = cmd.Wait(); close(exited) }()
				ticker := time.NewTicker(100 * time.Millisecond)
				readyDeadline := time.NewTimer(15 * time.Second)
				running := true
				for running {
					select {
					case acknowledged := <-r.restart:
						r.setState("restarting")
						stopChild(cmd)
						<-exited
						r.mu.Lock()
						r.pid = 0
						r.mu.Unlock()
						close(acknowledged)
						immediate = true
						running = false
					case <-r.ctx.Done():
						stopChild(cmd)
						<-exited
						stopTimer(readyDeadline)
						ticker.Stop()
						return
					case <-exited:
						r.logs.add("Client exited; reconnecting with backoff")
						running = false
					case <-readyDeadline.C:
						r.logs.add("Client did not open its owned SOCKS listener")
						stopChild(cmd)
						<-exited
						running = false
					case <-ticker.C:
						if ownsListener(cmd.Process.Pid, backend) {
							r.setState("running")
							stopTimer(readyDeadline)
						}
					}
				}
				ticker.Stop()
				stopTimer(readyDeadline)
			}
			r.setState("backoff")
		}
		r.mu.Lock()
		r.pid = 0
		r.mu.Unlock()
		if immediate {
			delay = time.Second
			continue
		}
		select {
		case acknowledged := <-r.restart:
			close(acknowledged)
			delay = time.Second
			continue
		case <-r.ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}

func stopTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}
