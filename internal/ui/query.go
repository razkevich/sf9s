package ui

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/razkevich/sf9s/internal/api"
	"github.com/razkevich/sf9s/internal/config"
)

const editorHeight = 5

type queryView struct {
	app *Model

	editor       textarea.Model
	table        *dataTable
	focusResults bool

	tooling bool
	running bool
	gen     int

	result  *api.Result
	allRows [][]string
	elapsed time.Duration
	fetched int

	history []string
	histIdx int
	draft   string

	saved      []config.SavedQuery
	picker     *dataTable
	showPicker bool

	card *detailCard
}

func newQueryView(app *Model) *queryView {
	ed := textarea.New()
	ed.Placeholder = "SELECT Id, Name FROM Account ORDER BY LastModifiedDate DESC LIMIT 50"
	ed.SetHeight(editorHeight)
	ed.CharLimit = 20000
	ed.Focus()
	v := &queryView{
		app:     app,
		editor:  ed,
		table:   newDataTable(),
		histIdx: -1,
	}
	v.table.emptyText = "no results yet — write a query and hit ctrl+r"
	v.history = app.deps.Store.History()
	return v
}

func (v *queryView) Title() string { return "query" }

func (v *queryView) Hints() string {
	api := "REST"
	if v.tooling {
		api = "Tooling"
	}
	if v.focusResults {
		return fmt.Sprintf("[%s] tab edit • enter card • m more • e/E export • ctrl+r rerun", api)
	}
	return fmt.Sprintf("[%s] ctrl+r run • ctrl+t tooling • ctrl+p/n history • ctrl+s saved • tab results", api)
}

func (v *queryView) Capturing() bool {
	return !v.focusResults || v.showPicker || v.card != nil || v.table.Filtering()
}

func (v *queryView) Init() tea.Cmd { return textarea.Blink }

func (v *queryView) resetOrg() {
	v.gen = v.app.nextGen()
	v.running = false
	v.result = nil
	v.allRows = nil
	v.fetched = 0
	v.table.SetData(nil, nil)
	v.table.emptyText = "no results yet — write a query and hit ctrl+r"
}

func (v *queryView) setEditorText(s string) {
	v.editor.SetValue(s)
	v.focusResults = false
	v.editor.Focus()
}

type queryDoneMsg struct {
	gen     int
	res     *api.Result
	err     error
	elapsed time.Duration
	more    bool
	history []string
}

func (v *queryView) runQuery() tea.Cmd {
	soql := strings.TrimSpace(v.editor.Value())
	if soql == "" {
		return toast(statusWarn, "nothing to run")
	}
	if v.app.client == nil {
		return toast(statusWarn, "select an org first")
	}
	v.gen = v.app.nextGen()
	gen := v.gen
	v.running = true
	client := v.app.client
	tooling := v.tooling
	store := v.app.deps.Store
	v.histIdx = -1
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		start := time.Now()
		res, err := client.Query(ctx, soql, tooling)
		hist, _ := store.AppendHistory(soql)
		return queryDoneMsg{gen: gen, res: res, err: err, elapsed: time.Since(start), history: hist}
	}
}

// maxAccumulatedRows bounds how much of a large result set sf9s holds in
// memory; past this the user should narrow the query.
const maxAccumulatedRows = 50000

func (v *queryView) fetchMore() tea.Cmd {
	if v.result == nil || v.result.NextRecordsURL == "" || v.running {
		return nil
	}
	if len(v.allRows) >= maxAccumulatedRows {
		return toast(statusWarn, fmt.Sprintf("row limit reached (%d) — narrow the query with WHERE or LIMIT", maxAccumulatedRows))
	}
	v.gen = v.app.nextGen()
	gen := v.gen
	v.running = true
	client := v.app.client
	next := v.result.NextRecordsURL
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		start := time.Now()
		res, err := client.QueryMore(ctx, next)
		return queryDoneMsg{gen: gen, res: res, err: err, elapsed: time.Since(start), more: true}
	}
}

