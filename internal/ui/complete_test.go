package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func queryViewFor(t *testing.T, m *Model) *queryView {
	t.Helper()
	drive(t, m, switchViewMsg{id: ViewQuery})
	qv, ok := m.views[ViewQuery].(*queryView)
	if !ok {
		t.Fatal("query view not active")
	}
	if cmd := qv.Init(); cmd != nil {
		drive(t, m, cmd())
	}
	return qv
}

func TestCompletionAfterFrom(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	loadAllOrgs(t, m)
	qv := queryViewFor(t, m)

	// Ambiguous prefix: the popup lists every queryable object.
	qv.setEditorText("SELECT Id FROM ")
	qv.editor.CursorEnd()
	drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlAt})

	if !qv.popup.open() {
		t.Fatalf("popup should offer objects after FROM; view:\n%s", m.View())
	}
	if got := qv.popup.selected().Text; got != "Account" {
		t.Fatalf("first candidate = %q, want Account", got)
	}
	if !strings.Contains(m.View(), "Account") {
		t.Fatalf("popup should be visible:\n%s", m.View())
	}
	drive(t, m, key("enter"))
	if got := qv.editor.Value(); got != "SELECT Id FROM Account" {
		t.Fatalf("accepted completion produced %q", got)
	}
	if qv.popup.open() {
		t.Error("popup should close after acceptance")
	}
}

func TestCompletionSingleMatchInsertsDirectly(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	loadAllOrgs(t, m)
	qv := queryViewFor(t, m)

	qv.setEditorText("SELECT Id FROM Acc")
	qv.editor.CursorEnd()
	drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlAt})
	if qv.popup.open() {
		t.Error("an unambiguous match should not require a choice")
	}
	if got := qv.editor.Value(); got != "SELECT Id FROM Account" {
		t.Fatalf("editor = %q", got)
	}
}

func TestCompletionServedAfterSchemaArrives(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	loadAllOrgs(t, m)
	drive(t, m, switchViewMsg{id: ViewQuery})
	qv := m.views[ViewQuery].(*queryView)
	qv.comp.reset() // simulate a cold cache: nothing known yet

	qv.setEditorText("SELECT Id FROM ")
	qv.editor.CursorEnd()
	drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlAt})
	if !qv.popup.open() {
		t.Fatalf("request made while loading must be honored once schema lands; view:\n%s", m.View())
	}
}

func TestCompletionFieldsFromLaterFromClause(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	loadAllOrgs(t, m)
	qv := queryViewFor(t, m)

	// Cursor inside SELECT, object named later in the statement.
	qv.setEditorText("SELECT Nam FROM Account")
	qv.editor.CursorStart()
	for i := 0; i < len("SELECT Nam"); i++ {
		drive(t, m, tea.KeyMsg{Type: tea.KeyRight})
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlAt})

	// "Nam" resolves to exactly one field of the object named after the
	// cursor, so it completes in place without a popup.
	if got := qv.editor.Value(); got != "SELECT Name FROM Account" {
		t.Fatalf("field completion should resolve from the later FROM clause, editor = %q\nview:\n%s", got, m.View())
	}
}

func TestCompletionPopupShowsFieldTypes(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	loadAllOrgs(t, m)
	qv := queryViewFor(t, m)

	qv.setEditorText("SELECT  FROM Account")
	qv.editor.CursorStart()
	for i := 0; i < len("SELECT "); i++ {
		drive(t, m, tea.KeyMsg{Type: tea.KeyRight})
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlAt})
	if !qv.popup.open() {
		t.Fatalf("empty prefix should list all fields; view:\n%s", m.View())
	}
	view := m.View()
	if !strings.Contains(view, "Name") || !strings.Contains(view, "string(255)") {
		t.Fatalf("candidates should show names and types:\n%s", view)
	}
	if !strings.Contains(view, "enter/tab accept") {
		t.Fatalf("popup should explain its keys:\n%s", view)
	}
}

func TestCompletionRelationshipCandidate(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	loadAllOrgs(t, m)
	qv := queryViewFor(t, m)

	qv.setEditorText("SELECT Own FROM Account")
	qv.editor.CursorStart()
	for i := 0; i < len("SELECT Own"); i++ {
		drive(t, m, tea.KeyMsg{Type: tea.KeyRight})
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlAt})
	if !qv.popup.open() {
		t.Fatalf("popup should open for Own; view:\n%s", m.View())
	}
	var texts []string
	for _, item := range qv.popup.items {
		texts = append(texts, item.Text)
	}
	joined := strings.Join(texts, ",")
	if !strings.Contains(joined, "OwnerId") || !strings.Contains(joined, "Owner.") {
		t.Fatalf("expected both the field and its relationship path, got %v", texts)
	}
}

func TestCompletionUnavailableSpotReportsWhy(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	loadAllOrgs(t, m)
	qv := queryViewFor(t, m)

	qv.setEditorText("SELECT Id FROM Account LIMIT 5")
	qv.editor.CursorEnd()
	drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlAt})
	if qv.popup.open() {
		t.Error("no completions belong after LIMIT")
	}
	if !strings.Contains(m.View(), "no completions here") {
		t.Fatalf("an explicit request should explain itself:\n%s", m.View())
	}
}
