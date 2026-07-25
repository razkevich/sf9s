package ui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/razkevich/sf9s/internal/api"
)

const deploysSOQL = `SELECT Id, Status, CreatedBy.Name, CreatedDate, StartDate, CompletedDate, CheckOnly,
NumberComponentsDeployed, NumberComponentsTotal, NumberComponentErrors,
NumberTestsCompleted, NumberTestsTotal, NumberTestErrors, ErrorMessage
FROM DeployRequest ORDER BY CreatedDate DESC LIMIT 50`

type deploysView struct {
	app     *Model
	table   *dataTable
	result  *api.Result
	loading bool
	gen     int
	card    *detailCard
}

func newDeploysView(app *Model) *deploysView {
	v := &deploysView{app: app, table: newDataTable()}
	v.table.emptyText = "no deployments found"
	v.table.SetCellStyle("Status", statusCellStyle)
	return v
}

func (v *deploysView) Title() string { return "deploys" }
func (v *deploysView) Keys() []keyHint {
	return []keyHint{{"enter", "inspect"}, {"s", "sort column"}, {"R", "refresh"}, {"/", "filter"}}
}
func (v *deploysView) Bail() bool {
	switch {
	case v.card != nil:
		v.card = nil
	case v.table.ClearFilter():
	default:
		return false
	}
	return true
}

func (v *deploysView) Capturing() bool { return v.table.Filtering() || v.card != nil }

type deploysMsg struct {
	gen int
	res *api.Result
	err error
}

func (v *deploysView) Init() tea.Cmd {
	v.loading = true
	v.gen = v.app.nextGen()
	gen := v.gen
	client := v.app.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := client.Query(ctx, deploysSOQL, true)
		return deploysMsg{gen: gen, res: res, err: err}
	}
}

func (v *deploysView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case deploysMsg:
		if msg.gen != v.gen {
			return nil
		}
		v.loading = false
		if msg.err != nil {
			return toastErr(msg.err)
		}
		v.result = msg.res
		v.table.SetData(msg.res.Columns, msg.res.Rows)
		return nil

	case tea.KeyMsg:
		if v.card != nil {
			if !v.card.Update(msg) {
				v.card = nil
			}
			return nil
		}
		if v.table.Update(msg) {
			return nil
		}
		switch msg.String() {
		case "enter":
			if row := v.table.CurrentRow(); row != nil && v.result != nil {
				v.card = newDetailCard("deployment", v.result.Columns, row, v.app.width, v.app.height-2)
			}
		case "s":
			if label := v.table.SortByCursorColumn(); label != "" {
				return toast(statusInfo, "sorted by "+label)
			}
			return toast(statusInfo, "sort cleared")
		case "R":
			return v.Init()
		case "esc":
			return goBack
		}
	}
	return nil
}

func (v *deploysView) View(width, height int) string {
	if v.card != nil {
		return v.card.View(width, height)
	}
	head := styleTitle.Render("Recent deployments")
	if v.loading {
		head += "  " + v.app.spin.View() + styleDim.Render(" querying…")
	} else if v.result != nil {
		head += styleDim.Render(fmt.Sprintf("  %d", v.result.TotalSize))
	}
	v.table.SetSize(width, height-1)
	return head + "\n" + v.table.View()
}
