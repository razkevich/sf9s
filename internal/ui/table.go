package ui

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const (
	maxColWidth = 40
	minColWidth = 4
)

// dataTable is a scrollable, filterable table with horizontal column
// paging — the workhorse behind every list view in sf9s.
type dataTable struct {
	cols []string
	rows [][]string

	filtered  []int
	filter    string
	filtering bool

	cursor int
	top    int
	colOff int

	width  int
	height int
	colW   []int

	emptyText  string
	cellStyles map[string]func(string) lipgloss.Style

	// sortCol is the column the rows are ordered by (-1 = server order),
	// sortDesc its direction. Sorting is a view over rows, never a mutation,
	// so paging and exports keep the order the org returned.
	sortCol  int
	sortDesc bool
}

func newDataTable() *dataTable {
	return &dataTable{
		emptyText:  "no rows",
		cellStyles: map[string]func(string) lipgloss.Style{},
		sortCol:    -1,
	}
}

// SetCellStyle registers a semantic style for one column's cells.
func (t *dataTable) SetCellStyle(col string, fn func(string) lipgloss.Style) {
	t.cellStyles[col] = fn
}

func (t *dataTable) SetSize(width, height int) {
	t.width = max(width, 10)
	t.height = max(height, 3)
	t.clampScroll()
}

func (t *dataTable) SetData(cols []string, rows [][]string) {
	t.cols = cols
	t.rows = make([][]string, len(rows))
	for i, row := range rows {
		clean := make([]string, len(row))
		for j, cell := range row {
			clean[j] = sanitizeCell(cell)
		}
		t.rows[i] = clean
	}
	t.cursor, t.top, t.colOff = 0, 0, 0
	t.computeWidths()
	t.applyFilter()
}

// SetDataPreservingView replaces the data but keeps the active filter,
// cursor and scroll position — used when a background pass enriches rows
// that are already on screen.
func (t *dataTable) SetDataPreservingView(cols []string, rows [][]string) {
	cursor, top, colOff, filter := t.cursor, t.top, t.colOff, t.filter
	t.SetData(cols, rows)
	t.filter = filter
	t.applyFilter()
	t.cursor, t.top, t.colOff = cursor, top, colOff
	t.clampScroll()
}

// FocusRowWhere moves the cursor to the row whose column col equals value.
func (t *dataTable) FocusRowWhere(col int, value string) {
	for i, idx := range t.filtered {
		row := t.rows[idx]
		if col < len(row) && row[col] == value {
			t.cursor = i
			t.clampScroll()
			return
		}
	}
}

// AppendRows adds rows without resetting cursor/filter (queryMore).
func (t *dataTable) AppendRows(rows [][]string) {
	for _, row := range rows {
		clean := make([]string, len(row))
		for j, cell := range row {
			clean[j] = sanitizeCell(cell)
		}
		t.rows = append(t.rows, clean)
	}
	t.computeWidths()
	t.applyFilter()
}

// escapeSeq matches CSI and OSC sequences. Untrusted org data must never
// reach the terminal as control codes: OSC 52 alone would let a crafted
// field or log line rewrite the operator's clipboard.
var escapeSeq = regexp.MustCompile(`\x1b(\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)?|[@-Z\\-_])`)

func stripEscapes(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}
	return escapeSeq.ReplaceAllString(s, "")
}

func sanitizeCell(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 32 || r == 0x7f {
			return -1
		}
		return r
	}, stripEscapes(s))
}

// sanitizeText keeps line structure (for log bodies) but removes escape
// sequences and other control bytes.
func sanitizeText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r == '\r' {
			return -1
		}
		if r < 32 || r == 0x7f {
			return -1
		}
		return r
	}, stripEscapes(s))
}

func (t *dataTable) computeWidths() {
	t.colW = make([]int, len(t.cols))
	for i, c := range t.cols {
		t.colW[i] = clampInt(runewidth.StringWidth(c), minColWidth, maxColWidth)
	}
	for _, row := range t.rows {
		for i := range t.cols {
			if i < len(row) {
				if w := runewidth.StringWidth(row[i]); w > t.colW[i] {
					t.colW[i] = min(w, maxColWidth)
				}
			}
		}
	}
}

