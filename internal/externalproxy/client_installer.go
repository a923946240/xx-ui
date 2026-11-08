package externalproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

type ClientStatus struct {
	Protocol, Name, Version, Release, LatestVersion, State, Error string
	Installed, Supported, UpdateAvailable                         bool
}

type clientDefinition struct{ protocol, name, repo string }

var clientDefinitions = []clientDefinition{
	{"naive", "NaiveProxy", "klzgrad/naiveproxy"},
	{"mieru", "mieru", "enfein/mieru"},
	{"trusttunnel", "TrustTunnel", "TrustTunnel/TrustTunnelClient"},
}

var (
	clientVersionPattern = regexp.MustCompile(`\d+\.\d+\.\d+(?:\.\d+)?`)
	clientTagPattern     = regexp.MustCompile(`^v[0-9][A-Za-z0-9.+_-]{0,79}$`)
)

type clientRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

type (
	clientAsset     struct{ tag, name, url, digest string }
	installedClient struct{ Tag, SHA256 string }
)

type ClientInstaller struct {
	mu          sync.Mutex
	bin         string
	states      map[string]ClientStatus
	busy        bool
	client      func() (*http.Client, error)
	apply       func(context.Context, string, func() error) error
	readVersion func(string, string) (string, error)
}

func NewClientInstaller(bin string, client func() (*http.Client, error), apply func(context.Context, string, func() error) error) *ClientInstaller {
	return &ClientInstaller{bin: bin, states: make(map[string]ClientStatus), client: client, apply: apply, readVersion: readClientVersion}
}

func clientDefinitionFor(protocol string) (clientDefinition, error) {
	for _, d := range clientDefinitions {
		if d.protocol == protocol {
			return d, nil
		}
	}
	return clientDefinition{}, errors.New("unsupported external client protocol")
}

func clientPlatformSupported() bool {
	return runtime.GOOS == "linux" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64")
}

func (i *ClientInstaller) List() []ClientStatus {
	i.mu.Lock()
	states := make(map[string]ClientStatus, len(i.states))
	for k, v := range i.states {
		states[k] = v
	}
	i.mu.Unlock()
	rows := make([]ClientStatus, 0, len(clientDefinitions))
	for _, d := range clientDefinitions {
		r := states[d.protocol]
		r.Protocol, r.Name, r.Supported = d.protocol, d.name, clientPlatformSupported()
		if r.State == "" {
			r.State = "idle"
		}
		path := filepath.Join(i.bin, d.protocol)
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			r.Installed = true
			r.Version, _ = i.readVersion(path, d.protocol)
			var meta installedClient
			if data, err := os.ReadFile(path + ".version.json"); err == nil && json.Unmarshal(data, &meta) == nil {
				if digest, err := clientFileDigest(path); err == nil && digest == meta.SHA256 {
					r.Release = meta.Tag
				}
			}
		}
		knownRelease := r.Release
		if knownRelease == "" && d.protocol != "naive" && r.Version != "" {
			knownRelease = "v" + r.Version
		}
		r.UpdateAvailable = r.Installed && r.LatestVersion != "" && knownRelease != r.LatestVersion
		rows = append(rows, r)
	}
	return rows
}

func (i *ClientInstaller) Start(protocol string, install bool) error {
	if _, err := clientDefinitionFor(protocol); err != nil {
		return err
	}
	if !clientPlatformSupported() {
		return errors.New("external client installation supports Linux amd64 and arm64 only")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.busy {
		return errors.New("an external client operation is already running")
	}
	i.busy = true
	r := i.states[protocol]
	r.State, r.Error = "checking", ""
	i.states[protocol] = r
	go i.run(protocol, install)
	return nil
}

func (i *ClientInstaller) state(protocol, state string, err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	r := i.states[protocol]
	r.State = state
	if err != nil {
		r.Error = err.Error()
	} else {
		r.Error = ""
	}
	i.states[protocol] = r
}

func (i *ClientInstaller) run(protocol string, install bool) {
	defer func() { i.mu.Lock(); i.busy = false; i.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	d, _ := clientDefinitionFor(protocol)
	client, err := i.client()
	if err == nil {
		client.CheckRedirect = clientDownloadRedirect
		err = i.perform(ctx, client, d, install)
	}
	if err != nil {
		i.state(protocol, "error", err)
	} else {
		i.state(protocol, "idle", nil)
	}
}

func clientDownloadRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 8 {
		return errors.New("too many download redirects")
	}
	u := req.URL
	if u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return errors.New("untrusted client download redirect")
	}
	switch u.Hostname() {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
		return nil
	}
	return errors.New("untrusted client download redirect")
}

