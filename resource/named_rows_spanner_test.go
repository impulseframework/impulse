package resource

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"google.golang.org/grpc/codes"
)

// namedTask is a row of the record fixture's Tasks, which names a project by ProjectId and
// by FormerProjectId, each a project of the tenant the application lists.
type namedTask struct {
	ID              string  `spanner:"Id"`
	DomainID        string  `spanner:"DomainId"`
	ProjectID       *string `spanner:"ProjectId"`
	FormerProjectID *string `spanner:"FormerProjectId"`
}

func (namedTask) Resource() accesstypes.Resource { return "Tasks" }

func (namedTask) NamedRows() []NamedRow {
	return []NamedRow{
		{Field: "ProjectID", Resource: "Projects", Table: "Projects", KeyColumn: "Id", TenantColumn: "DomainId"},
		{Field: "FormerProjectID", Resource: "Projects", Table: "Projects", KeyColumn: "Id", TenantColumn: "DomainId"},
	}
}

// namedOpenTask is a task whose former project is declared as a row of a kind that belongs
// to no tenant: what a write names is read only where the caller's List carries a
// condition.
type namedOpenTask struct {
	ID              string  `spanner:"Id"`
	DomainID        string  `spanner:"DomainId"`
	FormerProjectID *string `spanner:"FormerProjectId"`
}

func (namedOpenTask) Resource() accesstypes.Resource { return "Tasks" }

func (namedOpenTask) NamedRows() []NamedRow {
	return []NamedRow{{Field: "FormerProjectID", Resource: "Projects", Table: "Projects", KeyColumn: "Id"}}
}

// namedOpenTaskRequest is the request a namedOpenTask is written by.
type namedOpenTaskRequest struct {
	ID              string  `json:"id"              perm:"-"`
	DomainID        string  `json:"-"`
	FormerProjectID *string `json:"formerProjectId"`
}

// namedTaskRequest is the request a task is written by.
type namedTaskRequest struct {
	ID              string  `json:"id"              perm:"-"`
	DomainID        string  `json:"-"`
	ProjectID       *string `json:"projectId"`
	FormerProjectID *string `json:"formerProjectId"`
}

// namedComment is a row of the record fixture's Comments, which names a task.
type namedComment struct {
	ID       string  `spanner:"Id"`
	DomainID string  `spanner:"DomainId"`
	TaskID   *string `spanner:"TaskId"`
	Body     *string `spanner:"Body"`
}

func (namedComment) Resource() accesstypes.Resource { return "Comments" }

func (namedComment) NamedRows() []NamedRow {
	return []NamedRow{{Field: "TaskID", Resource: "Tasks", Table: "Tasks", KeyColumn: "Id", TenantColumn: "DomainId"}}
}

// namedPathComment is a comment whose task is declared as a kind whose tenant is read
// through a parent: the task's project, then the project's tenant.
type namedPathComment struct {
	ID       string  `spanner:"Id"`
	DomainID string  `spanner:"DomainId"`
	TaskID   *string `spanner:"TaskId"`
	Body     *string `spanner:"Body"`
}

func (namedPathComment) Resource() accesstypes.Resource { return "Comments" }

func (namedPathComment) NamedRows() []NamedRow {
	return []NamedRow{{
		Field: "TaskID", Resource: "Tasks", Table: "Tasks", KeyColumn: "Id",
		TenantColumn: "ProjectId", TenantPath: []BindingHop{{Table: "Projects", JoinColumn: "Id", Column: "DomainId"}},
	}}
}

// namedCommentRequest is the request a comment is written by.
type namedCommentRequest struct {
	ID       string  `json:"id"     perm:"-"`
	DomainID string  `json:"-"`
	TaskID   *string `json:"taskId"`
	Body     *string `json:"body"`
}

// namedRowsCaller is a caller of the named-row tests: every permission but List granted on
// whatever it is asked about, but for an Update or a Create of a kind, or of a field of it,
// that update or create holds a decision for, and List answered from list (Denied where it
// names nothing), for the user named. It counts the List checks it answers.
type namedRowsCaller struct {
	user   accesstypes.User
	list   map[accesstypes.Resource]accesstypes.Decision
	update map[accesstypes.Resource]accesstypes.Decision
	create map[accesstypes.Resource]accesstypes.Decision

	mu    sync.Mutex
	lists int
}

