package ui

import (
	"strings"
	"testing"
)

func TestDiagnoseOrgStatus(t *testing.T) {
	cases := []struct {
		name   string
		status string
		kind   orgStatusKind
		// wants are fragments the explanation must carry: the words that tell
		// the user what to do next.
		wants []string
	}{
		{"connected", "Connected", orgStatusHealthy, nil},
		{"active scratch org", "Active", orgStatusHealthy, nil},
		{"not probed yet", "", orgStatusChecking, nil},
		{"status pass missed it", "Unknown", orgStatusChecking, nil},
		{
			"inactive user",
			"Unable to refresh session due to: Error authenticating with the refresh token due to: inactive user",
			orgStatusInactiveUser,
			[]string{"deactivated", "admin", "reactivate"},
		},
		{
			"expired refresh token",
			"Unable to refresh session due to: expired access/refresh token",
			orgStatusReauth,
			[]string{"sf org login web --alias prod"},
		},
		{
			"invalid grant",
			"Unable to refresh session due to: invalid_grant - authentication failure",
			orgStatusReauth,
			[]string{"sf org login web --alias prod"},
		},
		{"refresh token auth error", "RefreshTokenAuthError", orgStatusReauth, []string{"sf org login web"}},
		{
			"connection refused",
			"connect ECONNREFUSED 127.0.0.1:6109",
			orgStatusUnreachable,
			[]string{"VPN", "running"},
		},
		{
			"host does not resolve",
			"getaddrinfo ENOTFOUND gone.my.salesforce.com",
			orgStatusUnreachable,
			[]string{"VPN"},
		},
		{"expired scratch org", "Expired", orgStatusExpired, []string{"cannot be recovered"}},
		{
			"never seen before",
			"Something the CLI has never printed before",
			orgStatusUnrecognized,
			[]string{"sf org display -o prod"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := diagnoseOrgStatus(tc.status, "prod")
			if got.kind != tc.kind {
				t.Fatalf("kind = %v, want %v (%+v)", got.kind, tc.kind, got)
			}
			if want := tc.kind != orgStatusHealthy && tc.kind != orgStatusChecking; got.problem() != want {
				t.Errorf("problem() = %v, want %v", got.problem(), want)
			}
			if !got.problem() {
				if got.headline != "" || got.detail != "" || got.action != "" {
					t.Errorf("a healthy org needs no explanation, got %+v", got)
				}
				return
			}
			if got.headline == "" || got.detail == "" || got.action == "" {
				t.Errorf("a problem must name itself, explain itself and say what to do: %+v", got)
			}
			text := got.headline + " " + got.detail + " " + got.action
			for _, want := range tc.wants {
				if !strings.Contains(text, want) {
					t.Errorf("explanation missing %q:\n%s", want, text)
				}
			}
		})
	}
}

// The inactive-user message arrives wrapped inside the refresh-token failure,
// so a naive substring order classifies it as "log in again" — the one piece
// of advice that cannot work, because the login is what Salesforce refuses.
func TestInactiveUserIsNotAReauthPrompt(t *testing.T) {
	d := diagnoseOrgStatus(
		"Unable to refresh session due to: Error authenticating with the refresh token due to: inactive user", "prod")
	if d.kind != orgStatusInactiveUser {
		t.Fatalf("kind = %v, want inactive user", d.kind)
	}
	if strings.Contains(d.action, "sf org login") {
		t.Errorf("re-authenticating cannot fix a deactivated user:\n%s", d.action)
	}
}

func TestDiagnosisNamesTheOrgItWasAskedAbout(t *testing.T) {
	d := diagnoseOrgStatus("RefreshTokenAuthError", "my-sandbox")
	if !strings.Contains(d.action, "--alias my-sandbox") {
		t.Errorf("the suggested command should be runnable as printed:\n%s", d.action)
	}
	// With no org to name, the command still has to read as a command.
	if got := diagnoseOrgStatus("RefreshTokenAuthError", "").action; !strings.Contains(got, "<org>") {
		t.Errorf("an unnamed org should leave a visible placeholder:\n%s", got)
	}
}
