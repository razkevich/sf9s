package ui

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/razkevich/sf9s/internal/api"
	"github.com/razkevich/sf9s/internal/config"
	"github.com/razkevich/sf9s/internal/soql"
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

	// exported is the path of the last export, kept on screen because a
	// four-second toast is not enough to find a file by.
	exported string

	// staleResults marks rows that belong to an earlier query than the one
	// now in the editor.
	staleResults bool

	// lastErr is the failure of the most recent run, shown until the next
	// successful one so it cannot be mistaken for an empty result.
	lastErr error

	comp  *completer
	popup *completionPopup
	// pendingComplete remembers an explicit completion request made while
	// the schema was still loading, so it fires once the schema arrives.
	pendingComplete bool
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
	v.comp = newCompleter(app)
	return v
}

func (v *queryView) Title() string { return "query" }

func (v *queryView) Keys() []keyHint {
	if v.popup.open() {
		return []keyHint{{"↑↓", "move"}, {"enter/tab", "accept"}, {"esc", "dismiss"}}
	}
	if v.focusResults {
		return []keyHint{
			{"tab", "back to editor"},
			{"enter", "inspect row"},
			{"y/Y", "copy cell/row"},
			{"s", "sort column"},
			{"o", "open record"},
			{"m", "fetch more"},
			{"e/E", "export CSV/JSON"},
			{"/", "filter"},
		}
	}
	editing := []keyHint{
		{"ctrl+r", "run"},
		{"tab", "complete"},
		{"ctrl+t", "toggle tooling"},
		{"ctrl+p/n", "history"},
		{"ctrl+s", "saved queries"},
		{"ctrl+u", "clear"},
	}
	if v.table.RowCount() > 0 {
		editing = append(editing, keyHint{"shift+tab", "go to results"})
	}
	return editing
}

func (v *queryView) Bail() bool {
	switch {
	case v.popup.open():
		v.popup = nil
	case v.card != nil:
		v.card = nil
	case v.showPicker:
		v.showPicker = false
	case v.table.ClearFilter():
	case v.focusResults:
		// Step back into the editor before leaving the view entirely.
		v.focusResults = false
		v.editor.Focus()
	default:
		return false
	}
	return true
}

func (v *queryView) Capturing() bool {
	return !v.focusResults || v.showPicker || v.card != nil || v.table.Filtering() || v.popup.open()
}

func (v *queryView) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, v.comp.warm())
}

func (v *queryView) resetOrg() {
	v.popup = nil
	v.comp.reset()
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
	// The rows on screen belong to the previous query; say so rather than
	// let them read as this one's.
	v.staleResults = v.table.RowCount() > 0
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
	stmt := strings.TrimSpace(v.editor.Value())
	if stmt == "" {
		return toast(statusWarn, "nothing to run")
	}
	if v.app.client == nil {
		return toast(statusWarn, "select an org first")
	}
	v.gen = v.app.nextGen()
	gen := v.gen
	v.running = true
	v.lastErr = nil
	v.exported = ""
	v.staleResults = false
	client := v.app.client
	tooling := v.tooling
	store := v.app.deps.Store
	v.histIdx = -1
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		start := time.Now()
		res, err := client.Query(ctx, stmt, tooling)
		hist, _ := store.AppendHistory(stmt)
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
			v.lastErr = msg.err
			return toastErr(msg.err)
		}
		v.lastErr = nil
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
		// Focus stays in the editor: silently moving it to the table turns a
		// half-typed next query into a stream of single-key commands.
		return toast(statusOK, fmt.Sprintf("%d/%d rows in %s", v.fetched, msg.res.TotalSize, msg.elapsed.Round(time.Millisecond)))

	case exportedMsg:
		// A toast fades in seconds and people miss where the file went, so
		// keep the path on screen until the next run.
		v.exported = msg.path
		return toast(statusOK, "exported "+msg.path)

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	if mine, err := v.comp.Update(msg); mine {
		pending := v.pendingComplete
		v.pendingComplete = false
		if err != nil {
			if pending {
				return toast(statusError, "schema unavailable: "+err.Error())
			}
			return nil
		}
		// Schema arrived: honor a request that couldn't be served yet, and
		// keep an open popup filling in live.
		if pending {
			return v.openPopup(true)
		}
		return v.refreshPopup()
	}
	if !v.focusResults {
		var cmd tea.Cmd
		v.editor, cmd = v.editor.Update(msg)
		return cmd
	}
	return nil
}