func (v *queryView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case queryDoneMsg:
		if msg.gen != v.gen {
			return nil
		}
		v.running = false
		if msg.history != nil {
			v.history = msg.history
		}
		if msg.err != nil {
			return toastErr(msg.err)
		}
		v.elapsed = msg.elapsed
		if msg.more {
			added := len(msg.res.Rows)
			sameSchema := len(msg.res.Columns) == len(v.result.Columns)
			for i := 0; sameSchema && i < len(msg.res.Columns); i++ {
				sameSchema = msg.res.Columns[i] == v.result.Columns[i]
			}
			merged := api.Merge(v.result, msg.res)
			v.result = merged
			v.allRows = merged.Rows
			v.fetched = len(merged.Rows)
			if sameSchema {
				v.table.AppendRows(msg.res.Rows)
			} else {
				// A later batch surfaced columns the first batch flattened
				// away (null relationships); re-align the whole table.
				v.table.SetData(merged.Columns, merged.Rows)
			}
			return toast(statusOK, fmt.Sprintf("fetched %d more rows (%d/%d)", added, v.fetched, v.result.TotalSize))
		}
		v.result = msg.res
		v.allRows = msg.res.Rows
		v.fetched = len(msg.res.Rows)
		v.table.SetData(msg.res.Columns, msg.res.Rows)
		v.table.emptyText = fmt.Sprintf("0 rows (totalSize %d)", msg.res.TotalSize)
		v.focusResults = len(msg.res.Rows) > 0
		if v.focusResults {
			v.editor.Blur()
		}
		return toast(statusOK, fmt.Sprintf("%d/%d rows in %s", v.fetched, msg.res.TotalSize, msg.elapsed.Round(time.Millisecond)))

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	if !v.focusResults {
		var cmd tea.Cmd
		v.editor, cmd = v.editor.Update(msg)
		return cmd
	}
	return nil
}

func (v *queryView) handleKey(msg tea.KeyMsg) tea.Cmd {
	if v.card != nil {
		if !v.card.Update(msg) {
			v.card = nil
		}
		return nil
	}
	if v.showPicker {
		return v.pickerKey(msg)
	}

	switch msg.String() {
	case "ctrl+r":
		return v.runQuery()
	case "ctrl+t":
		v.tooling = !v.tooling
		mode := "REST API"
		if v.tooling {
			mode = "Tooling API"
		}
		return toast(statusInfo, "queries now run against the "+mode)
	case "ctrl+s":
		return v.openPicker()
	case "tab":
		v.focusResults = !v.focusResults
		if v.focusResults {
			v.editor.Blur()
		} else {
			v.editor.Focus()
		}
		return nil
	}

	if v.focusResults {
		if v.table.Update(msg) {
			return nil
		}
		switch msg.String() {
		case "enter":
			if row := v.table.CurrentRow(); row != nil {
				v.card = newDetailCard("record", v.result.Columns, row, v.app.width, v.app.height-2)
			}
			return nil
		case "m":
			return v.fetchMore()
		case "e":
			return v.export("csv")
		case "E":
			return v.export("json")
		case "esc":
			return goBack
		}
		return nil
	}

	switch msg.String() {
	case "ctrl+p":
		return v.histMove(1)
	case "ctrl+n":
		return v.histMove(-1)
	case "esc":
		if len(v.allRows) > 0 {
			v.focusResults = true
			v.editor.Blur()
			return nil
		}
		return goBack
	}
	var cmd tea.Cmd
	v.editor, cmd = v.editor.Update(msg)
	return cmd
}

func (v *queryView) histMove(dir int) tea.Cmd {
	if len(v.history) == 0 {
		return toast(statusInfo, "history is empty")
	}
	if v.histIdx == -1 {
		v.draft = v.editor.Value()
	}
	next := v.histIdx + dir
	if next < -1 {
		next = -1
	}
	if next >= len(v.history) {
		next = len(v.history) - 1
	}
	v.histIdx = next
	if next == -1 {
		v.editor.SetValue(v.draft)
	} else {
		v.editor.SetValue(v.history[next])
	}
	v.editor.CursorEnd()
	return nil
}

func (v *queryView) openPicker() tea.Cmd {
	saved, err := v.app.deps.Store.SavedQueries()
	if err != nil {
		return toastErr(err)
	}
	if len(saved) == 0 {
		return toast(statusInfo, "no saved queries — add some to queries.yaml")
	}
	v.saved = saved
	v.picker = newDataTable()
	rows := make([][]string, len(saved))
	for i, q := range saved {
		mode := ""
		if q.Tooling {
			mode = "tooling"
		}
		rows[i] = []string{q.Name, mode, q.Query}
	}
	v.picker.SetData([]string{"Name", "API", "Query"}, rows)
	v.showPicker = true
	return nil
}

