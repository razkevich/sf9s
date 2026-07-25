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

func (v *metaView) Hints() string {
	if v.inComps {
		return "y copy name • esc back • / filter"
	}
	return "enter list components • / filter"
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
	key := "metadata-types-" + v.app.current.OrgID
	var cached []sfcli.MetadataType
	if v.app.deps.Store.CacheGet(key, describeTTL, &cached) && len(cached) > 0 {
		v.setTypes(cached)
		return nil
	}
	v.loading = true
	v.gen++
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
	v.gen++
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
		case "esc":
			if v.inComps {
				v.inComps = false
				return nil
			}
			return goBack
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

func shortDate(iso string) string {
	if t, err := time.Parse(time.RFC3339, iso); err == nil {
		return t.Local().Format("2006-01-02 15:04")
	}
	if len(iso) >= 10 {
		return iso[:10]
	}
	return iso
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
