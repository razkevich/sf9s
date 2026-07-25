package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrgInfoProductionClassification(t *testing.T) {
	cases := []struct {
		name    string
		info    OrgInfo
		prod    bool
		edition string
	}{
		{"enterprise production", OrgInfo{OrganizationType: "Enterprise Edition"}, true, "enterprise"},
		{"unlimited production", OrgInfo{OrganizationType: "Unlimited Edition"}, true, "unlimited"},
		{"professional production", OrgInfo{OrganizationType: "Professional Edition"}, true, "professional"},
		{"sandbox of enterprise", OrgInfo{OrganizationType: "Enterprise Edition", IsSandbox: true}, false, "sandbox"},
		{"developer edition", OrgInfo{OrganizationType: "Developer Edition"}, false, "developer"},
		{"trial", OrgInfo{OrganizationType: "Enterprise Edition", TrialExpirationDate: "2026-09-01T00:00:00.000+0000"}, false, "trial"},
		{"unknown", OrgInfo{}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.info.Production(); got != tc.prod {
				t.Errorf("Production() = %v, want %v", got, tc.prod)
			}
			if got := tc.info.Edition(); got != tc.edition {
				t.Errorf("Edition() = %q, want %q", got, tc.edition)
			}
		})
	}
}

func TestFetchOrgInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"totalSize":1,"done":true,"records":[{"attributes":{"type":"Organization"},
			"Id":"00D1","Name":"Acme","OrganizationType":"Enterprise Edition","InstanceName":"NA123",
			"IsSandbox":false,"TrialExpirationDate":null}]}`))
	}))
	defer srv.Close()
	c := NewClient(newStaticTokens(srv.URL, "tok"))
	info, err := c.FetchOrgInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Production() || info.Name != "Acme" || info.InstanceName != "NA123" {
		t.Fatalf("unexpected org info: %+v", info)
	}
}

func TestFetchOrgInfoEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"totalSize":0,"done":true,"records":[]}`))
	}))
	defer srv.Close()
	c := NewClient(newStaticTokens(srv.URL, "tok"))
	if _, err := c.FetchOrgInfo(context.Background()); err == nil {
		t.Fatal("an org with no Organization record should error, not panic")
	}
}
