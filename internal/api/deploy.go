package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// ComponentFailure is one metadata component the org refused to deploy.
type ComponentFailure struct {
	ComponentType string `json:"componentType"`
	FileName      string `json:"fileName"`
	FullName      string `json:"fullName"`
	Problem       string `json:"problem"`
	ProblemType   string `json:"problemType"`
	LineNumber    int    `json:"lineNumber"`
	ColumnNumber  int    `json:"columnNumber"`
}

// Location renders "line:column", omitting whichever the org left out —
// declarative metadata has no line to point at.
func (f ComponentFailure) Location() string {
	switch {
	case f.LineNumber == 0:
		return ""
	case f.ColumnNumber == 0:
		return strconv.Itoa(f.LineNumber)
	default:
		return strconv.Itoa(f.LineNumber) + ":" + strconv.Itoa(f.ColumnNumber)
	}
}

// TestFailure is one Apex test method that failed during the deployment.
type TestFailure struct {
	Name       string `json:"name"`
	MethodName string `json:"methodName"`
	Message    string `json:"message"`
	StackTrace string `json:"stackTrace"`
}

// DeployDetails is one deployment's outcome together with the failures that
// explain it. The DeployRequest list can only answer "did it fail", which
// the operator already knew; this answers what the org rejected and why.
type DeployDetails struct {
	ID                       string
	Status                   string
	Success                  bool
	CheckOnly                bool
	ErrorMessage             string
	NumberComponentsTotal    int
	NumberComponentsDeployed int
	NumberComponentErrors    int
	NumberTestsTotal         int
	NumberTestsCompleted     int
	NumberTestErrors         int
	ComponentFailures        []ComponentFailure
	TestFailures             []TestFailure
}

// HasFailures reports whether the org named anything specific that went
// wrong. Without it the deployment summary is the whole story.
func (d *DeployDetails) HasFailures() bool {
	return len(d.ComponentFailures) > 0 || len(d.TestFailures) > 0
}

type deployRequestPayload struct {
	ID           string `json:"id"`
	DeployResult struct {
		ID                       string `json:"id"`
		Status                   string `json:"status"`
		Success                  bool   `json:"success"`
		CheckOnly                bool   `json:"checkOnly"`
		ErrorMessage             string `json:"errorMessage"`
		NumberComponentsTotal    int    `json:"numberComponentsTotal"`
		NumberComponentsDeployed int    `json:"numberComponentsDeployed"`
		NumberComponentErrors    int    `json:"numberComponentErrors"`
		NumberTestsTotal         int    `json:"numberTestsTotal"`
		NumberTestsCompleted     int    `json:"numberTestsCompleted"`
		NumberTestErrors         int    `json:"numberTestErrors"`
		Details                  struct {
			ComponentFailures json.RawMessage `json:"componentFailures"`
			RunTestResult     struct {
				Failures json.RawMessage `json:"failures"`
			} `json:"runTestResult"`
		} `json:"details"`
	} `json:"deployResult"`
}

// DeployDetails fetches one deployment with its component-level failures.
func (c *Client) DeployDetails(ctx context.Context, id string) (*DeployDetails, error) {
	var payload deployRequestPayload
	path := "metadata/deployRequest/" + url.PathEscape(id) + "?includeDetails=true"
	if err := c.get(ctx, path, &payload); err != nil {
		return nil, err
	}
	r := payload.DeployResult
	d := &DeployDetails{
		ID:                       r.ID,
		Status:                   r.Status,
		Success:                  r.Success,
		CheckOnly:                r.CheckOnly,
		ErrorMessage:             r.ErrorMessage,
		NumberComponentsTotal:    r.NumberComponentsTotal,
		NumberComponentsDeployed: r.NumberComponentsDeployed,
		NumberComponentErrors:    r.NumberComponentErrors,
		NumberTestsTotal:         r.NumberTestsTotal,
		NumberTestsCompleted:     r.NumberTestsCompleted,
		NumberTestErrors:         r.NumberTestErrors,
	}
	if d.ID == "" {
		d.ID = payload.ID
	}
	var err error
	if d.ComponentFailures, err = decodeOneOrMany[ComponentFailure](r.Details.ComponentFailures); err != nil {
		return nil, err
	}
	if d.TestFailures, err = decodeOneOrMany[TestFailure](r.Details.RunTestResult.Failures); err != nil {
		return nil, err
	}
	return d, nil
}

// decodeOneOrMany handles the deploy result's habit of rendering a repeated
// element as a bare object whenever the deployment produced exactly one of
// them — the shape changes with the data, so neither form can be assumed.
func decodeOneOrMany[T any](raw json.RawMessage) ([]T, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var many []T
		if err := json.Unmarshal(trimmed, &many); err != nil {
			return nil, fmt.Errorf("unexpected deploy failure shape: %w", err)
		}
		return many, nil
	}
	var one T
	if err := json.Unmarshal(trimmed, &one); err != nil {
		return nil, fmt.Errorf("unexpected deploy failure shape: %w", err)
	}
	return []T{one}, nil
}