func (c *namedRowsCaller) Check(_ context.Context, _ accesstypes.Environment, _ accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	decisions := make(accesstypes.Decisions, len(resources))
	for _, res := range resources {
		decisions[res] = accesstypes.Granted()
		kind, _ := res.ResourceAndTag()
		if decision, held := c.update[kind]; held && perm == accesstypes.Update {
			decisions[res] = decision
		}
		if decision, held := c.create[kind]; held && perm == accesstypes.Create {
			decisions[res] = decision
		}
	}
	if perm != accesstypes.List {
		return decisions, nil
	}
	c.mu.Lock()
	c.lists++
	c.mu.Unlock()
	for _, res := range resources {
		decisions[res] = c.list[res]
	}

	return decisions, nil
}

func (*namedRowsCaller) PermissionDigest(context.Context, accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	return accesstypes.PermissionDigest{}, nil
}

func (*namedRowsCaller) HasGrants(context.Context, accesstypes.Scope) (bool, error) {
	return true, nil
}

func (*namedRowsCaller) Domains(context.Context) ([]accesstypes.Domain, error) {
	return []accesstypes.Domain{}, nil
}

func (*namedRowsCaller) Reaches(context.Context, accesstypes.Domain) (bool, error) {
	return true, nil
}

func (*namedRowsCaller) HeldDomains(context.Context) ([]accesstypes.Domain, error) {
	return []accesstypes.Domain{}, nil
}

func (c *namedRowsCaller) User() accesstypes.User { return c.user }

// listsAsked answers how many List checks the caller answered.
func (c *namedRowsCaller) listsAsked() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.lists
}

// listedUnder is a List decision held under one condition.
func listedUnder(res accesstypes.Resource, source string) accesstypes.Decision {
	return accesstypes.Conditional(accesstypes.ConditionGroup{Resources: []accesstypes.Resource{res}, Condition: mustCondition(source)})
}

// namedRowsCollection is the record fixture's collection with one more attribute, the
// client a project names, for a List on projects held under a condition of its own.
func namedRowsCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	data := onRecordConditionData()
	for i := range data.Resources {
		if data.Resources[i].Name == "Projects" {
			data.Resources[i].Attributes = append(data.Resources[i].Attributes, AttributeData{Name: "client", Column: "ClientId", Type: AttributeTypeString})
		}
	}
	g, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// namedRowsDatabase makes a database with the record fixture's tables and rows on the
