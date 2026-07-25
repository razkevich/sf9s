//go:build localstack

// This suite runs the sf9s TUI against a live sf-localstack instance — a real
// Salesforce API emulator, not our own mock — so wrong assumptions about the
// API's shape surface here rather than in front of a user. It is build-tagged
// so `go test ./...` stays hermetic.
//
//	docker run -p 8080:8080 razkevich/sf-localstack
//	go test -tags localstack ./e2e/ -run TestLocalstack
//
// Override the address with SF9S_LOCALSTACK_URL.
package e2e

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/razkevich/sf9s/internal/api"
	"github.com/razkevich/sf9s/internal/sfcli"
)

func localstackURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("SF9S_LOCALSTACK_URL")
	if url == "" {
		url = "http://localhost:8080"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/services/data/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("sf-localstack not reachable at %s (%v) — start it with: docker run -p 8080:8080 razkevich/sf-localstack", url, err)
	}
	resp.Body.Close()
	return url
}

// startLocalstack boots the TUI pointed at the emulator, reusing the fake sf
// CLI to hand out its URL and a token the emulator accepts.
func startLocalstack(t *testing.T) *harness {
	t.Helper()
	t.Setenv("SF9S_MOCK_URL", localstackURL(t))
	return startWith(t)
}

func TestLocalstackOrgAndQuery(t *testing.T) {
	h := startLocalstack(t)
	h.waitFor(t, "Authenticated orgs (3)", "Org:")

	h.key(tea.KeyEnter)
	h.typeString("SELECT Id, Name FROM Account LIMIT 5")
	h.key(tea.KeyCtrlR)
	// The emulator answers with a real SOQL response envelope; an empty org
	// still reports its totalSize, which is what we assert on.
	h.waitFor(t, "rows in")
	h.quit(t)
}

func TestLocalstackSchemaAndLimits(t *testing.T) {
	h := startLocalstack(t)
	h.waitFor(t, "Authenticated orgs (3)")
	h.key(tea.KeyEnter)
	h.waitFor(t, "SOQL")
	h.key(tea.KeyEsc)
	h.waitFor(t, "Authenticated orgs")

	// Global describe (added to sf-localstack alongside this suite).
	h.palette("schema")
	h.waitFor(t, "Account", "Contact")

	h.palette("limits")
	h.waitFor(t, "DailyApiRequests", "DataStorageMB")
	h.quit(t)
}

func TestLocalstackAPICompatibility(t *testing.T) {
	url := localstackURL(t)
	client := api.NewClient(api.NewCachedTokenSource(func(context.Context) (api.Credentials, error) {
		return api.Credentials{
			AccessToken: "00D000000000001!FAKE_ACCESS_TOKEN",
			InstanceURL: url,
			APIVersion:  "60.0",
		}, nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := client.Query(ctx, "SELECT Id, Name FROM Account LIMIT 5", false); err != nil {
		t.Errorf("Query against the emulator failed: %v", err)
	}
	objects, err := client.DescribeGlobal(ctx)
	if err != nil {
		t.Errorf("DescribeGlobal failed: %v", err)
	} else if len(objects) == 0 {
		t.Error("DescribeGlobal returned no objects")
	}
	if _, err := client.DescribeSObject(ctx, "Account"); err != nil {
		t.Errorf("DescribeSObject failed: %v", err)
	}
	limits, err := client.Limits(ctx)
	if err != nil {
		t.Errorf("Limits failed: %v", err)
	} else if limits["DailyApiRequests"].Max == 0 {
		t.Errorf("Limits missing DailyApiRequests: %+v", limits)
	}
}

func TestLocalstackSfCLICompatibility(t *testing.T) {
	url := localstackURL(t)
	t.Setenv("SF9S_MOCK_URL", url)
	sf := sfcli.New(sfcli.ExecRunner{Bin: fakeSfBin})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	creds, err := sf.Credentials(ctx, "e2e@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if creds.InstanceURL != url {
		t.Errorf("instance URL = %q, want %q", creds.InstanceURL, url)
	}
}
