package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/razkevich/sf9s/internal/sfcli"
)

type statusKind int

const (
	statusInfo statusKind = iota
	statusOK
	statusWarn
	statusError
)

type statusMsg struct {
	kind statusKind
	text string
}

type clearStatusMsg struct{ id int }

func toast(kind statusKind, text string) tea.Cmd {
	return func() tea.Msg { return statusMsg{kind: kind, text: text} }
}

func toastErr(err error) tea.Cmd {
	return toast(statusError, err.Error())
}

type orgsLoadedMsg struct {
	orgs []sfcli.Org
	err  error
	// partial marks the fast (status-free) first pass; a second message
	// follows with connection statuses filled in.
	partial bool
	// cached marks rows restored from disk, which must never overwrite
	// fresher data already on screen.
	cached bool
}

// goBackMsg asks the root model to navigate back to the orgs home view.
type goBackMsg struct{}

func goBack() tea.Msg { return goBackMsg{} }

// switchViewMsg asks the root to activate a view.
type switchViewMsg struct{ id ViewID }

// prefillQueryMsg jumps to the query view with a generated SOQL statement.
type prefillQueryMsg struct{ soql string }

// useOrgMsg sets the current org; jump indicates moving on to the query view.
type useOrgMsg struct {
	org  sfcli.Org
	jump bool
}
