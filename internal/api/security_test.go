package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRejectsPlaintextNonLoopbackInstance(t *testing.T) {
	c := NewClient(newStaticTokens("http://evil.example.com", "tok"))
	_, err := c.Query(context.Background(), "SELECT Id FROM Account", false)
	if err == nil || !strings.Contains(err.Error(), "refusing to send credentials") {
		t.Fatalf("plaintext non-loopback instance must be refused, got %v", err)
	}
}

func TestAllowsHTTPSAndLoopback(t *testing.T) {
	if err := checkInstanceURL("https://acme.my.salesforce.com"); err != nil {
		t.Errorf("https must be allowed: %v", err)
	}
	if err := checkInstanceURL("http://127.0.0.1:8080"); err != nil {
		t.Errorf("loopback must be allowed for local emulators: %v", err)
	}
	if err := checkInstanceURL("http://localhost:8080"); err != nil {
		t.Errorf("localhost must be allowed: %v", err)
	}
}

func TestRefusesRedirectWithBearerToken(t *testing.T) {
	var leaked string
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization")
		w.Write([]byte(`{"totalSize":0,"done":true,"records":[]}`))
	}))
	defer sink.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL+"/steal", http.StatusFound)
	}))
	defer redirector.Close()

	c := NewClient(newStaticTokens(redirector.URL, "SECRET"))
	_, err := c.Query(context.Background(), "SELECT Id FROM Account", false)
	if err == nil {
		t.Fatal("redirect should be refused")
	}
	if leaked != "" {
		t.Fatalf("bearer token followed a redirect: %q", leaked)
	}
}

func TestFlattenDepthCapped(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"totalSize":1,"done":true,"records":[{"attributes":{"type":"X"}`)
	depth := maxFlattenDepth + 5
	for i := 0; i < depth; i++ {
		sb.WriteString(`,"R":{"attributes":{"type":"Y"}`)
	}
	sb.WriteString(`,"Id":"1"`)
	for i := 0; i < depth; i++ {
		sb.WriteString(`}`)
	}
	sb.WriteString(`}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sb.String()))
	}))
	defer srv.Close()
	c := NewClient(newStaticTokens(srv.URL, "tok"))
	_, err := c.Query(context.Background(), "SELECT Id FROM X", false)
	if err == nil || !strings.Contains(err.Error(), "nesting deeper than") {
		t.Fatalf("pathological nesting must be rejected, got %v", err)
	}
}