// shared emulator (onRecordRows).
func namedRowsDatabase(t *testing.T, name string) *spanner.Client {
	t.Helper()

	container := spannerEmulator(t)
	db, err := container.CreateDatabase(context.Background(), name)
	if err != nil {
		t.Fatalf("initiator.SpannerContainer.CreateDatabase() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropDatabase(context.Background()); err != nil {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.MigrateUp("file://testdata/recordroles/schema"); err != nil {
		t.Fatalf("initiator.SpannerDB.MigrateUp() error = %v", err)
	}
	rows := onRecordRows()
	mutations := make([]*spanner.Mutation, 0, len(rows))
	for _, row := range rows {
		mutations = append(mutations, spanner.InsertMap(row.table, row.values))
	}
	if _, err := db.Apply(context.Background(), mutations); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	return db.Client
}

// namedWrite is one write of the named-row tests: a patch of a task or a comment, armed
// for caller in north unless caller is nil. An update-or-create is armed for the whole
// application unless partitioned says it is armed in north.
type namedWrite struct {
	patchType   PatchType
	caller      *namedRowsCaller
	id          string
	columns     map[accesstypes.Field]any
	partitioned bool
}

// armedPermission is the permission a patch of the type is armed for.
func armedPermission(patchType PatchType) accesstypes.Permission {
	if patchType == CreatePatchType {
		return accesstypes.Create
	}

	return accesstypes.Update
}

// taskPatch builds the write of one task. A create carries its tenant, as the decoder
// stamps it. A patch armed in north reads the fixture's collection; an update-or-create is
// armed for the whole application, since a partitioned write cannot be one, unless the
// write is partitioned, to be refused as one.
func taskPatch(t *testing.T, collection *GeneratedCollection, w *namedWrite) *PatchSet[namedTask] {
	t.Helper()

	p := NewPatchSet(NewMetadata[namedTask]()).SetPatchType(w.patchType)
	p.SetKey("ID", w.id)
	for field, value := range w.columns {
		p.Set(field, value)
	}
	if w.patchType != UpdatePatchType {
		p.Set("DomainID", onRecordNorth)
	}
	arm(t, p.querySet, collection, w)

	return p
}

// commentPatch builds the write of one comment, of either declaration, as taskPatch
// builds a task's.
func commentPatch[Comment Resourcer](t *testing.T, collection *GeneratedCollection, w *namedWrite) *PatchSet[Comment] {
	t.Helper()

	p := NewPatchSet(NewMetadata[Comment]()).SetPatchType(w.patchType)
	p.SetKey("ID", w.id)
	for field, value := range w.columns {
		p.Set(field, value)
	}
	if w.patchType != UpdatePatchType {
		p.Set("DomainID", onRecordNorth)
	}
	arm(t, p.querySet, collection, w)

	return p
}

// arm arms a patch's query set for the write's caller, at the fixture's instant.
func arm[Resource Resourcer](t *testing.T, q *QuerySet[Resource], collection *GeneratedCollection, w *namedWrite) {
	t.Helper()

	if w.caller == nil {
		return
	}
	perm := armedPermission(w.patchType)
	scope := accesstypes.DomainScope(onRecordNorth)
	if w.patchType == CreateOrUpdatePatchType && !w.partitioned {
		scope, collection = accesstypes.GlobalScope(), nil
	}
	var rSet any
	var err error
	switch any(q).(type) {
	case *QuerySet[namedTask]:
		rSet, err = NewSet[namedTask, namedTaskRequest](perm)
	case *QuerySet[namedComment]:
		rSet, err = NewSet[namedComment, namedCommentRequest](perm)
	case *QuerySet[namedPathComment]:
		rSet, err = NewSet[namedPathComment, namedCommentRequest](perm)
	case *QuerySet[namedOpenTask]:
		rSet, err = NewSet[namedOpenTask, namedOpenTaskRequest](perm)
	}
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	set, ok := rSet.(*Set[Resource])
	if !ok {
		t.Fatalf("no request set for %T", q)
	}
	q.collection = collection
	q.EnableUserPermissionEnforcement(set, w.caller, scope, perm)
	q.env = accesstypes.EnvironmentAt(onRecordNow)
}

// storedColumn reads one text column of one task past the library: "" for NULL, and found
// false where the task is not there.
func storedColumn(t *testing.T, client *spanner.Client, id, column string) (value string, found bool) {
	t.Helper()

	row, err := client.Single().ReadRow(context.Background(), "Tasks", spanner.Key{id}, []string{column})
	if err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return "", false
		}
		t.Fatalf("spanner.ReadOnlyTransaction.ReadRow(Tasks %s) error = %v", id, err)
	}
	var held spanner.NullString
	if err := row.Columns(&held); err != nil {
		t.Fatalf("spanner.Row.Columns(Tasks %s) error = %v", id, err)
	}

	return held.StringVal, true
}

// judgeNamed holds a write's answer: nothing where it is written, and where it is refused
// a 404 in the words of a row that is not there.
func judgeNamed(t *testing.T, err error, written bool) {
	t.Helper()

	if written {
		if err != nil {
			t.Fatalf("error = %v, want the row written", err)
		}

		return
	}
	if err == nil {
		t.Fatal("the row was written, want it refused")
	}
	if !httpio.HasNotFound(err) {
		t.Errorf("error = %v, want a not found", err)
	}
	if got := httpio.Message(err); got != referencedNotFoundMessage {
		t.Errorf("the refusal reads %q, want %q", got, referencedNotFoundMessage)
	}
}

// Callers of the named-row tests, made afresh for each write so a count is its own.
func listsEveryProject() *namedRowsCaller {
	return &namedRowsCaller{user: "nell", list: map[accesstypes.Resource]accesstypes.Decision{"Projects": accesstypes.Granted(), "Tasks": accesstypes.Granted()}}
}

func listsFirstClientsProjects() *namedRowsCaller {
	return &namedRowsCaller{user: "cora", list: map[accesstypes.Resource]accesstypes.Decision{"Projects": listedUnder("Projects", "client = 'c1'")}}
}

