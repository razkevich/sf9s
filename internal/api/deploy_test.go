package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const deployWithFailures = `{
	"id": "0Af000000000001AAA",
	"deployResult": {
		"id": "0Af000000000001AAA",
		"status": "Failed",
		"success": false,
		"checkOnly": false,
		"errorMessage": "Deploy failed",
		"numberComponentsTotal": 12,
		"numberComponentsDeployed": 9,
		"numberComponentErrors": 2,
		"numberTestsTotal": 4,
		"numberTestsCompleted": 3,
		"numberTestErrors": 1,
		"details": {
			"componentFailures": [
				{"componentType":"ApexClass","fileName":"classes/Billing.cls","fullName":"Billing",
				 "problem":"Variable does not exist: acct","problemType":"Error",
				 "lineNumber":42,"columnNumber":7},
				{"componentType":"CustomField","fileName":"objects/Account.object","fullName":"Account.Foo__c",
				 "problem":"duplicate value found","problemType":"Error","lineNumber":null,"columnNumber":null}
			],
			"runTestResult": {
				"numFailures": 1,
				"failures": [
					{"name":"BillingTest","methodName":"testTotals",
					 "message":"System.AssertException: Assertion Failed",
					 "stackTrace":"Class.BillingTest.testTotals: line 18, column 1"}
				]
			}
		}
	}
}`

func TestDeployDetailsFailures(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Write([]byte(deployWithFailures))
	}))
	defer srv.Close()

	c := NewClient(newStaticTokens(srv.URL, "tok"))
	d, err := c.DeployDetails(context.Background(), "0Af000000000001AAA")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/services/data/v64.0/metadata/deployRequest/0Af000000000001AAA"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotQuery != "includeDetails=true" {
		t.Errorf("query = %q, want includeDetails=true", gotQuery)
	}
	if !d.HasFailures() {
		t.Fatal("a deploy with component and test failures must report failures")
	}
	if d.Status != "Failed" || d.NumberComponentErrors != 2 || d.NumberTestErrors != 1 {
		t.Errorf("summary counts wrong: %+v", d)
	}
	if len(d.ComponentFailures) != 2 {
		t.Fatalf("component failures = %d, want 2", len(d.ComponentFailures))
	}
	first := d.ComponentFailures[0]
	if first.ComponentType != "ApexClass" || first.FullName != "Billing" ||
		first.Problem != "Variable does not exist: acct" || first.Location() != "42:7" {
		t.Errorf("first failure decoded wrong: %+v", first)
	}
	if got := d.ComponentFailures[1].Location(); got != "" {
		t.Errorf("a failure with no line must not invent one, got %q", got)
	}
	if len(d.TestFailures) != 1 {
		t.Fatalf("test failures = %d, want 1", len(d.TestFailures))
	}
	if tf := d.TestFailures[0]; tf.Name != "BillingTest" || tf.MethodName != "testTotals" ||
		!strings.Contains(tf.StackTrace, "line 18") {
		t.Errorf("test failure decoded wrong: %+v", tf)
	}
}

// A single failure arrives as a bare object where several arrive as an array,
// so the shape changes with the data.
func TestDeployDetailsSingleFailureIsNotAnArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"deployResult":{"id":"0Af1","status":"Failed","numberComponentErrors":1,
			"details":{
				"componentFailures":{"componentType":"ApexTrigger","fullName":"AccountTrigger",
					"problem":"Invalid type: Foo","problemType":"Error","lineNumber":3,"columnNumber":11},
				"runTestResult":{"failures":{"name":"T","methodName":"m","message":"boom"}}
			}}}`))
	}))
	defer srv.Close()

	c := NewClient(newStaticTokens(srv.URL, "tok"))
	d, err := c.DeployDetails(context.Background(), "0Af1")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.ComponentFailures) != 1 || d.ComponentFailures[0].FullName != "AccountTrigger" {
		t.Fatalf("single component failure not decoded: %+v", d.ComponentFailures)
	}
	if got := d.ComponentFailures[0].Location(); got != "3:11" {
		t.Errorf("location = %q, want 3:11", got)
	}
	if len(d.TestFailures) != 1 || d.TestFailures[0].Message != "boom" {
		t.Fatalf("single test failure not decoded: %+v", d.TestFailures)
	}
}

func TestDeployDetailsCleanDeploy(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"no details at all", `{"id":"0Af2","deployResult":{"id":"0Af2","status":"Succeeded","success":true,
			"numberComponentsTotal":3,"numberComponentsDeployed":3}}`},
		{"empty detail arrays", `{"deployResult":{"id":"0Af2","status":"Succeeded","success":true,
			"details":{"componentFailures":[],"runTestResult":{"failures":[]}}}}`},
		{"null details", `{"deployResult":{"id":"0Af2","status":"Succeeded","success":true,
			"details":{"componentFailures":null,"runTestResult":{"failures":null}}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			d, err := NewClient(newStaticTokens(srv.URL, "tok")).DeployDetails(context.Background(), "0Af2")
			if err != nil {
				t.Fatal(err)
			}
			if d.HasFailures() {
				t.Errorf("a clean deploy must report no failures: %+v", d)
			}
			if d.ID != "0Af2" || !d.Success {
				t.Errorf("summary decoded wrong: %+v", d)
			}
		})
	}
}

func TestDeployDetailsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`[{"message":"The requested resource does not exist","errorCode":"NOT_FOUND"}]`))
	}))
	defer srv.Close()

	_, err := NewClient(newStaticTokens(srv.URL, "tok")).DeployDetails(context.Background(), "0AfMISSING")
	if err == nil {
		t.Fatal("a missing deployment must surface an error")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("the org's own message should reach the caller, got %q", err)
	}
}

func TestComponentFailureLocation(t *testing.T) {
	cases := []struct {
		name string
		f    ComponentFailure
		want string
	}{
		{"line and column", ComponentFailure{LineNumber: 12, ColumnNumber: 4}, "12:4"},
		{"line only", ComponentFailure{LineNumber: 12}, "12"},
		{"column without line", ComponentFailure{ColumnNumber: 4}, ""},
		{"neither", ComponentFailure{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.f.Location(); got != tc.want {
				t.Errorf("Location() = %q, want %q", got, tc.want)
			}
		})
	}
}
