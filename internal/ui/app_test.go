package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/razkevich/sf9s/internal/api"
	"github.com/razkevich/sf9s/internal/config"
	"github.com/razkevich/sf9s/internal/sfcli"
)

type fakeRunner struct{ out map[string]string }

func (f fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	out, ok := f.out[key]
	if !ok {
		return []byte(`{"status":1,"message":"unexpected command: ` + key + `"}`), nil
	}
	return []byte(out), nil
}

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/services/data/v64.0/query"):
			w.Write([]byte(`{"totalSize":2,"done":true,"records":[
				{"attributes":{"type":"Account"},"Id":"001A","Name":"Acme"},
				{"attributes":{"type":"Account"},"Id":"001B","Name":"Globex"}]}`))
		case r.URL.Path == "/services/data/v64.0/sobjects":
			w.Write([]byte(`{"sobjects":[{"name":"Account","label":"Account","custom":false,"keyPrefix":"001","queryable":true},{"name":"Hidden","label":"Hidden","queryable":false}]}`))
		case r.URL.Path == "/services/data/v64.0/sobjects/Account/describe":
			w.Write([]byte(`{"name":"Account","label":"Account","keyPrefix":"001","fields":[{"name":"Id","label":"Account ID","type":"id"},{"name":"Name","label":"Name","type":"string","length":255,"nillable":false,"createable":true,"updateable":true}]}`))
		case r.URL.Path == "/services/data/v64.0/limits":
			w.Write([]byte(`{"DailyApiRequests":{"Max":100000,"Remaining":25000}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`[{"message":"not found","errorCode":"NOT_FOUND"}]`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const testOrgList = `{"status":0,"result":{"nonScratchOrgs":[
	{"username":"alex@corp.com","aliases":["prod"],"orgId":"00D1","instanceUrl":"https://x.my.salesforce.com","connectedStatus":"Connected","isDefaultUsername":true}
],"scratchOrgs":[]}}`

func newTestModel(t *testing.T, srvURL string) *Model {
	t.Helper()
	dir := t.TempDir()
	store := config.NewStore(config.Paths{ConfigDir: filepath.Join(dir, "c"), CacheDir: filepath.Join(dir, "cache")})
	runner := fakeRunner{out: map[string]string{"org list": testOrgList}}
	var copied []string
	deps := Deps{
		SF:    sfcli.New(runner),
		Store: store,
		NewAPI: func(username string) *api.Client {
			return api.NewClient(staticCreds{url: srvURL})
		},
		Clipboard: func(s string) error { copied = append(copied, s); return nil },
		OpenURL:   func(string) error { return nil },
		Version:   "test",
	}
	m := New(deps)
	m.width, m.height = 120, 40
	m.statusTTL = time.Millisecond
	return m
}

type staticCreds struct{ url string }

func (s staticCreds) Credentials(context.Context, bool) (api.Credentials, error) {
	return api.Credentials{AccessToken: "tok", InstanceURL: s.url, APIVersion: "64.0"}, nil
}

// drive pumps a message and any resulting async cmds to completion.
func drive(t *testing.T, m *Model, msg tea.Msg) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	queue := []tea.Msg{msg}
	for len(queue) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("message loop did not settle")
		}
		next := queue[0]
		queue = queue[1:]
		_, cmd := m.Update(next)
		if cmd == nil {
			continue
		}
		out := cmd()
		queue = append(queue, expand(out)...)
	}
}

func expand(msg tea.Msg) []tea.Msg {
	switch batch := msg.(type) {
	case tea.BatchMsg:
		var msgs []tea.Msg
		for _, c := range batch {
			if c != nil {
				msgs = append(msgs, expand(c())...)
			}
		}
		return msgs
	case nil:
		return nil
	}
	// Drop self-perpetuating timer messages (status clears, spinner ticks,
	// cursor blinks) so the synchronous driver terminates.
	switch msg.(type) {
	case clearStatusMsg, spinner.TickMsg, cursor.BlinkMsg:
		return nil
	}
	return []tea.Msg{msg}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+t":
		return tea.KeyMsg{Type: tea.KeyCtrlT}
	case "ctrl+p":
		return tea.KeyMsg{Type: tea.KeyCtrlP}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestStartupToOrgsView(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	drive(t, m, m.loadOrgs()())
	view := m.View()
	if !strings.Contains(view, "prod") || !strings.Contains(view, "alex@corp.com") {
		t.Fatalf("orgs view should list org:\n%s", view)
	}
	if m.current == nil || m.current.Alias != "prod" {
		t.Fatal("default org should be auto-selected")
	}
	if !strings.Contains(view, "⚡ prod") {
		t.Fatalf("status bar should show current org:\n%s", view)
	}
}

func TestNoOrgsFriendlyError(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	m.deps.SF = sfcli.New(fakeRunner{out: map[string]string{
		"org list": `{"status":0,"result":{"nonScratchOrgs":[],"scratchOrgs":[]}}`,
	}})
	drive(t, m, m.loadOrgs()())
	view := m.View()
	if !strings.Contains(view, "No authenticated orgs") || !strings.Contains(view, "sf org login web") {
		t.Fatalf("expected friendly no-orgs screen:\n%s", view)
	}
}

func TestQueryFlowEndToEnd(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	drive(t, m, m.loadOrgs()())
	drive(t, m, key("enter")) // select org, jump to query view
	if m.active != ViewQuery {
		t.Fatalf("enter on org should open query view, active=%v", m.active)
	}
	qv := m.views[ViewQuery].(*queryView)
	qv.setEditorText("SELECT Id, Name FROM Account")
	drive(t, m, key("ctrl+r"))
	view := m.View()
	if !strings.Contains(view, "Acme") || !strings.Contains(view, "Globex") {
		t.Fatalf("query results should render:\n%s", view)
	}
	if !qv.focusResults {
		t.Fatal("focus should move to results after a successful query")
	}
	if got := m.deps.Store.History(); len(got) != 1 || got[0] != "SELECT Id, Name FROM Account" {
		t.Fatalf("query should be persisted to history: %v", got)
	}
}

func TestQueryErrorSurfacesToast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`[{"message":"unexpected token: 'FORM'","errorCode":"MALFORMED_QUERY"}]`))
	}))
	defer srv.Close()
	m := newTestModel(t, srv.URL)
	drive(t, m, m.loadOrgs()())
	drive(t, m, key("enter"))
	qv := m.views[ViewQuery].(*queryView)
	qv.setEditorText("SELECT Id FORM Account")
	drive(t, m, key("ctrl+r"))
	if !strings.Contains(m.View(), "unexpected token") {
		t.Fatalf("API error should surface in status bar:\n%s", m.View())
	}
	if qv.running {
		t.Fatal("running flag must clear after error")
	}
}

func TestSchemaBrowseAndBuildQuery(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	drive(t, m, m.loadOrgs()())
	drive(t, m, switchViewMsg{id: ViewSchema})
	sv := m.views[ViewSchema].(*schemaView)
	if cmd := sv.Init(); cmd != nil {
		drive(t, m, cmd())
	}
	view := m.View()
	if !strings.Contains(view, "Account") {
		t.Fatalf("schema should list objects:\n%s", view)
	}
	if strings.Contains(view, "Hidden") {
		t.Fatalf("non-queryable objects should be hidden:\n%s", view)
	}
	drive(t, m, key("enter")) // open fields
	view = m.View()
	if !strings.Contains(view, "string(255)") {
		t.Fatalf("fields table should show typed fields:\n%s", view)
	}
	drive(t, m, key("c")) // build query
	if m.active != ViewQuery {
		t.Fatal("c should jump to query view")
	}
	qv := m.views[ViewQuery].(*queryView)
	if !strings.Contains(qv.editor.Value(), "SELECT Id, Name FROM Account") {
		t.Fatalf("query should be prefilled, got %q", qv.editor.Value())
	}
}

func TestLimitsView(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	drive(t, m, m.loadOrgs()())
	drive(t, m, switchViewMsg{id: ViewLimits})
	lv := m.views[ViewLimits].(*limitsView)
	if cmd := lv.Init(); cmd != nil {
		drive(t, m, cmd())
	}
	view := m.View()
	if !strings.Contains(view, "DailyApiRequests") || !strings.Contains(view, "75.0%") {
		t.Fatalf("limits should render with usage percent:\n%s", view)
	}
}

func TestPaletteNavigation(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	drive(t, m, m.loadOrgs()())
	drive(t, m, key(":"))
	if !m.palette.open {
		t.Fatal("palette should open on :")
	}
	for _, r := range "lim" {
		drive(t, m, key(string(r)))
	}
	drive(t, m, key("enter"))
	if m.active != ViewLimits {
		t.Fatalf("palette should jump to limits, active=%v", m.active)
	}
}

func TestOrgGuardOnViews(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	m.deps.SF = sfcli.New(fakeRunner{out: map[string]string{
		"org list": `{"status":0,"result":{"nonScratchOrgs":[
			{"username":"a@b.c","aliases":["x"],"orgId":"00D2","instanceUrl":"https://y.my.salesforce.com","connectedStatus":"Connected","isDefaultUsername":false}
		],"scratchOrgs":[]}}`,
	}})
	drive(t, m, m.loadOrgs()())
	if m.current != nil {
		t.Fatal("no default org — nothing should be auto-selected")
	}
	drive(t, m, switchViewMsg{id: ViewQuery})
	if m.active != ViewOrgs {
		t.Fatal("navigation without an org must stay on orgs view")
	}
	if !strings.Contains(m.View(), "select an org first") {
		t.Fatalf("expected guard toast:\n%s", m.View())
	}
}

func TestHelpOverlay(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	drive(t, m, m.loadOrgs()())
	drive(t, m, key("?"))
	if !strings.Contains(m.View(), "sf9s — keys") {
		t.Fatalf("help overlay should render:\n%s", m.View())
	}
	drive(t, m, key("x"))
	if m.showHelp {
		t.Fatal("any key should close help")
	}
}

func TestClipboardCopyInstanceURL(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	var copied []string
	m.deps.Clipboard = func(s string) error { copied = append(copied, s); return nil }
	drive(t, m, m.loadOrgs()())
	drive(t, m, key("Y"))
	if len(copied) != 1 || copied[0] != "https://x.my.salesforce.com" {
		t.Fatalf("Y should copy instance URL, copied=%v", copied)
	}
}
