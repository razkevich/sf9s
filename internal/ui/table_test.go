package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

func sampleTable() *dataTable {
	t := newDataTable()
	t.SetSize(60, 10)
	t.SetData(
		[]string{"Id", "Name", "Status"},
		[][]string{
			{"001A", "Acme Corp", "Active"},
			{"001B", "Globex\nInternational", "Inactive"},
			{"001C", "Wayne Enterprises", "Active"},
		},
	)
	return t
}

func TestTableSanitizesNewlines(t *testing.T) {
	tbl := sampleTable()
	if strings.Count(tbl.View(), "Globex International") != 1 {
		t.Fatalf("newlines should become spaces:\n%s", tbl.View())
	}
}

func TestTableFilter(t *testing.T) {
	tbl := sampleTable()
	tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range "wayne" {
		tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if tbl.RowCount() != 1 || tbl.CurrentRow()[0] != "001C" {
		t.Fatalf("case-insensitive filter failed: %d rows", tbl.RowCount())
	}
	tbl.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if tbl.RowCount() != 3 {
		t.Fatal("esc should clear filter")
	}
}

func TestTableNavigationClamps(t *testing.T) {
	tbl := sampleTable()
	for i := 0; i < 10; i++ {
		tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	if tbl.CurrentRow()[0] != "001C" {
		t.Fatal("cursor should clamp at last row")
	}
	tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if tbl.CurrentRow()[0] != "001A" {
		t.Fatal("g should jump to top")
	}
}

func TestTableHorizontalPaging(t *testing.T) {
	tbl := newDataTable()
	tbl.SetSize(30, 10)
	cols := []string{"AAAAAAAAAAAAAAA", "BBBBBBBBBBBBBBB", "CCCCCCCCCCCCCCC", "DDDDDDDDDDDDDDD"}
	tbl.SetData(cols, [][]string{{"1", "2", "3", "4"}})
	first := tbl.View()
	if strings.Contains(first, "DDDD") {
		t.Fatalf("last column should not fit at offset 0:\n%s", first)
	}
	tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	shifted := tbl.View()
	if strings.Contains(shifted, "AAAA") {
		t.Fatalf("first column should scroll out:\n%s", shifted)
	}
	if !strings.Contains(shifted, "cols") {
		t.Fatalf("footer should indicate column window:\n%s", shifted)
	}
}

func TestTableWideCellTruncation(t *testing.T) {
	tbl := newDataTable()
	tbl.SetSize(50, 10)
	tbl.SetData([]string{"Text"}, [][]string{{strings.Repeat("x", 200)}})
	for _, line := range strings.Split(tbl.View(), "\n") {
		if len([]rune(line)) > 60 {
			t.Fatalf("line exceeds width budget: %d runes", len([]rune(line)))
		}
	}
}

func TestTableCellLookupByColumn(t *testing.T) {
	tbl := sampleTable()
	tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if got := tbl.Cell("Status"); got != "Inactive" {
		t.Fatalf("Cell lookup wrong: %q", got)
	}
	if got := tbl.Cell("Nope"); got != "" {
		t.Fatalf("unknown column should be empty, got %q", got)
	}
}

func TestTableAppendRowsKeepsCursor(t *testing.T) {
	tbl := sampleTable()
	tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	tbl.AppendRows([][]string{{"001D", "Initech", "Active"}})
	if tbl.RowCount() != 4 {
		t.Fatal("append should add rows")
	}
	if tbl.CurrentRow()[0] != "001B" {
		t.Fatal("append should not move the cursor")
	}
}

func TestPadRightUsesDisplayWidth(t *testing.T) {
	// Byte-length padding misaligns every column containing a multibyte hint.
	for _, s := range []string{"1…9", "↑↓", "esc", "⇄"} {
		if got := runewidth.StringWidth(padRight(s, 10)); got != 10 {
			t.Errorf("padRight(%q, 10) has display width %d, want 10", s, got)
		}
	}
	if got := padRight("toolong", 3); got != "toolong" {
		t.Errorf("padding shorter than the string must not truncate: %q", got)
	}
}

func TestSortByColumnCyclesAscDescOff(t *testing.T) {
	tbl := newDataTable()
	tbl.SetSize(80, 10)
	tbl.SetData([]string{"Name", "Revenue"}, [][]string{
		{"Globex", "9"},
		{"acme", "10"},
		{"Initech", ""},
	})
	order := func() []string {
		var out []string
		for _, i := range tbl.filtered {
			out = append(out, tbl.rows[i][0])
		}
		return out
	}

	if got := tbl.SortByCursorColumn(); got != "Name ▲" {
		t.Fatalf("first press = %q, want ascending", got)
	}
	// Case-insensitive, so "acme" leads "Globex".
	if got := order(); got[0] != "acme" || got[2] != "Initech" {
		t.Fatalf("ascending order wrong: %v", got)
	}
	if got := tbl.SortByCursorColumn(); got != "Name ▼" {
		t.Fatalf("second press = %q, want descending", got)
	}
	if got := order(); got[0] != "Initech" {
		t.Fatalf("descending order wrong: %v", got)
	}
	if got := tbl.SortByCursorColumn(); got != "" {
		t.Fatalf("third press should clear the sort, got %q", got)
	}
	if got := order(); got[0] != "Globex" {
		t.Fatalf("clearing should restore server order: %v", got)
	}
}

func TestSortIsNumericWhenValuesAre(t *testing.T) {
	tbl := newDataTable()
	tbl.SetSize(80, 10)
	tbl.SetData([]string{"Revenue"}, [][]string{{"9"}, {"10"}, {"1000"}, {""}})
	tbl.colOff = 0
	tbl.SortByCursorColumn()
	var got []string
	for _, i := range tbl.filtered {
		got = append(got, tbl.rows[i][0])
	}
	// Numeric, not lexicographic — and blanks sink.
	want := []string{"9", "10", "1000", ""}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("numeric sort = %v, want %v", got, want)
		}
	}
}

