package ui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/razkevich/sf9s/internal/sfcli"
)

type orgsView struct {
	app   *Model
	table *dataTable
	orgs  []sfcli.Org
	busy  bool
}

func newOrgsView(app *Model) *orgsView {
	v := &orgsView{app: app, table: newDataTable()}
	v.table.emptyText = "no orgs — sf org login web --alias my-org"
	v.table.SetCellStyle("Status", statusCellStyle)
	v.table.SetCellStyle("Type", orgTypeCellStyle)
	v.setOrgs(app.orgs)
	return v
}

func (v *orgsView) Title() string { return "orgs" }

func (v *orgsView) Keys() []keyHint {
	return []keyHint{
		{"enter", "use org + query"},
		{"space", "use org"},
		{"o", "open in browser"},
		{"y", "copy token"},
		{"Y", "copy URL"},
		{"R", "reload orgs"},
		{"s", "sort column"},
	}
}

func (v *orgsView) Bail() bool { return v.table.ClearFilter() }

func (v *orgsView) Capturing() bool { return v.table.Filtering() }

func (v *orgsView) Init() tea.Cmd { return nil }

// setOrgs refreshes the table while preserving the user's cursor and filter,
// since statuses stream in after the first paint.
func (v *orgsView) setOrgs(orgs []sfcli.Org) {
	cursorUser := ""
	if row := v.table.CurrentRow(); row != nil {
		cursorUser = row[2]
	}
	v.orgs = orgs
	rows := make([][]string, len(orgs))
	for i, o := range orgs {
		marker := ""
		if o.IsDefault {
			marker = "●"
		}
		if o.IsDefaultHub {
			marker += "★"
		}
		status := o.ConnectedStatus
		if status == "" {
			status = "checking…"
		}
		// Prefer what the org said about itself over the CLI's coarse flags:
		// "production" and "developer" both look like "org" to the CLI.
		kind := o.Type()
		if info := v.app.orgInfo[o.OrgID]; info != nil {
			if info.Production() {
				kind = "PRODUCTION"
			} else if e := info.Edition(); e != "" {
				kind = e
			}
		}
		rows[i] = []string{marker, o.Title(), o.Username, kind, status, o.ExpirationDate, o.InstanceURL}
	}
	v.table.SetDataPreservingView([]string{" ", "Alias", "Username", "Type", "Status", "Expires", "Instance URL"}, rows)
	if cursorUser != "" {
		v.table.FocusRowWhere(2, cursorUser)
	}
}

func (v *orgsView) selected() *sfcli.Org {
	row := v.table.CurrentRow()
	if row == nil {
		return nil
	}
	for i := range v.orgs {
		if v.orgs[i].Username == row[2] {
			return &v.orgs[i]
		}
	}
	return nil
}

type orgActionMsg struct {
	toast statusMsg
}

func (v *orgsView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case orgActionMsg:
		v.busy = false
		return toast(msg.toast.kind, msg.toast.text)
	case tea.KeyMsg:
		if v.table.Update(msg) {
			return nil
		}
		switch msg.String() {
		case "enter", " ":
			// Selection is applied synchronously: routing it through a
			// command would let the next keystrokes reach the old view,
			// silently swallowing the first characters a user types.
			if org := v.selected(); org != nil {
				v.app.setOrg(*org)
				cmds := []tea.Cmd{toast(statusOK, "using org "+org.Title()), v.app.takePendingOrgInfo()}
				if msg.String() == "enter" {
					cmds = append(cmds, v.app.navigate(ViewQuery))
				}
				return tea.Batch(cmds...)
			}
		case "s":
			if label := v.table.SortByCursorColumn(); label != "" {
				return toast(statusInfo, "sorted by "+label)
			}
			return toast(statusInfo, "sort cleared")
		case "o":
			return v.openOrg()
		case "y":
			return v.withCreds("fetching token…", func(token, _ string) statusMsg {
				if err := v.app.deps.Clipboard(token); err != nil {
					return statusMsg{kind: statusError, text: err.Error()}
				}
				return statusMsg{kind: statusOK, text: "access token copied to clipboard"}
			})
		case "Y":
			if org := v.selected(); org != nil {
				if err := v.app.deps.Clipboard(org.InstanceURL); err != nil {
					return toastErr(err)
				}
				return toast(statusOK, "instance URL copied to clipboard")
			}
		case "R":
			if v.busy {
				return nil
			}
			return tea.Batch(toast(statusInfo, "reloading orgs…"), v.app.loadOrgs())
		}
	}
	return nil
}

// openOrg delegates to `sf org open` so the session token never passes
// through sf9s' process arguments or a URL we build.
func (v *orgsView) openOrg() tea.Cmd {
	org := v.selected()
	if org == nil || v.busy {
		return nil
	}
	v.busy = true
	sf := v.app.deps.SF
	username := org.Username
	return tea.Batch(
		toast(statusInfo, "opening "+org.Title()+" in your browser…"),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := sf.OpenOrg(ctx, username); err != nil {
				return orgActionMsg{toast: statusMsg{kind: statusError, text: err.Error()}}
			}
			return orgActionMsg{toast: statusMsg{kind: statusOK, text: "opened in browser"}}
		},
	)
}

// withCreds resolves fresh credentials for the selected org off the UI
// thread, then applies fn and reports its toast.
func (v *orgsView) withCreds(pending string, fn func(token, instanceURL string) statusMsg) tea.Cmd {
	org := v.selected()
	if org == nil || v.busy {
		return nil
	}
	v.busy = true
	sf := v.app.deps.SF
	username := org.Username
	return tea.Batch(
		toast(statusInfo, pending),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			creds, err := sf.Credentials(ctx, username)
			if err != nil {
				return orgActionMsg{toast: statusMsg{kind: statusError, text: err.Error()}}
			}
			return orgActionMsg{toast: fn(creds.AccessToken, creds.InstanceURL)}
		},
	)
}

func (v *orgsView) View(width, height int) string {
	v.table.SetSize(width, height-1)
	head := styleTitle.Render(fmt.Sprintf("Authenticated orgs (%d)", len(v.orgs)))
	if v.app.awaitingStatuses {
		head += "  " + v.app.spin.View() + styleDim.Render(" checking connections…")
	} else if v.busy {
		head += "  " + v.app.spin.View()
	}
	return head + "\n" + v.table.View()
}
