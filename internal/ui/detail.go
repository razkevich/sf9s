package ui

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// detailCard shows one row vertically (column: value), scrollable.
type detailCard struct {
	title string
	vp    viewport.Model
	ready bool
	// record holds the same data as JSON, for copying.
	record map[string]string
}

func newDetailCard(title string, cols, cells []string, width, height int) *detailCard {
	d := &detailCard{title: title, record: map[string]string{}}
	d.vp = viewport.New(max(width-4, 20), max(height-4, 3))
	keyW := 0
	for _, c := range cols {
		if len(c) > keyW {
			keyW = len(c)
		}
	}
	var b strings.Builder
	for i, c := range cols {
		val := ""
		if i < len(cells) {
			val = cells[i]
		}
		d.record[c] = val
		b.WriteString(styleOK.Render(padRight(c, keyW+2)))
		b.WriteString(val)
		b.WriteByte('\n')
	}
	d.vp.SetContent(b.String())
	d.ready = true
	return d
}

// JSON renders the record for the clipboard.
func (d *detailCard) JSON() (string, error) {
	raw, err := json.MarshalIndent(d.record, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// Update returns false when the card should close.
func (d *detailCard) Update(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "esc", "q", "enter":
		return false
	}
	var _ tea.Cmd
	d.vp, _ = d.vp.Update(msg)
	return true
}

func (d *detailCard) View(width, height int) string {
	d.vp.Width = max(width-6, 20)
	d.vp.Height = max(height-5, 3)
	body := styleTitle.Render(d.title) + "\n\n" + d.vp.View() + "\n" + styleDim.Render("esc close • ↑↓ scroll • y copy JSON")
	return styleOverlay.Render(body)
}
