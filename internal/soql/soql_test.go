package soql

import (
	"reflect"
	"strings"
	"testing"
)

// at builds an input/cursor pair from a string with "|" marking the cursor.
func at(s string) (string, int) {
	i := strings.Index(s, "|")
	if i < 0 {
		panic("test input must contain a | cursor marker")
	}
	return s[:i] + s[i+1:], i
}

func TestAnalyze(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		clause Clause
		object string
		prefix string
	}{
		{"after select", "SELECT |", ClauseSelect, "", ""},
		{"partial field", "SELECT Nam|", ClauseSelect, "", "Nam"},
		{"field with object known later", "SELECT Nam| FROM Account", ClauseSelect, "Account", "Nam"},
		{"second field", "SELECT Id, Ow| FROM Contact", ClauseSelect, "Contact", "Ow"},
		{"relationship prefix", "SELECT Owner.Na| FROM Case", ClauseSelect, "Case", "Owner.Na"},
		{"deep relationship", "SELECT Account.Owner.Ma| FROM Case", ClauseSelect, "Case", "Account.Owner.Ma"},
		{"after from", "SELECT Id FROM |", ClauseFrom, "", ""},
		{"partial object", "SELECT Id FROM Acc|", ClauseFrom, "Acc", "Acc"},
		{"where field", "SELECT Id FROM Account WHERE Nam|", ClauseFilter, "Account", "Nam"},
		{"and field", "SELECT Id FROM Account WHERE Id != null AND Ind|", ClauseFilter, "Account", "Ind"},
		{"order by", "SELECT Id FROM Lead ORDER BY Crea|", ClauseFilter, "Lead", "Crea"},
		{"group by", "SELECT COUNT(Id) FROM Lead GROUP BY Sta|", ClauseFilter, "Lead", "Sta"},
		{"past object into filter", "SELECT Id FROM Account Nam|", ClauseFilter, "Account", "Nam"},
		{"multi-line", "SELECT Id,\n  Name,\n  Ow|\nFROM Opportunity", ClauseSelect, "Opportunity", "Ow"},
		{"inside aggregate fn", "SELECT COUNT(I| FROM Account", ClauseSelect, "Account", "I"},
		{"after limit offers nothing", "SELECT Id FROM Account LIMIT 5|", ClauseUnknown, "Account", "5"},
		{"empty input", "|", ClauseUnknown, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, cursor := at(tc.input)
			got := Analyze(input, cursor)
			if got.Clause != tc.clause {
				t.Errorf("clause = %v, want %v", got.Clause, tc.clause)
			}
			if got.Object != tc.object {
				t.Errorf("object = %q, want %q", got.Object, tc.object)
			}
			if got.Prefix != tc.prefix {
				t.Errorf("prefix = %q, want %q", got.Prefix, tc.prefix)
			}
			if input[got.Start:cursor] != tc.prefix {
				t.Errorf("Start=%d does not delimit the prefix: %q", got.Start, input[got.Start:cursor])
			}
		})
	}
}

func TestAnalyzeSubquerySelectsChildObject(t *testing.T) {
	input, cursor := at("SELECT Id, (SELECT La| FROM Contacts) FROM Account")
	got := Analyze(input, cursor)
	if got.Clause != ClauseSelect {
		t.Errorf("clause = %v, want select", got.Clause)
	}
	if got.Object != "Contacts" {
		t.Errorf("subquery should resolve to the child relationship, got %q", got.Object)
	}
}

func TestAnalyzeOuterClauseAfterSubquery(t *testing.T) {
	input, cursor := at("SELECT Id, (SELECT LastName FROM Contacts), Nam| FROM Account")
	got := Analyze(input, cursor)
	if got.Object != "Account" {
		t.Errorf("closed subquery must not leak its object, got %q", got.Object)
	}
	if got.Clause != ClauseSelect {
		t.Errorf("clause = %v, want select", got.Clause)
	}
}

func TestAnalyzeCursorBoundsAreClamped(t *testing.T) {
	if got := Analyze("SELECT Id", -5); got.Prefix != "" {
		t.Errorf("negative cursor should clamp, got %+v", got)
	}
	if got := Analyze("SELECT Id", 999); got.Prefix != "Id" {
		t.Errorf("overlong cursor should clamp to end, got %+v", got)
	}
}

func TestRelationshipPath(t *testing.T) {
	path, partial := Context{Prefix: "Owner.Manager.Na"}.RelationshipPath()
	if !reflect.DeepEqual(path, []string{"Owner", "Manager"}) || partial != "Na" {
		t.Fatalf("path=%v partial=%q", path, partial)
	}
	path, partial = Context{Prefix: "Nam"}.RelationshipPath()
	if path != nil || partial != "Nam" {
		t.Fatalf("path=%v partial=%q", path, partial)
	}
}

func TestFilterOrdersPrefixMatchesFirst(t *testing.T) {
	cands := []Candidate{
		{Text: "LastName"},
		{Text: "Name"},
		{Text: "NameLocal"},
		{Text: "Industry"},
	}
	got := Filter(cands, "nam")
	want := []string{"Name", "NameLocal", "LastName"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i].Text != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if n := len(Filter(cands, "")); n != 4 {
		t.Errorf("empty partial should keep all candidates, got %d", n)
	}
}
