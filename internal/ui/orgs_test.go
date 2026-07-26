package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/razkevich/sf9s/internal/api"
	"github.com/razkevich/sf9s/internal/sfcli"
)

// The status the sf CLI prints for a deactivated user. The diagnosis is the
// last two words, and the Status column has room for none of them.
const inactiveUserStatus = "Unable to refresh session due to: Error authenticating with the refresh token due to: inactive user"

// brokenOrgModel is an org list in the states users actually find it in: one
// working org, one whose user was deactivated, one behind an unreachable host,
// and one dead scratch org.
func brokenOrgModel(t *testing.T) *Model {
	t.Helper()
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	m.deps.SF = sfcli.New(fakeRunner{out: map[string]string{
		"org list": `{"status":0,"result":{"nonScratchOrgs":[
			{"username":"alex@corp.com","aliases":["prod"],"orgId":"00D1","instanceUrl":"https://x.my.salesforce.com","connectedStatus":"Connected","isDefaultUsername":true},
			{"username":"sam@corp.com","aliases":["sick"],"orgId":"00D2","instanceUrl":"https://y.my.salesforce.com","connectedStatus":"` + inactiveUserStatus + `"},
			{"username":"dev@localhost","aliases":["local"],"orgId":"00D3","instanceUrl":"http://localhost:6109","connectedStatus":"connect ECONNREFUSED 127.0.0.1:6109"}
		],"scratchOrgs":[
			{"username":"scratch@corp.com","aliases":["temp"],"orgId":"00D4","instanceUrl":"https://z.scratch.my.salesforce.com","isExpired":true,"expirationDate":"2026-01-05"}
		]}}`,
	}})
	loadAllOrgs(t, m)
	return m
}

// openOrgCard puts the cursor on one org and opens its detail card.
func openOrgCard(t *testing.T, m *Model, username string) *orgsView {
	t.Helper()
	ov, ok := m.views[ViewOrgs].(*orgsView)
	if !ok {
		t.Fatal("orgs view not built")
	}
	ov.card = nil
	ov.table.FocusRowWhere(2, username)
	drive(t, m, key("d"))
	if ov.card == nil {
		t.Fatalf("d should open the detail card for %s", username)
	}
	return ov
}

// A wide layout, so assertions are about what the card says rather than where
// it wrapped.
func cardText(c *orgCard) string { return c.body(200) }

// fieldValue reads one labelled value out of a rendered card.
func fieldValue(body, label string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, label+" ") {
			return strings.TrimSpace(strings.TrimPrefix(line, label))
		}
	}
	return ""
}

func TestOrgDetailCardShowsEverythingKnownAboutTheOrg(t *testing.T) {
	m := brokenOrgModel(t)
	ov := openOrgCard(t, m, "scratch@corp.com")
	body := cardText(ov.card)
	for _, want := range []string{
		"temp",                                // alias
		"scratch@corp.com",                    // username
		"00D4",                                // org id
		"https://z.scratch.my.salesforce.com", // instance url
		"scratch",                             // type
		"2026-01-05",                          // expiry, which only scratch orgs have
		"CLI default", "no",
		"Status", "Expired",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("card is missing %q:\n%s", want, body)
		}
	}

	// An org with no expiry should not grow an empty row for one.
	prod := openOrgCard(t, m, "alex@corp.com")
	if strings.Contains(cardText(prod.card), "Expires") {
		t.Errorf("a non-scratch org has no expiry to show:\n%s", cardText(prod.card))
	}
}

func TestOrgDetailCardMarksTheCLIDefaultAndTheOrgInUse(t *testing.T) {
	m := brokenOrgModel(t)
	ov := openOrgCard(t, m, "alex@corp.com")
	body := cardText(ov.card)
	if got := fieldValue(body, "CLI default"); got != "yes" {
		t.Errorf("the CLI's default org should say so, got %q:\n%s", got, body)
	}
	if got := fieldValue(body, "In use by sf9s"); got != "yes" {
		t.Errorf("the org sf9s is working against should say so, got %q:\n%s", got, body)
	}

	other := openOrgCard(t, m, "sam@corp.com")
	body = cardText(other.card)
	if got := fieldValue(body, "CLI default"); got != "no" {
		t.Errorf("a non-default org must not claim to be the default, got %q:\n%s", got, body)
	}
	if got := fieldValue(body, "In use by sf9s"); got != "no" {
		t.Errorf("an org sf9s is not using must not claim otherwise, got %q:\n%s", got, body)
	}
}

