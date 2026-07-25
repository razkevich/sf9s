package api

import (
	"reflect"
	"testing"
)

func TestMergeSchemaDrift(t *testing.T) {
	base := &Result{
		TotalSize:      4,
		Columns:        []string{"Id", "Parent__r"},
		Rows:           [][]string{{"1", ""}, {"2", ""}},
		NextRecordsURL: "/next-1",
	}
	next := &Result{
		TotalSize:      4,
		Done:           true,
		Columns:        []string{"Id", "Parent__r.Id", "Parent__r.Name"},
		Rows:           [][]string{{"3", "P1", "Papa"}, {"4", "P2", "Quebec"}},
		NextRecordsURL: "",
	}
	merged := Merge(base, next)
	wantCols := []string{"Id", "Parent__r", "Parent__r.Id", "Parent__r.Name"}
	if !reflect.DeepEqual(merged.Columns, wantCols) {
		t.Fatalf("columns = %v, want %v", merged.Columns, wantCols)
	}
	if len(merged.Rows) != 4 {
		t.Fatalf("rows = %d", len(merged.Rows))
	}
	if !reflect.DeepEqual(merged.Rows[2], []string{"3", "", "P1", "Papa"}) {
		t.Fatalf("next rows misaligned: %v", merged.Rows[2])
	}
	if !reflect.DeepEqual(merged.Rows[0], []string{"1", "", "", ""}) {
		t.Fatalf("base rows misaligned: %v", merged.Rows[0])
	}
	if !merged.Done || merged.NextRecordsURL != "" {
		t.Fatal("pagination state should come from the newest page")
	}
}

func TestMergeSameSchema(t *testing.T) {
	base := &Result{Columns: []string{"Id"}, Rows: [][]string{{"1"}}}
	next := &Result{Columns: []string{"Id"}, Rows: [][]string{{"2"}}, Done: true}
	merged := Merge(base, next)
	if len(merged.Rows) != 2 || merged.Rows[1][0] != "2" {
		t.Fatalf("simple merge wrong: %v", merged.Rows)
	}
}
