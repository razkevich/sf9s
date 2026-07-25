package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/razkevich/sf9s/internal/api"
)

// deployRequestList is what the DeployRequest tooling query returns: one
// failed deploy and one clean one.
const deployRequestList = `{"totalSize":2,"done":true,"records":[
	{"attributes":{"type":"DeployRequest"},"Id":"0AfFAILED","Status":"Failed",
	 "CreatedBy":{"attributes":{"type":"User"},"Name":"Alex"},"CheckOnly":false,
	 "NumberComponentErrors":2,"ErrorMessage":"Deploy failed"},
	{"attributes":{"type":"DeployRequest"},"Id":"0AfCLEAN","Status":"Succeeded",
	 "CreatedBy":{"attributes":{"type":"User"},"Name":"Alex"},"CheckOnly":false,
	 "NumberComponentErrors":0,"ErrorMessage":null}]}`

const deployFailedDetails = `{"deployResult":{"id":"0AfFAILED","status":"Failed","success":false,
	"numberComponentErrors":2,"numberTestErrors":1,
	"details":{
		"componentFailures":[
			{"componentType":"ApexClass","fileName":"classes/Billing.cls","fullName":"Billing",
			 "problem":"Variable does not exist: acct — this problem text is far longer than any table column can show",
			 "problemType":"Error","lineNumber":42,"columnNumber":7},
			{"componentType":"CustomField","fileName":"objects/Account.object","fullName":"Account.Foo__c",
			 "problem":"duplicate value found","problemType":"Error"}],
		"runTestResult":{"failures":[
			{"name":"BillingTest","methodName":"testTotals","message":"System.AssertException: Assertion Failed",
			 "stackTrace":"Class.BillingTest.testTotals: line 18, column 1"}]}}}}`

const deployCleanDetails = `{"deployResult":{"id":"0AfCLEAN","status":"Succeeded","success":true,
	"numberComponentsTotal":3,"numberComponentsDeployed":3,"details":{"componentFailures":[]}}}`

// deploysServer answers the deploy list plus per-deployment details, so the
// view can be driven exactly as it runs against an org.
func deploysServer(t *testing.T, details map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/services/data/v64.0/tooling/query":
			w.Write([]byte(deployRequestList))
		case strings.HasPrefix(path, "/services/data/v64.0/metadata/deployRequest/"):
			id := strings.TrimPrefix(path, "/services/data/v64.0/metadata/deployRequest/")
			if r.URL.Query().Get("includeDetails") != "true" {
				t.Errorf("details must be requested explicitly, query = %q", r.URL.RawQuery)
			}
			body, ok := details[id]
			if !ok {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`[{"message":"deploy request not found","errorCode":"NOT_FOUND"}]`))
				return
			}
			w.Write([]byte(body))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`[{"message":"not found","errorCode":"NOT_FOUND"}]`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func deploysViewFor(t *testing.T, details map[string]string) (*Model, *deploysView) {
	t.Helper()
	m := newTestModel(t, deploysServer(t, details).URL)
	loadAllOrgs(t, m)
	drive(t, m, switchViewMsg{id: ViewDeploys})
	dv, ok := m.views[ViewDeploys].(*deploysView)
	if !ok {
		t.Fatal("deploys view not active")
	}
	if dv.table.RowCount() != 2 {
		t.Fatalf("precondition: two deployments listed, got %d", dv.table.RowCount())
	}
	return m, dv
}

func TestDeploysEnterListsComponentFailures(t *testing.T) {
	m, dv := deploysViewFor(t, map[string]string{"0AfFAILED": deployFailedDetails})
	drive(t, m, key("enter"))

	if !dv.inFails {
		t.Fatal("enter on a failed deploy should open its failures, not a summary card")
	}
	if dv.card != nil {
		t.Fatal("the generic key/value card is what enter used to do; it must not open here")
	}
	view := m.View()
	for _, want := range []string{
		"Deployment 0AfFAILED", "2 component error(s)", "1 test failure(s)",
		"ApexClass", "Billing", "42:7", "Variable does not exist",
		"CustomField", "Account.Foo__c", "duplicate value found",
		"ApexTest", "BillingTest.testTotals",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("failure table is missing %q:\n%s", want, view)
		}
	}
	if got := dv.failTable.RowCount(); got != 3 {
		t.Errorf("two component failures and one test failure = 3 rows, got %d", got)
	}
}

func TestDeploysEnterOnCleanDeployShowsSummary(t *testing.T) {
	m, dv := deploysViewFor(t, map[string]string{"0AfCLEAN": deployCleanDetails})
	drive(t, m, key("j")) // the succeeded deployment
	if got := dv.table.Cell("Id"); got != "0AfCLEAN" {
		t.Fatalf("precondition: cursor on the clean deploy, got %q", got)
	}
	drive(t, m, key("enter"))

	if dv.inFails {
		t.Fatal("a deploy with nothing to explain must not open an empty failure table")
	}
	if dv.card == nil {
		t.Fatal("a clean deploy should still show the summary it showed before")
	}
	view := m.View()
	for _, want := range []string{"deployment", "Status", "Succeeded", "0AfCLEAN"} {
		if !strings.Contains(view, want) {
			t.Errorf("summary card is missing %q:\n%s", want, view)
		}
	}
}

