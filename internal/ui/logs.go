package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/razkevich/sf9s/internal/api"
)

const logsSOQL = `SELECT Id, LogUser.Name, Operation, Status, LogLength, StartTime, DurationMilliseconds, Request, Application
FROM ApexLog ORDER BY StartTime DESC LIMIT 100`

type logsView struct {
	app     *Model
	table   *dataTable
	result  *api.Result
	loading bool
	gen     int

	inBody    bool
	body      string
	bodyLines []string
	bodyID    string
	vp        viewport.Model

	searching  bool
	searchTerm string
	matches    []int
	matchIdx   int

	confirmDelete bool
}

func newLogsView(app *Model) *logsView {
	v := &logsView{app: app, table: newDataTable()}
	v.table.emptyText = "no apex logs — enable debug logging in Setup or via sf apex log"
	v.table.SetCellStyle("Status", statusCellStyle)
	return v
}

func (v *logsView) Title() string { return "logs" }

func (v *logsView) Hints() string {
	switch {
	case v.confirmDelete:
		return "delete this log? y / n"
	case v.inBody:
		return "/ search • n/N next/prev match • esc back"
	default:
		return "enter open • d delete • R refresh • / filter"
	}
}

func (v *logsView) Capturing() bool {
	return v.table.Filtering() || v.searching || v.confirmDelete
}

type logsListMsg struct {
	gen int
	res *api.Result
	err error
}

type logBodyMsg struct {
	gen  int
	id   string
	body string
	err  error
}

type logDeletedMsg struct {
	gen int
	err error
}

func (v *logsView) Init() tea.Cmd {
	v.loading = true
	v.gen++
	gen := v.gen
	client := v.app.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := client.Query(ctx, logsSOQL, true)
		return logsListMsg{gen: gen, res: res, err: err}
	}
}

func (v *logsView) openBody(id string) tea.Cmd {
	v.loading = true
	v.gen++
	gen := v.gen
	client := v.app.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		body, err := client.ApexLogBody(ctx, id)
		return logBodyMsg{gen: gen, id: id, body: body, err: err}
	}
}

func (v *logsView) deleteLog(id string) tea.Cmd {
	v.loading = true
	v.gen++
	gen := v.gen
	client := v.app.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		err := client.DeleteApexLog(ctx, id)
		return logDeletedMsg{gen: gen, err: err}
	}
}

func (v *logsView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case logsListMsg:
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

	case logBodyMsg:
		if msg.gen != v.gen {
			return nil
		}
		v.loading = false
		if msg.err != nil {
			return toastErr(msg.err)
		}
		v.body = msg.body
		v.bodyID = msg.id
		v.bodyLines = strings.Split(msg.body, "\n")
		v.inBody = true
		v.searchTerm = ""
		v.matches = nil
		v.vp = viewport.New(v.app.width, v.app.height-4)
		v.vp.SetContent(v.body)
		return nil

	case logDeletedMsg:
		if msg.gen != v.gen {
			return nil
		}
		v.loading = false
		if msg.err != nil {
			return toastErr(msg.err)
		}
		return tea.Batch(toast(statusOK, "log deleted"), v.Init())

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *logsView) handleKey(msg tea.KeyMsg) tea.Cmd {
	if v.confirmDelete {
		v.confirmDelete = false
		if msg.String() == "y" {
			if id := v.table.Cell("Id"); id != "" {
				return v.deleteLog(id)
			}
		}
		return toast(statusInfo, "delete cancelled")
	}

	if v.inBody {
		return v.bodyKey(msg)
	}

	if v.table.Update(msg) {
		return nil
	}
	switch msg.String() {
	case "enter":
		if id := v.table.Cell("Id"); id != "" && !v.loading {
			return v.openBody(id)
		}
	case "d":
		if v.table.CurrentRow() != nil {
			v.confirmDelete = true
		}
	case "R":
		return v.Init()
	case "esc":
		return goBack
	}
	return nil
}

func (v *logsView) bodyKey(msg tea.KeyMsg) tea.Cmd {
	if v.searching {
		switch msg.Type {
		case tea.KeyEsc:
			v.searching = false
			v.searchTerm = ""
		case tea.KeyEnter:
			v.searching = false
			return v.runSearch()
		case tea.KeyBackspace:
			if r := []rune(v.searchTerm); len(r) > 0 {
				v.searchTerm = string(r[:len(r)-1])
			}
		case tea.KeyRunes, tea.KeySpace:
			v.searchTerm += string(msg.Runes)
		}
		return nil
	}
	switch msg.String() {
	case "esc", "q":
		v.inBody = false
		return nil
	case "/":
		v.searching = true
		v.searchTerm = ""
		return nil
	case "n":
		return v.jumpMatch(1)
	case "N":
		return v.jumpMatch(-1)
	}
	var cmd tea.Cmd
	v.vp, cmd = v.vp.Update(msg)
	return cmd
}

func (v *logsView) runSearch() tea.Cmd {
	v.matches = v.matches[:0]
	needle := strings.ToLower(v.searchTerm)
	if needle == "" {
		return nil
	}
	for i, line := range v.bodyLines {
		if strings.Contains(strings.ToLower(line), needle) {
			v.matches = append(v.matches, i)
		}
	}
	v.matchIdx = -1
	return v.jumpMatch(1)
}

func (v *logsView) jumpMatch(dir int) tea.Cmd {
	if len(v.matches) == 0 {
		return toast(statusInfo, "no matches")
	}
	v.matchIdx = (v.matchIdx + dir + len(v.matches)) % len(v.matches)
	v.vp.SetYOffset(max(v.matches[v.matchIdx]-2, 0))
	return toast(statusInfo, fmt.Sprintf("match %d/%d (line %d)", v.matchIdx+1, len(v.matches), v.matches[v.matchIdx]+1))
}

func (v *logsView) View(width, height int) string {
	if v.inBody {
		head := styleTitle.Render("Apex log "+v.bodyID) +
			styleDim.Render(fmt.Sprintf("  %d lines", len(v.bodyLines)))
		if v.searching {
			head += "  " + styleWarn.Render("search: "+v.searchTerm+"▌")
		} else if v.searchTerm != "" {
			head += styleDim.Render(fmt.Sprintf("  /%s (%d matches)", v.searchTerm, len(v.matches)))
		}
		v.vp.Width = width
		v.vp.Height = height - 1
		return head + "\n" + v.vp.View()
	}

	head := styleTitle.Render("Apex debug logs")
	if v.loading {
		head += "  " + v.app.spin.View() + styleDim.Render(" loading…")
	} else if v.result != nil {
		head += styleDim.Render(fmt.Sprintf("  %d", v.result.TotalSize))
	}
	if v.confirmDelete {
		head += "  " + styleErrText.Render("delete selected log? y/n")
	}
	v.table.SetSize(width, height-1)
	return head + "\n" + v.table.View()
}
