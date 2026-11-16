package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestNATSyncReaderKeyPersistsWithoutReturningPrivateMaterial(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	if key, err := natSyncPublicKey(false); err != nil || key != "" {
		t.Fatalf("unexpected key before creation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "nat-ssh-keys")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("looking up an absent key created state: %v", err)
	}
	type result struct {
		public string
		err    error
	}
	generated := make(chan result, 4)
	for range cap(generated) {
		go func() {
			key, err := natSyncPublicKey(true)
			generated <- result{public: key, err: err}
		}()
	}
	results := make([]result, cap(generated))
	for index := range results {
		results[index] = <-generated
	}
	first := results[0]
	if first.err != nil {
		t.Fatal(first.err)
	}
	key, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(first.public))
	if err != nil || key.Type() != ssh.KeyAlgoED25519 || len(rest) != 0 {
		t.Fatal("returned value is not one Ed25519 public key")
	}
	for _, again := range results[1:] {
		if again.err != nil || first.public != again.public {
			t.Fatalf("concurrent generation did not reuse the authorized key: %v", again.err)
		}
	}
	if stored, err := natSyncPublicKey(false); err != nil || stored != first.public {
		t.Fatalf("persisted key does not match the authorized key: %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(dir, "nat-ssh-keys", "reader-key"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := natSyncPublicKey(false); err == nil {
			t.Fatal("read a key with permissions for other users")
		}
	}
}

func TestNATSyncRelayReportRejectsStaleAndReservedAddresses(t *testing.T) {
	now := time.Unix(1700000060, 0)
	cases := []struct {
		name string
		body string
		ip   string
		seen int64
	}{
		{"json", `{"address":"8.8.4.4","seen_at":1700000010.125}`, "8.8.4.4", 1700000010},
		{"legacy", "8.8.4.4\n", "8.8.4.4", 0},
		{"stale", `{"address":"8.8.4.4","seen_at":1699999999}`, "", 0},
		{"future", `{"address":"8.8.4.4","seen_at":1700000060.125}`, "", 0},
		{"missing timestamp", `{"address":"8.8.4.4"}`, "", 0},
		{"documentation", "192.0.2.8", "", 0},
		{"multicast", "224.0.0.1", "", 0},
		{"carrier NAT", "100.64.0.8", "", 0},
		{"local", "127.0.0.1", "", 0},
		{"IPv6", "2606:4700:4700::1111", "", 0},
		{"multiple lines", "8.8.4.4\n9.9.9.9", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, err := parseNATSyncRelayReport([]byte(tc.body), now)
			if tc.ip == "" {
				if err == nil {
					t.Fatal("accepted an invalid NAT report")
				}
				return
			}
			if err != nil || report.CandidateIP != tc.ip || report.SeenAt != tc.seen {
				t.Fatalf("report has the wrong result: %+v, %v", report, err)
			}
		})
	}
}

func TestNATSyncRelayDialAllowsPrivateNetworkButBlocksMetadata(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		wantOK   bool
	}{
		{"10.0.10.20:22", true},
		{"[fd12:3456::5]:22", true},
		{"127.0.0.1:22", false},
		{"169.254.169.254:22", false},
		{"[::ffff:169.254.169.254]:22", false},
		{"[fd00:ec2::254]:22", false},
		{"100.100.100.200:22", false},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			err := guardNATSyncRelayDial(context.Background(), "tcp", tc.endpoint, nil)
			if (err == nil) != tc.wantOK {
				t.Fatalf("permitted=%t, want %t", err == nil, tc.wantOK)
			}
		})
	}
}

func TestNATSyncSSHUsesPinnedHostAndFixedReadCommand(t *testing.T) {
	newSigner := func() ssh.Signer {
		t.Helper()
		_, privateKey, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatal(err)
		}
		signer, err := ssh.NewSignerFromKey(privateKey)
		if err != nil {
			t.Fatal(err)
		}
		return signer
	}
	relayKey, readerKey := newSigner(), newSigner()
	for _, tc := range []struct {
		name        string
		fingerprint string
		response    []byte
		wantIP      string
	}{
		{"accepted", ssh.FingerprintSHA256(relayKey.PublicKey()), []byte("8.8.4.4\n"), "8.8.4.4"},
		{"wrong host", ssh.FingerprintSHA256(readerKey.PublicKey()), nil, ""},
		// Including a valid report prefix with only whitespace afterward would
		// succeed if io.Copy bypassed the bounded output writer.
		{
			"excessive output", ssh.FingerprintSHA256(relayKey.PublicKey()),
			append([]byte("8.8.4.4\n"), bytes.Repeat([]byte{' '}, 4096)...), "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			server := &ssh.ServerConfig{
				PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
					if conn.User() != "nat-sync" ||
						!bytes.Equal(key.Marshal(), readerKey.PublicKey().Marshal()) {
						return nil, errors.New("unexpected SSH identity")
					}
					return nil, nil
				},
			}
			server.AddHostKey(relayKey)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				sshConn, channels, requests, err := ssh.NewServerConn(conn, server)
				if err != nil {
					done <- err
					return
				}
				defer sshConn.Close()
				go ssh.DiscardRequests(requests)
				session, ok := <-channels
				if !ok || session.ChannelType() != "session" {
					done <- errors.New("expected an SSH session channel")
					return
				}
				channel, channelRequests, err := session.Accept()
				if err != nil {
					done <- err
					return
				}
				defer channel.Close()
				req, ok := <-channelRequests
				var payload struct{ Command string }
				if !ok || req.Type != "exec" || ssh.Unmarshal(req.Payload, &payload) != nil ||
					payload.Command != "xx-ui-nat-read" {
					done <- errors.New("expected only the fixed NAT read command")
					return
				}
				_ = req.Reply(true, nil)
				_, err = io.Copy(channel, bytes.NewReader(tc.response))
				if err == nil {
					_, err = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				}
				done <- err
			}()

			conn, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			cfg := NATSyncConfig{RelayUser: "nat-sync", HostKeyFingerprint: tc.fingerprint}
			report, err := readNATSyncSSHSession(conn, listener.Addr().String(), cfg, readerKey)
			if tc.wantIP == "" && err == nil {
				t.Fatal("expected the relay connection to be rejected")
			}
			if tc.name == "wrong host" && !errors.Is(err, errNATSyncHostKey) {
				t.Fatalf("mismatched key should report the fingerprint mismatch: %v", err)
			}
			if tc.wantIP != "" && (err != nil || report.CandidateIP != tc.wantIP) {
				t.Fatalf("got %+v, %v, want %s", report, err, tc.wantIP)
			}
			if err := <-done; err != nil && tc.response != nil {
				t.Fatalf("test SSH server: %v", err)
			}
		})
	}
}
