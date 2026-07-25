// Package sfcli bridges to the locally installed Salesforce CLI (`sf`).
// It is the single source of org inventory and credentials: sf9s never
// stores or refreshes tokens itself, it delegates to the CLI the same way
// k9s delegates to kubeconfig.
package sfcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

var (
	ErrCLINotFound = errors.New("sf CLI not found on PATH")
	ErrNoOrgs      = errors.New("no authenticated orgs")
)

// CLIError carries a failure reported by the sf CLI itself.
type CLIError struct {
	Name    string
	Message string
}

func (e *CLIError) Error() string {
	if e.Message == "" {
		return "sf CLI command failed"
	}
	return e.Message
}

// Runner executes the sf binary. Tests substitute a fake.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// safeArg rejects values that would read as flags. Aliases and metadata type
// names originate outside sf9s, and `sf` has flags (e.g. --output-file) that
// write to disk.
func safeArg(kind, value string) error {
	if value == "" {
		return fmt.Errorf("empty %s", kind)
	}
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("refusing %s that looks like a flag: %q", kind, value)
	}
	return nil
}

// ExecRunner runs the real `sf` executable with --json appended.
type ExecRunner struct {
	Bin string
}

func (r ExecRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	bin := r.Bin
	if bin == "" {
		bin = "sf"
	}
	cmd := exec.CommandContext(ctx, bin, append(args, "--json")...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
			return nil, ErrCLINotFound
		}
		// A context kill leaves truncated stdout — report the timeout, not
		// a bogus JSON parse error.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// sf exits non-zero on command failure but still prints a JSON
		// envelope on stdout; let the caller parse it for the message.
		if stdout.Len() > 0 {
			return stdout.Bytes(), nil
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, &CLIError{Message: msg}
	}
	return stdout.Bytes(), nil
}

type envelope struct {
	Status  int             `json:"status"`
	Result  json.RawMessage `json:"result"`
	Name    string          `json:"name"`
	Message string          `json:"message"`
}

func unwrap(out []byte, into any) error {
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil {
		return fmt.Errorf("unexpected sf CLI output: %w", err)
	}
	if env.Status != 0 {
		return &CLIError{Name: env.Name, Message: env.Message}
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, into); err != nil {
		return fmt.Errorf("unexpected sf CLI result shape: %w", err)
	}
	return nil
}

// Org is one authenticated org as reported by `sf org list`.
type Org struct {
	Username        string
	Alias           string
	OrgID           string
	InstanceURL     string
	ConnectedStatus string
	IsDevHub        bool
	IsDefault       bool
	IsDefaultHub    bool
	IsScratch       bool
	IsSandbox       bool
	ExpirationDate  string
}

// Title returns the friendliest short identifier for the org.
func (o Org) Title() string {
	if o.Alias != "" {
		return o.Alias
	}
	return o.Username
}

// Type returns a short human label for the org flavor.
func (o Org) Type() string {
	switch {
	case o.IsScratch:
		return "scratch"
	case o.IsDevHub:
		return "devhub"
	case o.IsSandbox:
		return "sandbox"
	default:
		return "org"
	}
}

type rawOrg struct {
	Username                string   `json:"username"`
	Alias                   string   `json:"alias"`
	Aliases                 []string `json:"aliases"`
	OrgID                   string   `json:"orgId"`
	InstanceURL             string   `json:"instanceUrl"`
	ConnectedStatus         string   `json:"connectedStatus"`
	IsDevHub                bool     `json:"isDevHub"`
	IsDefaultUsername       bool     `json:"isDefaultUsername"`
	IsDefaultDevHubUsername bool     `json:"isDefaultDevHubUsername"`
	IsSandbox               bool     `json:"isSandbox"`
	IsExpired               bool     `json:"isExpired"`
	ExpirationDate          string   `json:"expirationDate"`
	Status                  string   `json:"status"`
}

func (r rawOrg) toOrg(scratch bool) Org {
	alias := r.Alias
	if alias == "" && len(r.Aliases) > 0 {
		alias = r.Aliases[0]
	}
	status := r.ConnectedStatus
	if scratch {
		status = r.Status
		if r.IsExpired {
			status = "Expired"
		}
	}
	return Org{
		Username:        r.Username,
		Alias:           alias,
		OrgID:           r.OrgID,
		InstanceURL:     strings.TrimSuffix(r.InstanceURL, "/"),
		ConnectedStatus: status,
		IsDevHub:        r.IsDevHub,
		IsDefault:       r.IsDefaultUsername,
		IsDefaultHub:    r.IsDefaultDevHubUsername,
		IsScratch:       scratch,
		IsSandbox:       r.IsSandbox,
		ExpirationDate:  r.ExpirationDate,
	}
}

// Credentials is the token material resolved by `sf org display`.
type Credentials struct {
	AccessToken string `json:"accessToken"`
	InstanceURL string `json:"instanceUrl"`
	APIVersion  string `json:"apiVersion"`
	OrgID       string `json:"id"`
	Username    string `json:"username"`
}

// MetadataType is one entry of `sf org list metadata-types`.
type MetadataType struct {
	XMLName       string   `json:"xmlName"`
	DirectoryName string   `json:"directoryName"`
	InFolder      bool     `json:"inFolder"`
	Suffix        string   `json:"suffix"`
	ChildXMLNames []string `json:"childXmlNames"`
}

