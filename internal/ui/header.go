package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// The header follows k9s: a context block on the left, key legends in the
// middle (view-specific keys, plus numbered hotkeys that switch org the way
// k9s numbers switch namespace), and a logo on the right. It collapses to a
// single line when the terminal is too small to spend four rows on chrome.
const (
	headerRows      = 4
	headerMinHeight = 22
	headerMinWidth  = 90
	orgHotkeys      = 9
)

var (
	styleHeaderKey   = lipgloss.NewStyle().Foreground(colorDim)
	styleHeaderValue = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	styleHotkey      = lipgloss.NewStyle().Foreground(colorOK)
)

// compactHeader reports whether the terminal is too small for the full header.
func (m *Model) compactHeader() bool {
	return m.height < headerMinHeight || m.width < headerMinWidth
}

// headerHeight is how many rows View must reserve.
func (m *Model) headerHeight() int {
	if m.compactHeader() {
		return 1
	}
	return headerRows
}

// hotkeyOrgs are the orgs reachable with a number key. The order is the org
// list's own, never most-recently-used: a number that points somewhere new
// every time you switch is a number you can only press by accident.
func (m *Model) hotkeyOrgs() []string {
	titles := make([]string, 0, orgHotkeys)
	for _, o := range m.orgs {
		if len(titles) >= orgHotkeys {
			break
		}
		titles = append(titles, o.Title())
	}
	return titles
}

// orgForHotkey maps a number key (1-based) to an org.
func (m *Model) orgForHotkey(n int) string {
	titles := m.hotkeyOrgs()
	if n < 1 || n > len(titles) {
		return ""
	}
	return titles[n-1]
}

func (m *Model) header() string {
	if m.compactHeader() {
		return m.compactBar()
	}

	context := m.contextBlock()

	// Narrow by degrees: drop the logo, fold the org hotkeys to one column,
	// then show fewer key columns. Something from every block survives as
	// long as possible, and the full key list is always one `?` away.
	for _, blocks := range [][]string{
		{context, m.keyBlock(3), m.orgBlock(2), m.logoBlock()},
		{context, m.keyBlock(3), m.orgBlock(2)},
		{context, m.keyBlock(3), m.orgBlock(1)},
		{context, m.keyBlock(2), m.orgBlock(1)},
		{context, m.keyBlock(2)},
		{context, m.keyBlock(1)},
	} {
		joined := lipgloss.JoinHorizontal(lipgloss.Top, blocks...)
		if lipgloss.Width(joined) <= m.width {
			return joined
		}
	}
	// Never render a header without keys: an undiscoverable app is worse
	// than a cramped one, so fall back to the compact single-line bar.
	return m.compactBar()
}

func (m *Model) contextBlock() string {
	org, user, kind, status := "—", "—", "—", ""
	if m.current != nil {
		org = m.current.Title()
		user = m.current.Username
		kind = m.current.Type()
		status = m.current.ConnectedStatus
	}
	// The org's own edition is more honest than the CLI's flags, which
	// cannot tell production from Developer Edition.
	if info := m.currentOrgInfo(); info != nil {
		if edition := info.Edition(); edition != "" {
			kind = edition
		}
	}
	api := ""
	if m.current != nil && m.current.InstanceURL != "" {
		api = m.current.InstanceURL
	}
	rows := [][2]string{
		{"Org", org},
		{"User", user},
		{"Type", kind},
		{"Host", strings.TrimPrefix(strings.TrimPrefix(api, "https://"), "http://")},
	}
	if status != "" && !strings.EqualFold(status, "connected") {
		rows[3] = [2]string{"Status", status}
	}

	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		value := runewidth.Truncate(r[1], 30, "…")
		b.WriteString(styleHeaderKey.Render(padRight(r[0]+":", 7)))
		switch {
		case r[0] == "Status":
			b.WriteString(statusCellStyle(value).Render(value))
		case r[0] == "Org" && m.inProduction():
			// Impossible to miss: this org holds live business data.
			b.WriteString(styleProdBadge.Render(" PROD ") + " " + styleProdText.Render(value))
		case r[0] == "Type" && m.inProduction():
			b.WriteString(styleProdText.Render(value))
		default:
			b.WriteString(styleHeaderValue.Render(value))
		}
	}
	return lipgloss.NewStyle().PaddingLeft(1).PaddingRight(3).Render(b.String())
}

