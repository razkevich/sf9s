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

func (v *orgsView) Hints() string {
	return "enter use+query • space use • o open • y token • Y url • R reload • ? help"
}

func (v *orgsView) Capturing() bool { return v.table.Filtering() }

func (v *orgsView) Init() tea.Cmd { return nil }

func (v *orgsView) setOrgs(orgs []sfcli.Org) {
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
		expires := o.ExpirationDate
		rows[i] = []string{marker, o.Title(), o.Username, o.Type(), o.ConnectedStatus, expires, o.InstanceURL}
	}
	v.table.SetData([]string{" ", "Alias", "Username", "Type", "Status", "Expires", "Instance URL"}, rows)
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
			if org := v.selected(); org != nil {
				return func() tea.Msg { return useOrgMsg{org: *org, jump: msg.String() == "enter"} }
			}
		case "o":
			return v.withCreds("opening browser…", func(token, instanceURL string) statusMsg {
				url := instanceURL + "/secur/frontdoor.jsp?sid=" + token
				if err := v.app.deps.OpenURL(url); err != nil {
					return statusMsg{kind: statusError, text: err.Error()}
				}
				return statusMsg{kind: statusOK, text: "opened in browser"}
			})
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
			v.app.loadingOrgs = true
			return v.app.loadOrgs()
		}
	}
	return nil
}

// withCreds resolves fresh credentials for the selected org off the UI
// thread, then applies fn and reports its toast.
func (v *orgsView) withCreds(pending string, fn func(token, instanceURL string) statusMsg) tea.Cmd {
	org := v.selected()
	if org == nil {
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
	if v.busy {
		head += "  " + v.app.spin.View()
	}
	return head + "\n" + v.table.View()
}
