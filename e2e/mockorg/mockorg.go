// Package mockorg is a small in-process Salesforce org emulator covering
// the REST and Tooling endpoints sf9s uses. The e2e suite runs the real TUI
// against it; the fake sf CLI hands out its URL as the org's instance URL.
package mockorg

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
)

// Server wraps an httptest.Server with mutable behavior toggles.
type Server struct {
	*httptest.Server
	Requests    atomic.Int64
	FailQueries atomic.Bool
	DeletedLogs []string
}

func firstWord(q string) string {
	fields := strings.Fields(q)
	if len(fields) == 0 {
		return "<empty>"
	}
	return fields[0]
}

func New() *Server {
	s := &Server{}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /services/data/v64.0/query", func(w http.ResponseWriter, r *http.Request) {
		s.Requests.Add(1)
		if s.FailQueries.Load() {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `[{"message":"unexpected token: 'FRM'","errorCode":"MALFORMED_QUERY"}]`)
			return
		}
		q := r.URL.Query().Get("q")
		// Reject statements a real org would reject, so a dropped keystroke
		// surfaces as a test failure instead of silently "working".
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(q)), "SELECT ") {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `[{"message":%q,"errorCode":"MALFORMED_QUERY"}]`,
				"unexpected token: "+firstWord(q))
			return
		}
		switch {
		case strings.Contains(q, "FROM Account"):
			fmt.Fprint(w, `{"totalSize":3,"done":true,"records":[
				{"attributes":{"type":"Account"},"Id":"001E2E000000001","Name":"Acme Rockets","Owner":{"attributes":{"type":"User"},"Name":"Alex"},"AnnualRevenue":12000000},
				{"attributes":{"type":"Account"},"Id":"001E2E000000002","Name":"Globex","Owner":null,"AnnualRevenue":null},
				{"attributes":{"type":"Account"},"Id":"001E2E000000003","Name":"Initech","Owner":{"attributes":{"type":"User"},"Name":"Dana"},"AnnualRevenue":550000}]}`)
		case strings.Contains(q, "FROM Contact"):
			fmt.Fprint(w, `{"totalSize":4000,"done":false,"nextRecordsUrl":"/services/data/v64.0/query/01gE2E-2000","records":[
				{"attributes":{"type":"Contact"},"Id":"003E2E000000001","LastName":"Page1Row"}]}`)
		default:
			fmt.Fprint(w, `{"totalSize":0,"done":true,"records":[]}`)
		}
	})

	mux.HandleFunc("GET /services/data/v64.0/query/01gE2E-2000", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"totalSize":4000,"done":true,"records":[
			{"attributes":{"type":"Contact"},"Id":"003E2E000000002","LastName":"Page2Row"}]}`)
	})

	mux.HandleFunc("GET /services/data/v64.0/tooling/query", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		switch {
		case strings.Contains(q, "FROM ApexLog"):
			fmt.Fprint(w, `{"totalSize":1,"done":true,"records":[
				{"attributes":{"type":"ApexLog"},"Id":"07LE2E0000001","LogUser":{"attributes":{"type":"Name"},"Name":"Alex"},"Operation":"/apex/execute","Status":"Success","LogLength":128,"StartTime":"2026-07-25T10:00:00.000+0000","DurationMilliseconds":42,"Request":"Api","Application":"Unknown"}]}`)
		case strings.Contains(q, "FROM DeployRequest"):
			fmt.Fprint(w, `{"totalSize":1,"done":true,"records":[
				{"attributes":{"type":"DeployRequest"},"Id":"0AfE2E0000001","Status":"Succeeded","CreatedBy":{"attributes":{"type":"Name"},"Name":"Alex"},"CreatedDate":"2026-07-24T13:41:15.000+0000","StartDate":"2026-07-24T13:41:16.000+0000","CompletedDate":"2026-07-24T13:41:16.000+0000","CheckOnly":false,"NumberComponentsDeployed":3,"NumberComponentsTotal":3,"NumberComponentErrors":0,"NumberTestsCompleted":0,"NumberTestsTotal":0,"NumberTestErrors":0,"ErrorMessage":null}]}`)
		default:
			fmt.Fprint(w, `{"totalSize":0,"done":true,"records":[]}`)
		}
	})

	mux.HandleFunc("GET /services/data/v64.0/sobjects", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"sobjects":[
			{"name":"Account","label":"Account","custom":false,"keyPrefix":"001","queryable":true},
			{"name":"Contact","label":"Contact","custom":false,"keyPrefix":"003","queryable":true},
			{"name":"Invoice__c","label":"Invoice","custom":true,"keyPrefix":"a00","queryable":true}]}`)
	})

	mux.HandleFunc("GET /services/data/v64.0/sobjects/{name}/describe", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "User" {
			fmt.Fprint(w, `{"name":"User","label":"User","keyPrefix":"005","fields":[
				{"name":"Id","label":"User ID","type":"id"},
				{"name":"Alias","label":"Alias","type":"string","length":8},
				{"name":"ManagerId","label":"Manager ID","type":"reference","referenceTo":["User"],"relationshipName":"Manager"}]}`)
			return
		}
		fmt.Fprintf(w, `{"name":%q,"label":%q,"keyPrefix":"001","fields":[
			{"name":"Id","label":"Record ID","type":"id"},
			{"name":"Name","label":"Name","type":"string","length":255,"nillable":false,"createable":true,"updateable":true},
			{"name":"OwnerId","label":"Owner ID","type":"reference","referenceTo":["User"],"relationshipName":"Owner"},
			{"name":"ParentId","label":"Parent Account ID","type":"reference","referenceTo":["Account"],"relationshipName":"Parent"},
			{"name":"Phone","label":"Account Phone","type":"phone","length":40},
			{"name":"PhotoUrl","label":"Photo URL","type":"url","length":1024},
			{"name":"AnnualRevenue","label":"Annual Revenue","type":"currency"},
			{"name":"Industry","label":"Industry","type":"picklist","nillable":true,"createable":true,"updateable":true,"picklistValues":[{"label":"Technology","value":"Technology","active":true},{"label":"Energy","value":"Energy","active":true}]}]}`, name, name)
	})

	mux.HandleFunc("GET /services/data/v64.0/limits", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"DailyApiRequests":{"Max":100000,"Remaining":9000},"DataStorageMB":{"Max":1024,"Remaining":900},"FileStorageMB":{"Max":2048,"Remaining":2048}}`)
	})

	mux.HandleFunc("GET /services/data/v64.0/tooling/sobjects/ApexLog/{id}/Body", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "64.0 APEX_CODE,DEBUG\n10:00:00.0 EXECUTION_STARTED\n10:00:00.1 USER_DEBUG needle-in-log\n10:00:00.2 EXECUTION_FINISHED\n")
	})

	mux.HandleFunc("DELETE /services/data/v64.0/tooling/sobjects/ApexLog/{id}", func(w http.ResponseWriter, r *http.Request) {
		s.DeletedLogs = append(s.DeletedLogs, r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	})

	s.Server = httptest.NewServer(mux)
	return s
}
