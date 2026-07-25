package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type staticTokens struct {
	url     string
	token   atomic.Value
	forced  atomic.Int32
	failure error
}

func newStaticTokens(url, token string) *staticTokens {
	s := &staticTokens{url: url}
	s.token.Store(token)
	return s
}

func (s *staticTokens) Credentials(_ context.Context, force bool) (Credentials, error) {
	if s.failure != nil {
		return Credentials{}, s.failure
	}
	if force {
		s.forced.Add(1)
		s.token.Store("refreshed-token")
	}
	return Credentials{
		AccessToken: s.token.Load().(string),
		InstanceURL: s.url,
		APIVersion:  "64.0",
	}, nil
}

func TestQueryFlattening(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/services/data/v64.0/query" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); !strings.HasPrefix(got, "SELECT") {
			t.Errorf("query not passed through: %q", got)
		}
		w.Write([]byte(`{
			"totalSize": 2, "done": true,
			"records": [
				{"attributes":{"type":"Case"},
				 "Id":"500000000000001AAA",
				 "CaseNumber":"00001000",
				 "Account":{"attributes":{"type":"Account"},"Name":"Acme","Owner":{"attributes":{"type":"User"},"Alias":"arazk"}},
				 "Amount__c":18000000,
				 "Comment__c":"line1 \"quoted\"",
				 "ClosedDate":null,
				 "Histories":{"totalSize":3,"done":true,"records":[{"Id":"x"}]}},
				{"attributes":{"type":"Case"},
				 "Id":"500000000000002AAA",
				 "CaseNumber":"00001001",
				 "Account":null,
				 "Amount__c":0.5,
				 "Comment__c":null,
				 "ClosedDate":"2026-07-01",
				 "Histories":{"totalSize":0,"done":true,"records":[]}}
			]
		}`))
	}))
	defer srv.Close()

	c := NewClient(newStaticTokens(srv.URL, "tok"))
	res, err := c.Query(context.Background(), "SELECT Id FROM Case", false)
	if err != nil {
		t.Fatal(err)
	}
	wantCols := []string{"Id", "CaseNumber", "Account.Name", "Account.Owner.Alias", "Amount__c", "Comment__c", "ClosedDate", "Histories"}
	if len(res.Columns) != len(wantCols) {
		t.Fatalf("columns = %v, want %v", res.Columns, wantCols)
	}
	for i := range wantCols {
		if res.Columns[i] != wantCols[i] {
			t.Fatalf("columns = %v, want %v", res.Columns, wantCols)
		}
	}
	row0 := res.Rows[0]
	if row0[4] != "18000000" {
		t.Errorf("large number mangled: %q", row0[4])
	}
	if row0[2] != "Acme" || row0[3] != "arazk" {
		t.Errorf("nested flattening wrong: %v", row0)
	}
	if row0[5] != `line1 "quoted"` {
		t.Errorf("string unescaping wrong: %q", row0[5])
	}
	if row0[6] != "" {
		t.Errorf("null should render empty, got %q", row0[6])
	}
	if row0[7] != "(3 rows)" {
		t.Errorf("subquery summary wrong: %q", row0[7])
	}
	row1 := res.Rows[1]
	if row1[2] != "" || row1[3] != "" {
		t.Errorf("null relationship should yield empty cells: %v", row1)
	}
	if row1[7] != "(0 rows)" {
		t.Errorf("empty subquery wrong: %q", row1[7])
	}
}

func TestQueryAggregateColumns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"totalSize":1,"done":true,"records":[{"attributes":{"type":"AggregateResult"},"expr0":42,"Type":"Bug"}]}`))
	}))
	defer srv.Close()
	c := NewClient(newStaticTokens(srv.URL, "tok"))
	res, err := c.Query(context.Background(), "SELECT COUNT(Id), Type FROM Case GROUP BY Type", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Columns[0] != "expr0" || res.Rows[0][0] != "42" {
		t.Errorf("aggregate handling wrong: %v %v", res.Columns, res.Rows)
	}
}

func TestUnauthorizedRetriesWithFreshToken(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") == "Bearer stale-token" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`[{"message":"Session expired or invalid","errorCode":"INVALID_SESSION_ID"}]`))
			return
		}
		w.Write([]byte(`{"totalSize":0,"done":true,"records":[]}`))
	}))
	defer srv.Close()

	tokens := newStaticTokens(srv.URL, "stale-token")
	c := NewClient(tokens)
	res, err := c.Query(context.Background(), "SELECT Id FROM Case", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalSize != 0 || calls.Load() != 2 || tokens.forced.Load() != 1 {
		t.Errorf("retry flow wrong: calls=%d forced=%d", calls.Load(), tokens.forced.Load())
	}
}

func TestPersistentUnauthorizedSurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`[{"message":"Session expired or invalid","errorCode":"INVALID_SESSION_ID"}]`))
	}))
	defer srv.Close()
	c := NewClient(newStaticTokens(srv.URL, "bad"))
	_, err := c.Query(context.Background(), "SELECT Id FROM Case", false)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode != "INVALID_SESSION_ID" || apiErr.StatusCode != 401 {
		t.Fatalf("want INVALID_SESSION_ID APIError, got %v", err)
	}
}

func TestMalformedQueryError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`[{"message":"unexpected token: FORM","errorCode":"MALFORMED_QUERY"}]`))
	}))
	defer srv.Close()
	c := NewClient(newStaticTokens(srv.URL, "tok"))
	_, err := c.Query(context.Background(), "SELECT Id FORM Case", false)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode != "MALFORMED_QUERY" {
		t.Fatalf("want MALFORMED_QUERY, got %v", err)
	}
	if !strings.Contains(apiErr.Error(), "FORM") {
		t.Errorf("error message should carry Salesforce text: %v", apiErr)
	}
}

func TestToolingQueryPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/services/data/v64.0/tooling/query" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"totalSize":0,"done":true,"records":[]}`))
	}))
	defer srv.Close()
	c := NewClient(newStaticTokens(srv.URL, "tok"))
	if _, err := c.Query(context.Background(), "SELECT Id FROM ApexLog", true); err != nil {
		t.Fatal(err)
	}
}

