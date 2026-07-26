package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/razkevich/sf9s/internal/api"
	"github.com/razkevich/sf9s/internal/sfcli"
)

type orgsView struct {
	app   *Model
	table *dataTable
	orgs  []sfcli.Org
	busy  bool
	card  *orgCard
}

func newOrgsView(app *Model) *orgsView {
	v := &orgsView{app: app, table: newDataTable()}
	v.table.emptyText = "no orgs — sf org login web --alias my-org"
	v.table.SetCellStyle("Status", statusCellStyle)
	v.table.SetCellStyle("Type", orgTypeCellStyle)
	v.setOrgs(app.orgs)
	return v
}

func (v *orgsView) Title() string { return "orgs" }

func (v *orgsView) Keys() []keyHint {
	if v.card != nil {
		return []keyHint{{"y", "copy status"}, {"↑↓", "scroll"}, {"esc", "close"}}
	}
	return []keyHint{
		{"enter", "use org + query"},
		{"space", "use org"},
		{"d", "details"},
		{"o", "open in browser"},
		{"y", "copy token"},
		{"Y", "copy URL"},
		{"R", "reload orgs"},
		{"s", "sort column"},
	}
}

func (v *orgsView) Bail() bool {
	switch {
	case v.card != nil:
		v.card = nil
	case v.table.ClearFilter():
	default:
		return false
	}
	return true
}

func (v *orgsView) Capturing() bool { return v.table.Filtering() || v.card != nil }

func (v *orgsView) Init() tea.Cmd { return nil }

// setOrgs refreshes the table while preserving the user's cursor and filter,
// since statuses stream in after the first paint.
func (v *orgsView) setOrgs(orgs []sfcli.Org) {
	cursorUser := ""
	if row := v.table.CurrentRow(); row != nil {
		cursorUser = row[2]
	}
	v.orgs = orgs
	rows := make([][]string, len(orgs))
	for i, o := range orgs {
		// ● marks the org sf9s is using — the one every other key acts on.
		// The CLI's own default is a different thing and is marked ·.
		marker := ""
		if v.app.current != nil && o.Username == v.app.current.Username {
			marker = "●"
		} else if o.IsDefault {
			marker = "·"
		}
		if o.IsDefaultHub {
			marker += "★"
		}
		status := o.ConnectedStatus
		if status == "" {
			status = "checking…"
		}
		rows[i] = []string{marker, o.Title(), o.Username, orgKind(o, v.app.orgInfo[o.OrgID]),
			status, o.ExpirationDate, o.InstanceURL}
	}
	v.table.SetDataPreservingView([]string{" ", "Alias", "Username", "Type", "Status", "Expires", "Instance URL"}, rows)
	v.refreshCard()
	switch {
	case cursorUser != "":
		v.table.FocusRowWhere(2, cursorUser)
	case v.app.current != nil:
		// First paint: start on the org we are actually using, so -o and the
		// default org agree with what enter/o/y will act on.
		v.table.FocusRowWhere(2, v.app.current.Username)
	}
}

// refreshCard rebuilds an open card from the latest org data. Statuses and the
// org's own identity arrive after the first paint, and a card opened during
// that window would otherwise sit at "checking…" for as long as it is up.
func (v *orgsView) refreshCard() {
	if v.card == nil {
		return
	}
	for _, o := range v.orgs {
		if o.Username != v.card.username {
			continue
		}
		next := newOrgCard(o, v.app.orgInfo[o.OrgID],
			v.app.current != nil && v.app.current.Username == o.Username)
		// Carry the viewport over so the rebuild does not scroll the card out
		// from under the reader; width 0 forces a re-layout on the next paint.
		next.vp, next.width = v.card.vp, 0
		v.card = next
		return
	}
}

// orgKind labels an org's flavor. It prefers what the org said about itself
// over the CLI's coarse flags: "production" and "developer" both look like
// "org" to the CLI. The host-derived guess is instant and is upgraded to the
// authoritative answer once the org has told us what it is.
func orgKind(o sfcli.Org, info *api.OrgInfo) string {
	if info != nil {
		switch {
		case info.Production():
			return "PRODUCTION"
		case info.Edition() != "":
			return info.Edition()
		}
	} else if o.MaybeProduction() {
		return "prod?"
	}
	return o.Type()
}

func (v *orgsView) selected() *sfcli.Org {
	row := v.table.CurrentRow()
	if row == nil {
		return nil
	}
	for i := range v.orgs {
		if v.orgs[i].Username == row[2] {
			return &v.orgs[i]
		}
	}
	return nil
}

type orgActionMsg struct {
	toast statusMsg
}

