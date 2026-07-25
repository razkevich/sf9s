package ui

import "strings"

// orgStatusKind names the connection problems worth explaining. `sf org list`
// prints connectedStatus verbatim from whatever failed underneath — a
// refresh-token exchange, a socket dial — so the text is precise about the
// mechanism and silent about the remedy, which is the only part the user
// needs.
type orgStatusKind int

const (
	orgStatusHealthy orgStatusKind = iota
	orgStatusChecking
	orgStatusInactiveUser
	orgStatusReauth
	orgStatusUnreachable
	orgStatusExpired
	orgStatusUnrecognized
)

// orgDiagnosis turns a connection status into something actionable: what
// happened, what it means, and the one thing to do about it.
type orgDiagnosis struct {
	kind     orgStatusKind
	headline string
	detail   string
	action   string
}

// problem reports whether there is anything to explain. A healthy org and one
// we have not heard back about yet both look the same here: nothing to say.
func (d orgDiagnosis) problem() bool {
	return d.kind != orgStatusHealthy && d.kind != orgStatusChecking
}

// diagnoseOrgStatus explains a `sf org list` connectedStatus in plain English.
// orgRef names the org in any command the explanation suggests.
//
// Matching is substring-based and ordered most specific first: the inactive
// user message arrives wrapped inside the refresh-token failure that would
// otherwise claim it, and it is the one case where re-authenticating is the
// wrong advice. Anything unrecognized is reported as such rather than guessed
// at — the caller still shows the raw status, which is never discarded.
func diagnoseOrgStatus(status, orgRef string) orgDiagnosis {
	if orgRef == "" {
		orgRef = "<org>"
	}
	s := strings.ToLower(strings.TrimSpace(status))
	switch {
	case s == "", s == "checking…", s == "unknown":
		return orgDiagnosis{kind: orgStatusChecking}

	case s == "connected", s == "active":
		return orgDiagnosis{kind: orgStatusHealthy}

	case strings.Contains(s, "inactive user"):
		return orgDiagnosis{
			kind:     orgStatusInactiveUser,
			headline: "This org's user is deactivated",
			detail: "Salesforce refused to mint a session because the user account " +
				"itself is inactive in the org. The saved authorization is fine.",
			action: "Ask an org admin to reactivate the user (Setup → Users). " +
				"Logging in again will not help — the login is what is being refused.",
		}

	case containsAny(s, "econnrefused", "enotfound", "ehostunreach", "enetunreach",
		"etimedout", "econnreset", "getaddrinfo", "dial tcp", "connect timeout"):
		return orgDiagnosis{
			kind:     orgStatusUnreachable,
			headline: "The instance host could not be reached",
			detail: "Nothing answered at the org's instance URL, so this never got " +
				"as far as Salesforce: it is a network failure, not an auth one.",
			action: "Check the VPN, whether the instance is up, and — for a local " +
				"emulator or mock org — whether it is actually running.",
		}

	case containsAny(s, "refresh token", "refreshtokenautherror", "invalid_grant",
		"invalid grant", "revoked"):
		return orgDiagnosis{
			kind:     orgStatusReauth,
			headline: "The saved authorization is no longer valid",
			detail: "The refresh token the sf CLI holds for this org has expired or " +
				"been revoked, so no new session can be issued from it.",
			action: "Re-authenticate:  sf org login web --alias " + orgRef,
		}

	case strings.Contains(s, "expired"):
		return orgDiagnosis{
			kind:     orgStatusExpired,
			headline: "This scratch org has expired",
			detail: "Salesforce deletes scratch orgs at their expiry date. An expired " +
				"one cannot be recovered, and nothing in it can be read back.",
			action: "Create a replacement (sf org create scratch) and drop the stale " +
				"entry:  sf org logout -o " + orgRef,
		}

	default:
		return orgDiagnosis{
			kind:     orgStatusUnrecognized,
			headline: "sf9s has no explanation for this status",
			detail:   "It is shown above exactly as the sf CLI reported it.",
			action:   "For the CLI's own account of the connection:  sf org display -o " + orgRef,
		}
	}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
