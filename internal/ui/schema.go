package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
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
	card       *fieldCard
}

func newSchemaView(app *Model) *schemaView {
	v := &schemaView{app: app, objTable: newDataTable(), fieldTable: newDataTable()}
	v.objTable.emptyText = "no objects"
	return v
}

func (v *schemaView) Title() string { return "schema" }

func (v *schemaView) Keys() []keyHint {
	switch {
	case v.card != nil:
		return []keyHint{{"y", "copy values"}, {"↑↓", "scroll"}, {"esc", "close"}}
	case v.inFields:
		return []keyHint{{"enter", "field detail"}, {"y", "copy field"}, {"c", "build query"}, {"s", "sort column"}, {"esc", "back"}, {"/", "filter"}}
	}
	return []keyHint{{"enter", "fields"}, {"y", "copy name"}, {"c", "build query"}, {"/", "filter"}}
}

func (v *schemaView) Bail() bool {
	table := v.objTable
	if v.inFields {
		table = v.fieldTable
	}
	switch {
	case v.card != nil:
		v.card = nil
	case table.ClearFilter():
	case v.inFields:
		v.inFields = false
	default:
		return false
	}
	return true
}

func (v *schemaView) Capturing() bool {
	return v.objTable.Filtering() || v.fieldTable.Filtering() || v.card != nil
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
	// Checked first: a formula field carries a scalar type too, so testing
	// the type first would label a string formula "string(0)".
	if f.Calculated {
		return "formula(" + f.Type + ")"
	}
	switch f.Type {
	case "reference":
		return "reference→" + strings.Join(f.ReferenceTo, ",")
	case "picklist", "multipicklist":
		return fmt.Sprintf("%s(%d)", f.Type, len(f.PicklistValues))
	case "string", "textarea":
		return f.Type + "(" + strconv.Itoa(f.Length) + ")"
	default:
		return f.Type
	}
}

// fieldBadges spells out the properties worth scanning for. Only the ones
// the describe subset actually carries: unique and externalId are in the REST
// response but not in api.Field, so they cannot be shown yet.
func fieldBadges(f api.Field) string {
	var badges []string
	switch {
	case f.Calculated:
		// A formula is read-only by construction; saying both is noise.
		badges = append(badges, "formula")
	case !f.Createable && !f.Updateable:
		badges = append(badges, "readonly")
	}
	if f.Unique {
		badges = append(badges, "unique")
	}
	if f.ExternalID {
		badges = append(badges, "extId")
	}
	return strings.Join(badges, " ")
}

func (v *schemaView) showFields(d *api.SObjectDescribe) {
	v.describe = d
	rows := make([][]string, len(d.Fields))
	for i, f := range d.Fields {
		required := ""
		if !f.Nillable {
			required = "✔"
		}
		// Only active values preview here — the card behind enter lists the
		// inactive ones too, and this cell is truncated to a column anyway.
		var pick []string
		for _, p := range f.PicklistValues {
			if p.Active {
				pick = append(pick, p.Value)
			}
		}
		rows[i] = []string{f.Name, fieldTypeLabel(f), required, fieldBadges(f), f.Label, strings.Join(pick, ", ")}
	}
	// Required and the badges sit ahead of Label so they survive an 80-column
	// terminal without sideways paging; they are why this table is read.
	v.fieldTable.SetData([]string{"Field", "Type", "Required", "Attributes", "Label", "Picklist values"}, rows)
	v.inFields = true
}

// currentField resolves the highlighted row back to its describe entry — the
// table holds only rendered strings.
func (v *schemaView) currentField() *api.Field {
	name := v.fieldTable.Cell("Field")
	if name == "" || v.describe == nil {
		return nil
	}
	for i := range v.describe.Fields {
		if v.describe.Fields[i].Name == name {
			return &v.describe.Fields[i]
		}
	}
	return nil
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
		if v.card != nil {
			return v.cardKey(msg)
		}
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
				return nil
			}
			if f := v.currentField(); f != nil {
				v.card = newFieldCard(*f, v.app.width, v.app.height-2)
			}
		case "s":
			if label := table.SortByCursorColumn(); label != "" {
				return toast(statusInfo, "sorted by "+label)
			}
			return toast(statusInfo, "sort cleared")
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

// cardKey drives the open field card: y copies, everything else scrolls or
// closes it.
func (v *schemaView) cardKey(msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "y" {
		text, note := v.card.copyText()
		if err := v.app.deps.Clipboard(text); err != nil {
			return toastErr(err)
		}
		return toast(statusOK, note)
	}
	if !v.card.Update(msg) {
		v.card = nil
	}
	return nil
}

