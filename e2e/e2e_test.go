// Package e2e drives the real sf9s TUI program — real renderer, real
// Bubble Tea runtime, a real `sf` subprocess (fakesf, built on the fly) and
// real HTTP against an in-process mock org — and asserts on the terminal
// output a user would see.
package e2e

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/razkevich/sf9s/e2e/mockorg"
	"github.com/razkevich/sf9s/internal/api"
	"github.com/razkevich/sf9s/internal/config"
	"github.com/razkevich/sf9s/internal/sfcli"
	"github.com/razkevich/sf9s/internal/ui"
)

var fakeSfBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sf9s-e2e")
	if err != nil {
		panic(err)
	}
	fakeSfBin = filepath.Join(dir, "sf")
	build := exec.Command("go", "build", "-o", fakeSfBin, "./testdata/fakesf")
	out, err := build.CombinedOutput()
	if err != nil {
		panic("building fakesf: " + string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type harness struct {
	tm      *teatest.TestModel
	org     *mockorg.Server
	copied  *[]string
	confDir string
}

func start(t *testing.T) *harness {
	t.Helper()
	org := mockorg.New()
	t.Cleanup(org.Close)
	t.Setenv("SF9S_MOCK_URL", org.URL)
	t.Chdir(t.TempDir())

	confDir := t.TempDir()
	store := config.NewStore(config.Paths{
		ConfigDir: filepath.Join(confDir, "config"),
		CacheDir:  filepath.Join(confDir, "cache"),
	})
	sf := sfcli.New(sfcli.ExecRunner{Bin: fakeSfBin})
	var copied []string
	deps := ui.Deps{
		SF:    sf,
		Store: store,
		NewAPI: func(username string) *api.Client {
			return api.NewClient(api.NewCachedTokenSource(func(ctx context.Context) (api.Credentials, error) {
				creds, err := sf.Credentials(ctx, username)
				if err != nil {
					return api.Credentials{}, err
				}
				return api.Credentials{AccessToken: creds.AccessToken, InstanceURL: creds.InstanceURL, APIVersion: creds.APIVersion}, nil
			}))
		},
		Clipboard: func(s string) error { copied = append(copied, s); return nil },
		OpenURL:   func(string) error { return nil },
		Version:   "e2e",
	}
	tm := teatest.NewTestModel(t, ui.New(deps), teatest.WithInitialTermSize(140, 40))
	return &harness{tm: tm, org: org, copied: &copied, confDir: confDir}
}

// waitFor blocks until every substring has been seen in the output stream
// since this call started. teatest consumes the stream, so callers must
// request everything they expect from one UI transition in a single call.
func (h *harness) waitFor(t *testing.T, substrs ...string) {
	t.Helper()
	teatest.WaitFor(t, h.tm.Output(), func(bts []byte) bool {
		for _, s := range substrs {
			if !bytes.Contains(bts, []byte(s)) {
				return false
			}
		}
		return true
	}, teatest.WithDuration(15*time.Second), teatest.WithCheckInterval(20*time.Millisecond))
}

func (h *harness) typeString(s string) {
	for _, r := range s {
		h.tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func (h *harness) key(k tea.KeyType) {
	h.tm.Send(tea.KeyMsg{Type: k})
}

func (h *harness) palette(view string) {
	h.typeString(":")
	h.typeString(view)
	h.key(tea.KeyEnter)
}

func (h *harness) quit(t *testing.T) {
	t.Helper()
	h.tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	h.tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

func TestJourneyOrgsQueryExport(t *testing.T) {
	h := start(t)
	// Org discovery: all three orgs listed, default org in the status bar.
	h.waitFor(t, "Authenticated orgs (3)", "scratchy", "2026-08-15", "⚡ e2e")

	h.key(tea.KeyEnter) // use org, jump to query view
	h.typeString("SELECT Id, Name, Owner.Name, AnnualRevenue FROM Account")
	h.key(tea.KeyCtrlR)
	// Results: rows, count toast, flattened relationship column.
	h.waitFor(t, "Acme Rockets", "3/3 rows", "Owner.Name")

	// Row card shows the full record vertically.
	h.key(tea.KeyEnter)
	h.waitFor(t, "AnnualRevenue", "12000000")
	h.key(tea.KeyEsc)

	// Export CSV lands in the working directory with flattened headers.
	h.typeString("e")
	h.waitFor(t, "exported sf9s-export-")
	entries, err := filepath.Glob("sf9s-export-*.csv")
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one export file, got %v (%v)", entries, err)
	}
	raw, _ := os.ReadFile(entries[0])
	if !strings.Contains(string(raw), "Id,Name,Owner.Name,AnnualRevenue") ||
		!strings.Contains(string(raw), "Acme Rockets") {
		t.Fatalf("export content wrong:\n%s", raw)
	}
	h.quit(t)
}

func TestJourneyPagination(t *testing.T) {
	h := start(t)
	h.waitFor(t, "Authenticated orgs (3)")
	h.key(tea.KeyEnter)
	h.typeString("SELECT Id, LastName FROM Contact")
	h.key(tea.KeyCtrlR)
	h.waitFor(t, "Page1Row", "1/4000 rows")
	h.typeString("m")
	h.waitFor(t, "Page2Row", "2/4000")
	h.quit(t)
}

func TestJourneyQueryErrorRecovery(t *testing.T) {
	h := start(t)
	h.waitFor(t, "Authenticated orgs (3)")
	h.key(tea.KeyEnter)
	h.org.FailQueries.Store(true)
	h.typeString("SELECT Id FRM Account")
	h.key(tea.KeyCtrlR)
	h.waitFor(t, "unexpected token: 'FRM'")

	h.org.FailQueries.Store(false)
	for range "SELECT Id FRM Account" {
		h.key(tea.KeyBackspace)
	}
	h.typeString("SELECT Id, Name FROM Account")
	h.key(tea.KeyCtrlR)
	h.waitFor(t, "Acme Rockets")
	h.quit(t)
}

func TestJourneySchemaLimitsDeploys(t *testing.T) {
	h := start(t)
	h.waitFor(t, "Authenticated orgs (3)")
	h.key(tea.KeyEnter)
	h.waitFor(t, "SOQL")
	h.key(tea.KeyEsc) // editor -> back to orgs (no results yet)
	h.waitFor(t, "Authenticated orgs")

	h.palette("schema")
	h.waitFor(t, "Invoice__c")
	h.typeString("/")
	h.typeString("Account")
	h.key(tea.KeyEnter)
	h.key(tea.KeyEnter)
	h.waitFor(t, "picklist(2)", "Technology, Energy")

	h.palette("limits")
	h.waitFor(t, "DailyApiRequests", "91.0%")

	h.palette("deploys")
	h.waitFor(t, "Succeeded", "0AfE2E0000001")
	h.quit(t)
}

func TestJourneyApexLogSearchAndDelete(t *testing.T) {
	h := start(t)
	h.waitFor(t, "Authenticated orgs (3)")
	h.key(tea.KeyEnter)
	h.waitFor(t, "SOQL")
	h.key(tea.KeyEsc)
	h.waitFor(t, "Authenticated orgs")

	h.palette("logs")
	h.waitFor(t, "/apex/execute")
	h.key(tea.KeyEnter)
	h.waitFor(t, "EXECUTION_STARTED")
	h.typeString("/")
	h.typeString("needle")
	h.key(tea.KeyEnter)
	h.waitFor(t, "match 1/1")
	h.key(tea.KeyEsc)

	h.typeString("d")
	h.waitFor(t, "delete selected log? y/n")
	h.typeString("y")
	h.waitFor(t, "log deleted")
	if len(h.org.DeletedLogs) != 1 || h.org.DeletedLogs[0] != "07LE2E0000001" {
		t.Fatalf("delete should hit the org exactly once: %v", h.org.DeletedLogs)
	}
	h.quit(t)
}

func TestJourneyMetadataBrowser(t *testing.T) {
	h := start(t)
	h.waitFor(t, "Authenticated orgs (3)")
	h.key(tea.KeyEnter)
	h.waitFor(t, "SOQL")
	h.key(tea.KeyEsc)
	h.waitFor(t, "Authenticated orgs")

	h.palette("meta")
	h.waitFor(t, "ApexClass")
	h.key(tea.KeyEnter)
	h.waitFor(t, "InvoiceService", "PaymentService", "unmanaged")

	h.typeString("y")
	h.waitFor(t, "copied: InvoiceService")
	if len(*h.copied) == 0 || (*h.copied)[len(*h.copied)-1] != "InvoiceService" {
		t.Fatalf("clipboard should hold component name: %v", *h.copied)
	}
	h.quit(t)
}

func TestJourneySavedQueriesPicker(t *testing.T) {
	h := start(t)
	h.waitFor(t, "Authenticated orgs (3)")
	h.key(tea.KeyEnter)
	h.waitFor(t, "SOQL")
	h.key(tea.KeyCtrlS)
	h.waitFor(t, "Saved queries", "Recent users") // starter library seeded on first run
	h.key(tea.KeyEsc)
	h.quit(t)
}
