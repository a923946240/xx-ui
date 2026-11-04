package externalproxy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type clientFixtureTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (t clientFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = t.target.Scheme
	u.Host = t.target.Host
	copy.URL = &u
	return t.base.RoundTrip(copy)
}

func clientFixtureArchive(t *testing.T, duplicate bool) []byte {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	writer := tar.NewWriter(gz)
	entries := 1
	if duplicate {
		entries = 2
	}
	for range entries {
		if err := writer.WriteHeader(&tar.Header{Name: "bundle/mieru", Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func newClientFixture(t *testing.T, archive []byte, digest, assetURL string, apply func(context.Context, string, func() error) error) *ClientInstaller {
	t.Helper()
	name := clientAssetName("mieru", "v0.0.1", runtime.GOARCH)
	if assetURL == "" {
		assetURL = "https://github.com/enfein/mieru/releases/download/v0.0.1/" + name
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/enfein/mieru/releases/latest" {
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.0.1", "assets": []map[string]string{{"name": name, "browser_download_url": assetURL, "digest": digest}}})
			return
		}
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	client := server.Client()
	client.Transport = clientFixtureTransport{client.Transport, target}
	i := NewClientInstaller(t.TempDir(), func() (*http.Client, error) { return client, nil }, apply)
	i.readVersion = func(string, string) (string, error) { return "0.0.1", nil }
	return i
}

func waitClientFixture(t *testing.T, i *ClientInstaller) ClientStatus {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		i.mu.Lock()
		busy := i.busy
		i.mu.Unlock()
		if !busy {
			return i.List()[1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("client operation did not finish")
	return ClientStatus{}
}

func TestExternalClientRejectsUnverifiedDownloads(t *testing.T) {
	archive := clientFixtureArchive(t, false)
	hash := sha256.Sum256(archive)
	digest := "sha256:" + hex.EncodeToString(hash[:])
	for _, test := range []struct{ name, digest, url, want string }{
		{"wrong digest", "sha256:" + strings.Repeat("0", 64), "", "SHA-256 mismatch"},
		{"missing digest", "", "", "no verifiable SHA-256"},
		{"foreign URL", digest, "https://attacker.invalid/client.tar.gz", "untrusted official release asset URL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			i := newClientFixture(t, archive, test.digest, test.url, func(_ context.Context, _ string, commit func() error) error { called = true; return commit() })
			path := filepath.Join(i.bin, "mieru")
			original := []byte("original executable")
			if err := os.WriteFile(path, original, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := i.Start("mieru", true); err != nil {
				t.Fatal(err)
			}
			status := waitClientFixture(t, i)
			if status.State != "error" || !strings.Contains(status.Error, test.want) {
				t.Fatalf("status %+v", status)
			}
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, original) || called {
				t.Fatalf("unverified download modified installation: %q, %v, applied=%v", data, err, called)
			}
		})
	}
}

func TestExternalClientInstallsAtomicallyAndRollsBackActivationFailure(t *testing.T) {
	archive := clientFixtureArchive(t, false)
	hash := sha256.Sum256(archive)
	exe, _ := os.Executable()
	expected, _ := os.ReadFile(exe)
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{true: "activation rollback", false: "verified install"}[fail], func(t *testing.T) {
			calls := 0
			i := newClientFixture(t, archive, "sha256:"+hex.EncodeToString(hash[:]), "", func(_ context.Context, _ string, commit func() error) error {
				calls++
				if err := commit(); err != nil {
					return err
				}
				if fail && calls == 1 {
					return errors.New("activation failed")
				}
				return nil
			})
			path := filepath.Join(i.bin, "mieru")
			original := []byte("previous client")
			if err := os.WriteFile(path, original, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := i.Start("mieru", true); err != nil {
				t.Fatal(err)
			}
			status := waitClientFixture(t, i)
			actual, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if !bytes.Equal(actual, original) || status.State != "error" || !strings.Contains(status.Error, "restored") || calls != 2 {
					t.Fatalf("rollback failed: status=%+v calls=%d", status, calls)
				}
				return
			}
			backup, err := os.ReadFile(path + ".previous")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actual, expected) || !bytes.Equal(backup, original) || status.State != "idle" || status.Release != "v0.0.1" || status.UpdateAvailable || calls != 1 {
				t.Fatalf("installation mismatch: status=%+v calls=%d", status, calls)
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0o755 {
				t.Fatalf("mode %v", info.Mode())
			}
		})
	}
}

func TestExternalClientRejectsDuplicateExecutables(t *testing.T) {
	archive := clientFixtureArchive(t, true)
	hash := sha256.Sum256(archive)
	i := newClientFixture(t, archive, "sha256:"+hex.EncodeToString(hash[:]), "", func(_ context.Context, _ string, commit func() error) error { return commit() })
	if err := i.Start("mieru", true); err != nil {
		t.Fatal(err)
	}
	status := waitClientFixture(t, i)
	if status.State != "error" || !strings.Contains(status.Error, "duplicate") {
		t.Fatalf("duplicate executable accepted: %+v", status)
	}
	if _, err := os.Stat(filepath.Join(i.bin, "mieru")); !os.IsNotExist(err) {
		t.Fatalf("invalid archive left an installed client: %v", err)
	}
}

func TestExternalClientRejectsArbitraryProtocolsAndRedirects(t *testing.T) {
	i := NewClientInstaller(t.TempDir(), nil, nil)
	if err := i.Start("../naive", true); err == nil {
		t.Fatal("arbitrary executable path accepted")
	}
	for _, raw := range []string{"http://github.com/client", "https://github.com.attacker.invalid/client", "https://user:password@github.com/client", "https://github.com:8443/client"} {
		u, _ := url.Parse(raw)
		if err := clientDownloadRedirect(&http.Request{URL: u}, nil); err == nil {
			t.Fatalf("untrusted redirect accepted: %s", u.Host)
		}
	}
}