func (v *schemaView) View(width, height int) string {
	if v.card != nil {
		return v.card.View(width, height)
	}
	head := styleTitle.Render("Schema")
	if v.inFields && v.describe != nil {
		head = styleTitle.Render("Schema › "+v.describe.Name) +
			styleDim.Render(fmt.Sprintf("  %d fields • prefix %s", len(v.describe.Fields), v.describe.KeyPrefix))
	} else {
		head += styleDim.Render("  " + plural(v.objTable.RowCount(), "queryable object"))
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

// fieldCard is one field's describe in full, picklist expanded one value per
// line. The shared detailCard renders a flat column/value record, which would
// fold the picklist back into the single truncated cell this card exists to
// open up.
type fieldCard struct {
	title  string
	footer string
	vp     viewport.Model

	// values is the picklist in display order, and plain the whole card as
	// text: between them they cover both things y could reasonably mean.
	values []string
	plain  string
}

func newFieldCard(f api.Field, width, height int) *fieldCard {
	c := &fieldCard{title: "field " + f.Name}
	c.vp = viewport.New(max(width-4, 20), max(height-4, 3))

	props := fieldProperties(f)
	keyW := 0
	for _, p := range props {
		keyW = max(keyW, len(p[0]))
	}
	var styled, plain strings.Builder
	for _, p := range props {
		styled.WriteString(styleOK.Render(padRight(p[0], keyW+2)) + p[1] + "\n")
		plain.WriteString(padRight(p[0], keyW+2) + p[1] + "\n")
	}

	c.footer = "esc close • ↑↓ scroll • y copy details"
	if len(f.PicklistValues) > 0 {
		c.values = make([]string, 0, len(f.PicklistValues))
		for _, p := range f.PicklistValues {
			c.values = append(c.values, p.Value)
		}
		head := plural(len(f.PicklistValues), "picklist value")
		styled.WriteString("\n" + styleTitle.Render(head) + "\n")
		plain.WriteString("\n" + head + "\n")
		for _, p := range f.PicklistValues {
			s, t := picklistLine(p, picklistValueWidth(f.PicklistValues))
			styled.WriteString(s + "\n")
			plain.WriteString(t + "\n")
		}
		c.footer = "esc close • ↑↓ scroll • y copy values"
	}

	c.vp.SetContent(styled.String())
	c.plain = plain.String()
	return c
}

// fieldProperties is the key/value block above the picklist, ordered the way
// an admin reads it: what the field is, then what it will accept.
func fieldProperties(f api.Field) [][2]string {
	props := [][2]string{
		{"API Name", f.Name},
		{"Label", f.Label},
		{"Type", fieldTypeLabel(f)},
		{"Required", yesNo(!f.Nillable)},
	}
	if f.Length > 0 {
		props = append(props, [2]string{"Length", strconv.Itoa(f.Length)})
	}
	if len(f.ReferenceTo) > 0 {
		props = append(props, [2]string{"References", strings.Join(f.ReferenceTo, ", ")})
	}
	if f.RelationshipName != "" {
		props = append(props, [2]string{"Relationship", f.RelationshipName})
	}
	return append(props,
		[2]string{"Custom", yesNo(f.Custom)},
		[2]string{"Createable", yesNo(f.Createable)},
		[2]string{"Updateable", yesNo(f.Updateable)},
		[2]string{"Formula", yesNo(f.Calculated)},
	)
}

// picklistValueWidth aligns the labels into a column, capped so one runaway
// value cannot push every label off the right edge.
func picklistValueWidth(values []api.PicklistValue) int {
	w := 0
	for _, p := range values {
		w = max(w, len(p.Value))
	}
	return min(w, 32)
}

// picklistLine renders one entry styled and as plain text. The API value
// comes first: that is what goes into a SOQL filter or a data load.
func picklistLine(p api.PicklistValue, valueW int) (string, string) {
	var styled, plain string
	if p.Label != "" && p.Label != p.Value {
		styled, plain = styleDim.Render(p.Label), p.Label
	}
	if !p.Active {
		if plain != "" {
			styled, plain = styled+" ", plain+" "
		}
		styled, plain = styled+styleWarn.Render("(inactive)"), plain+"(inactive)"
	}
	if plain == "" {
		return "  " + p.Value, "  " + p.Value
	}
	pad := padRight(p.Value, valueW+2)
	return "  " + pad + styled, "  " + pad + plain
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// copyText is what y puts on the clipboard, with the note to show for it.
func (c *fieldCard) copyText() (string, string) {
	if len(c.values) > 0 {
		return strings.Join(c.values, "\n"), "copied " + plural(len(c.values), "picklist value")
	}
	return c.plain, "copied field details"
}

// Update returns false when the card should close.
func (c *fieldCard) Update(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "esc", "q", "enter":
		return false
	}
	c.vp, _ = c.vp.Update(msg)
	return true
}

func (c *fieldCard) View(width, height int) string {
	c.vp.Width = max(width-6, 20)
	c.vp.Height = max(height-5, 3)
	body := styleTitle.Render(c.title) + "\n\n" + c.vp.View() + "\n" + styleDim.Render(c.footer)
	return styleOverlay.Render(body)
}
