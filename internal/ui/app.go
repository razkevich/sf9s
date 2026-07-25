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

	orgs             []sfcli.Org
	orgsErr          error
	loadingOrgs      bool
	awaitingStatuses bool

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

	reqSeq int

	spin spinner.Model
}

// nextGen returns an app-wide monotonic request generation. Views stamp
// async requests with it so responses from destroyed view instances (e.g.
// after an org switch) can never match a rebuilt view's expectation.
func (m *Model) nextGen() int {
	m.reqSeq++
	return m.reqSeq
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

// orgCacheKey names the on-disk org inventory. Rendering it first makes
// relaunches instant; every launch still refreshes in the background because
// the sf CLI (a Node process) needs seconds to answer.
const orgCacheKey = "orgs-inventory"

const orgCacheTTL = 30 * 24 * time.Hour

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadOrgs(), m.spin.Tick}
	var cached []sfcli.Org
	if m.deps.Store.CacheGet(orgCacheKey, orgCacheTTL, &cached) && len(cached) > 0 {
		cmds = append([]tea.Cmd{func() tea.Msg {
			return orgsLoadedMsg{orgs: cached, partial: true, cached: true}
		}}, cmds...)
	}
	return tea.Batch(cmds...)
}

// loadOrgs renders the org inventory as soon as it is known, then fills in
// connection statuses; probing statuses costs seconds per authenticated org.
func (m *Model) loadOrgs() tea.Cmd {
	sf := m.deps.SF
	fast := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		orgs, err := sf.OrgsFast(ctx)
		return orgsLoadedMsg{orgs: orgs, err: err, partial: true}
	}
	statuses := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		orgs, err := sf.Orgs(ctx)
		return orgsLoadedMsg{orgs: orgs, err: err}
	}
	return tea.Batch(fast, statuses)
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
		if msg.cached && (len(m.orgs) > 0 || m.orgsErr != nil) {
			return m, nil // live data already arrived
		}
		if msg.err != nil {
			if msg.partial && len(m.orgs) > 0 {
				return m, nil // the status pass will report it
			}
			m.loadingOrgs = false
			// A failed reload must not nuke a working session; the fatal
			// screen is only for startup, before any orgs are known.
			if len(m.orgs) > 0 {
				m.awaitingStatuses = false
				return m, toast(statusError, "org reload failed: "+msg.err.Error())
			}
			m.orgsErr = msg.err
			return m, nil
		}
		m.loadingOrgs = false
		m.orgsErr = nil
		m.awaitingStatuses = msg.partial
		if msg.partial {
			m.orgs = msg.orgs
		} else {
			m.orgs = mergeOrgStatuses(m.orgs, msg.orgs)
			m.deps.Store.CachePut(orgCacheKey, m.orgs)
		}
		if ov, ok := m.views[ViewOrgs].(*orgsView); ok {
			ov.setOrgs(m.orgs)
		} else {
			m.currentView()
		}
		if m.current == nil {
			return m, m.autoSelectOrg()
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

// mergeOrgStatuses folds connection statuses from the slow pass into the rows
// already on screen, keeping the fast pass's ordering and adding any org the
// fast pass didn't report.
func mergeOrgStatuses(current, withStatus []sfcli.Org) []sfcli.Org {
	if len(current) == 0 {
		return withStatus
	}
	statuses := make(map[string]sfcli.Org, len(withStatus))
	for _, o := range withStatus {
		statuses[o.Username] = o
	}
	merged := make([]sfcli.Org, 0, len(withStatus))
	seen := make(map[string]bool, len(current))
	for _, o := range current {
		seen[o.Username] = true
		if full, ok := statuses[o.Username]; ok {
			merged = append(merged, full)
			continue
		}
		// Present in the fast pass but not the status pass (revoked or
		// removed between calls) — keep the row, mark it unknown.
		o.ConnectedStatus = "Unknown"
		merged = append(merged, o)
	}
	for _, o := range withStatus {
		if !seen[o.Username] {
			merged = append(merged, o)
		}
	}
	return merged
}

func (m *Model) autoSelectOrg() tea.Cmd {
	if m.deps.InitialOrg != "" {
		for _, o := range m.orgs {
			if o.Alias == m.deps.InitialOrg || o.Username == m.deps.InitialOrg {
				m.setOrg(o)
				return nil
			}
		}
		// An explicitly requested org that doesn't exist must not silently
		// fall back — the user would act against the wrong org.
		return toast(statusWarn, fmt.Sprintf("org %q not found — pick one below", m.deps.InitialOrg))
	}
	for _, o := range m.orgs {
		if o.IsDefault {
			m.setOrg(o)
			return nil
		}
	}
	return nil
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
	return cloud + "   " + styleLogo.Render(" sf9s ") + "\n\n" +
		styleDim.Render("  Salesforce orgs in your terminal")
}

func (m *Model) topBar() string {
	left := styleLogo.Render(" ⚡ sf9s ") + " "
	var tabs []string
	for _, id := range viewOrder {
		style := styleTab
		if id == m.active {
			style = styleTabOn
		}
		tabs = append(tabs, style.Render(" "+viewNames[id]+" "))
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
