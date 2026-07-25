package sfcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	out map[string][]byte
	err error
}

func (f fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	key := ""
	for i, a := range args {
		if i > 0 {
			key += " "
		}
		key += a
	}
	out, ok := f.out[key]
	if !ok {
		return nil, errors.New("unexpected command: " + key)
	}
	return out, nil
}

const orgListJSON = `{
  "status": 0,
  "result": {
    "other": [],
    "sandboxes": [
      {"username": "alex@corp.com.qa", "aliases": ["qa"], "orgId": "00DQA", "instanceUrl": "https://corp--qa.sandbox.my.salesforce.com/", "connectedStatus": "Connected", "isSandbox": true, "isDevHub": false, "isDefaultUsername": false, "isDefaultDevHubUsername": false}
    ],
    "nonScratchOrgs": [
      {"username": "alex@corp.com", "aliases": ["prod"], "orgId": "00DPROD", "instanceUrl": "https://corp.my.salesforce.com", "connectedStatus": "Connected", "isDevHub": true, "isDefaultUsername": false, "isDefaultDevHubUsername": true},
      {"username": "alex@corp.com.qa", "aliases": ["qa"], "orgId": "00DQA", "instanceUrl": "https://corp--qa.sandbox.my.salesforce.com/", "connectedStatus": "Connected", "isSandbox": true, "isDevHub": false, "isDefaultUsername": false, "isDefaultDevHubUsername": false}
    ],
    "devHubs": [
      {"username": "alex@corp.com", "aliases": ["prod"], "orgId": "00DPROD", "instanceUrl": "https://corp.my.salesforce.com", "connectedStatus": "Connected", "isDevHub": true, "isDefaultUsername": false, "isDefaultDevHubUsername": true}
    ],
    "scratchOrgs": [
      {"username": "test-x@example.com", "aliases": ["scratch1"], "orgId": "00DSCR", "instanceUrl": "https://cool-fox.scratch.my.salesforce.com", "status": "Active", "isExpired": false, "expirationDate": "2026-08-01", "isDefaultUsername": true, "devHubUsername": "alex@corp.com"}
    ]
  }
}`

func TestOrgsParsesAndDedupes(t *testing.T) {
	c := New(fakeRunner{out: map[string][]byte{"org list": []byte(orgListJSON)}})
	orgs, err := c.Orgs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 3 {
		t.Fatalf("want 3 deduped orgs, got %d: %+v", len(orgs), orgs)
	}
	if !orgs[0].IsDefault || orgs[0].Alias != "scratch1" {
		t.Errorf("default org should sort first, got %+v", orgs[0])
	}
	byAlias := map[string]Org{}
	for _, o := range orgs {
		byAlias[o.Alias] = o
	}
	prod := byAlias["prod"]
	if !prod.IsDevHub || !prod.IsDefaultHub || prod.Type() != "devhub" {
		t.Errorf("prod org flags wrong: %+v", prod)
	}
	qa := byAlias["qa"]
	if !qa.IsSandbox || qa.Type() != "sandbox" {
		t.Errorf("qa org flags wrong: %+v", qa)
	}
	if qa.InstanceURL != "https://corp--qa.sandbox.my.salesforce.com" {
		t.Errorf("instance URL should have trailing slash trimmed: %q", qa.InstanceURL)
	}
	scratch := byAlias["scratch1"]
	if !scratch.IsScratch || scratch.ExpirationDate != "2026-08-01" || scratch.ConnectedStatus != "Active" {
		t.Errorf("scratch org fields wrong: %+v", scratch)
	}
}

func TestOrgsEmpty(t *testing.T) {
	c := New(fakeRunner{out: map[string][]byte{
		"org list": []byte(`{"status":0,"result":{"other":[],"sandboxes":[],"nonScratchOrgs":[],"devHubs":[],"scratchOrgs":[]}}`),
	}})
	_, err := c.Orgs(context.Background())
	if !errors.Is(err, ErrNoOrgs) {
		t.Fatalf("want ErrNoOrgs, got %v", err)
	}
}

func TestOrgsCLIFailure(t *testing.T) {
	c := New(fakeRunner{out: map[string][]byte{
		"org list": []byte(`{"status":1,"name":"SomeError","message":"it broke"}`),
	}})
	_, err := c.Orgs(context.Background())
	var cliErr *CLIError
	if !errors.As(err, &cliErr) || cliErr.Message != "it broke" {
		t.Fatalf("want CLIError('it broke'), got %v", err)
	}
}