func (t *dataTable) applyFilter() {
	// Remember which row the cursor is on, not where it sits: filtering and
	// clearing change what is at each index, so keeping the index silently
	// moves the selection to a different record — and the next enter or
	// delete would act on that one.
	focused := -1
	if t.cursor >= 0 && t.cursor < len(t.filtered) {
		focused = t.filtered[t.cursor]
	}

	t.filtered = t.filtered[:0]
	needle := strings.ToLower(t.filter)
	for i, row := range t.rows {
		if needle == "" {
			t.filtered = append(t.filtered, i)
			continue
		}
		for _, cell := range row {
			if strings.Contains(strings.ToLower(cell), needle) {
				t.filtered = append(t.filtered, i)
				break
			}
		}
	}
	t.applySort()

	if focused >= 0 {
		for i, idx := range t.filtered {
			if idx == focused {
				t.cursor = i
				break
			}
		}
	}
	t.clampScroll()
}

// applySort orders the visible rows. Values that both parse as numbers sort
// numerically (so 9 comes before 10), everything else case-insensitively;
// blanks always sink to the bottom, where they are least distracting.
func (t *dataTable) applySort() {
	if t.sortCol < 0 || t.sortCol >= len(t.cols) {
		return
	}
	col := t.sortCol
	sort.SliceStable(t.filtered, func(i, j int) bool {
		a, b := t.cellAt(t.filtered[i], col), t.cellAt(t.filtered[j], col)
		switch {
		case a == "" && b == "":
			return false
		case a == "":
			return false
		case b == "":
			return true
		}
		less := compareCells(a, b)
		if t.sortDesc {
			return !less && a != b
		}
		return less
	})
}

func compareCells(a, b string) bool {
	af, aerr := strconv.ParseFloat(a, 64)
	bf, berr := strconv.ParseFloat(b, 64)
	if aerr == nil && berr == nil {
		return af < bf
	}
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a < b
}

func (t *dataTable) cellAt(row, col int) string {
	r := t.rows[row]
	if col >= len(r) {
		return ""
	}
	return r[col]
}

// SortByCursorColumn orders by the column currently in view, toggling
// direction when it is already the sort column, and clearing on a third press.
func (t *dataTable) SortByCursorColumn() string {
	if len(t.cols) == 0 {
		return ""
	}
	col := clampInt(t.colOff, 0, len(t.cols)-1)
	switch {
	case t.sortCol != col:
		t.sortCol, t.sortDesc = col, false
	case !t.sortDesc:
		t.sortDesc = true
	default:
		t.sortCol, t.sortDesc = -1, false
		t.applyFilter()
		return ""
	}
	t.applyFilter()
	arrow := "▲"
	if t.sortDesc {
		arrow = "▼"
	}
	return t.cols[col] + " " + arrow
}