func TestSortMarkerShownInHeader(t *testing.T) {
	tbl := sampleTable()
	tbl.SortByCursorColumn()
	if !strings.Contains(tbl.View(), "▲") {
		t.Fatalf("header should mark the sorted column:\n%s", tbl.View())
	}
}

func TestSortSurvivesFilterAndAppend(t *testing.T) {
	tbl := newDataTable()
	tbl.SetSize(80, 10)
	tbl.SetData([]string{"Name"}, [][]string{{"c"}, {"a"}, {"b"}})
	tbl.SortByCursorColumn()
	tbl.AppendRows([][]string{{"aa"}})
	var got []string
	for _, i := range tbl.filtered {
		got = append(got, tbl.rows[i][0])
	}
	want := []string{"a", "aa", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("appended rows should land in sort order: %v, want %v", got, want)
		}
	}
}

// Regression: clearing a filter kept the cursor's index rather than its row,
// so the selection silently moved to a different record and the next action
// acted on that one.
func TestCursorFollowsItsRowAcrossFilterChanges(t *testing.T) {
	tbl := newDataTable()
	tbl.SetSize(60, 10)
	tbl.SetData([]string{"Name"}, [][]string{
		{"alpha"}, {"beta"}, {"gamma"}, {"delta"},
	})

	// Filter down to one row and select it.
	tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range "gam" {
		tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	tbl.Update(tea.KeyMsg{Type: tea.KeyEnter}) // commit the filter
	if got := tbl.Cell("Name"); got != "gamma" {
		t.Fatalf("precondition: cursor on gamma, got %q", got)
	}

	if !tbl.ClearFilter() {
		t.Fatal("clearing a committed filter should report that it did")
	}
	if got := tbl.Cell("Name"); got != "gamma" {
		t.Fatalf("after clearing the filter the cursor jumped to %q, want gamma", got)
	}
	if tbl.RowCount() != 4 {
		t.Fatalf("all rows should be back, got %d", tbl.RowCount())
	}
}

func TestCursorFollowsItsRowAcrossSorting(t *testing.T) {
	tbl := newDataTable()
	tbl.SetSize(60, 10)
	tbl.SetData([]string{"Name"}, [][]string{{"c"}, {"a"}, {"b"}})
	tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}) // on "a"
	if got := tbl.Cell("Name"); got != "a" {
		t.Fatalf("precondition: cursor on a, got %q", got)
	}
	tbl.SortByCursorColumn()
	if got := tbl.Cell("Name"); got != "a" {
		t.Fatalf("sorting moved the selection to %q, want a", got)
	}
}
