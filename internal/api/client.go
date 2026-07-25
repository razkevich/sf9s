// Package api is a minimal Salesforce REST + Tooling API client for the
// read paths sf9s renders interactively. Credentials are resolved lazily
// through a TokenSource (backed by the sf CLI) and never persisted.
package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Credentials is the token material needed to call an org.
type Credentials struct {
	AccessToken string
	InstanceURL string
	APIVersion  string
}

// TokenSource resolves credentials for the current org. force bypasses any
// caching, used after a 401 to pick up a refreshed token.
type TokenSource interface {
	Credentials(ctx context.Context, force bool) (Credentials, error)
}

// APIError is a structured Salesforce REST error.
type APIError struct {
	StatusCode int
	ErrorCode  string
	Message    string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("Salesforce API error (HTTP %d)", e.StatusCode)
}

// Client talks to one org.
type Client struct {
	hc     *http.Client
	tokens TokenSource
}

// NewClient builds a client whose requests are bounded solely by the
// caller's context — a client-level Timeout would silently cap long log
// downloads and queries below the deadlines the UI actually sets. Redirects
// are refused so a bearer token can never follow one off the instance host.
func NewClient(tokens TokenSource) *Client {
	return &Client{
		hc: &http.Client{
			Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return fmt.Errorf("refusing redirect to %s", req.URL.Host)
			},
		},
		tokens: tokens,
	}
}

// allowPlaintext permits http:// instance URLs, needed only for local org
// emulators and tests. Set via SF9S_ALLOW_HTTP=1.
var allowPlaintext = os.Getenv("SF9S_ALLOW_HTTP") == "1"

func checkInstanceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid instance URL %q: %w", raw, err)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && (allowPlaintext || isLoopback(u.Hostname())) {
		return nil
	}
	return fmt.Errorf("refusing to send credentials to %s over %s (set SF9S_ALLOW_HTTP=1 for local emulators)", u.Host, u.Scheme)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func versionPath(creds Credentials) string {
	v := creds.APIVersion
	if v == "" {
		v = "64.0"
	}
	return "/services/data/v" + v
}

// get performs a GET with 401-refresh-retry and decodes JSON into out.
func (c *Client) get(ctx context.Context, path string, out any) error {
	body, err := c.raw(ctx, http.MethodGet, path)
	if err != nil {
		return err
	}
	defer body.Close()
	dec := json.NewDecoder(body)
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("unexpected Salesforce response: %w", err)
	}
	return nil
}

// raw performs a request and returns the response body on 2xx. path may be
// absolute ("/services/data/…") or relative to the version root ("query?q=…").
func (c *Client) raw(ctx context.Context, method, path string) (io.ReadCloser, error) {
	resp, err := c.roundTrip(ctx, method, path, false)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		resp, err = c.roundTrip(ctx, method, path, true)
		if err != nil {
			return nil, err
		}
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, decodeAPIError(resp)
	}
	return resp.Body, nil
}

func (c *Client) roundTrip(ctx context.Context, method, path string, forceToken bool) (*http.Response, error) {
	creds, err := c.tokens.Credentials(ctx, forceToken)
	if err != nil {
		return nil, err
	}
	if err := checkInstanceURL(creds.InstanceURL); err != nil {
		return nil, err
	}
	full := path
	if !strings.HasPrefix(path, "/") {
		full = versionPath(creds) + "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, method, creds.InstanceURL+full, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("Accept", "application/json, text/plain")
	req.Header.Set("Sforce-Call-Options", "client=sf9s")
	resp, err := c.hc.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return nil, fmt.Errorf("cannot reach %s: %w", creds.InstanceURL, urlErr.Err)
		}
		return nil, err
	}
	return resp, nil
}

