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

// keyHint is one entry of a view's key legend, rendered in the header grid
// (k9s style) and in the help overlay.
type keyHint struct {
	key  string
	desc string
}

// view is the contract every sub-model fulfills.
type view interface {
	Init() tea.Cmd
	Update(msg tea.Msg) tea.Cmd
	View(width, height int) string
	Title() string
	// Keys lists the actions available right now; it changes with the view's
	// own mode (browsing a list vs. reading a log body, say).
	Keys() []keyHint
	// Bail closes one level of the view's own state (a filter, an open card,
	// a drill-down) and reports whether it did. When it returns false the app
	// walks back up the crumb trail instead.
	Bail() bool
	Capturing() bool
}

// hintLine renders key hints as a single compact line.
func hintLine(hints []keyHint) string {
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts, h.key+" "+h.desc)
	}
	return strings.Join(parts, " • ")
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
	// orgsFromCache marks rows restored from disk. They make -o and the
	// default org work instantly, but they may name orgs that have since been
	// logged out — so a selection made from them is provisional until the
	// live list confirms it, and live data replaces rather than merges.
	orgsFromCache    bool
	currentFromCache bool

	current *sfcli.Org
	client  *api.Client
	// orgInfo is what each org says it is (edition, sandbox, trial), keyed by
	// org id. The sf CLI cannot distinguish production from Developer
	// Edition, and working in the wrong one is the costliest mistake here.
	orgInfo map[string]*api.OrgInfo

	active ViewID
	// stack is the navigation trail behind the active view; esc pops it, the
	// way k9s bails out one level at a time.
	stack []ViewID
	views map[ViewID]view

	palette  *palette
	showHelp bool
	helpView *helpOverlay

	status      statusMsg
	statusUntil time.Time
	statusSeq   int
	statusTTL   time.Duration

	reqSeq int
	// pendingOrgInfo carries the org-identity fetch that setOrg started, so
	// callers can return it as a command.
	pendingOrgInfo tea.Cmd

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
		orgInfo:     map[string]*api.OrgInfo{},
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
	// Stop background polling before discarding its view, so nothing keeps
	// querying the org we just left.
	if lv, ok := m.views[ViewLogs].(*logsView); ok {
		lv.stopTail()
	}
	for id := range m.views {
		if id != ViewOrgs && id != ViewQuery {
			delete(m.views, id)
		}
	}
	if qv, ok := m.views[ViewQuery].(*queryView); ok {
		qv.resetOrg()
	}
	m.pendingOrgInfo = m.loadOrgInfo(o)
}

const orgInfoTTL = 24 * time.Hour

// loadOrgInfo asks the org what it is, from cache when possible. Returns nil
// when the answer is already known.
func (m *Model) loadOrgInfo(org sfcli.Org) tea.Cmd {
	if org.OrgID == "" || m.orgInfo[org.OrgID] != nil {
		return nil
	}
	var cached api.OrgInfo
	if m.deps.Store.CacheGet("org-info-"+org.OrgID, orgInfoTTL, &cached) && cached.OrganizationType != "" {
		m.orgInfo[org.OrgID] = &cached
		return nil
	}
	client := m.client
	store := m.deps.Store
	id := org.OrgID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		info, err := client.FetchOrgInfo(ctx)
		if err != nil {
			return orgInfoMsg{orgID: id, err: err}
		}
		store.CachePut("org-info-"+id, info)
		return orgInfoMsg{orgID: id, info: info}
	}
}

// takePendingOrgInfo hands the identity fetch started by setOrg to whoever
// can return a command.
func (m *Model) takePendingOrgInfo() tea.Cmd {
	cmd := m.pendingOrgInfo
	m.pendingOrgInfo = nil
	return cmd
}

// currentOrgInfo is what the selected org reports about itself, if known yet.
func (m *Model) currentOrgInfo() *api.OrgInfo {
	if m.current == nil {
		return nil
	}
	return m.orgInfo[m.current.OrgID]
}

// inProduction reports whether the selected org holds live business data.
func (m *Model) inProduction() bool {
	info := m.currentOrgInfo()
	return info != nil && info.Production()
}

func (m *Model) navigate(id ViewID) tea.Cmd {
	if id != ViewOrgs && m.current == nil {
		return toast(statusWarn, "select an org first (enter on the orgs view)")
	}
	// Leaving the logs view ends any tail; otherwise it keeps polling the org
	// from a screen the user can no longer see.
	if lv, ok := m.views[ViewLogs].(*logsView); ok && id != ViewLogs {
		lv.stopTail()
	}
	if id != m.active {
		if i := crumbIndex(m.stack, id); i >= 0 {
			// Returning to a view already on the trail: everything after it
			// is no longer how we got here.
			m.stack = m.stack[:i]
		} else {
			m.pushCrumb(m.active)
		}
	}
	m.active = id
	_, existed := m.views[id]
	v := m.currentView()
	if !existed {
		return v.Init()
	}
	return nil
}

func crumbIndex(stack []ViewID, id ViewID) int {
	for i, v := range stack {
		if v == id {
			return i
		}
	}
	return -1
}

// pushCrumb records where we came from, keeping the trail free of loops: any
// earlier visit to the same view truncates the trail there.
func (m *Model) pushCrumb(from ViewID) {
	for i, id := range m.stack {
		if id == from {
			m.stack = m.stack[:i]
			break
		}
	}
	m.stack = append(m.stack, from)
	if len(m.stack) > 8 {
		m.stack = m.stack[len(m.stack)-8:]
	}
}