func TestDeploysFailureRowOpensFullProblemText(t *testing.T) {
	m, dv := deploysViewFor(t, map[string]string{"0AfFAILED": deployFailedDetails})
	drive(t, m, key("enter"))
	drive(t, m, key("enter")) // the focused failure, in full

	if dv.card == nil {
		t.Fatal("enter on a failure should open it in full")
	}
	view := m.View()
	for _, want := range []string{
		"failure", "classes/Billing.cls", "Problem type", "far longer than any table column",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("failure card is missing %q:\n%s", want, view)
		}
	}

	// An Apex test failure carries the stack trace, which is the part worth
	// reading and the part the table cannot show.
	drive(t, m, key("esc"))
	for i := 0; i < 2; i++ {
		drive(t, m, key("j"))
	}
	if got := dv.failTable.Cell("Type"); got != "ApexTest" {
		t.Fatalf("precondition: cursor on the test failure, got %q", got)
	}
	drive(t, m, key("enter"))
	if !strings.Contains(m.View(), "Class.BillingTest.testTotals") {
		t.Errorf("a test failure card should carry its stack trace:\n%s", m.View())
	}
}

func TestDeploysDetailsErrorToastsAndKeepsTheList(t *testing.T) {
	m, dv := deploysViewFor(t, nil)
	drive(t, m, key("enter"))

	if !strings.Contains(m.View(), "deploy request not found") {
		t.Fatalf("the org's message should surface as a toast:\n%s", m.View())
	}
	if dv.inFails || dv.card != nil {
		t.Error("a failed fetch must not open a half-built failure view")
	}
	if dv.loading {
		t.Error("a failed fetch must clear the loading flag, not leave a spinner up")
	}
	if dv.table.RowCount() != 2 {
		t.Errorf("the deployment list must survive a failed fetch, %d rows left", dv.table.RowCount())
	}
	// The list is still usable: a second attempt works.
	if !strings.Contains(m.View(), "0AfFAILED") {
		t.Errorf("list should still be on screen:\n%s", m.View())
	}
}

func TestDeploysDetailsSpinsWhileLoading(t *testing.T) {
	m, dv := deploysViewFor(t, map[string]string{"0AfFAILED": deployFailedDetails})
	dv.fetchDetails("0AfFAILED", dv.table.CurrentRow())
	if !dv.loading {
		t.Fatal("a details fetch should mark the view busy")
	}
	if !strings.Contains(m.View(), "querying") {
		t.Errorf("the view should say it is working:\n%s", m.View())
	}
}

func TestDeploysStaleDetailsResponseIgnored(t *testing.T) {
	m, dv := deploysViewFor(t, map[string]string{"0AfFAILED": deployFailedDetails})
	stale := dv.gen

	// A refresh takes a fresh generation; the in-flight details response
	// belongs to a question nobody is asking any more.
	drive(t, m, key("R"))
	if dv.gen == stale {
		t.Fatal("precondition: refresh should take a new generation")
	}
	drive(t, m, deployDetailsMsg{gen: stale, id: "0AfFAILED", details: &api.DeployDetails{
		ID:                "0AfFAILED",
		ComponentFailures: []api.ComponentFailure{{ComponentType: "ApexClass", FullName: "Stale"}},
	}})
	if dv.inFails {
		t.Fatal("a stale details response must not take over the view")
	}
	if strings.Contains(m.View(), "Stale") {
		t.Errorf("stale failures must not render:\n%s", m.View())
	}
}

func TestDeploysEscUnwindsOneLevelAtATime(t *testing.T) {
	m, dv := deploysViewFor(t, map[string]string{"0AfFAILED": deployFailedDetails})
	drive(t, m, key("enter"))
	drive(t, m, key("enter"))
	if dv.card == nil {
		t.Fatal("precondition: failure card open")
	}

	drive(t, m, key("esc"))
	if dv.card != nil || !dv.inFails {
		t.Fatal("esc should close the card and leave the failure table up")
	}
	drive(t, m, key("esc"))
	if dv.inFails {
		t.Fatal("esc should return to the deployment list")
	}
	if m.active != ViewDeploys {
		t.Fatalf("that esc should not leave the deploys view, got %v", m.active)
	}
	drive(t, m, key("esc"))
	if m.active != ViewOrgs {
		t.Fatalf("the next esc should bail out of the view, got %v", m.active)
	}
}

func TestDeploysFailureKeysAreAdvertised(t *testing.T) {
	m, dv := deploysViewFor(t, map[string]string{"0AfFAILED": deployFailedDetails})
	if !hintsContain(dv.Keys(), "enter") {
		t.Error("the list must advertise enter")
	}
	drive(t, m, key("enter"))
	if !hintsContain(dv.Keys(), "esc") || !hintsContain(dv.Keys(), "enter") {
		t.Errorf("the failure table must advertise enter and esc, got %v", dv.Keys())
	}
	drive(t, m, key("enter"))
	if !hintsContain(dv.Keys(), "esc") {
		t.Errorf("the card must advertise esc, got %v", dv.Keys())
	}
}

func hintsContain(hints []keyHint, key string) bool {
	for _, h := range hints {
		if h.key == key {
			return true
		}
	}
	return false
}