// MetadataComponent is one entry of `sf org list metadata -m <type>`.
type MetadataComponent struct {
	FullName           string `json:"fullName"`
	Type               string `json:"type"`
	FileName           string `json:"fileName"`
	CreatedByName      string `json:"createdByName"`
	CreatedDate        string `json:"createdDate"`
	LastModifiedByName string `json:"lastModifiedByName"`
	LastModifiedDate   string `json:"lastModifiedDate"`
	ManageableState    string `json:"manageableState"`
}

// Client wraps a Runner with typed sf commands.
type Client struct {
	runner Runner
}

func New(runner Runner) *Client {
	return &Client{runner: runner}
}

// Orgs lists every authenticated org, deduplicated by username.
func (c *Client) Orgs(ctx context.Context) ([]Org, error) {
	out, err := c.runner.Run(ctx, "org", "list")
	if err != nil {
		return nil, err
	}
	var result struct {
		NonScratchOrgs []rawOrg `json:"nonScratchOrgs"`
		ScratchOrgs    []rawOrg `json:"scratchOrgs"`
		Sandboxes      []rawOrg `json:"sandboxes"`
		DevHubs        []rawOrg `json:"devHubs"`
		Other          []rawOrg `json:"other"`
	}
	if err := unwrap(out, &result); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var orgs []Org
	add := func(raws []rawOrg, scratch bool) {
		for _, r := range raws {
			if r.Username == "" || seen[r.Username] {
				continue
			}
			seen[r.Username] = true
			orgs = append(orgs, r.toOrg(scratch))
		}
	}
	add(result.NonScratchOrgs, false)
	add(result.DevHubs, false)
	add(result.Sandboxes, false)
	add(result.Other, false)
	add(result.ScratchOrgs, true)

	if len(orgs) == 0 {
		return nil, ErrNoOrgs
	}
	sort.SliceStable(orgs, func(i, j int) bool {
		if orgs[i].IsDefault != orgs[j].IsDefault {
			return orgs[i].IsDefault
		}
		return strings.ToLower(orgs[i].Title()) < strings.ToLower(orgs[j].Title())
	})
	return orgs, nil
}

// Credentials resolves a fresh access token for the org; the sf CLI
// transparently refreshes expired tokens during this call.
func (c *Client) Credentials(ctx context.Context, usernameOrAlias string) (Credentials, error) {
	if err := safeArg("org", usernameOrAlias); err != nil {
		return Credentials{}, err
	}
	out, err := c.runner.Run(ctx, "org", "display", "-o", usernameOrAlias)
	if err != nil {
		return Credentials{}, err
	}
	var creds Credentials
	if err := unwrap(out, &creds); err != nil {
		return Credentials{}, err
	}
	creds.InstanceURL = strings.TrimSuffix(creds.InstanceURL, "/")
	return creds, nil
}

// OpenOrg launches the org in a browser via the CLI, so the session token
// never passes through sf9s' own process arguments or a URL we construct.
func (c *Client) OpenOrg(ctx context.Context, usernameOrAlias string) error {
	if err := safeArg("org", usernameOrAlias); err != nil {
		return err
	}
	out, err := c.runner.Run(ctx, "org", "open", "-o", usernameOrAlias)
	if err != nil {
		return err
	}
	return unwrap(out, nil)
}

// MetadataTypes lists the metadata types the org supports.
func (c *Client) MetadataTypes(ctx context.Context, usernameOrAlias string) ([]MetadataType, error) {
	if err := safeArg("org", usernameOrAlias); err != nil {
		return nil, err
	}
	out, err := c.runner.Run(ctx, "org", "list", "metadata-types", "-o", usernameOrAlias)
	if err != nil {
		return nil, err
	}
	var result struct {
		MetadataObjects []MetadataType `json:"metadataObjects"`
	}
	if err := unwrap(out, &result); err != nil {
		return nil, err
	}
	sort.Slice(result.MetadataObjects, func(i, j int) bool {
		return result.MetadataObjects[i].XMLName < result.MetadataObjects[j].XMLName
	})
	return result.MetadataObjects, nil
}

// ListMetadata lists components of one metadata type. The result key is an
// array for most types, a single object for types with exactly one component.
func (c *Client) ListMetadata(ctx context.Context, usernameOrAlias, metadataType string) ([]MetadataComponent, error) {
	if err := safeArg("org", usernameOrAlias); err != nil {
		return nil, err
	}
	if err := safeArg("metadata type", metadataType); err != nil {
		return nil, err
	}
	out, err := c.runner.Run(ctx, "org", "list", "metadata", "-m", metadataType, "-o", usernameOrAlias)
	if err != nil {
		return nil, err
	}
	var components []MetadataComponent
	if err := unwrap(out, &components); err != nil {
		var single MetadataComponent
		if err2 := unwrap(out, &single); err2 != nil {
			return nil, err
		}
		if single.FullName != "" {
			components = []MetadataComponent{single}
		}
	}
	sort.Slice(components, func(i, j int) bool {
		return strings.ToLower(components[i].FullName) < strings.ToLower(components[j].FullName)
	})
	return components, nil
}
