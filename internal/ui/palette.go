package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type paletteItem struct {
	name string
	desc string
	id   ViewID
	quit bool
}

// palette is the k9s-style `:` command jumper.
type palette struct {
	open    bool
	input   textinput.Model
	items   []paletteItem
	matches []int
	cursor  int
}

func newPalette() *palette {
	ti := textinput.New()
	ti.Prompt = ":"
	ti.Placeholder = "view name…"
	ti.CharLimit = 32
	return &palette{
		input: ti,
		items: []paletteItem{
			{name: "orgs", desc: "authenticated orgs (home)", id: ViewOrgs},
			{name: "query", desc: "SOQL query editor", id: ViewQuery},
			{name: "schema", desc: "object & field browser", id: ViewSchema},
			{name: "limits", desc: "org limits & usage", id: ViewLimits},
			{name: "meta", desc: "metadata inventory", id: ViewMeta},
			{name: "deploys", desc: "recent metadata deployments", id: ViewDeploys},
			{name: "logs", desc: "apex debug logs", id: ViewLogs},
			{name: "quit", desc: "exit sf9s", quit: true},
		},
	}
}

func (p *palette) Open() {
	p.open = true
	p.cursor = 0
	p.input.SetValue("")
	p.input.Focus()
	p.refilter()
}

func (p *palette) refilter() {
	p.matches = p.matches[:0]
	needle := strings.ToLower(strings.TrimSpace(p.input.Value()))
	for i, item := range p.items {
		if needle == "" || strings.HasPrefix(item.name, needle) || strings.Contains(item.name, needle) {
			p.matches = append(p.matches, i)
		}
	}
	if p.cursor >= len(p.matches) {
		p.cursor = 0
	}
}

func (p *palette) Update(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.open = false
		return nil
	case "enter":
		if len(p.matches) == 0 {
			p.open = false
			return nil
		}
		item := p.items[p.matches[p.cursor]]
		p.open = false
		if item.quit {
			return tea.Quit
		}
		return func() tea.Msg { return switchViewMsg{id: item.id} }
	case "up", "ctrl+p":
		if p.cursor > 0 {
			p.cursor--
		}
		return nil
	case "down", "ctrl+n", "tab":
		if p.cursor < len(p.matches)-1 {
			p.cursor++
		}
		return nil
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	p.refilter()
	return cmd
}

func (p *palette) View(width, height int) string {
	var b strings.Builder
	b.WriteString(p.input.View())
	b.WriteString("\n\n")
	for i, idx := range p.matches {
		item := p.items[idx]
		line := "  " + padRight(item.name, 10) + styleDim.Render(item.desc)
		if i == p.cursor {
			line = styleRowSelected.Render("▸ " + padRight(item.name, 10) + item.desc)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	box := styleOverlay.Width(min(60, width-4)).Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func padRight(s string, w int) string {
	for len(s) < w {
		s += " "
	}
	return s
}
