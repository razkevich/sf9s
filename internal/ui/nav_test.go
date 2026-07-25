package ui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/razkevich/sf9s/internal/api"
	"github.com/razkevich/sf9s/internal/sfcli"
)

func multiOrgModel(t *testing.T) *Model {
	t.Helper()
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	m.deps.SF = sfcli.New(fakeRunner{out: map[string]string{
		"org list": `{"status":0,"result":{"nonScratchOrgs":[
			{"username":"a@corp.com","aliases":["prod"],"orgId":"00D1","instanceUrl":"https://x.my.salesforce.com","connectedStatus":"Connected","isDefaultUsername":true},
			{"username":"b@corp.com","aliases":["staging"],"orgId":"00D2","instanceUrl":"https://y.my.salesforce.com","connectedStatus":"Connected"},
			{"username":"c@corp.com","aliases":["qa"],"orgId":"00D3","instanceUrl":"https://z.my.salesforce.com","connectedStatus":"Connected"}
		],"scratchOrgs":[]}}`,
	}})
	loadAllOrgs(t, m)
	return m
}

func TestNumberKeysSwitchOrg(t *testing.T) {
	m := multiOrgModel(t)
	if m.current == nil || m.current.Alias != "prod" {
		t.Fatalf("precondition: default org selected, got %v", m.current)
	}

	// <1> is always the current org, so the numbering stays stable while you
	// work; <2> and <3> are the others in list order.
	if got := m.orgForHotkey(1); got != "prod" {
		t.Errorf("hotkey 1 = %q, want the current org", got)
	}
	drive(t, m, key("2"))
	if m.current.Alias == "prod" {
		t.Fatal("<2> should switch to a different org")
	}
	switched := m.current.Alias
	if !strings.Contains(m.View(), "switched to "+switched) {
		t.Errorf("switch should be announced:\n%s", m.View())
	}
	if got := m.orgForHotkey(1); got != switched {
		t.Errorf("after switching, hotkey 1 should be the new org, got %q", got)
	}

	drive(t, m, key("1"))
	if !strings.Contains(m.View(), "already on "+switched) {
		t.Errorf("pressing the current org's key should say so:\n%s", m.View())
	}
	drive(t, m, key("9"))
	if !strings.Contains(m.View(), "no org on <9>") {
		t.Errorf("an unused number should say so:\n%s", m.View())
	}
}

func TestNumberKeysDoNotHijackTyping(t *testing.T) {
	m := multiOrgModel(t)
	qv := queryViewFor(t, m)
	qv.setEditorText("")
	drive(t, m, key("2"))
	if got := qv.editor.Value(); got != "2" {
		t.Fatalf("digits must reach a focused editor, editor = %q", got)
	}
	if m.current.Alias != "prod" {
		t.Errorf("typing in the editor must not switch org, now %q", m.current.Alias)
	}
}

func TestEscBailsOneLevelAtATime(t *testing.T) {
	m := multiOrgModel(t)
	drive(t, m, switchViewMsg{id: ViewLimits})
	drive(t, m, switchViewMsg{id: ViewMeta})
	if m.active != ViewMeta {
		t.Fatalf("precondition: on meta, got %v", m.active)
	}
	if !strings.Contains(m.View(), "orgs") || !strings.Contains(m.View(), "limits") {
		t.Fatalf("crumbs should show the trail:\n%s", m.View())
	}

	drive(t, m, key("esc"))
	if m.active != ViewLimits {
		t.Fatalf("esc should bail to the previous view, got %v", m.active)
	}
	drive(t, m, key("esc"))
	if m.active != ViewOrgs {
		t.Fatalf("esc should continue back to orgs, got %v", m.active)
	}
	drive(t, m, key("esc"))
	if m.active != ViewOrgs {
		t.Fatalf("esc on orgs should stay put, got %v", m.active)
	}
}

func TestEscLetsTheViewBailFirst(t *testing.T) {
	m := multiOrgModel(t)
	drive(t, m, switchViewMsg{id: ViewSchema})
	sv := m.views[ViewSchema].(*schemaView)
	if cmd := sv.Init(); cmd != nil {
		drive(t, m, cmd())
	}
	drive(t, m, key("enter")) // into the field list
	if !sv.inFields {
		t.Fatal("precondition: viewing fields")
	}
	drive(t, m, key("esc"))
	if sv.inFields {
		t.Error("esc should leave the field list first")
	}
	if m.active != ViewSchema {
		t.Fatalf("that esc should not leave the schema view, got %v", m.active)
	}
	drive(t, m, key("esc"))
	if m.active != ViewOrgs {
		t.Fatalf("the next esc should bail out of the view, got %v", m.active)
	}
}

func TestCrumbTrailHasNoLoops(t *testing.T) {
	m := multiOrgModel(t)
	drive(t, m, switchViewMsg{id: ViewLimits})
	drive(t, m, switchViewMsg{id: ViewMeta})
	drive(t, m, switchViewMsg{id: ViewLimits}) // revisit
	if len(m.stack) != 1 || m.stack[0] != ViewOrgs {
		t.Fatalf("revisiting a view should truncate the trail there, got %v", m.stack)
	}
}

