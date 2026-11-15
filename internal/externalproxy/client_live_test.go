package externalproxy

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/a923946240/xx-ui/v3/internal/util/netproxy"
)

func TestExternalClientsOfficialInstallation(t *testing.T) {
	if os.Getenv("EXTERNAL_CLIENT_LIVE_TEST") != "1" {
		t.Skip("requires explicit official-release network test")
	}
	i := NewClientInstaller(t.TempDir(), func() (*http.Client, error) {
		return netproxy.NewHTTPClient(os.Getenv("EXTERNAL_CLIENT_TEST_PROXY"), 5*time.Minute)
	}, func(_ context.Context, _ string, commit func() error) error { return commit() })
	for index, d := range clientDefinitions {
		if err := i.Start(d.protocol, true); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(6 * time.Minute)
		for {
			i.mu.Lock()
			busy := i.busy
			i.mu.Unlock()
			if !busy {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("official installation did not finish")
			}
			time.Sleep(100 * time.Millisecond)
		}
		status := i.List()[index]
		if status.State != "idle" || !status.Installed || status.Release != status.LatestVersion || status.Version == "" || status.UpdateAvailable {
			t.Fatalf("official installation failed: %+v", status)
		}
		t.Logf("%s installed and reports version %s, verified release %s", d.name, status.Version, status.Release)
	}
}
