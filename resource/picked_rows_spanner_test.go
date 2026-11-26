package resource

import (
	"context"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/spxscan/spxapi"
)

// pickedMethod is the method whose request the picked-row tests hold.
const pickedMethod accesstypes.Resource = "AssignTask"

// readCountedClient is a client of the picked-row tests: the emulator's, counting the
// statements read through it, or one that says its database is dbType, or one that holds
// no Spanner reader (none), as a mock's.
type readCountedClient struct {
	Client
	dbType DBType
	none   bool

	mu    sync.Mutex
	reads int
}

func (c *readCountedClient) DBType() DBType {
	if c.dbType != "" {
		return c.dbType
	}

	return c.Client.DBType()
}

func (c *readCountedClient) SpannerReadOnlyTransaction() spxapi.Querier {
	if c.none {
		return nil
	}

	return &readCountedQuerier{Querier: c.Client.SpannerReadOnlyTransaction(), client: c}
}

// readsMade answers how many statements were read through the client.
func (c *readCountedClient) readsMade() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.reads
}

// readCountedQuerier counts each statement read through it on its client.
type readCountedQuerier struct {
	spxapi.Querier
	client *readCountedClient
}

func (q *readCountedQuerier) Query(ctx context.Context, statement spanner.Statement) *spanner.RowIterator {
	q.client.mu.Lock()
	q.client.reads++
	q.client.mu.Unlock()

	return q.Querier.Query(ctx, statement)
}

// pickedRowsCollection is the named-row tests' collection with the client a project names,
// the view ActiveProjects and the computed list ListedProjects, each a List may be held
// under a condition on.
func pickedRowsCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	data := onRecordConditionData()
	for i := range data.Resources {
		if data.Resources[i].Name == "Projects" {
			data.Resources[i].Attributes = append(data.Resources[i].Attributes, AttributeData{Name: "client", Column: "ClientId", Type: AttributeTypeString})
		}
	}
	data.Resources = append(data.Resources,
		CollectionResource{
			Name: "ActiveProjects", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.List},
			Domain:     &DomainBindingData{Column: "DomainId"},
			Attributes: []AttributeData{{Name: "client", Column: "ClientId", Type: AttributeTypeString}},
		},
		CollectionResource{
			Name: "ListedProjects", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.List},
			Attributes: []AttributeData{{Name: "client", Column: "Client", Type: AttributeTypeString}},
		},
	)
	g, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// The rows a method's request picks in the picked-row tests: a project as a kind of no
// tenant, a project of the tenant, a project of the view ActiveProjects and one of the
// computed list ListedProjects.
var (
	pickedOpenProject   = NamedRow{Field: "ProjectID", Resource: "Projects", Table: "Projects", KeyColumn: "Id"}
	pickedTenantProject = NamedRow{Field: "ProjectID", Resource: "Projects", Table: "Projects", KeyColumn: "Id", TenantColumn: "DomainId"}
	pickedActiveProject = activeProject(false)
	pickedListedProject = listedProjectRow("ProjectID", true, false)
)

// listsEveryKind lists the projects, the view and the computed list, with no condition.
func listsEveryKind() *namedRowsCaller {
	return &namedRowsCaller{user: "nell", list: map[accesstypes.Resource]accesstypes.Decision{
		"Projects": accesstypes.Granted(), "ActiveProjects": accesstypes.Granted(), "ListedProjects": accesstypes.Granted(),
	}}
}