func TestPaletteAliasesAndQuit(t *testing.T) {
	m := multiOrgModel(t)

	// :sc is schema's alias.
	drive(t, m, key(":"))
	for _, r := range "sc" {
		drive(t, m, key(string(r)))
	}
	drive(t, m, key("enter"))
	if m.active != ViewSchema {
		t.Fatalf(":sc should open schema, got %v", m.active)
	}

	// :q must quit (k9s), not open "query" — exact aliases win over prefixes.
	drive(t, m, key(":"))
	drive(t, m, key("q"))
	if got := m.palette.selectedItem(); !got.quit {
		t.Fatalf(":q should select quit, selected %q", got.name)
	}

	// :sql reaches the query view.
	m.palette.open = false
	drive(t, m, key(":"))
	for _, r := range "sql" {
		drive(t, m, key(string(r)))
	}
	drive(t, m, key("enter"))
	if m.active != ViewQuery {
		t.Fatalf(":sql should open the query view, got %v", m.active)
	}
}

func TestCtrlAListsEveryViewWithAliases(t *testing.T) {
	m := multiOrgModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	view := m.View()
	if !strings.Contains(view, "Views") {
		t.Fatalf("ctrl+a should open the view list:\n%s", view)
	}
	for _, want := range []string{"schema", "sobjects, sc", "logs", "log, apex", "quit", "q, exit"} {
		if !strings.Contains(view, want) {
			t.Errorf("view list missing %q:\n%s", want, view)
		}
	}
}

func TestCompactHeaderOnSmallTerminal(t *testing.T) {
	m := multiOrgModel(t)
	m.width, m.height = 80, 14
	if !m.compactHeader() {
		t.Fatal("a 80x14 terminal should use the compact header")
	}
	view := m.View()
	if lines := strings.Split(view, "\n"); len(lines) > 14 {
		t.Errorf("rendered %d lines into a 14-line terminal", len(lines))
	}
	if !strings.Contains(view, "sf9s") || !strings.Contains(view, "prod") {
		t.Errorf("compact header should still orient the user:\n%s", view)
	}

	m.width, m.height = 120, 40
	if m.compactHeader() {
		t.Error("a 120x40 terminal should use the full header")
	}
}

func TestHeaderKeepsOrgHotkeysWhenNarrow(t *testing.T) {
	m := multiOrgModel(t)
	// Long aliases and a long instance host, the way a real org list looks.
	for i := range m.orgs {
		m.orgs[i].Alias = "very-long-org-alias-" + m.orgs[i].Alias
		m.orgs[i].InstanceURL = "https://some-long-instance-name.my.salesforce.com"
	}
	m.setOrg(m.orgs[0])

	for _, width := range []int{130, 110, 95} {
		m.width, m.height = width, 40
		view := m.View()
		if !strings.Contains(view, "<1>") {
			t.Errorf("width %d dropped the org hotkeys:\n%s", width, view)
		}
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > width {
				t.Errorf("width %d: line overflows by %d", width, lipgloss.Width(line)-width)
				break
			}
		}
	}
}

func TestProductionOrgIsUnmistakable(t *testing.T) {
	m := multiOrgModel(t)
	if m.current == nil {
		t.Fatal("precondition: an org is selected")
	}
	// Before the org answers, nothing is claimed either way.
	if m.inProduction() {
		t.Error("production must not be assumed before the org says so")
	}

	drive(t, m, orgInfoMsg{orgID: m.current.OrgID, info: &api.OrgInfo{
		Name: "Acme", OrganizationType: "Enterprise Edition",
	}})
	if !m.inProduction() {
		t.Fatal("an Enterprise org that is not a sandbox or trial is production")
	}
	view := m.View()
	if !strings.Contains(view, "PROD") {
		t.Fatalf("the header must mark production unmistakably:\n%s", view)
	}
	if !strings.Contains(view, "enterprise") {
		t.Fatalf("the header should show the real edition:\n%s", view)
	}
	if !strings.Contains(view, "is PRODUCTION") {
		t.Fatalf("switching into production should warn:\n%s", view)
	}
}

func TestSandboxIsNotFlaggedAsProduction(t *testing.T) {
	m := multiOrgModel(t)
	drive(t, m, orgInfoMsg{orgID: m.current.OrgID, info: &api.OrgInfo{
		OrganizationType: "Enterprise Edition", IsSandbox: true,
	}})
	if m.inProduction() {
		t.Fatal("a sandbox of an enterprise org is not production")
	}
	if strings.Contains(m.View(), "PROD ") {
		t.Fatalf("no production badge belongs on a sandbox:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "sandbox") {
		t.Fatalf("the header should say sandbox:\n%s", m.View())
	}
}

func TestOrgInfoFailureIsNonFatal(t *testing.T) {
	m := multiOrgModel(t)
	before := m.View()
	drive(t, m, orgInfoMsg{orgID: m.current.OrgID, err: context.DeadlineExceeded})
	if m.inProduction() {
		t.Error("a failed identity lookup must not claim production")
	}
	if m.current == nil {
		t.Error("a failed identity lookup must not unselect the org")
	}
	if strings.Contains(m.View(), "deadline") {
		t.Errorf("identity is advisory; its failure should not shout at the user:\nbefore:\n%s\nafter:\n%s", before, m.View())
	}
}

func TestOrgListLabelsProductionWhenKnown(t *testing.T) {
	m := multiOrgModel(t)
	target := m.orgs[1]
	drive(t, m, orgInfoMsg{orgID: target.OrgID, info: &api.OrgInfo{OrganizationType: "Unlimited Edition"}})
	view := m.View()
	if !strings.Contains(view, "PRODUCTION") {
		t.Fatalf("the org list should label a known production org:\n%s", view)
	}
	// And an org we have never opened stays honestly unlabelled.
	if strings.Count(view, "PRODUCTION") != 1 {
		t.Fatalf("only the org we asked about should be labelled:\n%s", view)
	}
}
