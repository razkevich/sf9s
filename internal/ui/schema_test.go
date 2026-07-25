package ui

import (
	"strings"
	"testing"

	"github.com/razkevich/sf9s/internal/api"
)

// accountDescribe is the field mix the schema view has to explain: a picklist
// too long for its cell, a required field, a formula, a read-only audit field
// and a lookup.
func accountDescribe() *api.SObjectDescribe {
	pick := func(label, value string, active bool) api.PicklistValue {
		return api.PicklistValue{Label: label, Value: value, Active: active}
	}
	return &api.SObjectDescribe{
		Name: "Account", Label: "Account", KeyPrefix: "001",
		Fields: []api.Field{
			{Name: "Name", Label: "Account Name", Type: "string", Length: 255,
				Createable: true, Updateable: true},
			{Name: "Industry", Label: "Industry", Type: "picklist",
				Nillable: true, Createable: true, Updateable: true,
				PicklistValues: []api.PicklistValue{
					pick("Agriculture", "Agriculture", true),
					pick("Apparel", "Apparel", true),
					pick("Banking", "Banking", true),
					pick("Biotechnology", "Biotechnology", true),
					pick("Chemicals", "Chemicals", true),
					pick("Communications", "Communications", true),
					pick("Consumer Goods", "Consumer_Goods", true),
					pick("Education", "Education", true),
					pick("Electronics", "Electronics", true),
					pick("Buggy Whips", "Buggy_Whips", false),
					pick("Telecommunications", "Telecommunications", true),
				}},
			{Name: "AnnualRevenueBand__c", Label: "Revenue Band", Type: "string",
				Nillable: true, Calculated: true},
			{Name: "CreatedDate", Label: "Created Date", Type: "datetime"},
			{Name: "OwnerId", Label: "Owner ID", Type: "reference", Nillable: true,
				Createable: true, Updateable: true,
				ReferenceTo: []string{"User"}, RelationshipName: "Owner"},
		},
	}
}

// schemaWithFields opens the schema view on a fields table built from d. The
// terminal is tall so a card's whole body renders without scrolling.
func schemaWithFields(t *testing.T, d *api.SObjectDescribe) (*Model, *schemaView) {
	t.Helper()
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	m.height = 60
	loadAllOrgs(t, m)
	drive(t, m, switchViewMsg{id: ViewSchema})
	sv, ok := m.views[ViewSchema].(*schemaView)
	if !ok {
		t.Fatal("schema view should be active")
	}
	sv.showFields(d)
	return m, sv
}

func fieldRow(t *testing.T, sv *schemaView, name string) []string {
	t.Helper()
	for _, row := range sv.fieldTable.rows {
		if row[0] == name {
			return row
		}
	}
	t.Fatalf("no row for field %q", name)
	return nil
}

func cellOf(t *testing.T, sv *schemaView, row []string, col string) string {
	t.Helper()
	for i, c := range sv.fieldTable.cols {
		if c == col {
			return row[i]
		}
	}
	t.Fatalf("no column %q in %v", col, sv.fieldTable.cols)
	return ""
}

func hasKey(hints []keyHint, key string) bool {
	for _, h := range hints {
		if h.key == key {
			return true
		}
	}
	return false
}