// popCrumb walks one level back, ending at the orgs view.
func (m *Model) popCrumb() tea.Cmd {
	if len(m.stack) == 0 {
		if m.active == ViewOrgs {
			return nil
		}
		return m.navigateNoCrumb(ViewOrgs)
	}
	prev := m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]
	return m.navigateNoCrumb(prev)
}

// navigateNoCrumb switches view without extending the trail (used when
// walking back through it).
func (m *Model) navigateNoCrumb(id ViewID) tea.Cmd {
	saved := m.stack
	cmd := m.navigate(id)
	m.stack = saved
	return cmd
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
		switch {
		case msg.partial:
			m.orgs = msg.orgs
			m.orgsFromCache = msg.cached
		case m.orgsFromCache:
			// The live status pass supersedes cached rows entirely, so an org
			// that was logged out since last run disappears instead of being
			// resurrected as "Unknown".
			m.orgs = msg.orgs
			m.orgsFromCache = false
			m.deps.Store.CachePut(orgCacheKey, m.orgs)
		default:
			m.orgs = mergeOrgStatuses(m.orgs, msg.orgs)
			m.deps.Store.CachePut(orgCacheKey, m.orgs)
		}
		if ov, ok := m.views[ViewOrgs].(*orgsView); ok {
			ov.setOrgs(m.orgs)
		} else {
			m.currentView()
		}
		var cmds []tea.Cmd
		dropped := false
		if !msg.cached {
			var cmd tea.Cmd
			cmd, dropped = m.confirmCachedSelection()
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		// After dropping a stale org, wait for the user to choose rather than
		// silently moving them onto a different org.
		if m.current == nil && !dropped {
			m.currentFromCache = msg.cached
			if cmd := m.autoSelectOrg(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)

	case orgInfoMsg:
		if msg.err != nil {
			return m, nil // identity is advisory; the org still works
		}
		m.orgInfo[msg.orgID] = msg.info
		if ov, ok := m.views[ViewOrgs].(*orgsView); ok {
			ov.setOrgs(m.orgs) // the list can now label this org honestly
		}
		if m.current != nil && m.current.OrgID == msg.orgID && msg.info.Production() {
			return m, toast(statusWarn, "⚠ "+m.current.Title()+" is PRODUCTION ("+msg.info.Edition()+") — changes here are real")
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
		return m, m.popCrumb()

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

// switchOrgByHotkey selects the org behind a number key, mirroring how k9s
// numbers switch namespace.
func (m *Model) switchOrgByHotkey(key string) tea.Cmd {
	n := int(key[0] - '0')
	title := m.orgForHotkey(n)
	if title == "" {
		return toast(statusWarn, "no org on <"+key+">")
	}
	if m.current != nil && m.current.Title() == title {
		return toast(statusInfo, "already on "+title)
	}
	for _, o := range m.orgs {
		if o.Title() == title {
			m.setOrg(o)
			return tea.Batch(toast(statusOK, "switched to "+title), m.takePendingOrgInfo())
		}
	}
	return nil
}

// confirmCachedSelection validates an org chosen from the disk cache against
// the live list, dropping it if it no longer exists so the user cannot work
// against an org they have logged out of.
// It reports whether a selection was dropped.
func (m *Model) confirmCachedSelection() (tea.Cmd, bool) {
	if m.current == nil || !m.currentFromCache {
		return nil, false
	}
	for _, o := range m.orgs {
		if o.Username == m.current.Username {
			m.currentFromCache = false
			return nil, false
		}
	}
	stale := m.current.Title()
	m.current = nil
	m.client = nil
	m.currentFromCache = false
	m.active = ViewOrgs
	return toast(statusWarn, "org "+stale+" is no longer authenticated — pick one below"), true
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
				return m.takePendingOrgInfo()
			}
		}
		// An explicitly requested org that doesn't exist must not silently
		// fall back — the user would act against the wrong org.
		return toast(statusWarn, fmt.Sprintf("org %q not found — pick one below", m.deps.InitialOrg))
	}
	for _, o := range m.orgs {
		if o.IsDefault {
			m.setOrg(o)
			return m.takePendingOrgInfo()
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

	switch key := msg.String(); key {
	case ":":
		m.palette.Open()
		return m, nil
	case "?":
		m.showHelp = true
		m.helpView.SetContent(m.active, v.Keys())
		return m, nil
	case "ctrl+a":
		m.palette.OpenAliases()
		return m, nil
	case "esc":
		// The view unwinds its own state first (filter, card, drill-down);
		// only when it has nothing left to close do we leave it.
		if v.Bail() {
			return m, nil
		}
		return m, m.popCrumb()
	case "q":
		if m.active == ViewOrgs {
			return m, tea.Quit
		}
		return m, m.popCrumb()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if cmd := m.switchOrgByHotkey(key); cmd != nil {
			return m, cmd
		}
		return m, nil
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

	headerH := m.headerHeight()
	contentH := max(m.height-headerH-1, 1)
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
	header := lipgloss.NewStyle().Height(headerH).MaxHeight(headerH).Render(m.header())
	return header + "\n" + body + "\n" + m.statusBar()
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
	return cloud + "   " + styleLogoChip.Render(" sf9s ") + "\n\n" +
		styleDim.Render("  Salesforce orgs in your terminal")
}

func (m *Model) statusBar() string {
	left := m.crumbs()
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
		left += styleStatusDim.Render("  ") +
			style.Render(runeTrunc(m.status.text, m.width-lipgloss.Width(left)-4))
	} else if m.compactHeader() && !m.palette.open && !m.showHelp {
		// The compact header has no room for the key legend, so keep it here.
		left += styleStatusDim.Render("  " + hintLine(m.currentView().Keys()))
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