func (t *dataTable) clampScroll() {
	if t.cursor >= len(t.filtered) {
		t.cursor = len(t.filtered) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
	vis := t.visibleRows()
	if t.cursor < t.top {
		t.top = t.cursor
	}
	if t.cursor >= t.top+vis {
		t.top = t.cursor - vis + 1
	}
	if t.top < 0 {
		t.top = 0
	}
	if t.colOff >= len(t.cols) {
		t.colOff = max(len(t.cols)-1, 0)
	}
	if t.colOff < 0 {
		t.colOff = 0
	}
}

func (t *dataTable) visibleRows() int {
	return max(t.height-2, 1)
}

// CurrentRow returns the selected row's cells, or nil.
func (t *dataTable) CurrentRow() []string {
	if t.cursor < 0 || t.cursor >= len(t.filtered) {
		return nil
	}
	return t.rows[t.filtered[t.cursor]]
}

// Cell returns the selected row's value for a named column.
func (t *dataTable) Cell(col string) string {
	row := t.CurrentRow()
	if row == nil {
		return ""
	}
	for i, c := range t.cols {
		if c == col && i < len(row) {
			return row[i]
		}
	}
	return ""
}

func (t *dataTable) Filtering() bool { return t.filtering }

// ClearFilter drops a committed filter, reporting whether there was one.
func (t *dataTable) ClearFilter() bool {
	if t.filter == "" {
		return false
	}
	t.filter = ""
	t.applyFilter()
	return true
}
func (t *dataTable) RowCount() int { return len(t.filtered) }

// Update consumes navigation and filter keys; returns true if handled.
func (t *dataTable) Update(msg tea.KeyMsg) bool {
	if t.filtering {
		switch msg.Type {
		case tea.KeyEsc:
			t.filtering, t.filter = false, ""
			t.applyFilter()
		case tea.KeyEnter:
			t.filtering = false
		case tea.KeyBackspace:
			if r := []rune(t.filter); len(r) > 0 {
				t.filter = string(r[:len(r)-1])
				t.applyFilter()
			}
		case tea.KeyUp:
			t.cursor--
			t.clampScroll()
		case tea.KeyDown:
			t.cursor++
			t.clampScroll()
		case tea.KeyRunes, tea.KeySpace:
			t.filter += string(msg.Runes)
			t.applyFilter()
		}
		return true
	}
	if msg.String() == "esc" && t.filter != "" {
		t.filter = ""
		t.applyFilter()
		return true
	}
	switch msg.String() {
	case "up", "k":
		t.cursor--
	case "down", "j":
		t.cursor++
	case "pgup", "ctrl+u":
		t.cursor -= t.visibleRows()
	case "pgdown", "ctrl+d":
		t.cursor += t.visibleRows()
	case "home", "g":
		t.cursor = 0
	case "end", "G":
		t.cursor = len(t.filtered) - 1
	case "left", "h":
		t.colOff--
	case "right", "l":
		t.colOff++
	case "/":
		t.filtering = true
	default:
		return false
	}
	t.clampScroll()
	return true
}

func (t *dataTable) View() string {
	if len(t.cols) == 0 {
		return styleDim.Render(t.emptyText)
	}
	lastCol := t.lastVisibleCol()

	var b strings.Builder
	b.WriteString(t.renderRow(t.cols, lastCol, rowHeader))
	b.WriteByte('\n')

	vis := t.visibleRows()
	if len(t.filtered) == 0 {
		b.WriteString(styleDim.Render(t.emptyText))
		b.WriteByte('\n')
	}
	for i := t.top; i < min(t.top+vis, len(t.filtered)); i++ {
		row := t.rows[t.filtered[i]]
		kind := rowPlain
		if i == t.cursor {
			kind = rowSelected
		}
		b.WriteString(t.renderRow(row, lastCol, kind))
		b.WriteByte('\n')
	}
	b.WriteString(t.footer(lastCol))
	return b.String()
}

func (t *dataTable) lastVisibleCol() int {
	used := 0
	last := t.colOff
	for i := t.colOff; i < len(t.cols); i++ {
		used += t.colW[i] + 2
		if used > t.width && i > t.colOff {
			break
		}
		last = i
	}
	return last
}

type rowKind int

const (
	rowPlain rowKind = iota
	rowHeader
	rowSelected
)

func (t *dataTable) renderRow(cells []string, lastCol int, kind rowKind) string {
	remaining := t.width
	var parts []string
	for i := t.colOff; i <= lastCol && i < len(t.cols); i++ {
		w := min(t.colW[i], remaining)
		if w <= 0 {
			break
		}
		raw := ""
		if i < len(cells) {
			raw = cells[i]
		}
		if kind == rowHeader && i == t.sortCol {
			marker := " ▲"
			if t.sortDesc {
				marker = " ▼"
			}
			raw = runewidth.Truncate(raw, max(w-2, 1), "") + marker
		}
		cell := runewidth.FillRight(runewidth.Truncate(raw, w, "…"), w)
		if kind == rowPlain {
			if fn, ok := t.cellStyles[t.cols[i]]; ok {
				cell = fn(raw).Render(cell)
			}
		}
		parts = append(parts, cell)
		remaining -= w + 2
	}
	line := strings.Join(parts, "  ")
	switch kind {
	case rowHeader:
		return styleTableHeader.Render(line)
	case rowSelected:
		return styleRowSelected.Render(line)
	default:
		return line
	}
}

func (t *dataTable) footer(lastCol int) string {
	pos := "0/0"
	if len(t.filtered) > 0 {
		pos = fmt.Sprintf("%d/%d", t.cursor+1, len(t.filtered))
	}
	seg := pos
	if len(t.cols) > 0 && (t.colOff > 0 || lastCol < len(t.cols)-1) {
		seg += fmt.Sprintf("  cols %d-%d/%d (←/→)", t.colOff+1, lastCol+1, len(t.cols))
	}
	if t.filtering {
		seg += "  filter: " + t.filter + "▌"
	} else if t.filter != "" {
		seg += "  filter: " + t.filter + " (esc clears, / edits)"
	}
	return styleDim.Render(runewidth.Truncate(seg, t.width, "…"))
}

// plural renders "1 thing" / "2 things".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func clampInt(v, lo, hi int) int {
	return max(lo, min(v, hi))
}

// CurrentCell returns the value under the cursor in the focused column.
func (t *dataTable) CurrentCell() string {
	row := t.CurrentRow()
	if row == nil || t.colOff >= len(row) {
		return ""
	}
	return row[t.colOff]
}
