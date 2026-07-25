package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/razkevich/sf9s/internal/api"
)

const describeTTL = 15 * time.Minute

type schemaView struct {
	app *Model

	objTable   *dataTable
	fieldTable *dataTable
	objects    []api.SObjectSummary
	describe   *api.SObjectDescribe
	inFields   bool
	loading    bool
	gen        int
}

func newSchemaView(app *Model) *schemaView {
	v := &schemaView{app: app, objTable: newDataTable(), fieldTable: newDataTable()}
	v.objTable.emptyText = "no objects"
	return v
}

func (v *schemaView) Title() string { return "schema" }

func (v *schemaView) Hints() string {
	if v.inFields {
		return "y copy field • c build query • esc back • / filter"
	}
	return "enter fields • y copy name • c build query • R reload • / filter"
}

func (v *schemaView) Capturing() bool {
	return v.objTable.Filtering() || v.fieldTable.Filtering()
}

type describeGlobalMsg struct {
	gen     int
	objects []api.SObjectSummary
	err     error
}

type describeObjectMsg struct {
	gen      int
	describe *api.SObjectDescribe
	err      error
}

func (v *schemaView) cacheKeyGlobal() string {
	return "describe-global-" + v.app.current.OrgID
}

func (v *schemaView) Init() tea.Cmd {
	var cached []api.SObjectSummary
	if v.app.deps.Store.CacheGet(v.cacheKeyGlobal(), describeTTL, &cached) && len(cached) > 0 {
		v.setObjects(cached)
		return nil
	}
	return v.fetchObjects()
}

func (v *schemaView) fetchObjects() tea.Cmd {
	v.loading = true
	v.gen = v.app.nextGen()
	gen := v.gen
	client := v.app.client
	store := v.app.deps.Store
	key := v.cacheKeyGlobal()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		objects, err := client.DescribeGlobal(ctx)
		if err == nil {
			store.CachePut(key, objects)
		}
		return describeGlobalMsg{gen: gen, objects: objects, err: err}
	}
}

func (v *schemaView) setObjects(objects []api.SObjectSummary) {
	v.objects = objects
	rows := make([][]string, 0, len(objects))
	for _, o := range objects {
		if !o.Queryable {
			continue
		}
		custom := ""
		if o.Custom {
			custom = "✔"
		}
		rows = append(rows, []string{o.Name, o.Label, custom, o.KeyPrefix})
	}
	v.objTable.SetData([]string{"API Name", "Label", "Custom", "Prefix"}, rows)
}

func (v *schemaView) loadDescribe(name string) tea.Cmd {
	key := "describe-" + v.app.current.OrgID + "-" + name
	var cached api.SObjectDescribe
	if v.app.deps.Store.CacheGet(key, describeTTL, &cached) && cached.Name != "" {
		v.showFields(&cached)
		return nil
	}
	v.loading = true
	v.gen = v.app.nextGen()
	gen := v.gen
	client := v.app.client
	store := v.app.deps.Store
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		describe, err := client.DescribeSObject(ctx, name)
		if err == nil {
			store.CachePut(key, describe)
		}
		return describeObjectMsg{gen: gen, describe: describe, err: err}
	}
}

func fieldTypeLabel(f api.Field) string {
	switch f.Type {
	case "reference":
		return "reference→" + strings.Join(f.ReferenceTo, ",")
	case "picklist", "multipicklist":
		return fmt.Sprintf("%s(%d)", f.Type, len(f.PicklistValues))
	case "string", "textarea":
		return f.Type + "(" + strconv.Itoa(f.Length) + ")"
	default:
		if f.Calculated {
			return "formula(" + f.Type + ")"
		}
		return f.Type
	}
}

func (v *schemaView) showFields(d *api.SObjectDescribe) {
	v.describe = d
	rows := make([][]string, len(d.Fields))
	for i, f := range d.Fields {
		flags := ""
		if f.Nillable {
			flags += "n"
		}
		if f.Createable {
			flags += "c"
		}
		if f.Updateable {
			flags += "u"
		}
		var pick []string
		for _, p := range f.PicklistValues {
			if p.Active {
				pick = append(pick, p.Value)
			}
		}
		rows[i] = []string{f.Name, fieldTypeLabel(f), f.Label, flags, strings.Join(pick, ", ")}
	}
	v.fieldTable.SetData([]string{"Field", "Type", "Label", "Flags", "Picklist values"}, rows)
	v.inFields = true
}

func (v *schemaView) buildQuery(objectName string, fields []api.Field) tea.Cmd {
	names := make([]string, 0, min(len(fields), 100))
	for _, f := range fields {
		names = append(names, f.Name)
		if len(names) == 100 {
			break
		}
	}
	if len(names) == 0 {
		names = []string{"Id"}
	}
	soql := "SELECT " + strings.Join(names, ", ") + " FROM " + objectName + " ORDER BY LastModifiedDate DESC LIMIT 50"
	return func() tea.Msg { return prefillQueryMsg{soql: soql} }
}

func (v *schemaView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case describeGlobalMsg:
		if msg.gen != v.gen {
			return nil
		}
		v.loading = false
		if msg.err != nil {
			return toastErr(msg.err)
		}
		v.setObjects(msg.objects)
		return nil

	case describeObjectMsg:
		if msg.gen != v.gen {
			return nil
		}
		v.loading = false
		if msg.err != nil {
			return toastErr(msg.err)
		}
		v.showFields(msg.describe)
		return nil

	case tea.KeyMsg:
		table := v.objTable
		if v.inFields {
			table = v.fieldTable
		}
		if table.Update(msg) {
			return nil
		}
		switch msg.String() {
		case "enter":
			if !v.inFields {
				if name := v.objTable.Cell("API Name"); name != "" {
					return v.loadDescribe(name)
				}
			}
		case "esc":
			if v.inFields {
				v.inFields = false
				return nil
			}
			return goBack
		case "R":
			if !v.inFields && !v.loading {
				return v.fetchObjects()
			}
		case "y":
			cell := v.objTable.Cell("API Name")
			what := "object"
			if v.inFields {
				cell = v.fieldTable.Cell("Field")
				what = "field"
			}
			if cell != "" {
				if err := v.app.deps.Clipboard(cell); err != nil {
					return toastErr(err)
				}
				return toast(statusOK, what+" name copied: "+cell)
			}
		case "c":
			if v.inFields && v.describe != nil {
				return v.buildQuery(v.describe.Name, v.describe.Fields)
			}
			if name := v.objTable.Cell("API Name"); name != "" {
				return func() tea.Msg {
					return prefillQueryMsg{soql: "SELECT Id, Name FROM " + name + " ORDER BY LastModifiedDate DESC LIMIT 50"}
				}
			}
		}
	}
	return nil
}

func (v *schemaView) View(width, height int) string {
	head := styleTitle.Render("Schema")
	if v.inFields && v.describe != nil {
		head = styleTitle.Render("Schema › "+v.describe.Name) +
			styleDim.Render(fmt.Sprintf("  %d fields • prefix %s", len(v.describe.Fields), v.describe.KeyPrefix))
	} else {
		head += styleDim.Render(fmt.Sprintf("  %d queryable objects", v.objTable.RowCount()))
	}
	if v.loading {
		head += "  " + v.app.spin.View() + styleDim.Render(" describing…")
	}
	table := v.objTable
	if v.inFields {
		table = v.fieldTable
	}
	table.SetSize(width, height-1)
	return head + "\n" + table.View()
}
