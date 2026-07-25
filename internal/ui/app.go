// Package ui is the Bubble Tea application: a root model owning global
// navigation, the current-org context, and one sub-model per view.
package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/razkevich/sf9s/internal/api"
	"github.com/razkevich/sf9s/internal/config"
	"github.com/razkevich/sf9s/internal/sfcli"
)

// ViewID identifies a top-level view.
type ViewID int

const (
	ViewOrgs ViewID = iota
	ViewQuery
	ViewSchema
	ViewLimits
	ViewMeta
	ViewDeploys
	ViewLogs
)

var viewNames = map[ViewID]string{
	ViewOrgs:    "orgs",
	ViewQuery:   "query",
	ViewSchema:  "schema",
	ViewLimits:  "limits",
	ViewMeta:    "meta",
	ViewDeploys: "deploys",
	ViewLogs:    "logs",
}

var viewOrder = []ViewID{ViewOrgs, ViewQuery, ViewSchema, ViewLimits, ViewMeta, ViewDeploys, ViewLogs}

// view is the contract every sub-model fulfills.
type view interface {
	Init() tea.Cmd
	Update(msg tea.Msg) tea.Cmd
	View(width, height int) string
	Title() string
	Hints() string
	Capturing() bool
}

// Deps wires the outside world into the UI; tests substitute fakes.
type Deps struct {
	SF         *sfcli.Client
	Store      *config.Store
	NewAPI     func(username string) *api.Client
	Clipboard  func(string) error
	OpenURL    func(string) error
	Version    string
	InitialOrg string
}

// Model is the root Bubble Tea model.
type Model struct {
	deps Deps

	width, height int

	orgs        []sfcli.Org
	orgsErr     error
	loadingOrgs bool

	current *sfcli.Org
	client  *api.Client

	active ViewID
	views  map[ViewID]view

	palette  *palette
	showHelp bool
	helpView *helpOverlay

	status      statusMsg
	statusUntil time.Time
	statusSeq   int
	statusTTL   time.Duration

	spin spinner.Model
}

// New builds the root model.
func New(deps Deps) *Model {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(colorAccent)
	m := &Model{
		deps:        deps,
		views:       map[ViewID]view{},
		palette:     newPalette(),
		helpView:    newHelpOverlay(),
		spin:        sp,
		loadingOrgs: true,
		active:      ViewOrgs,
		statusTTL:   5 * time.Second,
	}
	return m
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loadOrgs(), m.spin.Tick)
}

func (m *Model) loadOrgs() tea.Cmd {
	sf := m.deps.SF
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		orgs, err := sf.Orgs(ctx)
		return orgsLoadedMsg{orgs: orgs, err: err}
	}
}

func (m *Model) currentView() view {
	if v, ok := m.views[m.active]; ok {
		return v
	}
	v := m.buildView(m.active)
	m.views[m.active] = v
	return v
}

func (m *Model) buildView(id ViewID) view {
	switch id {
	case ViewOrgs:
		return newOrgsView(m)
	case ViewQuery:
		return newQueryView(m)
	case ViewSchema:
		return newSchemaView(m)
	case ViewLimits:
		return newLimitsView(m)
	case ViewMeta:
		return newMetaView(m)
	case ViewDeploys:
		return newDeploysView(m)
	case ViewLogs:
		return newLogsView(m)
	}
	return newOrgsView(m)
}

// setOrg switches the working org and resets org-scoped views.
func (m *Model) setOrg(org sfcli.Org) {
	if m.current != nil && m.current.Username == org.Username {
		return
	}
	o := org
	m.current = &o
	m.client = m.deps.NewAPI(org.Username)
	for id := range m.views {
		if id != ViewOrgs && id != ViewQuery {
			delete(m.views, id)
		}
	}
	if qv, ok := m.views[ViewQuery].(*queryView); ok {
		qv.resetOrg()
	}
}