func TestFieldsTableSpellsOutRequiredAndAttributes(t *testing.T) {
	m, sv := schemaWithFields(t, accountDescribe())

	for _, tc := range []struct{ field, required, attributes string }{
		{"Name", "✔", ""},
		{"Industry", "", ""},
		{"AnnualRevenueBand__c", "", "formula"},
		{"CreatedDate", "✔", "readonly"},
		{"OwnerId", "", ""},
	} {
		row := fieldRow(t, sv, tc.field)
		if got := cellOf(t, sv, row, "Required"); got != tc.required {
			t.Errorf("%s Required = %q, want %q", tc.field, got, tc.required)
		}
		if got := cellOf(t, sv, row, "Attributes"); got != tc.attributes {
			t.Errorf("%s Attributes = %q, want %q", tc.field, got, tc.attributes)
		}
	}

	m.width = 80 // the columns that matter must survive a narrow terminal
	view := m.View()
	for _, want := range []string{"Required", "Attributes", "✔", "formula", "readonly"} {
		if !strings.Contains(view, want) {
			t.Errorf("fields table should show %q without sideways paging:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Flags") {
		t.Errorf("the ncu/cu letter soup should be gone:\n%s", view)
	}
}

func TestFieldsTablePreviewsOnlyActivePicklistValues(t *testing.T) {
	_, sv := schemaWithFields(t, accountDescribe())
	preview := cellOf(t, sv, fieldRow(t, sv, "Industry"), "Picklist values")
	if !strings.HasPrefix(preview, "Agriculture, Apparel") {
		t.Errorf("preview should list values in describe order, got %q", preview)
	}
	if strings.Contains(preview, "Buggy_Whips") {
		t.Errorf("inactive values belong in the card, not the preview: %q", preview)
	}
}

func TestFieldCardShowsWholePicklist(t *testing.T) {
	m, sv := schemaWithFields(t, accountDescribe())
	sv.fieldTable.FocusRowWhere(0, "Industry")

	// The complaint this card answers: the tail of the list cannot be read
	// from the table cell.
	if strings.Contains(m.View(), "Telecommunications") {
		t.Fatal("precondition: the preview cell should be truncated short of the last value")
	}

	drive(t, m, key("enter"))
	if sv.card == nil {
		t.Fatal("enter on a field should open its card")
	}
	view := m.View()
	for _, want := range []string{
		"field Industry", "Agriculture", "Consumer_Goods", "Telecommunications",
		"11 picklist values",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("card should show %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "Consumer Goods") {
		t.Errorf("a value whose label differs should show both:\n%s", view)
	}
	if !strings.Contains(view, "(inactive)") {
		t.Errorf("inactive values should be marked:\n%s", view)
	}
	if !hasKey(sv.Keys(), "y") || !hasKey(sv.Keys(), "esc") {
		t.Errorf("the card's keys should be advertised, got %v", sv.Keys())
	}
}

func TestFieldCardShowsIdentityAndReferenceTargets(t *testing.T) {
	m, sv := schemaWithFields(t, accountDescribe())
	sv.fieldTable.FocusRowWhere(0, "OwnerId")
	drive(t, m, key("enter"))
	view := m.View()
	for _, want := range []string{"API Name", "OwnerId", "Owner ID", "References", "User", "Relationship", "Owner"} {
		if !strings.Contains(view, want) {
			t.Errorf("card should show %q:\n%s", want, view)
		}
	}
}

func TestFieldCardReportsRequiredInWords(t *testing.T) {
	m, sv := schemaWithFields(t, accountDescribe())
	sv.fieldTable.FocusRowWhere(0, "Name")
	drive(t, m, key("enter"))
	if !strings.Contains(m.View(), "Required    yes") {
		t.Errorf("a non-nillable field should read as required:\n%s", m.View())
	}
}

func TestFieldCardCopiesPicklistValues(t *testing.T) {
	m, sv := schemaWithFields(t, accountDescribe())
	var copied []string
	m.deps.Clipboard = func(s string) error { copied = append(copied, s); return nil }
	sv.fieldTable.FocusRowWhere(0, "Industry")
	drive(t, m, key("enter"))
	drive(t, m, key("y"))

	if len(copied) != 1 {
		t.Fatalf("y should copy once, got %v", copied)
	}
	lines := strings.Split(copied[0], "\n")
	if len(lines) != 11 {
		t.Errorf("every value should be copied on its own line, got %d:\n%s", len(lines), copied[0])
	}
	if lines[0] != "Agriculture" || lines[len(lines)-1] != "Telecommunications" {
		t.Errorf("values should be copied verbatim, in order: %q", lines)
	}
	if !strings.Contains(m.View(), "copied 11 picklist values") {
		t.Errorf("the copy should be announced:\n%s", m.View())
	}
	if sv.card == nil {
		t.Error("copying should not close the card")
	}
}

func TestFieldCardWithoutPicklistCopiesDetails(t *testing.T) {
	m, sv := schemaWithFields(t, accountDescribe())
	var copied []string
	m.deps.Clipboard = func(s string) error { copied = append(copied, s); return nil }
	sv.fieldTable.FocusRowWhere(0, "OwnerId")
	drive(t, m, key("enter"))
	drive(t, m, key("y"))

	if len(copied) != 1 || !strings.Contains(copied[0], "OwnerId") || !strings.Contains(copied[0], "User") {
		t.Fatalf("y should copy the card body for a non-picklist field, got %v", copied)
	}
}

func TestFieldCardScrollsAndClosesOnEsc(t *testing.T) {
	m, sv := schemaWithFields(t, accountDescribe())
	m.height = 16 // short enough that the picklist runs off the card
	sv.fieldTable.FocusRowWhere(0, "Industry")
	drive(t, m, key("enter"))
	m.View() // the card sizes its viewport when it renders

	drive(t, m, key("j"))
	if sv.card.vp.YOffset == 0 {
		t.Error("a card taller than the screen should scroll")
	}

	drive(t, m, key("esc"))
	if sv.card != nil {
		t.Fatal("esc should close the card")
	}
	if !sv.inFields {
		t.Fatal("esc should close only the card, leaving the fields table up")
	}
	if !strings.Contains(m.View(), "Industry") {
		t.Errorf("the fields table should be back:\n%s", m.View())
	}
}

func TestFieldCardKeepsFieldsTableFiltered(t *testing.T) {
	m, sv := schemaWithFields(t, accountDescribe())
	sv.fieldTable.filter = "industry"
	sv.fieldTable.applyFilter()
	drive(t, m, key("enter"))
	if sv.card == nil {
		t.Fatal("enter should open the card for the only filtered row")
	}
	drive(t, m, key("esc"))
	if sv.fieldTable.filter != "industry" {
		t.Errorf("closing the card should not clear the filter, got %q", sv.fieldTable.filter)
	}
}

func TestFieldBadges(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field api.Field
		want  string
	}{
		{"writable", api.Field{Createable: true, Updateable: true}, ""},
		{"create only", api.Field{Createable: true}, ""},
		{"read only", api.Field{}, "readonly"},
		{"formula", api.Field{Calculated: true}, "formula"},
		{"formula is not also labelled readonly", api.Field{Calculated: true, Updateable: false}, "formula"},
	} {
		if got := fieldBadges(tc.field); got != tc.want {
			t.Errorf("%s: fieldBadges = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestFieldTypeLabelNamesFormulasOfEveryType(t *testing.T) {
	// A formula carries a scalar type as well, and formula fields report
	// length 0 — so testing the type first labelled a string formula
	// "string(0)".
	cases := []struct {
		field api.Field
		want  string
	}{
		{api.Field{Type: "string", Length: 0, Calculated: true}, "formula(string)"},
		{api.Field{Type: "picklist", Calculated: true}, "formula(picklist)"},
		{api.Field{Type: "currency", Calculated: true}, "formula(currency)"},
		{api.Field{Type: "string", Length: 255}, "string(255)"},
	}
	for _, tc := range cases {
		if got := fieldTypeLabel(tc.field); got != tc.want {
			t.Errorf("fieldTypeLabel(%+v) = %q, want %q", tc.field, got, tc.want)
		}
	}
}

func TestFieldBadgesIncludeUniqueAndExternalID(t *testing.T) {
	got := fieldBadges(api.Field{Unique: true, ExternalID: true, Createable: true, Updateable: true})
	if !strings.Contains(got, "unique") || !strings.Contains(got, "extId") {
		t.Errorf("badges = %q, want unique and extId", got)
	}
	if got := fieldBadges(api.Field{Calculated: true}); got != "formula" {
		t.Errorf("a plain formula field should badge only formula, got %q", got)
	}
}
