package ui

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/razkevich/sf9s/internal/soql"
)

const popupRows = 7

// completionPopup is the suggestion list shown under the query editor.
type completionPopup struct {
	items   []soql.Candidate
	cursor  int
	top     int
	replace [2]int // byte range in the editor text the accepted text replaces
}

func (p *completionPopup) open() bool { return p != nil && len(p.items) > 0 }

func (p *completionPopup) move(delta int) {
	p.cursor += delta
	if p.cursor < 0 {
		p.cursor = len(p.items) - 1
	}
	if p.cursor >= len(p.items) {
		p.cursor = 0
	}
	if p.cursor < p.top {
		p.top = p.cursor
	}
	if p.cursor >= p.top+popupRows {
		p.top = p.cursor - popupRows + 1
	}
}

func (p *completionPopup) selected() soql.Candidate {
	if !p.open() {
		return soql.Candidate{}
	}
	return p.items[p.cursor]
}

func (p *completionPopup) View(width int) string {
	if !p.open() {
		return ""
	}
	textW := 0
	for _, item := range p.items {
		if w := runewidth.StringWidth(item.Text); w > textW {
			textW = w
		}
	}
	textW = min(textW, 40)

	var b strings.Builder
	end := min(p.top+popupRows, len(p.items))
	for i := p.top; i < end; i++ {
		item := p.items[i]
		line := runewidth.FillRight(runewidth.Truncate(item.Text, textW, "…"), textW)
		if item.Detail != "" {
			line += "  " + item.Detail
		}
		line = runewidth.Truncate(line, max(width-4, 10), "…")
		if i == p.cursor {
			b.WriteString(styleRowSelected.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}
	body := strings.TrimRight(b.String(), "\n")
	body += "\n" + styleDim.Render(fmt.Sprintf("%d/%d — ↑↓ move • enter/tab accept • esc dismiss",
		p.cursor+1, len(p.items)))
	return styleOverlay.Render(body)
}