// cursorOffset converts the textarea's caret into a byte offset in the full
// editor text. LineInfo is relative to the *visual* (soft-wrapped) row and
// CharOffset counts display width, so the logical rune column is
// StartColumn + ColumnOffset — using CharOffset directly breaks on wrapped
// lines and on double-width runes.
func (v *queryView) cursorOffset() int {
	text := v.editor.Value()
	lines := strings.Split(text, "\n")
	row := clampInt(v.editor.Line(), 0, len(lines)-1)
	offset := 0
	for i := 0; i < row; i++ {
		offset += len(lines[i]) + 1
	}
	li := v.editor.LineInfo()
	runes := []rune(lines[row])
	col := clampInt(li.StartColumn+li.ColumnOffset, 0, len(runes))
	offset += len(string(runes[:col]))
	return min(offset, len(text))
}

// rowColFor maps a byte offset to a logical row and rune column.
func rowColFor(text string, offset int) (int, int) {
	offset = clampInt(offset, 0, len(text))
	before := text[:offset]
	row := strings.Count(before, "\n")
	lineStart := strings.LastIndexByte(before, '\n') + 1
	return row, len([]rune(before[lineStart:]))
}

// wordBeforeCursor reports whether the character left of the cursor is part
// of an identifier, i.e. there is a prefix worth completing.
func (v *queryView) wordBeforeCursor() bool {
	text := v.editor.Value()
	offset := v.cursorOffset()
	if offset == 0 || offset > len(text) {
		return false
	}
	c := text[offset-1]
	return c == '_' || c == '.' || c == '$' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// openPopup computes candidates at the cursor. explicit distinguishes a user
// request (ctrl+space, which reports why nothing matched) from the implicit
// refresh after each keystroke.
func (v *queryView) openPopup(explicit bool) tea.Cmd {
	text := v.editor.Value()
	cursor := v.cursorOffset()
	items, sctx, cmd := v.comp.Candidates(text, cursor)
	if len(items) == 0 {
		v.popup = nil
		// A request that arrives while the schema is still downloading is
		// remembered, not refused.
		if explicit && (cmd != nil || v.comp.loading()) {
			v.pendingComplete = true
			if cmd == nil {
				return toast(statusInfo, "loading schema…")
			}
			return tea.Batch(cmd, toast(statusInfo, "loading schema…"))
		}
		if explicit {
			switch {
			case sctx.Clause == soql.ClauseUnknown:
				return toast(statusInfo, "no completions here — try inside SELECT, after FROM, or in WHERE")
			case sctx.Object == "":
				return toast(statusInfo, "type the FROM object first, then fields complete")
			default:
				return toast(statusInfo, "no match for "+sctx.Prefix)
			}
		}
		return cmd
	}
	_, partial := sctx.RelationshipPath()
	v.popup = &completionPopup{
		items:   items,
		replace: [2]int{cursor - len(partial), cursor},
	}
	if len(items) == 1 && explicit {
		return tea.Batch(cmd, v.acceptCompletion())
	}
	return cmd
}

// refreshPopup recomputes an open popup in place, closing it when the cursor
// moves somewhere with nothing to offer.
func (v *queryView) refreshPopup() tea.Cmd {
	if v.popup == nil {
		return nil
	}
	return v.openPopup(false)
}

// acceptCompletion inserts the highlighted candidate over the typed prefix.
func (v *queryView) acceptCompletion() tea.Cmd {
	if !v.popup.open() {
		return nil
	}
	candidate := v.popup.selected()
	text := v.editor.Value()
	start, end := v.popup.replace[0], v.popup.replace[1]
	if start < 0 || end > len(text) || start > end {
		v.popup = nil
		return nil
	}
	updated := text[:start] + candidate.Text + text[end:]
	caret := start + len(candidate.Text)
	v.popup = nil
	v.setEditorTextWithCursor(updated, caret)
	// A relationship candidate ("Owner.") continues the path — offer the
	// target object's fields immediately.
	if strings.HasSuffix(candidate.Text, ".") {
		return v.openPopup(false)
	}
	return nil
}

// setEditorTextWithCursor replaces the buffer and places the caret at a byte
// offset. The textarea exposes no row setter, and SetValue leaves the caret on
// the last row, so walk to the target row (CursorUp/Down step through visual
// rows, hence the loop on Line()) and then set the logical column directly.
func (v *queryView) setEditorTextWithCursor(text string, offset int) {
	v.editor.SetValue(text)
	row, col := rowColFor(text, offset)
	for guard := 0; v.editor.Line() > row && guard <= len(text); guard++ {
		v.editor.CursorUp()
	}
	for guard := 0; v.editor.Line() < row && guard <= len(text); guard++ {
		v.editor.CursorDown()
	}
	v.editor.SetCursor(col)
}

func (v *queryView) handleKey(msg tea.KeyMsg) tea.Cmd {
	if v.card != nil {
		if msg.String() == "y" {
			raw, err := v.card.JSON()
			if err != nil {
				return toastErr(err)
			}
			if err := v.app.deps.Clipboard(raw); err != nil {
				return toastErr(err)
			}
			return toast(statusOK, "copied record as JSON")
		}
		if !v.card.Update(msg) {
			v.card = nil
		}
		return nil
	}
	if v.showPicker {
		return v.pickerKey(msg)
	}

	// The completion popup owns navigation keys while it is open.
	if v.popup.open() {
		switch msg.String() {
		case "up", "ctrl+p":
			v.popup.move(-1)
			return nil
		case "down", "ctrl+n":
			v.popup.move(1)
			return nil
		case "enter", "tab":
			return v.acceptCompletion()
		case "esc":
			v.popup = nil
			return nil
		}
	}

	switch msg.String() {
	case "ctrl+space", "ctrl+@":
		if !v.focusResults {
			v.pendingComplete = false
			v.comp.clearError()
			return v.openPopup(true)
		}
	case "ctrl+r":
		v.popup = nil
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
		// In the editor, tab completes when the cursor follows a word —
		// shell-style — and otherwise switches panes.
		if !v.focusResults && v.wordBeforeCursor() {
			v.pendingComplete = false
			v.comp.clearError()
			return v.openPopup(true)
		}
		return v.toggleFocus()
	case "shift+tab":
		// Always a pane switch: after running a query the cursor sits at the
		// end of a word, so plain tab would only ever try to complete.
		return v.toggleFocus()
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
		case "s":
			if label := v.table.SortByCursorColumn(); label != "" {
				return toast(statusInfo, "sorted by "+label)
			}
			return toast(statusInfo, "sort cleared")
		case "o":
			return v.openRecord()
		case "m":
			return v.fetchMore()
		case "y":
			if cell := v.table.CurrentCell(); cell != "" {
				if err := v.app.deps.Clipboard(cell); err != nil {
					return toastErr(err)
				}
				return toast(statusOK, fmt.Sprintf("copied cell (%d chars)", len(cell)))
			}
			return toast(statusWarn, "cell is empty")
		case "Y":
			if row := v.table.CurrentRow(); row != nil {
				rec := map[string]string{}
				for i, col := range v.result.Columns {
					if i < len(row) {
						rec[col] = row[i]
					}
				}
				raw, err := json.MarshalIndent(rec, "", "  ")
				if err != nil {
					return toastErr(err)
				}
				if err := v.app.deps.Clipboard(string(raw)); err != nil {
					return toastErr(err)
				}
				return toast(statusOK, fmt.Sprintf("copied row as JSON (%d fields)", len(rec)))
			}
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
	// Keep an open popup in sync with what's being typed.
	if v.popup != nil {
		return tea.Batch(cmd, v.refreshPopup())
	}
	return cmd
}

// toggleFocus moves between the editor and the results table.
func (v *queryView) toggleFocus() tea.Cmd {
	if !v.focusResults && v.table.RowCount() == 0 {
		return toast(statusInfo, "no results to move to yet")
	}
	v.focusResults = !v.focusResults
	if v.focusResults {
		v.editor.Blur()
		return nil
	}
	v.editor.Focus()
	return nil
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

// recordIDPattern matches a Salesforce record id (15 or 18 characters).
var recordIDPattern = regexp.MustCompile(`^[a-zA-Z0-9]{15}([a-zA-Z0-9]{3})?$`)

// openRecord opens the focused row's record in the browser. Reading a record
// in the terminal and then hunting for it in the UI is a routine annoyance;
// this closes that loop.
func (v *queryView) openRecord() tea.Cmd {
	id := v.recordIDForRow()
	if id == "" {
		return toast(statusWarn, "no record id in this row — SELECT Id to open records")
	}
	if v.app.current == nil {
		return nil
	}
	sf := v.app.deps.SF
	username := v.app.current.Username
	return tea.Batch(
		toast(statusInfo, "opening "+id+"…"),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := sf.OpenPath(ctx, username, "/"+id); err != nil {
				return statusMsg{kind: statusError, text: err.Error()}
			}
			return statusMsg{kind: statusOK, text: "opened " + id + " in your browser"}
		},
	)
}

// recordIDForRow finds the id of the focused row: the Id column when the
// query selected one, otherwise any cell that looks like a record id.
func (v *queryView) recordIDForRow() string {
	if id := v.table.Cell("Id"); recordIDPattern.MatchString(id) {
		return id
	}
	row := v.table.CurrentRow()
	for _, cell := range row {
		if recordIDPattern.MatchString(cell) {
			return cell
		}
	}
	return ""
}

// collapse renders a multi-line query as one line for the compact editor.
func collapse(q string) string {
	return strings.Join(strings.Fields(q), " ")
}

// exportedMsg reports where an export landed.
type exportedMsg struct{ path string }

// wrapText breaks a long single-line value onto terminal-width lines.
func wrapText(s string, width int) string {
	if width < 20 {
		width = 20
	}
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case runewidth.StringWidth(line)+1+runewidth.StringWidth(word) <= width:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// oneLine flattens a message for single-line display. Salesforce error bodies
// are frequently multi-line, and extra lines push the app's own chrome off
// the screen.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
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
	dir := v.app.deps.Store.ExportDir()
	name := filepath.Join(dir, fmt.Sprintf("sf9s-export-%s.%s", time.Now().Format("20060102-150405"), format))
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
		return exportedMsg{path: name}
	}
}

func (v *queryView) View(width, height int) string {
	v.editor.SetWidth(width - 4)
	if v.card != nil {
		return v.card.View(width, height)
	}
	if v.showPicker {
		// The list names the queries; the pane below shows the highlighted one
		// in full. Running a query you could not read is a poor bargain.
		listH := clampInt(len(v.saved)+2, 4, max(height/2, 4))
		v.picker.SetSize(width-4, listH)
		preview := ""
		if row := v.picker.CurrentRow(); len(row) > 2 {
			preview = "\n" + styleDim.Render("query") + "\n" +
				styleEditorFocused.Width(width-4).Render(wrapText(row[2], width-10))
		}
		return styleTitle.Render("Saved queries") + " " +
			styleDim.Render("(enter runs, esc closes)") + "\n" + v.picker.View() + preview
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
	switch {
	case v.exported != "":
		resultHead = styleOK.Render("saved → " + truncateMiddle(v.exported, max(width-10, 20)))
	case v.lastErr != nil:
		resultHead = styleErrText.Render("✖ " + truncateMiddle(oneLine(v.lastErr.Error()), max(width-4, 20)))
	case v.staleResults && v.result != nil:
		resultHead = styleWarn.Render(fmt.Sprintf("%d rows from the previous query — ctrl+r to run this one", v.fetched))
	case v.result != nil:
		resultHead = styleDim.Render(fmt.Sprintf("%d/%d rows • %s", v.fetched, v.result.TotalSize, v.elapsed.Round(time.Millisecond)))
		if v.result.NextRecordsURL != "" {
			resultHead += styleDim.Render(" • m = more")
		}
	}

	// While you are reading results the query is reference material, not a
	// workspace: collapse it to one line and give the rows the space back.
	// tab (or esc) returns to the full editor.
	var editor string
	edH := 1
	if v.focusResults {
		editor = styleDim.Render("  " + runewidth.Truncate(collapse(v.editor.Value()), width-4, "…"))
	} else {
		// Shrink before sacrificing result rows on short terminals:
		// head(1) + editor(edH+2) + resultHead(1) + table(>=3) must fit.
		edH = min(editorHeight, max(1, height-7))
		v.editor.SetHeight(edH)
		editor = styleEditorFocused.Width(width - 2).Render(v.editor.View())
		edH += 2 // border
	}

	tableH := max(height-edH-2, 3)
	if v.popup.open() {
		// Keep results visible under the suggestions when there's room.
		popup := v.popup.View(width)
		popupH := lipgloss.Height(popup)
		remaining := height - edH - 1 - popupH
		if remaining < 4 {
			return head + "\n" + editor + "\n" + popup
		}
		v.table.SetSize(width, remaining)
		return head + "\n" + editor + "\n" + popup + "\n" + v.table.View()
	}

	v.table.SetSize(width, tableH)
	return head + "\n" + editor + "\n" + resultHead + "\n" + v.table.View()
}