func TestOrgDetailCardShowsTheStatusTheColumnCutsOff(t *testing.T) {
	m := brokenOrgModel(t)
	ov := openOrgCard(t, m, "sam@corp.com")
	body := cardText(ov.card)
	if !strings.Contains(body, inactiveUserStatus) {
		t.Fatalf("the card must carry the whole status, not a prefix:\n%s", body)
	}
	for _, want := range []string{"deactivated", "reactivate", "admin"} {
		if !strings.Contains(body, want) {
			t.Errorf("the explanation is missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "sf org login") {
		t.Errorf("re-authenticating cannot revive a deactivated user:\n%s", body)
	}
}

func TestOrgDetailCardExplainsAnUnreachableHost(t *testing.T) {
	m := brokenOrgModel(t)
	ov := openOrgCard(t, m, "dev@localhost")
	body := cardText(ov.card)
	if !strings.Contains(body, "ECONNREFUSED") {
		t.Errorf("the raw socket error should still be there:\n%s", body)
	}
	for _, want := range []string{"reached", "VPN", "running"} {
		if !strings.Contains(body, want) {
			t.Errorf("the explanation is missing %q:\n%s", want, body)
		}
	}
}

func TestOrgDetailCardExplainsAnExpiredScratchOrg(t *testing.T) {
	m := brokenOrgModel(t)
	ov := openOrgCard(t, m, "scratch@corp.com")
	body := cardText(ov.card)
	if !strings.Contains(body, "cannot be recovered") {
		t.Errorf("an expired scratch org is gone for good, and should say so:\n%s", body)
	}
}

// An unrecognized status is the one case where sf9s has nothing to add, so it
// must at least hand over what it was told, word for word.
func TestOrgDetailCardNeverSwallowsAnUnknownStatus(t *testing.T) {
	m := brokenOrgModel(t)
	weird := "SESSION_ID_SANDBOX_MISMATCH: something new from the CLI"
	for i := range m.orgs {
		if m.orgs[i].Username == "sam@corp.com" {
			m.orgs[i].ConnectedStatus = weird
		}
	}
	ov, ok := m.views[ViewOrgs].(*orgsView)
	if !ok {
		t.Fatal("orgs view not built")
	}
	ov.setOrgs(m.orgs)
	ov = openOrgCard(t, m, "sam@corp.com")

	body := cardText(ov.card)
	if !strings.Contains(body, weird) {
		t.Fatalf("the raw status must survive verbatim:\n%s", body)
	}
	if !strings.Contains(body, "no explanation") {
		t.Errorf("sf9s should admit it does not recognize the status:\n%s", body)
	}
}

// Wrapping, not truncation: the whole reason the card exists is that the
// Status column cuts the message off before the useful part.
func TestOrgDetailCardWrapsInsteadOfTruncating(t *testing.T) {
	m := brokenOrgModel(t)
	ov := openOrgCard(t, m, "sam@corp.com")
	narrow := ov.card.body(46)
	if !strings.Contains(narrow, "\n") {
		t.Fatal("precondition: a narrow card spans several lines")
	}
	if strings.Contains(narrow, inactiveUserStatus) {
		t.Fatal("precondition: the status cannot fit on one 46-column line")
	}
	if !strings.Contains(collapse(narrow), inactiveUserStatus) {
		t.Errorf("every word of the status should survive wrapping:\n%s", narrow)
	}
	for _, line := range strings.Split(narrow, "\n") {
		if w := len([]rune(line)); w > 46 {
			t.Errorf("line overflows the card by %d columns: %q", w-46, line)
		}
	}
}

// Connection statuses land seconds after the org list does, so a card opened
// during the sweep has to pick them up instead of sitting at "checking…".
func TestOrgDetailCardFollowsLateArrivingStatus(t *testing.T) {
	m := brokenOrgModel(t)
	for i := range m.orgs {
		if m.orgs[i].Username == "sam@corp.com" {
			m.orgs[i].ConnectedStatus = ""
		}
	}
	ov, ok := m.views[ViewOrgs].(*orgsView)
	if !ok {
		t.Fatal("orgs view not built")
	}
	ov.setOrgs(m.orgs)
	ov = openOrgCard(t, m, "sam@corp.com")
	if !strings.Contains(cardText(ov.card), "checking…") {
		t.Fatalf("precondition: the status has not arrived yet:\n%s", cardText(ov.card))
	}

	for i := range m.orgs {
		if m.orgs[i].Username == "sam@corp.com" {
			m.orgs[i].ConnectedStatus = inactiveUserStatus
		}
	}
	ov.setOrgs(m.orgs)
	if ov.card == nil {
		t.Fatal("an org list refresh must not close the card")
	}
	body := cardText(ov.card)
	if !strings.Contains(body, inactiveUserStatus) || !strings.Contains(body, "deactivated") {
		t.Errorf("the open card should show the status that just arrived:\n%s", body)
	}
	if ov.card.status != inactiveUserStatus {
		t.Errorf("y would copy a stale status: %q", ov.card.status)
	}
}

func TestOrgDetailCardOpensAndCloses(t *testing.T) {
	m := brokenOrgModel(t)
	ov := openOrgCard(t, m, "sam@corp.com")
	if !strings.Contains(m.View(), "org details") {
		t.Fatalf("the card should be on screen:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "deactivated") {
		t.Errorf("the diagnosis should be readable on a normal terminal:\n%s", m.View())
	}
	drive(t, m, key("esc"))
	if ov.card != nil {
		t.Fatal("esc should close the card")
	}
	if m.active != ViewOrgs {
		t.Fatalf("closing the card must not leave the orgs view, got %v", m.active)
	}
}

// The card is an inspection, not a selection: it must not disturb which org
// every other key acts on, and enter must still do what it always did.
func TestOrgDetailCardDoesNotChangeTheWorkingOrg(t *testing.T) {
	m := brokenOrgModel(t)
	before := m.current.Username
	ov := openOrgCard(t, m, "sam@corp.com")
	if m.current.Username != before {
		t.Fatalf("opening the card switched org to %q", m.current.Username)
	}

	// enter closes the card without selecting the org underneath it.
	drive(t, m, key("enter"))
	if ov.card != nil {
		t.Error("enter should close the card")
	}
	if m.current.Username != before {
		t.Fatalf("enter inside the card switched org to %q", m.current.Username)
	}
	if m.active != ViewOrgs {
		t.Fatalf("enter inside the card navigated away, to %v", m.active)
	}

	// And with the card gone, enter is the org-selecting key it has always been.
	drive(t, m, key("enter"))
	if m.current.Username != "sam@corp.com" {
		t.Fatalf("enter should use the highlighted org, current is %q", m.current.Username)
	}
	if m.active != ViewQuery {
		t.Fatalf("enter should still jump to the query view, got %v", m.active)
	}
}

// While the card is up it owns the keyboard: a digit here must not silently
// retarget every later action at a different org, and q must not quit.
func TestOrgDetailCardOwnsTheKeyboardWhileOpen(t *testing.T) {
	m := brokenOrgModel(t)
	ov := openOrgCard(t, m, "sam@corp.com")
	before := m.current.Username

	drive(t, m, key("2"))
	if m.current.Username != before {
		t.Errorf("a digit inside the card switched org to %q", m.current.Username)
	}
	if ov.card == nil {
		t.Fatal("a digit should not close the card")
	}

	_, cmd := m.Update(key("q"))
	if ov.card != nil {
		t.Error("q should close the card")
	}
	if cmd != nil {
		if msg := cmd(); msg != nil {
			if _, quit := msg.(tea.QuitMsg); quit {
				t.Fatal("q inside the card quit sf9s")
			}
		}
	}
}

func TestOrgDetailCardCopiesTheFullStatus(t *testing.T) {
	m := brokenOrgModel(t)
	var copied []string
	m.deps.Clipboard = func(s string) error { copied = append(copied, s); return nil }
	openOrgCard(t, m, "sam@corp.com")

	drive(t, m, key("y"))
	if len(copied) != 1 || copied[0] != inactiveUserStatus {
		t.Fatalf("y should copy the whole status for pasting into a ticket, copied %q", copied)
	}
	if !strings.Contains(m.View(), "status copied") {
		t.Errorf("the copy should be confirmed:\n%s", m.View())
	}
}

func TestOrgViewAdvertisesTheDetailKey(t *testing.T) {
	m := brokenOrgModel(t)
	ov, ok := m.views[ViewOrgs].(*orgsView)
	if !ok {
		t.Fatal("orgs view not built")
	}
	if !strings.Contains(hintLine(ov.Keys()), "d details") {
		t.Errorf("the detail key should be advertised:\n%s", hintLine(ov.Keys()))
	}
	openOrgCard(t, m, "sam@corp.com")
	hints := hintLine(ov.Keys())
	for _, want := range []string{"copy status", "esc close"} {
		if !strings.Contains(hints, want) {
			t.Errorf("the card's own keys should be advertised, missing %q:\n%s", want, hints)
		}
	}
}

func TestOrgKindPrefersWhatTheOrgSaysAboutItself(t *testing.T) {
	org := sfcli.Org{InstanceURL: "https://x.my.salesforce.com"}
	if got := orgKind(org, nil); got != "prod?" {
		t.Errorf("an unidentified org that could be production = %q, want prod?", got)
	}
	if got := orgKind(org, &api.OrgInfo{OrganizationType: "Enterprise Edition"}); got != "PRODUCTION" {
		t.Errorf("orgKind = %q, want PRODUCTION", got)
	}
	if got := orgKind(org, &api.OrgInfo{OrganizationType: "Developer Edition"}); got != "developer" {
		t.Errorf("orgKind = %q, want developer", got)
	}
	// An identity that names no edition must not downgrade what the host
	// already told us.
	sandbox := sfcli.Org{InstanceURL: "https://x.sandbox.my.salesforce.com"}
	if got := orgKind(sandbox, &api.OrgInfo{}); got != "sandbox" {
		t.Errorf("orgKind = %q, want sandbox", got)
	}
}

func TestCopiedTokenIsWarnedAbout(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	m.deps.SF = sfcli.New(fakeRunner{out: map[string]string{
		"org list":                     testOrgList,
		"org display -o alex@corp.com": `{"status":0,"result":{"id":"00D1","accessToken":"00D1!LIVE_TOKEN","instanceUrl":"https://x.my.salesforce.com","apiVersion":"64.0"}}`,
	}})
	var copied string
	m.deps.Clipboard = func(s string) error { copied = s; return nil }
	m.deps.ClipboardRead = nil // no auto-clear here, so the warning stands
	loadAllOrgs(t, m)

	drive(t, m, key("y"))
	if copied != "00D1!LIVE_TOKEN" {
		t.Fatalf("y should copy the access token, got %q", copied)
	}
	view := m.View()
	if !strings.Contains(view, "grants full API access") {
		t.Errorf("copying a live session token deserves a warning:\n%s", view)
	}
	if strings.Contains(view, "clipboard clears") {
		t.Errorf("without a way to read the clipboard, sf9s must not promise to clear it:\n%s", view)
	}
}

func TestCopiedTokenIsClearedButLaterCopiesAreNot(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	m.deps.SF = sfcli.New(fakeRunner{out: map[string]string{
		"org list":                     testOrgList,
		"org display -o alex@corp.com": `{"status":0,"result":{"id":"00D1","accessToken":"00D1!LIVE_TOKEN","instanceUrl":"https://x.my.salesforce.com","apiVersion":"64.0"}}`,
	}})
	clipboard := ""
	var writes []string
	m.deps.Clipboard = func(s string) error {
		clipboard = s
		writes = append(writes, s)
		return nil
	}
	m.deps.ClipboardRead = func() (string, error) { return clipboard, nil }
	m.clipboardTTL = time.Millisecond
	loadAllOrgs(t, m)

	drive(t, m, key("y"))
	if len(writes) == 0 || writes[0] != "00D1!LIVE_TOKEN" {
		t.Fatalf("the token should have been copied first, writes = %v", writes)
	}
	if clipboard != "" {
		t.Errorf("the scheduled clear should have removed the token, clipboard = %q", clipboard)
	}
	if !strings.Contains(m.View(), "cleared from the clipboard") {
		t.Errorf("the user should be told it was cleared:\n%s", m.View())
	}

	// Anything the user copied since must survive.
	clipboard = "something the user copied since"
	drive(t, m, clearClipboardMsg{expect: "00D1!LIVE_TOKEN"})
	if clipboard != "something the user copied since" {
		t.Errorf("a later copy must not be clobbered, clipboard = %q", clipboard)
	}
}

func TestClipboardExpiryNoteOnlyPromisesWhatItCanDo(t *testing.T) {
	if got := clipboardExpiryNote(true); !strings.Contains(got, "90s") {
		t.Errorf("the note should state the deadline, got %q", got)
	}
	if got := clipboardExpiryNote(false); got != "" {
		t.Errorf("no promise when sf9s cannot clear, got %q", got)
	}
}
