package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

type paletteItem struct {
	name string
	// aliases are the short forms k9s users expect (:sc, :lim, :q).
	aliases []string
	desc    string
	id      ViewID
	quit    bool
}

// matches reports whether needle names this item, and whether it does so
// exactly — exact names win, so :q quits instead of opening "query".
func (i paletteItem) matches(needle string) (hit, exact bool) {
	if needle == "" {
		return true, false
	}
	for _, name := range append([]string{i.name}, i.aliases...) {
		switch {
		case name == needle:
			return true, true
		case strings.HasPrefix(name, needle):
			hit = true
		}
	}
	return hit, false
}

func (i paletteItem) aliasLabel() string {
	if len(i.aliases) == 0 {
		return ""
	}
	return strings.Join(i.aliases, ", ")
}

// orgPrefix switches org from command mode: `:org qa5`. Numbered hotkeys only
// reach the first nine, and the org you use most is not always in that set.
const orgPrefix = "org "

// palette is the k9s-style `:` command jumper.
type palette struct {
	open    bool
	input   textinput.Model
	items   []paletteItem
	matches []int
	cursor  int
	// listing marks the ctrl+a view: same list, framed as a reference.
	listing bool
}

func newPalette() *palette {
	ti := textinput.New()
	ti.Prompt = ":"
	ti.Placeholder = "view name…"
	ti.CharLimit = 32
	return &palette{
		input: ti,
		items: []paletteItem{
			{name: "orgs", aliases: []string{"o"}, desc: "authenticated orgs (home) — :org <alias> switches", id: ViewOrgs},
			{name: "query", aliases: []string{"soql", "sql"}, desc: "SOQL query editor", id: ViewQuery},
			{name: "schema", aliases: []string{"sobjects", "sc"}, desc: "object & field browser", id: ViewSchema},
			{name: "limits", aliases: []string{"lim"}, desc: "org limits & usage", id: ViewLimits},
			{name: "meta", aliases: []string{"metadata", "md"}, desc: "metadata inventory", id: ViewMeta},
			{name: "deploys", aliases: []string{"deploy", "dep"}, desc: "recent metadata deployments", id: ViewDeploys},
			{name: "logs", aliases: []string{"log", "apex"}, desc: "apex debug logs", id: ViewLogs},
			{name: "quit", aliases: []string{"q", "exit"}, desc: "exit sf9s", quit: true},
		},
	}
}

func (p *palette) Open() {
	p.open = true
	p.listing = false
	p.cursor = 0
	p.input.SetValue("")
	p.input.Focus()
	p.refilter()
}

// OpenAliases shows every view with its aliases — k9s ctrl-a.
func (p *palette) OpenAliases() {
	p.Open()
	p.listing = true
}

// orgArgument reads `:org <alias>` and returns the requested org.
func (p *palette) orgArgument() (string, bool) {
	value := strings.TrimSpace(p.input.Value())
	if !strings.HasPrefix(strings.ToLower(value), orgPrefix) {
		return "", false
	}
	target := strings.TrimSpace(value[len(orgPrefix):])
	return target, target != ""
}

// selectedItem is the entry Enter would act on.
func (p *palette) selectedItem() paletteItem {
	if p.cursor < 0 || p.cursor >= len(p.matches) {
		return paletteItem{}
	}
	return p.items[p.matches[p.cursor]]
}

func (p *palette) refilter() {
	p.matches = p.matches[:0]
	needle := strings.ToLower(strings.TrimSpace(p.input.Value()))
	var exact []int
	for i, item := range p.items {
		hit, isExact := item.matches(needle)
		switch {
		case isExact:
			exact = append(exact, i)
		case hit:
			p.matches = append(p.matches, i)
		}
	}
	// An exact name or alias always leads, so :q quits and :o opens orgs.
	p.matches = append(exact, p.matches...)
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
		if target, ok := p.orgArgument(); ok {
			p.open = false
			return func() tea.Msg { return switchOrgMsg{title: target} }
		}
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
	if p.listing {
		b.WriteString(styleTitle.Render("Views") + styleDim.Render("  (type to filter, enter to jump)") + "\n")
	}
	b.WriteString(p.input.View())
	b.WriteString("\n\n")
	if target, ok := p.orgArgument(); ok {
		b.WriteString(styleHotkey.Render("▸ switch to org ") + styleTitle.Render(target) + "\n")
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
			styleOverlay.Width(min(72, width-4)).Render(b.String()))
	}
	for i, idx := range p.matches {
		item := p.items[idx]
		row := padRight(item.name, 9) + padRight(item.aliasLabel(), 18) + item.desc
		if i == p.cursor {
			b.WriteString(styleRowSelected.Render("▸ " + row))
		} else {
			b.WriteString("  " + padRight(item.name, 9) +
				styleHotkey.Render(padRight(item.aliasLabel(), 18)) + styleDim.Render(item.desc))
		}
		b.WriteByte('\n')
	}
	box := styleOverlay.Width(min(72, width-4)).Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// padRight pads to a display width, not a byte count: hints like "↑↓" and
// "1…9" would otherwise misalign every column they appear in.
func padRight(s string, w int) string {
	return runewidth.FillRight(s, w)
}
