// fakesf is a stand-in for the Salesforce CLI used by the e2e suite. It
// answers the exact commands sf9s issues, pointing credentials at the mock
// org server given by SF9S_MOCK_URL.
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	args := strings.Join(os.Args[1:], " ")
	mock := os.Getenv("SF9S_MOCK_URL")
	switch {
	case strings.HasPrefix(args, "org list metadata-types"):
		fmt.Printf(`{"status":0,"result":{"metadataObjects":[
			{"xmlName":"ApexClass","directoryName":"classes","inFolder":false,"suffix":"cls"},
			{"xmlName":"Flow","directoryName":"flows","inFolder":false,"suffix":"flow"}]}}`)
	case strings.HasPrefix(args, "org list metadata"):
		fmt.Printf(`{"status":0,"result":[
			{"fullName":"InvoiceService","type":"ApexClass","lastModifiedByName":"Alex","lastModifiedDate":"2026-07-01T10:00:00.000Z","createdByName":"Alex","createdDate":"2026-01-01T10:00:00.000Z","manageableState":"unmanaged"},
			{"fullName":"PaymentService","type":"ApexClass","lastModifiedByName":"Dana","lastModifiedDate":"2026-07-02T10:00:00.000Z","createdByName":"Dana","createdDate":"2026-01-02T10:00:00.000Z","manageableState":"unmanaged"}]}`)
	case strings.HasPrefix(args, "org list"):
		display := func(host string) string {
			if os.Getenv("SF9S_PRETTY_URLS") == "1" {
				return "https://" + host
			}
			return mock
		}
		fmt.Printf(`{"status":0,"result":{"nonScratchOrgs":[
			{"username":"e2e@example.com","alias":"e2e","orgId":"00DE2E","instanceUrl":%q,"connectedStatus":"Connected","isDefaultUsername":true},
			{"username":"other@example.com","alias":"other","orgId":"00DOTH","instanceUrl":%q,"connectedStatus":"Connected","isSandbox":true}],
			"scratchOrgs":[{"username":"scratchy@example.com","alias":"scratchy","orgId":"00DSCR","instanceUrl":%q,"status":"Active","expirationDate":"2026-08-15"}]}}`,
			display("acme.my.salesforce.com"), display("acme--staging.sandbox.my.salesforce.com"), display("nimble-fox-8f2k.scratch.my.salesforce.com"))
	case strings.HasPrefix(args, "org open"):
		fmt.Printf(`{"status":0,"result":{"url":"https://example.my.salesforce.com","orgId":"00DE2E","username":"e2e@example.com"}}`)
	case strings.HasPrefix(args, "org display"):
		fmt.Printf(`{"status":0,"result":{"id":"00DE2E","accessToken":"E2E_TOKEN","instanceUrl":%q,"apiVersion":"64.0","username":"e2e@example.com","connectedStatus":"Connected"}}`, mock)
	default:
		fmt.Printf(`{"status":1,"name":"UnknownCommand","message":"fakesf: unhandled command: %s"}`, args)
		os.Exit(1)
	}
}
