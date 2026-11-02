package externalproxy

import (
	"context"
	"errors"
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
		select {
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
