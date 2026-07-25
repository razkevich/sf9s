package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// helpOverlay lists every key for the current view plus the global ones. It
// scrolls: the full list does not fit an 80x24 terminal, and silently
// clipping the one screen whose job is to reassure is the worst place to do
// it.
type helpOverlay struct {
	viewID ViewID
	hints  []keyHint
	offset int
}

func newHelpOverlay() *helpOverlay {
	return &helpOverlay{}
}

func (h *helpOverlay) SetContent(id ViewID, hints []keyHint) {
	h.viewID = id
	h.hints = hints
	h.offset = 0
}

// Update returns true while the overlay stays open: scrolling keys keep it
// up, anything else closes it.
func (h *helpOverlay) Update(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "up", "k", "ctrl+p":
		h.offset--
	case "down", "j", "ctrl+n", " ":
		h.offset++
	case "pgup", "ctrl+u":
		h.offset -= 5
	case "pgdown", "ctrl+d":
		h.offset += 5
	case "home", "g":
		h.offset = 0
	default:
		return false
	}
	h.offset = max(h.offset, 0)
	return true
}

var globalHelp = []keyHint{
	{": / f2", "command mode — :query, :schema, :q to quit"},
	{"ctrl+a", "list every view and its aliases"},
	{"1…9", "switch org (orgs view only, numbered in the header)"},
	{"? / f1", "this help (f1 works while typing too)"},
	{"esc", "bail out one level"},
	{"q", "back (quit on the orgs view)"},
	{"ctrl+c", "quit from anywhere"},
	{"/", "filter rows in any table"},
	{"s", "sort by the column in view"},
	{"↑↓ / jk", "move between rows"},
	{"←→ / hl", "scroll columns sideways"},
	{"g / G", "jump to top / bottom"},
	{"pgup / pgdn", "page up / down"},
}

var viewHelp = map[ViewID][]keyHint{
	ViewOrgs: {
		{"enter", "use org and open query view"},
		{"space", "use org, stay here"},
		{"d", "org details + why the connection is failing"},
		{"y (in card)", "copy the full connection status"},
		{"o", "open org in browser"},
		{"y / Y", "copy access token / instance URL"},
		{"R", "re-run org discovery"},
		{"1…9", "switch to a numbered org"},
	},
	ViewQuery: {
		{"ctrl+r", "run query"},
		{"tab / ctrl+space", "complete object or field at cursor"},
		{"shift+tab", "switch editor ⇄ results"},
		{"ctrl+t", "toggle Tooling API"},
		{"ctrl+p / ctrl+n", "history prev / next"},
		{"ctrl+s", "saved query library"},
		{"ctrl+u", "clear the editor"},
		{"enter (results)", "inspect row as a card"},
		{"y / Y (results)", "copy cell / row as JSON"},
		{"o (results)", "open the record in Salesforce"},
		{"m (results)", "fetch next page"},
		{"e / E (results)", "export CSV / JSON"},
	},
	ViewSchema: {
		{"enter", "open object's fields, then the field detail card"},
		{"y (card)", "copy picklist values / field details"},
		{"esc (card)", "close the card"},
		{"y", "copy object/field API name"},
		{"c", "build SELECT query for object"},
		{"R", "reload the object list"},
		{"esc", "back to object list"},
	},
	ViewLimits: {
		{"R", "refresh limits"},
	},
	ViewMeta: {
		{"enter", "list components of type"},
		{"y", "copy component full name"},
		{"R", "reload the type list"},
		{"esc", "back to type list"},
	},
	ViewDeploys: {
		{"enter", "show component and Apex test failures"},
		{"enter (failures)", "full problem text and stack trace"},
		{"R", "refresh"},
		{"esc", "back to the deployment list"},
	},
	ViewLogs: {
		{"enter", "open log body"},
		{"t", "tail: poll for new logs every 2s"},
		{"d", "delete log (confirms)"},
		{"R", "refresh"},
		{"/ n N (in log)", "search / next / previous match"},
	},
}

func (h *helpOverlay) lines() []string {
	keyW := 0
	for _, set := range [][]keyHint{viewHelp[h.viewID], globalHelp} {
		for _, kv := range set {
			keyW = max(keyW, lipgloss.Width(kv.key))
		}
	}
	keyW += 2

	var out []string
	out = append(out, styleTableHeader.Render(viewNames[h.viewID]+" view"))
	for _, kv := range viewHelp[h.viewID] {
		out = append(out, "  "+styleOK.Render(padRight(kv.key, keyW))+kv.desc)
	}
	out = append(out, "", styleTableHeader.Render("everywhere"))
	for _, kv := range globalHelp {
		out = append(out, "  "+styleOK.Render(padRight(kv.key, keyW))+kv.desc)
	}
	return out
}

func (h *helpOverlay) View(width, height int) string {
	all := h.lines()
	boxW := min(72, max(width-4, 20))
	// The box costs two rows of border; the body also carries a title, two
	// blank lines and a footer.
	bodyH := max(height-7, 3)

	maxOffset := max(len(all)-bodyH, 0)
	h.offset = min(h.offset, maxOffset)
	visible := all[h.offset:min(h.offset+bodyH, len(all))]

	footer := styleDim.Render("any other key closes")
	if maxOffset > 0 {
		footer = styleHotkey.Render(scrollLabel(h.offset, maxOffset)) +
			styleDim.Render("  ↑↓ scroll · any other key closes")
	}

	body := styleTitle.Render("sf9s — keys") + "\n\n" +
		strings.Join(visible, "\n") + "\n\n" + footer
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
		styleOverlay.Width(boxW).Render(body))
}

func scrollLabel(offset, maxOffset int) string {
	switch {
	case offset == 0:
		return "more below ▾"
	case offset >= maxOffset:
		return "▴ end"
	default:
		return "▴ more ▾"
	}
}
