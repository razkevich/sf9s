package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