func (i *ClientInstaller) perform(ctx context.Context, client *http.Client, d clientDefinition, install bool) error {
	asset, err := fetchClientRelease(ctx, client, d)
	if err != nil {
		return err
	}
	i.mu.Lock()
	r := i.states[d.protocol]
	r.LatestVersion = asset.tag
	i.states[d.protocol] = r
	i.mu.Unlock()
	if !install {
		return nil
	}
	if err := os.MkdirAll(i.bin, 0o755); err != nil {
		return errors.New("cannot create external client directory")
	}
	dir, err := os.MkdirTemp(i.bin, ".client-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	i.state(d.protocol, "downloading", nil)
	archive := filepath.Join(dir, "archive")
	if err := downloadClientArchive(ctx, client, asset, archive); err != nil {
		return err
	}
	i.state(d.protocol, "verifying", nil)
	staged := filepath.Join(dir, d.protocol)
	if err := extractClientBinary(ctx, archive, asset.name, d.protocol, staged); err != nil {
		return err
	}
	if err := validateClientELF(staged); err != nil {
		return err
	}
	version, err := i.readVersion(staged, d.protocol)
	if err != nil || version != clientVersionPattern.FindString(asset.tag) {
		return errors.New("downloaded client version does not match its release")
	}
	digest, err := clientFileDigest(staged)
	if err != nil {
		return err
	}
	dest := filepath.Join(i.bin, d.protocol)
	backup := dest + ".previous"
	hadPrevious, replaced := false, false
	i.state(d.protocol, "installing", nil)
	err = i.apply(ctx, d.protocol, func() error {
		if info, e := os.Lstat(dest); e == nil {
			if !info.Mode().IsRegular() {
				return errors.New("client destination must be a regular file")
			}
			if e := copyClientBackup(dest, backup); e != nil {
				return e
			}
			hadPrevious = true
		} else if !os.IsNotExist(e) {
			return e
		}
		if e := os.Chmod(staged, 0o755); e != nil {
			return e
		}
		if e := os.Rename(staged, dest); e != nil {
			return e
		}
		replaced = true
		return nil
	})
	if err == nil {
		data, _ := json.Marshal(installedClient{Tag: asset.tag, SHA256: digest})
		err = os.WriteFile(filepath.Join(dir, "version.json"), data, 0o600)
		if err == nil {
			err = os.Rename(filepath.Join(dir, "version.json"), dest+".version.json")
		}
	}
	if err != nil && replaced {
		restoreCtx, stop := context.WithTimeout(context.Background(), 35*time.Second)
		defer stop()
		restoreErr := i.apply(restoreCtx, d.protocol, func() error {
			if hadPrevious {
				return os.Rename(backup, dest)
			}
			return os.Remove(dest)
		})
		if restoreErr != nil {
			return errors.New("client update failed and automatic rollback failed; inspect the previous binary")
		}
		return errors.New("client activation failed; the previous installation was restored")
	}
	return err
}

func fetchClientRelease(ctx context.Context, client *http.Client, d clientDefinition) (clientAsset, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+d.repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "xx-ui-external-clients")
	resp, err := client.Do(req)
	if err != nil {
		return clientAsset{}, errors.New("cannot check the official release; check panel network/proxy settings")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return clientAsset{}, fmt.Errorf("official release lookup returned HTTP %d", resp.StatusCode)
	}
	var release clientRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&release); err != nil || !clientTagPattern.MatchString(release.Tag) || release.Draft || release.Prerelease {
		return clientAsset{}, errors.New("invalid official stable release metadata")
	}
	name := clientAssetName(d.protocol, release.Tag, runtime.GOARCH)
	for _, a := range release.Assets {
		if a.Name != name {
			continue
		}
		u, err := url.Parse(a.URL)
		if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/"+d.repo+"/releases/download/"+release.Tag+"/"+name {
			return clientAsset{}, errors.New("untrusted official release asset URL")
		}
		digest := strings.TrimPrefix(a.Digest, "sha256:")
		if raw, err := hex.DecodeString(digest); !strings.HasPrefix(a.Digest, "sha256:") || err != nil || len(raw) != sha256.Size {
			return clientAsset{}, errors.New("official release has no verifiable SHA-256 digest")
		}
		return clientAsset{release.Tag, name, a.URL, digest}, nil
	}
	return clientAsset{}, errors.New("the official release has no client for this architecture")
}

func clientAssetName(protocol, tag, arch string) string {
	switch protocol {
	case "naive":
		if arch == "amd64" {
			arch = "x64"
		}
		return "naiveproxy-" + tag + "-linux-" + arch + ".tar.xz"
	case "mieru":
		return "mieru_" + strings.TrimPrefix(tag, "v") + "_linux_" + arch + ".tar.gz"
	default:
		if arch == "amd64" {
			arch = "x86_64"
		} else {
			arch = "aarch64"
		}
		return "trusttunnel_client-" + tag + "-linux-" + arch + ".tar.gz"
	}
}

func downloadClientArchive(ctx context.Context, client *http.Client, asset clientAsset, path string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, asset.url, nil)
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("client download failed; check panel network/proxy settings")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("client download returned HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, (64<<20)+1))
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.New("client download was interrupted")
	}
	if n > 64<<20 {
		return errors.New("client archive exceeds 64 MiB")
	}
	if hex.EncodeToString(h.Sum(nil)) != asset.digest {
		return errors.New("client archive SHA-256 mismatch; installation was not changed")
	}
	return nil
}

func clientFileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyClientBackup(source, dest string) error {
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".client-backup-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = io.Copy(tmp, f)
	if err == nil {
		err = tmp.Chmod(0o755)
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp.Name(), dest)
}

type cappedVersionOutput struct{ data []byte }

func (b *cappedVersionOutput) Write(p []byte) (int, error) {
	n := len(p)
	if len(b.data) < 4096 {
		remaining := 4096 - len(b.data)
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

func readClientVersion(path, protocol string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	args := []string{"--version"}
	if protocol == "mieru" {
		args = []string{"version"}
	}
	cmd := exec.CommandContext(ctx, path, args...)
	out := &cappedVersionOutput{}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return "", errors.New("cannot read installed client version")
	}
	version := clientVersionPattern.FindString(string(out.data))
	if version == "" {
		return "", errors.New("client version output is not recognized")
	}
	return version, nil
}