// keyBlock lists what the current view can do right now, plus the global keys
// that are easy to forget.
func (m *Model) keyBlock(cols int) string {
	hints := append([]keyHint{}, m.currentView().Keys()...)
	if m.currentView().Capturing() {
		// While a view is taking text, ":" and "?" are just characters.
		hints = append(hints,
			keyHint{"f2", "command"},
			keyHint{"f1", "help"},
		)
	} else {
		hints = append(hints,
			keyHint{":", "command"},
			keyHint{"?", "help"},
			keyHint{"ctrl+a", "views"},
		)
	}
	return renderKeyColumns(hints, headerRows, cols)
}

// orgBlock renders the numbered org hotkeys in at most cols columns. Aliases
// are truncated: the number is what you press, the name only has to be
// recognizable.
func (m *Model) orgBlock(cols int) string {
	// Number keys act only on the orgs view, so advertise them only there —
	// elsewhere they would promise something that does not happen, and the
	// space is better spent on the keys that do work.
	if m.active != ViewOrgs {
		return ""
	}
	titles := m.hotkeyOrgs()
	if len(titles) == 0 {
		return ""
	}
	if cols == 1 {
		titles = titles[:min(len(titles), headerRows)]
	}
	hints := make([]keyHint, 0, len(titles)+1)
	for i, t := range titles {
		hints = append(hints, keyHint{fmt.Sprintf("%d", i+1), runewidth.Truncate(t, 14, "…")})
	}
	if rest := len(m.orgs) - len(titles); rest > 0 && cols > 1 {
		hints = append(hints, keyHint{":org", fmt.Sprintf("+%d more", rest)})
	}
	return renderKeyColumns(hints, headerRows, cols)
}

// renderKeyColumns lays hints out top-to-bottom in up to maxCols columns of
// rows each, the way k9s packs its header legend.
func renderKeyColumns(hints []keyHint, rows, maxCols int) string {
	if len(hints) == 0 || rows <= 0 {
		return ""
	}
	cols := (len(hints) + rows - 1) / rows
	cols = min(cols, maxCols)

	lines := make([]string, rows)
	for c := 0; c < cols; c++ {
		// Width the column to its own content so columns stay aligned.
		keyW, descW := 0, 0
		for r := 0; r < rows; r++ {
			if i := c*rows + r; i < len(hints) {
				keyW = max(keyW, runewidth.StringWidth(hints[i].key)+2)
				descW = max(descW, runewidth.StringWidth(hints[i].desc))
			}
		}
		for r := 0; r < rows; r++ {
			cell := strings.Repeat(" ", keyW+descW+1)
			if i := c*rows + r; i < len(hints) {
				h := hints[i]
				key := styleHotkey.Render(padRight("<"+h.key+">", keyW))
				desc := runewidth.FillRight(h.desc, descW)
				cell = key + " " + styleDim.Render(desc)
			}
			lines[r] += cell + "  "
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Model) logoBlock() string {
	art := "  .--.\n" +
		".-(    ).\n" +
		"(___.__)__)"
	return lipgloss.NewStyle().PaddingLeft(2).Render(
		styleDim.Render(art) + "\n" + styleLogoChip.Render(" sf9s ") + styleVersion.Render(" "+m.deps.Version))
}

// compactBar is the single-line header for small terminals: view tabs and the
// current org, which is the minimum needed to stay oriented.
func (m *Model) compactBar() string {
	left := styleLogoChip.Render(" ⚡ sf9s ") + " "
	var tabs []string
	for _, id := range viewOrder {
		style := styleTab
		if id == m.active {
			style = styleTabOn
		}
		tabs = append(tabs, style.Render(" "+viewNames[id]+" "))
	}
	left += strings.Join(tabs, "")
	if m.current != nil {
		left += "  " + styleHeaderValue.Render("⚡ "+m.current.Title())
	}
	return runeTrunc(left, m.width)
}

// crumbs is the k9s breadcrumb trail of the view stack, shown at the bottom.
func (m *Model) crumbs() string {
	trail := append([]ViewID{}, m.stack...)
	trail = append(trail, m.active)
	parts := make([]string, 0, len(trail))
	for i, id := range trail {
		name := viewNames[id]
		if i == len(trail)-1 {
			parts = append(parts, styleRowSelected.Render(" "+name+" "))
			continue
		}
		parts = append(parts, styleStatusDim.Render(" "+name+" "))
	}
	return strings.Join(parts, styleStatusDim.Render("›"))
}
