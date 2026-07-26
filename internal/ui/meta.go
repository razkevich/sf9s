package ui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/razkevich/sf9s/internal/sfcli"
)

type metaView struct {
	app *Model

	typeTable *dataTable
	compTable *dataTable
	types     []sfcli.MetadataType
	current   string
	inComps   bool
	loading   bool
	gen       int
}

func newMetaView(app *Model) *metaView {
	v := &metaView{app: app, typeTable: newDataTable(), compTable: newDataTable()}
	v.typeTable.emptyText = "no metadata types"
	v.compTable.emptyText = "no components"
	return v
}

func (v *metaView) Title() string { return "meta" }

func (v *metaView) Keys() []keyHint {
	if v.inComps {
		return []keyHint{{"y", "copy name"}, {"s", "sort column"}, {"esc", "back"}, {"/", "filter"}}
	}
	return []keyHint{{"enter", "list components"}, {"R", "reload"}, {"/", "filter"}}
}

func (v *metaView) Bail() bool {
	table := v.typeTable
	if v.inComps {
		table = v.compTable
	}
	switch {
	case table.ClearFilter():
	case v.inComps:
		v.inComps = false
	default:
		return false
	}
	return true
}

func (v *metaView) Capturing() bool {
	return v.typeTable.Filtering() || v.compTable.Filtering()
}

type metaTypesMsg struct {
	gen   int
	types []sfcli.MetadataType
	err   error
}

type metaCompsMsg struct {
	gen   int
	comps []sfcli.MetadataComponent
	err   error
}

func (v *metaView) Init() tea.Cmd {
	var cached []sfcli.MetadataType
	if v.app.deps.Store.CacheGet("metadata-types-"+v.app.current.OrgID, describeTTL, &cached) && len(cached) > 0 {
		v.setTypes(cached)
		return nil
	}
	return v.fetchTypes()
}

func (v *metaView) fetchTypes() tea.Cmd {
	key := "metadata-types-" + v.app.current.OrgID
	v.loading = true
	v.gen = v.app.nextGen()
	gen := v.gen
	sf := v.app.deps.SF
	store := v.app.deps.Store
	username := v.app.current.Username
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		types, err := sf.MetadataTypes(ctx, username)
		if err == nil {
			store.CachePut(key, types)
		}
		return metaTypesMsg{gen: gen, types: types, err: err}
	}
}

func (v *metaView) setTypes(types []sfcli.MetadataType) {
	v.types = types
	rows := make([][]string, len(types))
	for i, t := range types {
		folder := ""
		if t.InFolder {
			folder = "✔"
		}
		rows[i] = []string{t.XMLName, t.Suffix, t.DirectoryName, folder}
	}
	v.typeTable.SetData([]string{"Type", "Suffix", "Directory", "In folder"}, rows)
}

func (v *metaView) loadComponents(metadataType string) tea.Cmd {
	v.loading = true
	v.gen = v.app.nextGen()
	gen := v.gen
	sf := v.app.deps.SF
	username := v.app.current.Username
	v.current = metadataType
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		comps, err := sf.ListMetadata(ctx, username, metadataType)
		return metaCompsMsg{gen: gen, comps: comps, err: err}
	}
}

func (v *metaView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case metaTypesMsg:
		if msg.gen != v.gen {
			return nil
		}
		v.loading = false
		if msg.err != nil {
			return toastErr(msg.err)
		}
		v.setTypes(msg.types)
		return nil

	case metaCompsMsg:
		if msg.gen != v.gen {
			return nil
		}
		v.loading = false
		if msg.err != nil {
			return toastErr(msg.err)
		}
		rows := make([][]string, len(msg.comps))
		for i, c := range msg.comps {
			rows[i] = []string{c.FullName, c.LastModifiedByName, shortDate(c.LastModifiedDate), c.CreatedByName, shortDate(c.CreatedDate), c.ManageableState}
		}
		v.compTable.SetData([]string{"Full name", "Modified by", "Modified", "Created by", "Created", "State"}, rows)
		v.inComps = true
		return toast(statusOK, fmt.Sprintf("%d components of %s", len(msg.comps), v.current))

	case tea.KeyMsg:
		table := v.typeTable
		if v.inComps {
			table = v.compTable
		}
		if table.Update(msg) {
			return nil
		}
		switch msg.String() {
		case "enter":
			if !v.inComps && !v.loading {
				if name := v.typeTable.Cell("Type"); name != "" {
					return v.loadComponents(name)
				}
			}
		case "R":
			if !v.inComps && !v.loading {
				return v.fetchTypes()
			}
		case "y":
			if v.inComps {
				if name := v.compTable.Cell("Full name"); name != "" {
					if err := v.app.deps.Clipboard(name); err != nil {
						return toastErr(err)
					}
					return toast(statusOK, "copied: "+name)
				}
			}
		}
	}
	return nil
}

// shortDate renders a Salesforce timestamp in the reader's own timezone.
// "2026-07-24T13:41:15.000+0000" answers no question a human asked.
func shortDate(iso string) string {
	t, ok := parseSalesforceTime(iso)
	if !ok {
		return iso
	}
	return t.Local().Format("2006-01-02 15:04")
}

// relDate adds how long ago it was, which is what "recent" means to a person.
func relDate(iso string) string {
	t, ok := parseSalesforceTime(iso)
	if !ok {
		return iso
	}
	return t.Local().Format("2006-01-02 15:04") + " (" + humanSince(time.Since(t)) + ")"
}

func parseSalesforceTime(iso string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.000-0700", "2006-01-02"} {
		if t, err := time.Parse(layout, iso); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func humanSince(d time.Duration) string {
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	}
}

func (v *metaView) View(width, height int) string {
	head := styleTitle.Render("Metadata")
	if v.inComps {
		head = styleTitle.Render("Metadata › "+v.current) + styleDim.Render(fmt.Sprintf("  %d components", v.compTable.RowCount()))
	} else {
		head += styleDim.Render(fmt.Sprintf("  %d types", v.typeTable.RowCount()))
	}
	if v.loading {
		head += "  " + v.app.spin.View() + styleDim.Render(" listing (sf CLI)…")
	}
	table := v.typeTable
	if v.inComps {
		table = v.compTable
	}
	table.SetSize(width, height-1)
	return head + "\n" + table.View()
}
