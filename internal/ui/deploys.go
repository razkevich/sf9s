package ui

import (
	"context"
	"fmt"
	"strings"
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

	// failTable holds the components (and Apex tests) of one deployment that
	// the org rejected. The list alone only answers "did it fail", which is
	// the one thing the operator already knew.
	failTable *dataTable
	inFails   bool
	failID    string
	details   *api.DeployDetails
	// failures maps a rendered row back to everything the org said about it,
	// so the card can show text the table has to truncate.
	failures map[string]deployFailure
}

// deployFailure flattens a component failure and an Apex test failure into
// one shape, so both read as rows of the same table.
type deployFailure struct {
	kind        string
	name        string
	location    string
	problem     string
	file        string
	problemType string
	stack       string
}

func newDeploysView(app *Model) *deploysView {
	v := &deploysView{app: app, table: newDataTable(), failTable: newDataTable()}
	v.table.emptyText = "no deployments found"
	v.table.SetCellStyle("Status", statusCellStyle)
	v.failTable.emptyText = "no component failures reported"
	return v
}

func (v *deploysView) Title() string { return "deploys" }
func (v *deploysView) Keys() []keyHint {
	switch {
	case v.card != nil:
		return []keyHint{{"esc", "close"}, {"↑↓", "scroll"}}
	case v.inFails:
		return []keyHint{{"enter", "full problem text"}, {"s", "sort column"}, {"esc", "back"}, {"/", "filter"}}
	default:
		return []keyHint{{"enter", "failures"}, {"s", "sort column"}, {"R", "refresh"}, {"/", "filter"}}
	}
}

func (v *deploysView) Bail() bool {
	switch {
	case v.card != nil:
		v.card = nil
	case v.inFails && v.failTable.ClearFilter():
	case v.inFails:
		v.inFails = false
	case v.table.ClearFilter():
	default:
		return false
	}
	return true
}

func (v *deploysView) Capturing() bool {
	return v.table.Filtering() || v.failTable.Filtering() || v.card != nil
}

type deploysMsg struct {
	gen int
	res *api.Result
	err error
}

type deployDetailsMsg struct {
	gen int
	id  string
	// row is the list row the fetch was started from; the cursor may have
	// moved on by the time the org answers.
	row     []string
	details *api.DeployDetails
	err     error
}

func (v *deploysView) Init() tea.Cmd {
	v.loading = true
	v.inFails = false
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

func (v *deploysView) fetchDetails(id string, row []string) tea.Cmd {
	v.loading = true
	v.gen = v.app.nextGen()
	gen := v.gen
	client := v.app.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		details, err := client.DeployDetails(ctx, id)
		return deployDetailsMsg{gen: gen, id: id, row: row, details: details, err: err}
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
		v.table.SetData(msg.res.Columns, humanizeTimes(msg.res.Columns, msg.res.Rows,
			map[string]bool{"CreatedDate": true, "StartDate": true, "CompletedDate": true}))
		return nil

	case deployDetailsMsg:
		if msg.gen != v.gen {
			return nil
		}
		v.loading = false
		if msg.err != nil {
			return toastErr(msg.err)
		}
		v.details, v.failID = msg.details, msg.id
		if !msg.details.HasFailures() {
			// Nothing component-level to explain, so the summary is the story.
			if v.result != nil && msg.row != nil {
				v.card = newDetailCard("deployment", v.result.Columns, msg.row, v.app.width, v.app.height-2)
			}
			return nil
		}
		v.setFailures(msg.details)
		v.inFails = true
		return nil

	case tea.KeyMsg:
		if v.card != nil {
			if !v.card.Update(msg) {
				v.card = nil
			}
			return nil
		}
		if v.inFails {
			return v.failuresKey(msg)
		}
		if v.table.Update(msg) {
			return nil
		}
		switch msg.String() {
		case "enter":
			if id := v.table.Cell("Id"); id != "" && !v.loading {
				return v.fetchDetails(id, v.table.CurrentRow())
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

func (v *deploysView) failuresKey(msg tea.KeyMsg) tea.Cmd {
	if v.failTable.Update(msg) {
		return nil
	}
	switch msg.String() {
	case "enter":
		if row := v.failTable.CurrentRow(); row != nil {
			if f, ok := v.failures[failureKey(row)]; ok {
				v.card = f.card(v.app.width, v.app.height-2)
			}
		}
	case "s":
		if label := v.failTable.SortByCursorColumn(); label != "" {
			return toast(statusInfo, "sorted by "+label)
		}
		return toast(statusInfo, "sort cleared")
	case "esc":
		v.inFails = false
	}
	return nil
}

func (v *deploysView) setFailures(d *api.DeployDetails) {
	v.failures = map[string]deployFailure{}
	rows := make([][]string, 0, len(d.ComponentFailures)+len(d.TestFailures))
	add := func(f deployFailure) {
		row := []string{f.kind, f.name, f.location, f.problem}
		v.failures[failureKey(row)] = f
		rows = append(rows, row)
	}
	for _, f := range d.ComponentFailures {
		add(deployFailure{
			kind:        f.ComponentType,
			name:        f.FullName,
			location:    f.Location(),
			problem:     f.Problem,
			file:        f.FileName,
			problemType: f.ProblemType,
		})
	}
	for _, f := range d.TestFailures {
		name := f.Name
		if f.MethodName != "" {
			name += "." + f.MethodName
		}
		add(deployFailure{
			kind:        "ApexTest",
			name:        name,
			problem:     f.Message,
			problemType: "Test failure",
			stack:       f.StackTrace,
		})
	}
	v.failTable.SetData([]string{"Type", "Name", "Line", "Problem"}, rows)
}

// failureKey identifies a row after the table has sanitized its cells, which
// is the only form the cursor ever hands back.
func failureKey(cells []string) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		parts[i] = sanitizeCell(c)
	}
	return strings.Join(parts, "\x00")
}

// card renders one failure in full: the table has to truncate the problem
// text, and that text is the only part worth reading.
func (f deployFailure) card(width, height int) *detailCard {
	var cols, cells []string
	add := func(col, val string) {
		if val == "" {
			return
		}
		cols = append(cols, col)
		cells = append(cells, val)
	}
	add("Type", f.kind)
	add("Name", f.name)
	add("File", f.file)
	add("Line", f.location)
	add("Problem type", f.problemType)
	add("Problem", f.problem)
	add("Stack trace", f.stack)
	return newDetailCard("failure", cols, cells, width, height)
}

// failureSummary reports the counts the org itself gave, falling back to the
// rows it sent: a check-only run can list failures while reporting no errors.
func failureSummary(d *api.DeployDetails) string {
	comps := d.NumberComponentErrors
	if comps == 0 {
		comps = len(d.ComponentFailures)
	}
	tests := d.NumberTestErrors
	if tests == 0 {
		tests = len(d.TestFailures)
	}
	parts := []string{fmt.Sprintf("%d component error(s)", comps)}
	if tests > 0 {
		parts = append(parts, fmt.Sprintf("%d test failure(s)", tests))
	}
	if d.Status != "" {
		parts = append(parts, d.Status)
	}
	return strings.Join(parts, " · ")
}

func (v *deploysView) View(width, height int) string {
	if v.card != nil {
		return v.card.View(width, height)
	}
	if v.inFails && v.details != nil {
		head := styleTitle.Render("Deployment "+v.failID) +
			styleDim.Render("  "+failureSummary(v.details))
		v.failTable.SetSize(width, height-1)
		return head + "\n" + v.failTable.View()
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
