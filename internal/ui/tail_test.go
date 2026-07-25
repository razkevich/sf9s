package ui

import (
	"strings"
	"testing"

	"github.com/razkevich/sf9s/internal/api"
	"github.com/razkevich/sf9s/internal/sfcli"
)

func logsViewFor(t *testing.T, m *Model) *logsView {
	t.Helper()
	drive(t, m, switchViewMsg{id: ViewLogs})
	lv, ok := m.views[ViewLogs].(*logsView)
	if !ok {
		t.Fatal("logs view not active")
	}
	return lv
}

func logResult(ids ...string) *api.Result {
	rows := make([][]string, len(ids))
	for i, id := range ids {
		rows[i] = []string{id, "Alex", "/apex/execute", "Success"}
	}
	return &api.Result{
		TotalSize: len(ids),
		Done:      true,
		Columns:   []string{"Id", "LogUser.Name", "Operation", "Status"},
		Rows:      rows,
	}
}

func TestTailOnlyReportsUnseenLogs(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	loadAllOrgs(t, m)
	lv := logsViewFor(t, m)

	// Logs already listed before tailing must not be announced as new.
	drive(t, m, logsListMsg{gen: lv.gen, res: logResult("07L1", "07L2")})
	drive(t, m, key("t"))
	if !lv.tailing {
		t.Fatal("t should start tailing")
	}
	gen := lv.tailGen

	drive(t, m, tailResultMsg{gen: gen, res: logResult("07L1", "07L2")})
	if got := lv.table.RowCount(); got != 2 {
		t.Fatalf("unchanged poll should not add rows, got %d", got)
	}

	// Put the cursor on a specific log before new ones arrive.
	drive(t, m, key("j"))
	selected := lv.table.Cell("Id")
	if selected != "07L2" {
		t.Fatalf("precondition: cursor on 07L2, got %q", selected)
	}

	drive(t, m, tailResultMsg{gen: gen, res: logResult("07L3", "07L1", "07L2")})
	if got := lv.table.RowCount(); got != 3 {
		t.Fatalf("new log should be added, rows = %d", got)
	}
	if top := lv.result.Rows[0][0]; top != "07L3" {
		t.Fatalf("newest log should be listed first, got %q", top)
	}
	if got := lv.table.Cell("Id"); got != selected {
		t.Errorf("cursor should follow its log as rows shift, got %q want %q", got, selected)
	}
	if !strings.Contains(m.View(), "1 new log(s)") {
		t.Fatalf("arrival should be announced:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "tailing") {
		t.Fatalf("header should show the tail is live:\n%s", m.View())
	}
}

func TestTailStopsOnKeyNavigateAndOrgSwitch(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	loadAllOrgs(t, m)
	lv := logsViewFor(t, m)
	drive(t, m, logsListMsg{gen: lv.gen, res: logResult("07L1")})

	drive(t, m, key("t"))
	gen := lv.tailGen
	drive(t, m, key("t"))
	if lv.tailing {
		t.Error("second t should stop tailing")
	}
	// A poll that was already in flight must be ignored once stopped.
	drive(t, m, tailResultMsg{gen: gen, res: logResult("07L9", "07L1")})
	if lv.table.RowCount() != 1 {
		t.Error("stopped tail must not append late results")
	}

	drive(t, m, key("t"))
	drive(t, m, switchViewMsg{id: ViewLimits})
	if lv.tailing {
		t.Error("navigating away should stop the tail")
	}

	drive(t, m, switchViewMsg{id: ViewLogs})
	lv2 := m.views[ViewLogs].(*logsView)
	drive(t, m, key("t"))
	m.setOrg(sfcli.Org{Username: "other@example.com", OrgID: "00DX"})
	if _, ok := m.views[ViewLogs]; ok {
		t.Error("org switch should discard the logs view (and its tail)")
	}
	if lv2.tailing {
		// The view is gone, but a lingering tail would keep polling the old org.
		t.Error("org switch should stop tailing")
	}
}

func TestTailErrorStopsAndReports(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	loadAllOrgs(t, m)
	lv := logsViewFor(t, m)
	drive(t, m, key("t"))
	drive(t, m, tailResultMsg{gen: lv.tailGen, err: &api.APIError{StatusCode: 403, Message: "insufficient access"}})
	if lv.tailing {
		t.Error("a failed poll should stop the tail rather than loop")
	}
	if !strings.Contains(m.View(), "tail stopped: insufficient access") {
		t.Fatalf("failure should be reported once:\n%s", m.View())
	}
}

// Regression: the delete prompt used to re-read the cursor when the user
// answered, so a log arriving in between made `y` delete the wrong log.
func TestTailArrivalDoesNotRedirectDelete(t *testing.T) {
	srv := testServer(t)
	m := newTestModel(t, srv.URL)
	loadAllOrgs(t, m)
	lv := logsViewFor(t, m)
	drive(t, m, logsListMsg{gen: lv.gen, res: logResult("07L1", "07L2")})

	drive(t, m, key("t"))
	drive(t, m, key("d")) // confirm prompt for the focused log
	if lv.confirmDeleteID != "07L1" {
		t.Fatalf("prompt should capture the focused log, got %q", lv.confirmDeleteID)
	}
	if !strings.Contains(m.View(), "delete log 07L1?") {
		t.Fatalf("prompt should name the log it will delete:\n%s", m.View())
	}

	// A new log arrives while the prompt is up.
	drive(t, m, tailResultMsg{gen: lv.tailGen, res: logResult("07LNEW", "07L1", "07L2")})
	if lv.confirmDeleteID != "07L1" {
		t.Fatalf("pending delete target must not change, got %q", lv.confirmDeleteID)
	}

	drive(t, m, key("y"))
	if !strings.Contains(m.View(), "log deleted") && !strings.Contains(m.View(), "not found") {
		t.Logf("view after delete:\n%s", m.View())
	}
	if lv.confirmDelete {
		t.Error("prompt should close after answering")
	}
}