func (m *Model) navigate(id ViewID) tea.Cmd {
	if id != ViewOrgs && m.current == nil {
		return toast(statusWarn, "select an org first (enter on the orgs view)")
	}
	m.active = id
	_, existed := m.views[id]
	v := m.currentView()
	if !existed {
		return v.Init()
	}
	return nil
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case orgsLoadedMsg:
		m.loadingOrgs = false
		m.orgsErr = msg.err
		if msg.err == nil {
			m.orgs = msg.orgs
			if ov, ok := m.views[ViewOrgs].(*orgsView); ok {
				ov.setOrgs(msg.orgs)
			} else {
				m.currentView()
			}
			if m.current == nil {
				m.autoSelectOrg()
			}
		}
		return m, nil

	case statusMsg:
		m.status = msg
		m.statusSeq++
		m.statusUntil = time.Now().Add(5 * time.Second)
		seq := m.statusSeq
		return m, tea.Tick(m.statusTTL, func(time.Time) tea.Msg { return clearStatusMsg{id: seq} })

	case clearStatusMsg:
		if msg.id == m.statusSeq {
			m.status = statusMsg{}
		}
		return m, nil

	case useOrgMsg:
		m.setOrg(msg.org)
		cmds := []tea.Cmd{toast(statusOK, "using org "+msg.org.Title())}
		if msg.jump {
			cmds = append(cmds, m.navigate(ViewQuery))
		}
		return m, tea.Batch(cmds...)

	case switchViewMsg:
		return m, m.navigate(msg.id)

	case goBackMsg:
		if m.active != ViewOrgs {
			m.active = ViewOrgs
			m.currentView()
		}
		return m, nil

	case prefillQueryMsg:
		cmd := m.navigate(ViewQuery)
		if qv, ok := m.views[ViewQuery].(*queryView); ok {
			qv.setEditorText(msg.soql)
		}
		return m, cmd

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Everything else (async results) goes to all live views; each view
	// discards messages that aren't its own via typed msgs + generations.
	var cmds []tea.Cmd
	for _, v := range m.views {
		if cmd := v.Update(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) autoSelectOrg() {
	if m.deps.InitialOrg != "" {
		for _, o := range m.orgs {
			if o.Alias == m.deps.InitialOrg || o.Username == m.deps.InitialOrg {
				m.setOrg(o)
				return
			}
		}
	}
	for _, o := range m.orgs {
		if o.IsDefault {
			m.setOrg(o)
			return
		}
	}
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.showHelp {
		if m.helpView.Update(msg) {
			return m, nil
		}
		m.showHelp = false
		return m, nil
	}
	if m.palette.open {
		cmd := m.palette.Update(msg)
		return m, cmd
	}

	v := m.currentView()
	if v.Capturing() {
		return m, v.Update(msg)
	}

	switch msg.String() {
	case ":":
		m.palette.Open()
		return m, nil
	case "?":
		m.showHelp = true
		m.helpView.SetContent(m.active, v.Hints())
		return m, nil
	case "q":
		if m.active == ViewOrgs {
			return m, tea.Quit
		}
		return m, func() tea.Msg { return goBackMsg{} }
	}
	return m, v.Update(msg)
}

func (m *Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	if m.loadingOrgs {
		return m.centered(splashArt() + "\n\n" + m.spin.View() +
			styleDim.Render("  discovering authenticated orgs (sf org list)…"))
	}
	if m.orgsErr != nil {
		return m.centered(m.startupError())
	}

	contentH := m.height - 2
	var body string
	switch {
	case m.showHelp:
		body = m.helpView.View(m.width, contentH)
	case m.palette.open:
		body = m.palette.View(m.width, contentH)
	default:
		body = m.currentView().View(m.width, contentH)
	}
	body = lipgloss.NewStyle().Height(contentH).MaxHeight(contentH).Render(body)
	return m.topBar() + "\n" + body + "\n" + m.statusBar()
}

func (m *Model) startupError() string {
	switch {
	case errors.Is(m.orgsErr, sfcli.ErrCLINotFound):
		return styleErrText.Render("Salesforce CLI not found") + "\n\n" +
			"sf9s reads your orgs from the sf CLI, which isn't on PATH.\n" +
			"Install it:  npm install -g @salesforce/cli\n\n" +
			styleDim.Render("press ctrl+c to exit")
	case errors.Is(m.orgsErr, sfcli.ErrNoOrgs):
		return styleErrText.Render("No authenticated orgs") + "\n\n" +
			"Log into an org first:  sf org login web --alias my-org\n\n" +
			styleDim.Render("press ctrl+c to exit")
	default:
		return styleErrText.Render("Could not list orgs") + "\n\n" + m.orgsErr.Error() + "\n\n" +
			styleDim.Render("press ctrl+c to exit")
	}
}

func (m *Model) centered(s string) string {
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, s)
}

func splashArt() string {
	cloud := styleDim.Render("      .--.\n   .-(    ).\n  (___.__)__)")
	return cloud + "   " + styleLogo.Render("sf9s") + "\n\n" +
		styleDim.Render("  Salesforce orgs in your terminal")
}

func (m *Model) topBar() string {
	left := styleLogo.Render("⚡ sf9s") + " "
	var tabs []string
	for _, id := range viewOrder {
		style := styleTab
		if id == m.active {
			style = styleTabOn
		}
		tabs = append(tabs, style.Render(viewNames[id]))
	}
	left += strings.Join(tabs, "")
	version := styleVersion.Render(m.deps.Version + " ")
	pad := m.width - lipgloss.Width(left) - lipgloss.Width(version)
	if pad < 0 {
		return runeTrunc(left, m.width)
	}
	return left + strings.Repeat(" ", pad) + version
}

func (m *Model) statusBar() string {
	org := styleStatusDim.Render(" no org — pick one on the orgs view")
	if m.current != nil {
		org = styleStatusOrg.Render(" ⚡ "+m.current.Title()) +
			styleStatusDim.Render(fmt.Sprintf(" %s [%s]", m.current.Username, m.current.Type()))
	}
	left := org
	if m.status.text != "" && time.Now().Before(m.statusUntil) {
		style := styleStatusDim
		switch m.status.kind {
		case statusOK:
			style = styleToastOK
		case statusWarn:
			style = styleToastWarn
		case statusError:
			style = styleToastErr
		}
		left = org + styleStatusDim.Render("  ") + style.Render(runeTrunc(m.status.text, m.width-lipgloss.Width(org)-4))
	} else if !m.palette.open && !m.showHelp {
		hints := m.currentView().Hints()
		left = org + styleStatusDim.Render("  "+hints)
	}
	pad := m.width - lipgloss.Width(left)
	if pad < 0 {
		return runeTrunc(left, m.width)
	}
	return left + styleBand.Render(strings.Repeat(" ", pad))
}

// runeTrunc truncates a styled string to the terminal width.
func runeTrunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}
