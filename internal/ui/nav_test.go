package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
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
	// The mapping must not follow the active org around.
	if got := m.orgForHotkey(1); got != "prod" {
		t.Errorf("hotkey 1 moved after a switch: %q", got)
	}
	if got := m.orgForHotkey(2); got != switched {
		t.Errorf("hotkey 2 should still be the org it switched to, got %q", got)
	}

	drive(t, m, key("2"))
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
		if m.active != ViewOrgs {
			t.Fatal("precondition: on the orgs view, where the numbers work")
		}
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

// Regression for the most dangerous behaviour found in review: after running
// a query, focus stayed in the results table, so typing the next query fired
// single-key commands — and a digit silently retargeted every later action at
// a different org.
func TestTypingAfterAQueryCannotSwitchOrg(t *testing.T) {
	m := multiOrgModel(t)
	qv := queryViewFor(t, m)
	qv.setEditorText("SELECT Id FROM Account")
	drive(t, m, key("ctrl+r"))

	if qv.focusResults {
		t.Fatal("focus must stay in the editor after a run, not jump to the table")
	}
	startOrg := m.current.Title()
	for _, r := range "SELECT Id FROM Contact LIMIT 5" {
		drive(t, m, key(string(r)))
	}
	if m.current.Title() != startOrg {
		t.Fatalf("typing a query switched org from %q to %q", startOrg, m.current.Title())
	}
	if got := qv.editor.Value(); got != "SELECT Id FROM AccountSELECT Id FROM Contact LIMIT 5" {
		t.Fatalf("every character should have reached the editor, got %q", got)
	}
}

func TestOrgHotkeysAreOrgsViewOnly(t *testing.T) {
	m := multiOrgModel(t)
	start := m.current.Title()
	drive(t, m, switchViewMsg{id: ViewLimits})
	if strings.Contains(m.View(), "<1> ") {
		t.Errorf("org numbers must not be advertised where they do nothing:\n%s", m.View())
	}
	drive(t, m, key("2"))
	if m.current.Title() != start {
		t.Fatalf("a digit outside the orgs view switched org to %q", m.current.Title())
	}
	drive(t, m, switchViewMsg{id: ViewOrgs})
	drive(t, m, key("2"))
	if m.current.Title() == start {
		t.Fatal("on the orgs view a digit should switch org")
	}
}

func TestHotkeyNumbersAreStable(t *testing.T) {
	m := multiOrgModel(t)
	before := m.hotkeyOrgs()
	drive(t, m, key("3"))
	after := m.hotkeyOrgs()
	if len(before) != len(after) {
		t.Fatalf("hotkey list changed length: %v → %v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("hotkey %d moved from %q to %q — numbers must not renumber themselves",
				i+1, before[i], after[i])
		}
	}
}

func TestQueryErrorStaysVisibleAndClearsStaleResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`[{"message":"unexpected token: 'FRM'\nposition 10","errorCode":"MALFORMED_QUERY"}]`))
	}))
	defer srv.Close()
	m := newTestModel(t, srv.URL)
	loadAllOrgs(t, m)
	qv := queryViewFor(t, m)
	qv.setEditorText("SELECT Id FRM Account")
	drive(t, m, key("ctrl+r"))

	// The toast fades; the error must not.
	drive(t, m, clearStatusMsg{id: m.statusSeq})
	view := m.View()
	if !strings.Contains(view, "unexpected token") {
		t.Fatalf("the failure must stay on screen after the toast fades:\n%s", view)
	}
	// A multi-line API message must not add rows and push the frame off screen.
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "position 10") && !strings.Contains(line, "unexpected token") {
			t.Errorf("error rendered across lines, which scrolls the app:\n%s", view)
		}
	}
	if lines := strings.Count(view, "\n") + 1; lines > m.height {
		t.Errorf("view is %d lines in a %d-line terminal", lines, m.height)
	}
}

func TestHelpFitsAndScrollsOnASmallTerminal(t *testing.T) {
	m := multiOrgModel(t)
	m.width, m.height = 80, 24
	drive(t, m, key("?"))

	view := m.View()
	if lines := strings.Count(view, "\n") + 1; lines > 24 {
		t.Fatalf("help rendered %d lines into a 24-line terminal", lines)
	}
	// The box must be closed and the affordance visible, not silently clipped.
	if !strings.Contains(view, "╰") {
		t.Errorf("help box has no bottom border — it is being clipped:\n%s", view)
	}
	if !strings.Contains(view, "more below") {
		t.Errorf("clipped help must say there is more:\n%s", view)
	}

	// Scrolling keys keep it open and reach the end.
	for i := 0; i < 12; i++ {
		drive(t, m, key("j"))
	}
	if !m.showHelp {
		t.Fatal("scrolling must not close the help overlay")
	}
	view = m.View()
	if !strings.Contains(view, "pgup / pgdn") {
		t.Errorf("the last entries should be reachable by scrolling:\n%s", view)
	}
	if !strings.Contains(view, "▴ end") {
		t.Errorf("the end of the list should say so:\n%s", view)
	}

	drive(t, m, key("x"))
	if m.showHelp {
		t.Error("a non-scrolling key should still close help")
	}
}

