package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type helpOverlay struct {
	viewID ViewID
	hints  string
}

func newHelpOverlay() *helpOverlay {
	return &helpOverlay{}
}

func (h *helpOverlay) SetContent(id ViewID, hints string) {
	h.viewID = id
	h.hints = hints
}

// Update returns true while the overlay stays open.
func (h *helpOverlay) Update(msg tea.KeyMsg) bool {
	return false
}

var globalHelp = [][2]string{
	{":", "command palette (jump to any view)"},
	{"?", "this help"},
	{"esc / q", "back to orgs view"},
	{"q (on orgs)", "quit"},
	{"ctrl+c", "quit from anywhere"},
	{"/", "filter rows in any table"},
	{"↑↓/jk  ←→/hl", "move rows / scroll columns"},
	{"g/G  pgup/pgdn", "jump to top/bottom, page"},
}

var viewHelp = map[ViewID][][2]string{
	ViewOrgs: {
		{"enter", "use org and open query view"},
		{"space", "use org, stay here"},
		{"o", "open org in browser (logged in)"},
		{"y / Y", "copy access token / instance URL"},
		{"R", "re-run org discovery"},
	},
	ViewQuery: {
		{"ctrl+r", "run query"},
		{"ctrl+t", "toggle Tooling API"},
		{"ctrl+p / ctrl+n", "history prev / next"},
		{"ctrl+s", "saved query library"},
		{"tab", "switch editor ⇄ results"},
		{"enter (results)", "inspect row as a card"},
		{"m (results)", "fetch next page"},
		{"e / E (results)", "export CSV / JSON"},
	},
	ViewSchema: {
		{"enter", "open object's fields"},
		{"y", "copy object/field API name"},
		{"c", "build SELECT query for object"},
		{"esc", "back to object list"},
	},
	ViewLimits: {
		{"R", "refresh limits"},
	},
	ViewMeta: {
		{"enter", "list components of type"},
		{"y", "copy component full name"},
		{"esc", "back to type list"},
	},
	ViewDeploys: {
		{"enter", "inspect deployment"},
		{"R", "refresh"},
	},
	ViewLogs: {
		{"enter", "open log body"},
		{"d", "delete log (confirms)"},
		{"R", "refresh"},
		{"/ n N (in log)", "search / next / previous match"},
	},
}

func (h *helpOverlay) View(width, height int) string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("sf9s — keys") + "\n\n")
	b.WriteString(styleTableHeader.Render(viewNames[h.viewID]+" view") + "\n")
	for _, kv := range viewHelp[h.viewID] {
		b.WriteString("  " + styleOK.Render(padRight(kv[0], 18)) + kv[1] + "\n")
	}
	b.WriteString("\n" + styleTableHeader.Render("global") + "\n")
	for _, kv := range globalHelp {
		b.WriteString("  " + styleOK.Render(padRight(kv[0], 18)) + kv[1] + "\n")
	}
	b.WriteString("\n" + styleDim.Render("any key to close"))
	box := styleOverlay.Width(min(64, width-4)).Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