func listsNoProject() *namedRowsCaller {
	return &namedRowsCaller{user: "nat"}
}

func listsHeldProjects(t *testing.T, collection *GeneratedCollection) *namedRowsCaller {
	t.Helper()

	held, ok := collection.RecordCondition(onRecordAuth, "ProjectEditor", "Projects", "Projects", accesstypes.List)
	if !ok {
		t.Fatal("RecordCondition(staff, ProjectEditor, Projects, Projects, List) is not ok")
	}

	return &namedRowsCaller{user: "erin", list: map[accesstypes.Resource]accesstypes.Decision{"Projects": listedUnder("Projects", held)}}
}

// TestPatchSet_namedRows_onSpanner pins the rule on the emulator: a create and an update
// set a column that names a project only to a project their caller can list, a row the
// caller cannot list is answered as a row that is not there, and nothing is written.
func TestPatchSet_namedRows_onSpanner(t *testing.T) {
	t.Parallel()

	client := namedRowsDatabase(t, "named-rows")
	db := NewSpannerClient(client)
	collection := namedRowsCollection(t)
	ctx := context.Background()
	project := func(id string) *string { return &id }

	cases := []struct {
		name    string
		caller  func() *namedRowsCaller
		former  string
		written bool
	}{
		{name: "a caller with no condition names a project of the tenant", caller: listsEveryProject, former: "p2", written: true},
		{name: "a caller with no condition names a project of another tenant", caller: listsEveryProject, former: "p9"},
		{name: "a caller who lists projects under a condition names one it holds for", caller: listsFirstClientsProjects, former: "p1", written: true},
		{name: "a caller who lists projects under a condition names one it does not hold for", caller: listsFirstClientsProjects, former: "p2"},
		{name: "a caller with no List on projects names one", caller: listsNoProject, former: "p1"},
		{name: "erin, who holds ProjectEditor on project 1 alone, names project 2", caller: func() *namedRowsCaller { return listsHeldProjects(t, collection) }, former: "p2"},
		{name: "erin names project 1", caller: func() *namedRowsCaller { return listsHeldProjects(t, collection) }, former: "p1", written: true},
	}
	for i, tt := range cases {
		t.Run("a create: "+tt.name, func(t *testing.T) {
			t.Parallel()

			id := fmt.Sprintf("nc%d", i)
			err := taskPatch(t, collection, &namedWrite{patchType: CreatePatchType, caller: tt.caller(), id: id, columns: map[accesstypes.Field]any{"FormerProjectID": project(tt.former)}}).Apply(ctx, db)
			judgeNamed(t, err, tt.written)
			got, found := storedColumn(t, client, id, "FormerProjectId")
			if found != tt.written || (found && got != tt.former) {
				t.Errorf("the stored task names %q (stored %v), want %s written %v", got, found, tt.former, tt.written)
			}
		})
		t.Run("an update: "+tt.name, func(t *testing.T) {
			t.Parallel()

			id := fmt.Sprintf("nu%d", i)
			if err := taskPatch(t, collection, &namedWrite{patchType: CreatePatchType, id: id, columns: map[accesstypes.Field]any{"FormerProjectID": project("p3")}}).Apply(ctx, db); err != nil {
				t.Fatalf("writing the task to update: %v", err)
			}
			err := taskPatch(t, collection, &namedWrite{patchType: UpdatePatchType, caller: tt.caller(), id: id, columns: map[accesstypes.Field]any{"FormerProjectID": project(tt.former)}}).Apply(ctx, db)
			judgeNamed(t, err, tt.written)
			want := "p3"
			if tt.written {
				want = tt.former
			}
			if got, _ := storedColumn(t, client, id, "FormerProjectId"); got != want {
				t.Errorf("the stored task names %q, want %q", got, want)
			}
		})
	}

	t.Run("an update that sends the project the task names, by a caller who cannot list it", func(t *testing.T) {
		t.Parallel()

		err := taskPatch(t, collection, &namedWrite{patchType: UpdatePatchType, caller: listsNoProject(), id: "t2", columns: map[accesstypes.Field]any{"FormerProjectID": project("p1"), "ProjectID": project("p3")}}).Apply(ctx, db)
		judgeNamed(t, err, false)
		err = taskPatch(t, collection, &namedWrite{patchType: UpdatePatchType, caller: listsNoProject(), id: "t2", columns: map[accesstypes.Field]any{"FormerProjectID": project("p1")}}).Apply(ctx, db)
		judgeNamed(t, err, true)
	})

	t.Run("an update that does not send the column asks nothing", func(t *testing.T) {
		t.Parallel()

		caller := listsNoProject()
		body := "Seen to."
		err := commentPatch[namedComment](t, collection, &namedWrite{patchType: UpdatePatchType, caller: caller, id: "k1", columns: map[accesstypes.Field]any{"Body": &body}}).Apply(ctx, db)
		judgeNamed(t, err, true)
		if asked := caller.listsAsked(); asked != 0 {
			t.Errorf("the caller was asked List %d times, want none", asked)
		}
	})

	t.Run("a column set to nothing", func(t *testing.T) {
		t.Parallel()

		var none *string
		err := taskPatch(t, collection, &namedWrite{patchType: CreatePatchType, caller: listsNoProject(), id: "n-none", columns: map[accesstypes.Field]any{"FormerProjectID": none}}).Apply(ctx, db)
		judgeNamed(t, err, true)
		err = taskPatch(t, collection, &namedWrite{patchType: UpdatePatchType, caller: listsNoProject(), id: "n-none", columns: map[accesstypes.Field]any{"FormerProjectID": none}}).Apply(ctx, db)
		judgeNamed(t, err, true)
	})

	t.Run("a granted List on a kind of no tenant reads nothing: a key of no row is left to the database", func(t *testing.T) {
		t.Parallel()

		p := NewPatchSet(NewMetadata[namedOpenTask]()).SetPatchType(CreatePatchType)
		p.SetKey("ID", "n-open")
		p.Set("DomainID", onRecordNorth)
		p.Set("FormerProjectID", project("p-none"))
		arm(t, p.querySet, collection, &namedWrite{patchType: CreatePatchType, caller: listsEveryProject()})
		err := p.Apply(ctx, db)
		if err == nil {
			t.Fatal("the task naming no project was written")
		}
		if httpio.Message(err) == referencedNotFoundMessage {
			t.Errorf("error = %v, want the database's own refusal of the foreign key: the granted List read the row", err)
		}
		if _, found := storedColumn(t, client, "n-open", "FormerProjectId"); found {
			t.Error("the task naming no project is stored")
		}
	})

	t.Run("a patch that is not armed", func(t *testing.T) {
		t.Parallel()

		err := taskPatch(t, collection, &namedWrite{patchType: CreatePatchType, id: "n-trusted", columns: map[accesstypes.Field]any{"FormerProjectID": project("p9")}}).Apply(ctx, db)
		judgeNamed(t, err, true)
	})

	t.Run("a comment created with the task it names, in one transaction", func(t *testing.T) {
		t.Parallel()

		caller := &namedRowsCaller{user: "nell", list: map[accesstypes.Resource]accesstypes.Decision{"Projects": accesstypes.Granted()}}
		task := "n-task"
		err := db.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
			if err := taskPatch(t, collection, &namedWrite{patchType: CreatePatchType, caller: caller, id: task, columns: map[accesstypes.Field]any{"ProjectID": project("p1")}}).Buffer(ctx, txn); err != nil {
				return err
			}

			return commentPatch[namedComment](t, collection, &namedWrite{patchType: CreatePatchType, caller: caller, id: "n-comment", columns: map[accesstypes.Field]any{"TaskID": &task}}).Buffer(ctx, txn)
		})
		judgeNamed(t, err, true)
		err = commentPatch[namedComment](t, collection, &namedWrite{patchType: CreatePatchType, caller: caller, id: "n-comment-after", columns: map[accesstypes.Field]any{"TaskID": &task}}).Apply(ctx, db)
		judgeNamed(t, err, false)
	})

	t.Run("an update-or-create of a task that is there, the column as it is", func(t *testing.T) {
		t.Parallel()

		err := taskPatch(t, collection, &namedWrite{patchType: CreateOrUpdatePatchType, caller: listsNoProject(), id: "t1", columns: map[accesstypes.Field]any{"ProjectID": project("p1")}}).Apply(ctx, db)
		judgeNamed(t, err, true)
	})

	t.Run("an update-or-create of a task that is not there, naming a project the caller cannot list", func(t *testing.T) {
		t.Parallel()

		err := taskPatch(t, collection, &namedWrite{patchType: CreateOrUpdatePatchType, caller: listsNoProject(), id: "n-upsert", columns: map[accesstypes.Field]any{"ProjectID": project("p1")}}).Apply(ctx, db)
		judgeNamed(t, err, false)
		if _, found := storedColumn(t, client, "n-upsert", "ProjectId"); found {
			t.Error("the refused task is stored")
		}
	})
}