func decodeAPIError(resp *http.Response) error {
	apiErr := &APIError{StatusCode: resp.StatusCode}
	var body []struct {
		Message   string `json:"message"`
		ErrorCode string `json:"errorCode"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(raw, &body); err == nil && len(body) > 0 {
		apiErr.Message = body[0].Message
		apiErr.ErrorCode = body[0].ErrorCode
	} else if s := strings.TrimSpace(string(raw)); s != "" && len(s) < 500 {
		apiErr.Message = s
	}
	return apiErr
}

// Query runs a SOQL query against the data or tooling endpoint.
func (c *Client) Query(ctx context.Context, soql string, tooling bool) (*Result, error) {
	endpoint := "query"
	if tooling {
		endpoint = "tooling/query"
	}
	return c.queryPath(ctx, endpoint+"?q="+url.QueryEscape(soql))
}

// QueryMore follows a nextRecordsUrl from a previous Result.
func (c *Client) QueryMore(ctx context.Context, nextRecordsURL string) (*Result, error) {
	return c.queryPath(ctx, nextRecordsURL)
}

func (c *Client) queryPath(ctx context.Context, path string) (*Result, error) {
	var payload queryResponse
	if err := c.get(ctx, path, &payload); err != nil {
		return nil, err
	}
	return buildResult(payload)
}

// SObjectSummary is one entry of describeGlobal.
type SObjectSummary struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Custom    bool   `json:"custom"`
	KeyPrefix string `json:"keyPrefix"`
	Queryable bool   `json:"queryable"`
}

func (c *Client) DescribeGlobal(ctx context.Context) ([]SObjectSummary, error) {
	var payload struct {
		SObjects []SObjectSummary `json:"sobjects"`
	}
	if err := c.get(ctx, "sobjects", &payload); err != nil {
		return nil, err
	}
	return payload.SObjects, nil
}

// PicklistValue is one active/inactive picklist entry.
type PicklistValue struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Active bool   `json:"active"`
}

// Field is the subset of a field describe sf9s renders.
type Field struct {
	Name             string          `json:"name"`
	Label            string          `json:"label"`
	Type             string          `json:"type"`
	Length           int             `json:"length"`
	Custom           bool            `json:"custom"`
	Nillable         bool            `json:"nillable"`
	Createable       bool            `json:"createable"`
	Updateable       bool            `json:"updateable"`
	Calculated       bool            `json:"calculated"`
	ReferenceTo      []string        `json:"referenceTo"`
	RelationshipName string          `json:"relationshipName"`
	PicklistValues   []PicklistValue `json:"picklistValues"`
}

// SObjectDescribe is a full object describe (fields only).
type SObjectDescribe struct {
	Name      string  `json:"name"`
	Label     string  `json:"label"`
	KeyPrefix string  `json:"keyPrefix"`
	Custom    bool    `json:"custom"`
	Fields    []Field `json:"fields"`
}

func (c *Client) DescribeSObject(ctx context.Context, name string) (*SObjectDescribe, error) {
	var d SObjectDescribe
	if err := c.get(ctx, "sobjects/"+url.PathEscape(name)+"/describe", &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// Limit is one org limit with current consumption.
type Limit struct {
	Max       int64 `json:"Max"`
	Remaining int64 `json:"Remaining"`
}

func (c *Client) Limits(ctx context.Context) (map[string]Limit, error) {
	var limits map[string]Limit
	if err := c.get(ctx, "limits", &limits); err != nil {
		return nil, err
	}
	return limits, nil
}

// ApexLogBody fetches the raw text body of one debug log.
func (c *Client) ApexLogBody(ctx context.Context, id string) (string, error) {
	body, err := c.raw(ctx, http.MethodGet, "tooling/sobjects/ApexLog/"+url.PathEscape(id)+"/Body")
	if err != nil {
		return "", err
	}
	defer body.Close()
	b, err := io.ReadAll(io.LimitReader(body, 32<<20))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DeleteApexLog deletes one debug log — the only write sf9s performs.
func (c *Client) DeleteApexLog(ctx context.Context, id string) error {
	body, err := c.raw(ctx, http.MethodDelete, "tooling/sobjects/ApexLog/"+url.PathEscape(id))
	if err != nil {
		return err
	}
	body.Close()
	return nil
}
