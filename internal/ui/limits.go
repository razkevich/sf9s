package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/razkevich/sf9s/internal/api"
)

type limitsView struct {
	app     *Model
	vp      viewport.Model
	limits  map[string]api.Limit
	loading bool
	gen     int
	ready   bool
}

func newLimitsView(app *Model) *limitsView {
	return &limitsView{app: app}
}

func (v *limitsView) Title() string   { return "limits" }
func (v *limitsView) Hints() string   { return "R refresh • ↑↓ scroll" }
func (v *limitsView) Capturing() bool { return false }

type limitsMsg struct {
	gen    int
	limits map[string]api.Limit
	err    error
}

func (v *limitsView) Init() tea.Cmd {
	v.loading = true
	v.gen++
	gen := v.gen
	client := v.app.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		limits, err := client.Limits(ctx)
		return limitsMsg{gen: gen, limits: limits, err: err}
	}
}

func (v *limitsView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case limitsMsg:
		if msg.gen != v.gen {
			return nil
		}
		v.loading = false
		if msg.err != nil {
			return toastErr(msg.err)
		}
		v.limits = msg.limits
		v.renderContent()
		return nil
	case tea.KeyMsg:
		if msg.String() == "R" {
			return v.Init()
		}
		if msg.String() == "esc" {
			return goBack
		}
		var cmd tea.Cmd
		v.vp, cmd = v.vp.Update(msg)
		return cmd
	}
	return nil
}

type limitRow struct {
	name string
	used int64
	max  int64
	pct  float64
}

func (v *limitsView) renderContent() {
	rows := make([]limitRow, 0, len(v.limits))
	for name, l := range v.limits {
		if l.Max == 0 {
			continue
		}
		used := l.Max - l.Remaining
		rows = append(rows, limitRow{name: name, used: used, max: l.Max, pct: float64(used) / float64(l.Max)})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].pct != rows[j].pct {
			return rows[i].pct > rows[j].pct
		}
		return rows[i].name < rows[j].name
	})

	nameW := 0
	for _, r := range rows {
		if len(r.name) > nameW {
			nameW = len(r.name)
		}
	}

	var b strings.Builder
	for _, r := range rows {
		bar := usageBar(r.pct, 24)
		line := fmt.Sprintf("%s %s %5.1f%%  %s", padRight(r.name, nameW+1), bar, r.pct*100, styleDim.Render(fmt.Sprintf("%d / %d", r.used, r.max)))
		b.WriteString(line + "\n")
	}
	if len(rows) == 0 {
		b.WriteString(styleDim.Render("no limits reported"))
	}
	v.vp.SetContent(b.String())
	v.ready = true
}

func usageBar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	filled := int(pct*float64(width) + 0.5)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	switch {
	case pct >= 0.9:
		return styleErrText.Render(bar)
	case pct >= 0.75:
		return styleWarn.Render(bar)
	default:
		return styleOK.Render(bar)
	}
}

func (v *limitsView) View(width, height int) string {
	head := styleTitle.Render("Org limits")
	if v.loading {
		head += "  " + v.app.spin.View() + styleDim.Render(" fetching…")
	} else {
		head += styleDim.Render(fmt.Sprintf("  %d tracked (sorted by usage)", len(v.limits)))
	}
	v.vp.Width = width
	v.vp.Height = height - 1
	if !v.ready {
		return head + "\n"
	}
	return head + "\n" + v.vp.View()
}