func TestCredentials(t *testing.T) {
	c := New(fakeRunner{out: map[string][]byte{
		"org display -o qa": []byte(`{"status":0,"result":{"id":"00DQA","accessToken":"00DQA!secret","instanceUrl":"https://corp--qa.sandbox.my.salesforce.com/","apiVersion":"64.0","username":"alex@corp.com.qa","connectedStatus":"Connected"}}`),
	}})
	creds, err := c.Credentials(context.Background(), "qa")
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessToken != "00DQA!secret" || creds.APIVersion != "64.0" {
		t.Errorf("credentials wrong: %+v", creds)
	}
	if creds.InstanceURL != "https://corp--qa.sandbox.my.salesforce.com" {
		t.Errorf("instance URL not trimmed: %q", creds.InstanceURL)
	}
}

func TestMetadataTypes(t *testing.T) {
	c := New(fakeRunner{out: map[string][]byte{
		"org list metadata-types -o qa": []byte(`{"status":0,"result":{"metadataObjects":[{"xmlName":"Flow","directoryName":"flows","inFolder":false,"suffix":"flow"},{"xmlName":"ApexClass","directoryName":"classes","inFolder":false,"suffix":"cls"}]}}`),
	}})
	types, err := c.MetadataTypes(context.Background(), "qa")
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 2 || types[0].XMLName != "ApexClass" {
		t.Fatalf("want sorted [ApexClass Flow], got %+v", types)
	}
}

func TestListMetadataArrayAndSingle(t *testing.T) {
	c := New(fakeRunner{out: map[string][]byte{
		"org list metadata -m ApexClass -o qa": []byte(`{"status":0,"result":[{"fullName":"Zeta","type":"ApexClass","lastModifiedByName":"Alex"},{"fullName":"alpha","type":"ApexClass","lastModifiedByName":"Bob"}]}`),
		"org list metadata -m Community -o qa": []byte(`{"status":0,"result":{"fullName":"OnlyOne","type":"Community"}}`),
	}})
	comps, err := c.ListMetadata(context.Background(), "qa", "ApexClass")
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 2 || comps[0].FullName != "alpha" {
		t.Fatalf("want case-insensitive sort [alpha Zeta], got %+v", comps)
	}
	single, err := c.ListMetadata(context.Background(), "qa", "Community")
	if err != nil {
		t.Fatal(err)
	}
	if len(single) != 1 || single[0].FullName != "OnlyOne" {
		t.Fatalf("want single component, got %+v", single)
	}
}

func TestFlagLikeArgumentsRejected(t *testing.T) {
	c := New(fakeRunner{out: map[string][]byte{}})
	if _, err := c.Credentials(context.Background(), "--output-file=/tmp/x"); err == nil {
		t.Error("flag-like org alias must be rejected before reaching sf")
	}
	if _, err := c.ListMetadata(context.Background(), "qa", "--output-file=/tmp/x"); err == nil {
		t.Error("flag-like metadata type must be rejected")
	}
	if _, err := c.MetadataTypes(context.Background(), ""); err == nil {
		t.Error("empty org must be rejected")
	}
}

func TestContextCancellationReportedAsTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ExecRunner{Bin: "sleep"}.Run(ctx, "5")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestUnwrapGarbage(t *testing.T) {
	c := New(fakeRunner{out: map[string][]byte{"org list": []byte("not json at all")}})
	if _, err := c.Orgs(context.Background()); err == nil {
		t.Fatal("want error on garbage output")
	}
}

func TestListMetadataDecodesNames(t *testing.T) {
	c := New(fakeRunner{out: map[string][]byte{
		"org list metadata -m Layout -o qa": []byte(`{"status":0,"result":[
			{"fullName":"Account-Account %28Marketing%29 Layout","type":"Layout"},
			{"fullName":"Case-Case %5BSupport%5D Layout","type":"Layout"}]}`),
	}})
	comps, err := c.ListMetadata(context.Background(), "qa", "Layout")
	if err != nil {
		t.Fatal(err)
	}
	if comps[0].FullName != "Account-Account (Marketing) Layout" {
		t.Errorf("percent-encoded name not decoded: %q", comps[0].FullName)
	}
	if comps[1].FullName != "Case-Case [Support] Layout" {
		t.Errorf("percent-encoded name not decoded: %q", comps[1].FullName)
	}
}