func (v *queryView) pickerKey(msg tea.KeyMsg) tea.Cmd {
	if v.picker.Update(msg) {
		return nil
	}
	switch msg.String() {
	case "esc":
		v.showPicker = false
	case "enter":
		if row := v.picker.CurrentRow(); row != nil {
			for _, q := range v.saved {
				if q.Name == row[0] {
					v.editor.SetValue(q.Query)
					v.tooling = q.Tooling
					break
				}
			}
			v.showPicker = false
			v.focusResults = false
			v.editor.Focus()
			return v.runQuery()
		}
	}
	return nil
}

// csvSafe neutralizes leading characters that spreadsheet apps execute as
// formulas, so exported org data can't run on open.
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

func (v *queryView) export(format string) tea.Cmd {
	if v.result == nil || len(v.allRows) == 0 {
		return toast(statusWarn, "nothing to export")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return toastErr(err)
	}
	name := filepath.Join(cwd, fmt.Sprintf("sf9s-export-%s.%s", time.Now().Format("20060102-150405"), format))
	cols := v.result.Columns
	rows := make([][]string, len(v.allRows))
	for i, row := range v.allRows {
		clean := make([]string, len(row))
		for j, cell := range row {
			clean[j] = sanitizeCell(cell)
		}
		rows[i] = clean
	}
	return func() tea.Msg {
		// O_EXCL so a pre-planted symlink or file can never be followed.
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return statusMsg{kind: statusError, text: err.Error()}
		}
		write := func() error {
			switch format {
			case "csv":
				w := csv.NewWriter(f)
				if err := w.Write(cols); err != nil {
					return err
				}
				safe := make([]string, len(cols))
				for _, row := range rows {
					for j := range safe {
						if j < len(row) {
							safe[j] = csvSafe(row[j])
						} else {
							safe[j] = ""
						}
					}
					if err := w.Write(safe); err != nil {
						return err
					}
				}
				w.Flush()
				return w.Error()
			default:
				out := make([]map[string]string, len(rows))
				for i, row := range rows {
					rec := map[string]string{}
					for j, col := range cols {
						if j < len(row) {
							rec[col] = row[j]
						}
					}
					out[i] = rec
				}
				enc := json.NewEncoder(f)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
		}
		if err := write(); err != nil {
			_ = f.Close()
			return statusMsg{kind: statusError, text: err.Error()}
		}
		if err := f.Close(); err != nil {
			return statusMsg{kind: statusError, text: err.Error()}
		}
		return statusMsg{kind: statusOK, text: "exported " + name}
	}
}

func (v *queryView) View(width, height int) string {
	v.editor.SetWidth(width - 4)
	if v.card != nil {
		return v.card.View(width, height)
	}
	if v.showPicker {
		v.picker.SetSize(width-4, height-3)
		return styleTitle.Render("Saved queries") + " " + styleDim.Render("(enter runs, esc closes)") + "\n" + v.picker.View()
	}

	head := styleTitle.Render("SOQL")
	if v.tooling {
		head += " " + styleWarn.Render("[tooling]")
	}
	if v.running {
		head += "  " + v.app.spin.View() + styleDim.Render(" running…")
	}
	if v.histIdx >= 0 {
		head += styleDim.Render(fmt.Sprintf("  history %d/%d", v.histIdx+1, len(v.history)))
	}

	resultHead := ""
	if v.result != nil {
		resultHead = styleDim.Render(fmt.Sprintf("%d/%d rows • %s", v.fetched, v.result.TotalSize, v.elapsed.Round(time.Millisecond)))
		if v.result.NextRecordsURL != "" {
			resultHead += styleDim.Render(" • m = more")
		}
	}

	// Shrink the editor before sacrificing result rows on short terminals:
	// head(1) + editor(edH+2) + resultHead(1) + table(>=3) must fit height.
	edH := min(editorHeight, max(1, height-7))
	v.editor.SetHeight(edH)
	editorBox := styleEditorBlurred
	if !v.focusResults {
		editorBox = styleEditorFocused
	}
	editor := editorBox.Width(width - 2).Render(v.editor.View())

	tableH := max(height-edH-4, 3)
	v.table.SetSize(width, tableH)
	return head + "\n" + editor + "\n" + resultHead + "\n" + v.table.View()
}
