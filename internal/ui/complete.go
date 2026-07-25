package ui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/razkevich/sf9s/internal/api"
	"github.com/razkevich/sf9s/internal/soql"
)

// completer turns the org's schema into SOQL completion candidates. Describes
// come from the shared on-disk cache, so suggestions are instant once an
// object has been looked at; a miss fetches in the background and fills in.
type completer struct {
	app      *Model
	objects  []api.SObjectSummary
	fields   map[string][]api.Field
	inflight map[string]bool
	// lastErr records the most recent schema failure so a retry storm can't
	// form against an unreachable org.
	lastErr error
}

func newCompleter(app *Model) *completer {
	return &completer{app: app, fields: map[string][]api.Field{}, inflight: map[string]bool{}}
}

type objectsReadyMsg struct {
	objects []api.SObjectSummary
	err     error
}

type fieldsReadyMsg struct {
	object string
	fields []api.Field
	err    error
}

func (c *completer) orgID() string {
	if c.app.current == nil {
		return ""
	}
	return c.app.current.OrgID
}

// warm loads whatever schema is already cached for the current org, so the
// first completion in a session is instant.
func (c *completer) warm() tea.Cmd {
	c.reset()
	var cached []api.SObjectSummary
	if c.app.deps.Store.CacheGet("describe-global-"+c.orgID(), describeTTL, &cached) && len(cached) > 0 {
		c.objects = cached
		return nil
	}
	return c.fetchObjects()
}

// loading reports whether any schema request is in flight, so a completion
// request made too early can be retried instead of reported as "no match".
func (c *completer) loading() bool { return len(c.inflight) > 0 }

// reset drops schema learned for a previous org, including any error that
// was blocking retries.
func (c *completer) reset() {
	c.objects = nil
	c.fields = map[string][]api.Field{}
	c.inflight = map[string]bool{}
	c.lastErr = nil
}

func (c *completer) fetchObjects() tea.Cmd {
	if c.app.client == nil || c.inflight["*"] || c.lastErr != nil {
		return nil
	}
	c.inflight["*"] = true
	client := c.app.client
	store := c.app.deps.Store
	key := "describe-global-" + c.orgID()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		objects, err := client.DescribeGlobal(ctx)
		if err != nil {
			return objectsReadyMsg{err: err}
		}
		store.CachePut(key, objects)
		return objectsReadyMsg{objects: objects}
	}
}

func (c *completer) fieldsFor(object string) ([]api.Field, tea.Cmd) {
	if object == "" {
		return nil, nil
	}
	if fields, ok := c.fields[object]; ok {
		return fields, nil
	}
	key := "describe-" + c.orgID() + "-" + object
	var cached api.SObjectDescribe
	if c.app.deps.Store.CacheGet(key, describeTTL, &cached) && cached.Name != "" {
		c.fields[object] = cached.Fields
		return cached.Fields, nil
	}
	if c.app.client == nil || c.inflight[object] || c.lastErr != nil {
		return nil, nil
	}
	c.inflight[object] = true
	client := c.app.client
	store := c.app.deps.Store
	return nil, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		desc, err := client.DescribeSObject(ctx, object)
		if err != nil {
			return fieldsReadyMsg{object: object, err: err}
		}
		if desc == nil {
			return fieldsReadyMsg{object: object, err: errors.New("empty describe response")}
		}
		store.CachePut(key, desc)
		return fieldsReadyMsg{object: object, fields: desc.Fields}
	}
}

// Update folds a schema response in. It reports whether the message belonged
// to the completer, plus any error worth telling the user about.
func (c *completer) Update(msg tea.Msg) (bool, error) {
	switch msg := msg.(type) {
	case objectsReadyMsg:
		delete(c.inflight, "*")
		if msg.err != nil {
			c.lastErr = msg.err
			return true, msg.err
		}
		c.lastErr = nil
		c.objects = msg.objects
		return true, nil
	case fieldsReadyMsg:
		delete(c.inflight, msg.object)
		if msg.err != nil {
			c.lastErr = msg.err
			return true, msg.err
		}
		c.lastErr = nil
		c.fields[msg.object] = msg.fields
		return true, nil
	}
	return false, nil
}

// resolveObject maps a name that may be a child-relationship name (as used in
// subqueries, e.g. "Contacts") onto a real sObject.
func (c *completer) resolveObject(name string) string {
	if name == "" {
		return ""
	}
	lower := strings.ToLower(name)
	for _, o := range c.objects {
		if strings.ToLower(o.Name) == lower {
			return o.Name
		}
	}
	// Child relationship names are usually the plural of the object.
	for _, o := range c.objects {
		if strings.ToLower(o.Name)+"s" == lower || strings.ToLower(o.Name) == strings.TrimSuffix(lower, "s") {
			return o.Name
		}
	}
	return ""
}

// walkRelationships follows a dotted path (Owner.Manager) to the object those
// relationships lead to.
func (c *completer) walkRelationships(object string, path []string) (string, tea.Cmd) {
	current := object
	for _, segment := range path {
		fields, cmd := c.fieldsFor(current)
		if fields == nil {
			return "", cmd
		}
		next := ""
		for _, f := range fields {
			if strings.EqualFold(f.RelationshipName, segment) && len(f.ReferenceTo) > 0 {
				next = f.ReferenceTo[0]
				break
			}
		}
		if next == "" {
			return "", nil
		}
		current = next
	}
	return current, nil
}

// Candidates returns the completions for a cursor position, plus a command to
// fetch missing schema. An empty list means "nothing to offer here".
func (c *completer) Candidates(input string, cursor int) ([]soql.Candidate, soql.Context, tea.Cmd) {
	ctx := soql.Analyze(input, cursor)
	switch ctx.Clause {
	case soql.ClauseFrom:
		if c.objects == nil {
			return nil, ctx, c.fetchObjects()
		}
		out := make([]soql.Candidate, 0, len(c.objects))
		for _, o := range c.objects {
			if !o.Queryable {
				continue
			}
			out = append(out, soql.Candidate{Text: o.Name, Detail: o.Label})
		}
		return soql.Filter(out, ctx.Prefix), ctx, nil

	case soql.ClauseSelect, soql.ClauseFilter:
		object := c.resolveObject(ctx.Object)
		if object == "" {
			if c.objects == nil {
				return nil, ctx, c.fetchObjects()
			}
			return nil, ctx, nil
		}
		path, partial := ctx.RelationshipPath()
		if len(path) > 0 {
			target, cmd := c.walkRelationships(object, path)
			if target == "" {
				return nil, ctx, cmd
			}
			object = target
		}
		fields, cmd := c.fieldsFor(object)
		if fields == nil {
			return nil, ctx, cmd
		}
		out := make([]soql.Candidate, 0, len(fields)+4)
		for _, f := range fields {
			out = append(out, soql.Candidate{Text: f.Name, Detail: fieldTypeLabel(f)})
			if f.RelationshipName != "" && len(f.ReferenceTo) > 0 {
				out = append(out, soql.Candidate{
					Text:   f.RelationshipName + ".",
					Detail: "→ " + strings.Join(f.ReferenceTo, ","),
				})
			}
		}
		return soql.Filter(out, partial), ctx, nil
	}
	return nil, ctx, nil
}