func (v *orgsView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case orgActionMsg:
		v.busy = false
		// Pass the whole message through: rebuilding it from kind and text
		// would drop the follow-up it carries (clearing a copied token).
		out := msg.toast
		return func() tea.Msg { return out }
	case tea.KeyMsg:
		if v.card != nil {
			return v.cardKey(msg)
		}
		if v.table.Update(msg) {
			return nil
		}
		switch msg.String() {
		case "d":
			if org := v.selected(); org != nil {
				v.card = newOrgCard(*org, v.app.orgInfo[org.OrgID],
					v.app.current != nil && v.app.current.Username == org.Username)
			}
		case "enter", " ":
			// Selection is applied synchronously: routing it through a
			// command would let the next keystrokes reach the old view,
			// silently swallowing the first characters a user types.
			if org := v.selected(); org != nil {
				v.app.setOrg(*org)
				cmds := []tea.Cmd{toast(statusOK, "using org "+org.Title()), v.app.takePendingOrgInfo()}
				if msg.String() == "enter" {
					cmds = append(cmds, v.app.navigate(ViewQuery))
				}
				return tea.Batch(cmds...)
			}
		case "s":
			if label := v.table.SortByCursorColumn(); label != "" {
				return toast(statusInfo, "sorted by "+label)
			}
			return toast(statusInfo, "sort cleared")
		case "o":
			return v.openOrg()
		case "y":
			return v.withCreds("fetching token…", func(token, _ string) statusMsg {
				if err := v.app.deps.Clipboard(token); err != nil {
					return statusMsg{kind: statusError, text: err.Error()}
				}
				return statusMsg{
					kind: statusWarn,
					text: "access token copied — it grants full API access to this org" +
						clipboardExpiryNote(v.app.deps.ClipboardRead != nil && v.app.clipboardTTL > 0),
					clearClipboard: token,
				}
			})
		case "Y":
			if org := v.selected(); org != nil {
				if err := v.app.deps.Clipboard(org.InstanceURL); err != nil {
					return toastErr(err)
				}
				return toast(statusOK, "instance URL copied to clipboard")
			}
		case "R":
			if v.busy {
				return nil
			}
			return tea.Batch(toast(statusInfo, "reloading orgs…"), v.app.loadOrgs())
		}
	}
	return nil
}

// cardKey routes keys while the detail card is up. y copies the connection
// status rather than the token the list's y copies: the card exists to explain
// a failure, and the status is what goes into the ticket about it.
func (v *orgsView) cardKey(msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "y" {
		if v.card.status == "" {
			return toast(statusWarn, "no connection status to copy yet")
		}
		if err := v.app.deps.Clipboard(v.card.status); err != nil {
			return toastErr(err)
		}
		return toast(statusOK, "connection status copied to clipboard")
	}
	if !v.card.Update(msg) {
		v.card = nil
	}
	return nil
}

// tokenClipboardTTL bounds how long a live session token sits on the
// clipboard, where clipboard managers keep history.
const tokenClipboardTTL = 90 * time.Second

func clipboardExpiryNote(willClear bool) string {
	if willClear {
		return "; clipboard clears in 90s"
	}
	return ""
}

// openOrg delegates to `sf org open` so the session token never passes
// through sf9s' process arguments or a URL we build.
func (v *orgsView) openOrg() tea.Cmd {
	org := v.selected()
	if org == nil || v.busy {
		return nil
	}
	v.busy = true
	sf := v.app.deps.SF
	username := org.Username
	return tea.Batch(
		toast(statusInfo, "opening "+org.Title()+" in your browser…"),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := sf.OpenOrg(ctx, username); err != nil {
				return orgActionMsg{toast: statusMsg{kind: statusError, text: err.Error()}}
			}
			return orgActionMsg{toast: statusMsg{kind: statusOK, text: "opened in browser"}}
		},
	)
}

// withCreds resolves fresh credentials for the selected org off the UI
// thread, then applies fn and reports its toast.
func (v *orgsView) withCreds(pending string, fn func(token, instanceURL string) statusMsg) tea.Cmd {
	org := v.selected()
	if org == nil || v.busy {
		return nil
	}
	v.busy = true
	sf := v.app.deps.SF
	username := org.Username
	return tea.Batch(
		toast(statusInfo, pending),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			creds, err := sf.Credentials(ctx, username)
			if err != nil {
				return orgActionMsg{toast: statusMsg{kind: statusError, text: err.Error()}}
			}
			return orgActionMsg{toast: fn(creds.AccessToken, creds.InstanceURL)}
		},
	)
}