func TestOrgTypeUsesInstanceHostWhenFlagsAreMissing(t *testing.T) {
	cases := []struct {
		url  string
		want string
		prod bool
	}{
		{"https://acme--dev.sandbox.my.salesforce.com", "sandbox", false},
		{"https://cool-fox.scratch.my.salesforce.com", "scratch", false},
		{"https://acme-dev-ed.my.salesforce.com", "developer", false},
		{"https://acme.develop.my.salesforce.com", "developer", false},
		{"https://acme.my.salesforce.com", "org", true},
		{"http://localhost:8080", "local", false},
		{"http://127.0.0.1:52732", "local", false},
	}
	for _, tc := range cases {
		o := Org{InstanceURL: tc.url}
		if got := o.Type(); got != tc.want {
			t.Errorf("%s → Type() = %q, want %q", tc.url, got, tc.want)
		}
		if got := o.MaybeProduction(); got != tc.prod {
			t.Errorf("%s → MaybeProduction() = %v, want %v", tc.url, got, tc.prod)
		}
	}
}

// Salesforce CLI 2.136.8 removed credentials from `org display`. sf9s must
// keep working on both sides of that change.
func TestCredentialsFallsBackToShowAccessToken(t *testing.T) {
	c := New(fakeRunner{out: map[string][]byte{
		"org display -o qa":                []byte(`{"status":0,"result":{"id":"00DQA","instanceUrl":"https://corp--qa.sandbox.my.salesforce.com","apiVersion":"64.0","username":"alex@corp.com.qa"}}`),
		"org auth show-access-token -o qa": []byte(`{"status":0,"result":"00DQA!FRESH_TOKEN"}`),
	}})
	creds, err := c.Credentials(context.Background(), "qa")
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessToken != "00DQA!FRESH_TOKEN" {
		t.Fatalf("token not resolved from the new command: %+v", creds)
	}
	if creds.InstanceURL == "" || creds.APIVersion != "64.0" {
		t.Fatalf("the rest of the credentials must still come from org display: %+v", creds)
	}
}

func TestCredentialsUsesOrgDisplayTokenWhenPresent(t *testing.T) {
	// Older CLIs still include it; that must not cost a second process.
	c := New(fakeRunner{out: map[string][]byte{
		"org display -o qa": []byte(`{"status":0,"result":{"accessToken":"00DQA!OLD","instanceUrl":"https://x","apiVersion":"64.0"}}`),
	}})
	creds, err := c.Credentials(context.Background(), "qa")
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessToken != "00DQA!OLD" {
		t.Fatalf("got %q", creds.AccessToken)
	}
}

func TestCredentialsExplainsAnOldCLIWithoutTheCommand(t *testing.T) {
	c := New(fakeRunner{out: map[string][]byte{
		"org display -o qa":                []byte(`{"status":0,"result":{"instanceUrl":"https://x","apiVersion":"64.0"}}`),
		"org auth show-access-token -o qa": []byte(`{"status":1,"name":"CommandError","message":"Command org:auth:show-access-token not found."}`),
	}})
	_, err := c.Credentials(context.Background(), "qa")
	if err == nil || !strings.Contains(err.Error(), "upgrade the CLI") {
		t.Fatalf("want an actionable message, got %v", err)
	}
}

func TestAccessTokenAcceptsObjectShape(t *testing.T) {
	c := New(fakeRunner{out: map[string][]byte{
		"org display -o qa":                []byte(`{"status":0,"result":{"instanceUrl":"https://x","apiVersion":"64.0"}}`),
		"org auth show-access-token -o qa": []byte(`{"status":0,"result":{"accessToken":"00DQA!OBJ"}}`),
	}})
	creds, err := c.Credentials(context.Background(), "qa")
	if err != nil || creds.AccessToken != "00DQA!OBJ" {
		t.Fatalf("creds=%+v err=%v", creds, err)
	}
}

// A `sf` whose grandchild outlives it must not hang sf9s: output is captured
// through pipes, and Run waits for every writer to close them.
func TestExecRunnerDoesNotHangOnOrphanedGrandchild(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "sf")
	body := "#!/bin/sh\nsleep 60 &\nprintf '%s'\nexit 0\n"
	payload := `{"status":0,"result":{"nonScratchOrgs":[],"scratchOrgs":[]}}`
	if err := os.WriteFile(script, []byte(fmt.Sprintf(body, payload)), 0o755); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := ExecRunner{Bin: script}.Run(context.Background(), "org", "list")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run failed: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return: an orphaned grandchild is holding the output pipe open")
	}
}