// editsHeldTasks is erin as a writer of tasks: she holds ProjectEditor on project 1 alone,
// so her List on projects and her Update on tasks are each held under the condition a role
// held on one record answers for it (RecordCondition).
func editsHeldTasks(t *testing.T, collection *GeneratedCollection) *namedRowsCaller {
	t.Helper()

	caller := listsHeldProjects(t, collection)
	held, ok := collection.RecordCondition(onRecordAuth, "ProjectEditor", "Projects", "Tasks", accesstypes.Update)
	if !ok {
		t.Fatal("RecordCondition(staff, ProjectEditor, Projects, Tasks, Update) is not ok")
	}
	caller.update = map[accesstypes.Resource]accesstypes.Decision{
		"Tasks": accesstypes.Conditional(accesstypes.ConditionGroup{Resources: []accesstypes.Resource{"Tasks"}, Condition: mustCondition(held)}),
	}

	return caller
}

// TestPatchSet_namedRows_afterTheRowsOwnChecks_onSpanner pins where an update and an
// update-or-create ask about the rows they name: once the row is found in the request's
// tenant and the write's conditions hold for it. A caller the row's own checks refuse is
// answered by them whatever the column is sent as, so the answer tells nothing of a row
// the caller may not change; a caller they pass is answered as before.
func TestPatchSet_namedRows_afterTheRowsOwnChecks_onSpanner(t *testing.T) {
	t.Parallel()

	client := namedRowsDatabase(t, "named-rows-after")
	db := NewSpannerClient(client)
	collection := namedRowsCollection(t)
	ctx := context.Background()
	project := func(id string) *string { return &id }
	update := func(caller *namedRowsCaller, id string, field accesstypes.Field, value string) error {
		return taskPatch(t, collection, &namedWrite{patchType: UpdatePatchType, caller: caller, id: id, columns: map[accesstypes.Field]any{field: project(value)}}).Apply(ctx, db)
	}

	t.Run("erin, who may change the tasks of project 1 alone, changes a task of project 2", func(t *testing.T) {
		t.Parallel()

		// t3 is a task of p2: p2 is the project it names, and p3 one erin cannot list.
		sent := update(editsHeldTasks(t, collection), "t3", "ProjectID", "p2")
		other := update(editsHeldTasks(t, collection), "t3", "ProjectID", "p3")
		for _, err := range []error{sent, other} {
			if !httpio.HasForbidden(err) {
				t.Errorf("error = %v, want the update's own refusal, a forbidden", err)
			}
		}
		if httpio.Message(sent) != httpio.Message(other) {
			t.Errorf("the refusals read %q and %q, want one sentence whatever the column is sent as", httpio.Message(sent), httpio.Message(other))
		}
		if got, _ := storedColumn(t, client, "t3", "ProjectId"); got != "p2" {
			t.Errorf("t3 names %q, want p2", got)
		}
	})

	t.Run("a caller of north changes a task of south", func(t *testing.T) {
		t.Parallel()

		// t9 is south's and names p9; p-none is no project's key.
		missing := httpio.Message(recordNotFound[namedTask]("t9"))
		for _, value := range []string{"p9", "p-none"} {
			err := update(listsEveryProject(), "t9", "ProjectID", value)
			if !httpio.HasNotFound(err) || httpio.Message(err) != missing {
				t.Errorf("sending %s: error = %v, reading %q; want 404, %q, as a task that is not there", value, err, httpio.Message(err), missing)
			}
		}
		if got, _ := storedColumn(t, client, "t9", "ProjectId"); got != "p9" {
			t.Errorf("t9 names %q, want p9", got)
		}
	})

	t.Run("a caller the row's own checks pass is answered as before", func(t *testing.T) {
		t.Parallel()

		// Two tasks of project 1, the second come from p2, which erin cannot list.
		for id, former := range map[string]*string{"na1": nil, "na2": project("p2")} {
			columns := map[accesstypes.Field]any{"ProjectID": project("p1"), "FormerProjectID": former}
			if err := taskPatch(t, collection, &namedWrite{patchType: CreatePatchType, id: id, columns: columns}).Apply(ctx, db); err != nil {
				t.Fatalf("writing the task %s: %v", id, err)
			}
		}
		judgeNamed(t, update(editsHeldTasks(t, collection), "na1", "FormerProjectID", "p2"), false)
		judgeNamed(t, update(editsHeldTasks(t, collection), "na2", "FormerProjectID", "p2"), true)
	})

	t.Run("an update-or-create of a task of another tenant", func(t *testing.T) {
		t.Parallel()

		upsert := func(value string) error {
			w := &namedWrite{patchType: CreateOrUpdatePatchType, caller: listsNoProject(), id: "t9", columns: map[accesstypes.Field]any{"ProjectID": project(value)}, partitioned: true}

			return taskPatch(t, collection, w).Apply(ctx, db)
		}
		// p9 is the project t9 names, and p1 one the caller cannot list.
		sent, other := upsert("p9"), upsert("p1")
		if sent == nil || other == nil {
			t.Fatalf("errors = %v and %v, want both refused", sent, other)
		}
		if sent.Error() != other.Error() {
			t.Errorf("the refusals are %q and %q, want one whatever the column is sent as", sent, other)
		}
		if httpio.Message(other) == referencedNotFoundMessage {
			t.Errorf("error = %v, want the row's own refusal, not the named row's", other)
		}
	})
}