func TestQueryMoreUsesAbsolutePath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/services/data/v64.0/query/01g-2000" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"totalSize":4000,"done":true,"records":[]}`))
	}))
	defer srv.Close()
	c := NewClient(newStaticTokens(srv.URL, "tok"))
	res, err := c.QueryMore(context.Background(), "/services/data/v64.0/query/01g-2000")
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalSize != 4000 {
		t.Errorf("totalSize = %d", res.TotalSize)
	}
}

func TestDescribeGlobalAndSObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/services/data/v64.0/sobjects":
			w.Write([]byte(`{"sobjects":[{"name":"Account","label":"Account","custom":false,"keyPrefix":"001","queryable":true}]}`))
		case "/services/data/v64.0/sobjects/Account/describe":
			w.Write([]byte(`{"name":"Account","label":"Account","keyPrefix":"001","fields":[{"name":"Industry","label":"Industry","type":"picklist","picklistValues":[{"label":"Tech","value":"Technology","active":true}]},{"name":"OwnerId","label":"Owner ID","type":"reference","referenceTo":["User"],"relationshipName":"Owner"}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewClient(newStaticTokens(srv.URL, "tok"))
	global, err := c.DescribeGlobal(context.Background())
	if err != nil || len(global) != 1 || global[0].KeyPrefix != "001" {
		t.Fatalf("describeGlobal wrong: %v %v", global, err)
	}
	desc, err := c.DescribeSObject(context.Background(), "Account")
	if err != nil || len(desc.Fields) != 2 {
		t.Fatalf("describe wrong: %v %v", desc, err)
	}
	if desc.Fields[0].PicklistValues[0].Value != "Technology" {
		t.Errorf("picklist values missing: %+v", desc.Fields[0])
	}
	if desc.Fields[1].ReferenceTo[0] != "User" {
		t.Errorf("referenceTo missing: %+v", desc.Fields[1])
	}
}

func TestLimits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"DailyApiRequests":{"Max":100000,"Remaining":97001,"Salesforce Mobile Dashboards":{"Max":500,"Remaining":500}},"DataStorageMB":{"Max":1024,"Remaining":38}}`))
	}))
	defer srv.Close()
	c := NewClient(newStaticTokens(srv.URL, "tok"))
	limits, err := c.Limits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if limits["DailyApiRequests"].Remaining != 97001 || limits["DataStorageMB"].Max != 1024 {
		t.Errorf("limits wrong: %+v", limits)
	}
}

func TestApexLogBodyAndDelete(t *testing.T) {
	var deleted atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/ApexLog/07L1/Body"):
			w.Write([]byte("EXECUTION_STARTED|hello"))
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/ApexLog/07L1"):
			deleted.Store(true)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewClient(newStaticTokens(srv.URL, "tok"))
	body, err := c.ApexLogBody(context.Background(), "07L1")
	if err != nil || body != "EXECUTION_STARTED|hello" {
		t.Fatalf("log body wrong: %q %v", body, err)
	}
	if err := c.DeleteApexLog(context.Background(), "07L1"); err != nil || !deleted.Load() {
		t.Fatalf("delete failed: %v", err)
	}
}

func TestTokenSourceFailurePropagates(t *testing.T) {
	tokens := &staticTokens{failure: errors.New("org has expired")}
	c := NewClient(tokens)
	_, err := c.Query(context.Background(), "SELECT Id FROM Case", false)
	if err == nil || !strings.Contains(err.Error(), "org has expired") {
		t.Fatalf("token failure should propagate, got %v", err)
	}
}