// orgCard is the org detail overlay. It does not reuse detailCard: that one
// puts every field on a single line, which truncates a connection status
// exactly where the diagnosis lives ("…due to: inactive user"), and the whole
// point of this card is to show that tail.
type orgCard struct {
	title string
	// username identifies the org the card is about, so a card left open can
	// be rebuilt from fresher data.
	username string
	fields   [][2]string
	diag     orgDiagnosis
	// status is the raw connection status, kept whole for the clipboard.
	status string
	vp     viewport.Model
	// width is the content width the body was last laid out for; wrapping is
	// redone when the terminal changes size.
	width int
}

func newOrgCard(org sfcli.Org, info *api.OrgInfo, inUse bool) *orgCard {
	c := &orgCard{
		title:    "org details · " + org.Title(),
		username: org.Username,
		status:   org.ConnectedStatus,
		diag:     diagnoseOrgStatus(org.ConnectedStatus, org.Title()),
		vp:       viewport.New(20, 3),
	}
	add := func(key, value string) {
		if value == "" {
			value = "—"
		}
		c.fields = append(c.fields, [2]string{key, value})
	}
	add("Alias", org.Alias)
	add("Username", org.Username)
	add("Org ID", org.OrgID)
	add("Instance URL", org.InstanceURL)
	add("Type", orgKind(org, info))
	if info != nil && info.Name != "" {
		add("Org name", info.Name)
	}
	// Only scratch orgs carry an expiry, and for them it is the difference
	// between an org that is broken and one that no longer exists.
	if org.Type() == "scratch" {
		add("Expires", org.ExpirationDate)
	}
	add("CLI default", yesNo(org.IsDefault))
	if org.IsDefaultHub {
		add("Default Dev Hub", "yes")
	}
	add("In use by sf9s", yesNo(inUse))
	status := org.ConnectedStatus
	if status == "" {
		status = "checking…"
	}
	add("Status", status)
	return c
}

// Update returns false when the card should close.
func (c *orgCard) Update(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "esc", "q", "enter", "d":
		return false
	}
	c.vp, _ = c.vp.Update(msg)
	return true
}

func (c *orgCard) View(width, height int) string {
	inner := max(width-6, 24)
	c.vp.Width = inner
	c.vp.Height = max(height-5, 3)
	if c.width != inner {
		c.width = inner
		c.vp.SetContent(c.body(inner))
	}
	body := styleTitle.Render(c.title) + "\n\n" + c.vp.View() + "\n" +
		styleDim.Render("esc close • ↑↓ scroll • y copy status")
	return styleOverlay.Render(body)
}

// body lays the card out for a content width, wrapping every value so nothing
// is ever cut off.
func (c *orgCard) body(width int) string {
	keyW := 0
	for _, f := range c.fields {
		keyW = max(keyW, lipgloss.Width(f[0]))
	}
	keyW += 2

	var b strings.Builder
	for _, f := range c.fields {
		for i, line := range wrapLines(f[1], width-keyW) {
			if i == 0 {
				b.WriteString(styleOK.Render(padRight(f[0], keyW)))
			} else {
				b.WriteString(strings.Repeat(" ", keyW))
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	if !c.diag.problem() {
		return b.String()
	}

	headline := styleErrText
	if c.diag.kind == orgStatusUnrecognized {
		// Nothing is known to be wrong beyond "this isn't Connected"; saying
		// it in red would overstate what sf9s actually understands.
		headline = styleWarn
	}
	b.WriteByte('\n')
	b.WriteString(headline.Render("⚠ "+c.diag.headline) + "\n")
	writeIndented(&b, "  ", c.diag.detail, width, lipgloss.NewStyle())
	if c.diag.action != "" {
		b.WriteByte('\n')
		writeIndented(&b, "  ", "next: "+c.diag.action, width, styleOK)
	}
	return b.String()
}

func writeIndented(b *strings.Builder, indent, text string, width int, style lipgloss.Style) {
	for _, line := range wrapLines(text, width-lipgloss.Width(indent)) {
		b.WriteString(indent + style.Render(line) + "\n")
	}
}

// wrapLines hard-wraps plain text to a width, trimming the padding lipgloss
// adds so trailing blanks never widen the card's border.
func wrapLines(s string, width int) []string {
	lines := strings.Split(lipgloss.NewStyle().Width(max(width, 20)).Render(s), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return lines
}

func (v *orgsView) View(width, height int) string {
	if v.card != nil {
		return v.card.View(width, height)
	}
	v.table.SetSize(width, height-1)
	head := styleTitle.Render(fmt.Sprintf("Authenticated orgs (%d)", len(v.orgs)))
	if v.app.awaitingStatuses {
		head += "  " + v.app.spin.View() + styleDim.Render(" checking connections…")
	} else if v.busy {
		head += "  " + v.app.spin.View()
	}
	return head + "\n" + v.table.View()
}