// TestCaller_HoldPicked_onSpanner pins a method's picked fields on the emulator: every row
// the request names is one the caller's list of its kind shows, read through the client
// as a write's check reads it, and any other is answered as a row that is not there. A
// table of no tenant listed outright is not read; a view is read and a computed list
// asked always; a value that is empty is not asked, nor any grant when nothing is left.
func TestCaller_HoldPicked_onSpanner(t *testing.T) {
	t.Parallel()

	client := namedRowsDatabase(t, "picked-rows")
	collection := pickedRowsCollection(t)
	ctx := context.Background()
	north := accesstypes.DomainScope(onRecordNorth)
	text := func(id string) *string { return &id }
	var none *string

	cases := []struct {
		name    string
		caller  func() *namedRowsCaller
		scope   accesstypes.Scope
		picked  []PickedRow
		refused bool
		// reads is the statements read through the client, lists the List checks asked;
		// -1 is not counted.
		reads, lists int
	}{
		{name: "a table of no tenant, listed outright: not read", caller: listsEveryProject, picked: []PickedRow{{Row: pickedOpenProject, Value: text("p1")}}, reads: 0, lists: 1},
		{name: "a tenant's table: a row of the tenant", caller: listsEveryProject, picked: []PickedRow{{Row: pickedTenantProject, Value: text("p1")}}, reads: 1, lists: 1},
		{name: "a tenant's table: a row of another tenant", caller: listsEveryProject, picked: []PickedRow{{Row: pickedTenantProject, Value: text("p9")}}, refused: true, reads: 1, lists: 1},
		{name: "a tenant's table under a condition: a row it holds for", caller: listsFirstClientsProjects, picked: []PickedRow{{Row: pickedTenantProject, Value: text("p1")}}, reads: 1, lists: 1},
		{name: "a tenant's table under a condition: a row it does not hold for", caller: listsFirstClientsProjects, picked: []PickedRow{{Row: pickedTenantProject, Value: text("p2")}}, refused: true, reads: 1, lists: 1},
		{name: "a tenant's table with no List: not read", caller: listsNoProject, picked: []PickedRow{{Row: pickedTenantProject, Value: text("p1")}}, refused: true, reads: 0, lists: 1},
		{name: "a view: a row it shows", caller: listsEveryKind, picked: []PickedRow{{Row: pickedActiveProject, Value: text("p2")}}, reads: 1, lists: 1},
		{name: "a view: a row its own query leaves out", caller: listsEveryKind, picked: []PickedRow{{Row: pickedActiveProject, Value: text("p3")}}, refused: true, reads: 1, lists: 1},
		{name: "a computed list: a row it yields", caller: listsEveryKind, picked: []PickedRow{{Row: pickedListedProject, Value: "p2"}}, reads: 0, lists: 1},
		{name: "a computed list: a key it does not yield", caller: listsEveryKind, picked: []PickedRow{{Row: pickedListedProject, Value: "p3"}}, refused: true, reads: 0, lists: 1},
		{name: "a field of several keys: all listed", caller: listsEveryProject, picked: []PickedRow{{Row: pickedTenantProject, Value: []string{"p1", "p2"}}}, reads: 2, lists: 1},
		{name: "a field of several keys: one not", caller: listsEveryProject, picked: []PickedRow{{Row: pickedTenantProject, Value: []string{"p1", "p9"}}}, refused: true, reads: 2, lists: 1},
		{name: "a field of several keys: the same key twice, asked once", caller: listsEveryProject, picked: []PickedRow{{Row: pickedTenantProject, Value: []string{"p1", "p1"}}}, reads: 1, lists: 1},
		{name: "two fields of the kinds of one scope: one List check", caller: listsEveryKind, picked: []PickedRow{{Row: pickedTenantProject, Value: text("p1")}, {Row: pickedActiveProject, Value: text("p2")}}, reads: 2, lists: 1},
		{name: "a nil pointer: no grant asked", caller: listsNoProject, picked: []PickedRow{{Row: pickedTenantProject, Value: none}}, reads: 0, lists: 0},
		{name: "an empty text: no grant asked", caller: listsNoProject, picked: []PickedRow{{Row: pickedTenantProject, Value: ""}, {Row: pickedTenantProject, Value: []string{"", ""}}}, reads: 0, lists: 0},
		{name: "no picked row: no grant asked", caller: listsNoProject, reads: 0, lists: 0},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			perms, list := tt.caller(), newProjectList()
			sources := MergeNamedRowSources(NamedRowSources{"ActiveProjects": ViewRows(&countedView{query: activeProjectsQuery})}, list.sources())
			caller := &Caller{Permissions: perms, Scope: north, Env: accesstypes.EnvironmentAt(onRecordNow), collection: collection, namedRowSources: sources}
			db := &readCountedClient{Client: NewSpannerClient(client)}
			err := caller.HoldPicked(ctx, db, pickedMethod, tt.picked...)
			judgePicked(t, err, tt.refused)
			if tt.reads >= 0 && db.readsMade() != tt.reads {
				t.Errorf("statements read = %d, want %d", db.readsMade(), tt.reads)
			}
			if tt.lists >= 0 && perms.listsAsked() != tt.lists {
				t.Errorf("List checks asked = %d, want %d", perms.listsAsked(), tt.lists)
			}
		})
	}

	t.Run("a zero caller", func(t *testing.T) {
		t.Parallel()

		var nobody *Caller
		for _, caller := range []*Caller{nobody, {}} {
			if err := caller.HoldPicked(ctx, NewSpannerClient(client), pickedMethod, PickedRow{Row: pickedTenantProject, Value: text("p1")}); !errors.Is(err, errNoCaller) {
				t.Errorf("error = %v, want errNoCaller", err)
			}
		}
	})

	t.Run("the refusal names the method and what was looked in", func(t *testing.T) {
		t.Parallel()

		caller := &Caller{Permissions: listsEveryKind(), Scope: north, Env: accesstypes.EnvironmentAt(onRecordNow), collection: collection, namedRowSources: newProjectList().sources()}
		for _, tt := range []struct {
			row   NamedRow
			value any
			cause string
		}{
			{row: pickedTenantProject, value: "p9", cause: "AssignTask: referenced Projects row not found"},
			{row: pickedListedProject, value: "p3", cause: "AssignTask: referenced ListedProjects row not found"},
		} {
			err := caller.HoldPicked(ctx, NewSpannerClient(client), pickedMethod, PickedRow{Row: tt.row, Value: tt.value})
			judgePicked(t, err, true)
			if err != nil && !strings.Contains(err.Error(), tt.cause) {
				t.Errorf("error = %v, want its cause to say %q", err, tt.cause)
			}
		}
	})

	t.Run("a client that holds no Spanner reader passes what needs a read", func(t *testing.T) {
		t.Parallel()

		caller := &Caller{Permissions: listsEveryProject(), Scope: north, Env: accesstypes.EnvironmentAt(onRecordNow), collection: collection}
		mock := &readCountedClient{Client: NewSpannerClient(client), none: true}
		if err := caller.HoldPicked(ctx, mock, pickedMethod, PickedRow{Row: pickedTenantProject, Value: text("p9")}); err != nil {
			t.Errorf("error = %v, want none", err)
		}
	})

	t.Run("a read on a database other than Spanner", func(t *testing.T) {
		t.Parallel()

		caller := &Caller{Permissions: listsEveryProject(), Scope: north, Env: accesstypes.EnvironmentAt(onRecordNow), collection: collection}
		other := &readCountedClient{Client: NewSpannerClient(client), dbType: PostgresDBType}
		err := caller.HoldPicked(ctx, other, pickedMethod, PickedRow{Row: pickedTenantProject, Value: text("p1")})
		if err == nil || httpio.HasNotFound(err) || !strings.Contains(err.Error(), namedRowsNotImplemented+" is not implemented for postgres") {
			t.Errorf("error = %v, want the read said not implemented for postgres", err)
		}
	})

	t.Run("a tenant's kind asked by a caller of no tenant", func(t *testing.T) {
		t.Parallel()

		for _, row := range []NamedRow{pickedTenantProject, pickedActiveProject, pickedListedProject} {
			perms := listsEveryKind()
			caller := &Caller{Permissions: perms, Scope: accesstypes.GlobalScope(), Env: accesstypes.EnvironmentAt(onRecordNow), collection: collection}
			err := caller.HoldPicked(ctx, NewSpannerClient(client), pickedMethod, PickedRow{Row: row, Value: "p1"})
			if err == nil || httpio.HasNotFound(err) || !strings.Contains(err.Error(), "regenerate") {
				t.Errorf("%s: error = %v, want the error that the generated code is out of step, saying to regenerate", row.Resource, err)
			}
			if perms.listsAsked() != 0 {
				t.Errorf("%s: List checks asked = %d, want none", row.Resource, perms.listsAsked())
			}
		}
	})
}

// judgePicked holds HoldPicked's answer: nil where the rows are listed, and where one is
// not a 404 in the words of a row that is not there.
func judgePicked(t *testing.T, err error, refused bool) {
	t.Helper()

	if !refused {
		if err != nil {
			t.Errorf("error = %v, want none", err)
		}

		return
	}
	if !httpio.HasNotFound(err) {
		t.Errorf("error = %v, want a not found", err)

		return
	}
	if got := httpio.Message(err); got != referencedNotFoundMessage {
		t.Errorf("the refusal reads %q, want %q", got, referencedNotFoundMessage)
	}
}
