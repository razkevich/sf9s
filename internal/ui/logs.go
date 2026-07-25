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

	inBody     bool
	bodyLines  []string
	lowerLines []string
	bodyID     string
	vp         viewport.Model

	searching  bool
	searchTerm string
	matches    []int
	matchIdx   int

	confirmDelete bool

	tailing  bool
	seen     map[string]bool
	tailGen  int
	newestID string
}

// tailInterval paces the ApexLog poll. The Tooling API offers no streaming
// for debug logs, so tailing means polling; 2s matches `sf apex tail`.
const tailInterval = 2 * time.Second

func newLogsView(app *Model) *logsView {
	v := &logsView{app: app, table: newDataTable(), seen: map[string]bool{}}
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
	case v.tailing:
		return "TAILING • t stop • enter open • / filter"
	default:
		return "enter open • t tail • d delete • R refresh • / filter"
	}
}

func (v *logsView) Capturing() bool {
	return v.table.Filtering() || v.searching || v.confirmDelete || v.inBody
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

// tailTickMsg drives one poll of a tail session; gen identifies the session
// so a stopped or restarted tail can't keep polling.
type tailTickMsg struct{ gen int }

type tailResultMsg struct {
	gen int
	res *api.Result
	err error
}

func (v *logsView) Init() tea.Cmd {
	v.loading = true
	v.gen = v.app.nextGen()
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
	v.gen = v.app.nextGen()
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
	v.gen = v.app.nextGen()
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
		v.rememberIDs(msg.res)
		return nil

	case tailTickMsg:
		if !v.tailing || msg.gen != v.tailGen {
			return nil
		}
		return v.pollTail(msg.gen)

	case tailResultMsg:
		if !v.tailing || msg.gen != v.tailGen {
			return nil
		}
		if msg.err != nil {
			v.tailing = false
			return toast(statusError, "tail stopped: "+msg.err.Error())
		}
		fresh := v.mergeTail(msg.res)
		cmds := []tea.Cmd{v.scheduleTail(msg.gen)}
		if fresh > 0 {
			cmds = append(cmds, toast(statusOK, fmt.Sprintf("%d new log(s)", fresh)))
		}
		return tea.Batch(cmds...)

	case logBodyMsg:
		if msg.gen != v.gen {
			return nil
		}
		v.loading = false
		if msg.err != nil {
			return toastErr(msg.err)
		}
		v.bodyID = msg.id
		body := sanitizeText(msg.body)
		v.bodyLines = strings.Split(body, "\n")
		v.lowerLines = make([]string, len(v.bodyLines))
		for i, line := range v.bodyLines {
			v.lowerLines[i] = strings.ToLower(line)
		}
		v.inBody = true
		v.searchTerm = ""
		v.matches = nil
		v.vp = viewport.New(v.app.width, v.app.height-4)
		v.vp.SetContent(body)
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
	case "t":
		return v.toggleTail()
	case "R":
		v.stopTail()
		return v.Init()
	case "esc":
		v.stopTail()
		return goBack
	}
	return nil
}

// toggleTail starts or stops polling for new debug logs.
func (v *logsView) toggleTail() tea.Cmd {
	if v.tailing {
		v.stopTail()
		return toast(statusInfo, "tail stopped")
	}
	if v.app.client == nil {
		return toast(statusWarn, "select an org first")
	}
	v.tailing = true
	v.tailGen = v.app.nextGen()
	return tea.Batch(
		toast(statusOK, "tailing apex logs — press t to stop"),
		v.pollTail(v.tailGen),
	)
}

// stopTail ends the session; in-flight polls are discarded by generation.
func (v *logsView) stopTail() {
	v.tailing = false
	v.tailGen = 0
}

func (v *logsView) scheduleTail(gen int) tea.Cmd {
	return tea.Tick(tailInterval, func(time.Time) tea.Msg { return tailTickMsg{gen: gen} })
}

func (v *logsView) pollTail(gen int) tea.Cmd {
	client := v.app.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := client.Query(ctx, logsSOQL, true)
		return tailResultMsg{gen: gen, res: res, err: err}
	}
}

// rememberIDs marks every log currently listed as already seen, so a tail
// started later only flags what arrives after it.
func (v *logsView) rememberIDs(res *api.Result) {
	for _, id := range idsOf(res) {
		v.seen[id] = true
	}
}

func idsOf(res *api.Result) []string {
	if res == nil {
		return nil
	}
	col := -1
	for i, c := range res.Columns {
		if c == "Id" {
			col = i
			break
		}
	}
	if col < 0 {
		return nil
	}
	ids := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		if col < len(row) {
			ids = append(ids, row[col])
		}
	}
	return ids
}

// mergeTail prepends logs not seen before and returns how many were new.
func (v *logsView) mergeTail(res *api.Result) int {
	if res == nil || len(res.Rows) == 0 {
		return 0
	}
	ids := idsOf(res)
	var freshRows [][]string
	for i, row := range res.Rows {
		if i < len(ids) && v.seen[ids[i]] {
			continue
		}
		if i < len(ids) {
			v.seen[ids[i]] = true
		}
		freshRows = append(freshRows, row)
	}
	if len(freshRows) == 0 {
		return 0
	}
	if v.result == nil {
		v.result = res
		v.table.SetData(res.Columns, res.Rows)
		return len(freshRows)
	}
	// Newest first: the fresh rows go on top of what is already listed.
	combined := append(append([][]string{}, freshRows...), v.result.Rows...)
	v.result = &api.Result{
		TotalSize: res.TotalSize,
		Done:      res.Done,
		Columns:   res.Columns,
		Rows:      combined,
	}
	v.table.SetDataPreservingView(res.Columns, combined)
	v.newestID = ids[0]
	return len(freshRows)
}

func (v *logsView) bodyKey(msg tea.KeyMsg) tea.Cmd {
	if v.searching {
		switch msg.Type {
		case tea.KeyEsc:
			v.searching = false
			v.searchTerm = ""
			v.matches = nil
			v.matchIdx = -1
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
	for i, line := range v.lowerLines {
		if strings.Contains(line, needle) {
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
	if v.tailing {
		head += "  " + v.app.spin.View() + styleOK.Render(" tailing")
	}
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