// TestPatchSet_namedRows_sameRequest_onSpanner pins the pass for a row the same request
// created: a write names it with nothing asked where the row is the request's tenant's,
// read from the created row and, for a kind whose tenant is read through a parent, along
// that way; a row created in another tenant is judged as any other, and so refused.
func TestPatchSet_namedRows_sameRequest_onSpanner(t *testing.T) {
	t.Parallel()

	client := namedRowsDatabase(t, "named-rows-same-request")
	db := NewSpannerClient(client)
	collection := namedRowsCollection(t)
	ctx := context.Background()

	// createTask buffers the create of a task of the tenant under the project, unarmed, as
	// an earlier patch of the request.
	createTask := func(ctx context.Context, txn ReadWriteTransaction, id, domain, project string) error {
		p := NewPatchSet(NewMetadata[namedTask]()).SetPatchType(CreatePatchType)
		p.SetKey("ID", id)
		p.Set("DomainID", domain)
		p.Set("ProjectID", &project)

		return p.Buffer(ctx, txn)
	}
	cases := []struct {
		name, domain, project string
		written               bool
	}{
		{name: "a task created in north", domain: onRecordNorth, project: "p1", written: true},
		{name: "a task created in south", domain: onRecordSouth, project: "p9"},
	}
	for i, tt := range cases {
		t.Run(tt.name+", then a comment of north that names it", func(t *testing.T) {
			t.Parallel()

			task := fmt.Sprintf("ns-task%d", i)
			err := db.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
				if err := createTask(ctx, txn, task, tt.domain, tt.project); err != nil {
					return err
				}

				return commentPatch[namedComment](t, collection, &namedWrite{patchType: CreatePatchType, caller: listsEveryProject(), id: fmt.Sprintf("ns-comment%d", i), columns: map[accesstypes.Field]any{"TaskID": &task}}).Buffer(ctx, txn)
			})
			judgeNamed(t, err, tt.written)
		})
		t.Run(tt.name+", then a comment of north that names it, the task's tenant read through its project", func(t *testing.T) {
			t.Parallel()

			task := fmt.Sprintf("ns-path-task%d", i)
			err := db.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
				if err := createTask(ctx, txn, task, tt.domain, tt.project); err != nil {
					return err
				}

				return commentPatch[namedPathComment](t, collection, &namedWrite{patchType: CreatePatchType, caller: listsEveryProject(), id: fmt.Sprintf("ns-path-comment%d", i), columns: map[accesstypes.Field]any{"TaskID": &task}}).Buffer(ctx, txn)
			})
			judgeNamed(t, err, tt.written)
		})
	}
}