func TestHelpListsKeysTheViewActuallyHas(t *testing.T) {
	m := multiOrgModel(t)
	m.width, m.height = 120, 45
	drive(t, m, switchViewMsg{id: ViewQuery})
	// The editor takes "?" as a character, so f1 is the way in from there.
	drive(t, m, tea.KeyMsg{Type: tea.KeyF1})
	if !m.showHelp {
		t.Fatal("f1 must open help even while the editor has focus")
	}
	view := m.View()
	for _, want := range []string{"shift+tab", "ctrl+u", "open the record", "copy cell / row"} {
		if !strings.Contains(view, want) {
			t.Errorf("help is missing %q:\n%s", want, view)
		}
	}
}

func TestCommandModeSwitchesOrgByName(t *testing.T) {
	m := multiOrgModel(t)
	// Numbered hotkeys only reach the first nine; :org reaches any of them.
	drive(t, m, key(":"))
	for _, r := range "org staging" {
		drive(t, m, key(string(r)))
	}
	if !strings.Contains(m.View(), "switch to org") {
		t.Fatalf("command mode should preview the org switch:\n%s", m.View())
	}
	drive(t, m, key("enter"))
	if m.current.Alias != "staging" {
		t.Fatalf(":org staging selected %q", m.current.Alias)
	}
}

func TestCommandModeOrgMatchingIsHelpful(t *testing.T) {
	m := multiOrgModel(t)

	drive(t, m, switchOrgMsg{title: "nope"})
	if !strings.Contains(m.View(), "no org matching nope") {
		t.Errorf("an unknown org should say so:\n%s", m.View())
	}

	// A partial that matches several must not guess.
	drive(t, m, switchOrgMsg{title: "a"})
	view := m.View()
	if !strings.Contains(view, "matches") {
		t.Errorf("an ambiguous name should list the candidates:\n%s", view)
	}

	// A partial that matches one is enough.
	drive(t, m, switchOrgMsg{title: "stag"})
	if m.current.Alias != "staging" {
		t.Errorf("unambiguous partial should select it, got %q", m.current.Alias)
	}
}

func TestUnknownInitialOrgKeepsExplainingItself(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	m.deps.InitialOrg = "porduction"
	loadAllOrgs(t, m)
	if m.current != nil {
		t.Fatal("a typo must not select some other org")
	}

	// The toast fades; the reason must not, or the header is just dashes.
	drive(t, m, clearStatusMsg{id: m.statusSeq})
	view := m.View()
	if !strings.Contains(view, `org "porduction" was not found`) {
		t.Fatalf("the header should keep explaining why no org is selected:\n%s", view)
	}

	// Picking an org clears it.
	drive(t, m, key("enter"))
	if strings.Contains(m.View(), "was not found") {
		t.Errorf("the notice should go once an org is chosen:\n%s", m.View())
	}
}

// Every key a view advertises in its header legend must actually do
// something there. Two views once listed "s" for sort without binding it.
func TestAdvertisedKeysAreHandled(t *testing.T) {
	m := multiOrgModel(t)
	views := []ViewID{ViewOrgs, ViewQuery, ViewSchema, ViewLimits, ViewMeta, ViewDeploys, ViewLogs}

	// Labels whose handled form differs from how they are written for a
	// human reader.
	alias := map[string]string{"space": `, " ":`}

	// Keys handled centrally rather than by the view, and pane/navigation
	// hints that describe movement rather than a single binding.
	global := map[string]bool{
		":": true, "?": true, "f1": true, "f2": true, "ctrl+a": true, "esc": true,
		"↑↓": true, "1…9": true, "n/N": true, "enter/tab": true, "y/Y": true,
		"e/E": true, "ctrl+p/n": true, "/": true,
	}

	for _, id := range views {
		drive(t, m, switchViewMsg{id: id})
		v := m.views[id]
		if v == nil {
			t.Fatalf("%s view was not built", viewNames[id])
		}
		src := viewSource(t, viewNames[id])
		for _, hint := range v.Keys() {
			if global[hint.key] {
				continue
			}
			if a, ok := alias[hint.key]; ok {
				if !strings.Contains(src, a) {
					t.Errorf("%s advertises %q but never handles it", viewNames[id], hint.key)
				}
				continue
			}
			if !strings.Contains(src, `case "`+hint.key+`"`) &&
				!strings.Contains(src, `msg.String() == "`+hint.key+`"`) {
				t.Errorf("%s advertises %q (%s) but never handles it",
					viewNames[id], hint.key, hint.desc)
			}
		}
	}
}

func viewSource(t *testing.T, view string) string {
	t.Helper()
	b, err := os.ReadFile(view + ".go")
	if err != nil {
		t.Fatalf("reading %s.go: %v", view, err)
	}
	return string(b)
}
