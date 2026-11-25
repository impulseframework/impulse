package generation

import (
	"go/format"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/generation/parser"
)

// deletedWithTables is the schema of deletedwithfixture: a group's members and roles name
// it, the accounts table stands, and beside them the application's tables name a group, a
// task, a project, an assignment or a member's row, each by a foreign key the database
// does not follow.
func deletedWithTables() map[string]*tableMetadata {
	pk := columnMeta{IsPrimaryKey: true}
	key := func(ordinal int64) columnMeta {
		return columnMeta{IsPrimaryKey: true, OrdinalPosition: ordinal, KeyOrdinalPosition: ordinal}
	}
	fk := func(column, table string) foreignKeyMeta {
		return foreignKeyMeta{Name: "FK_" + column, Columns: []string{column}, ReferencedTable: table, ReferencedColumns: []string{"Id"}, DeleteRule: "NO ACTION"}
	}
	names := func(table string, ordinal int64) columnMeta {
		return columnMeta{IsForeignKey: true, ReferencedTable: table, ReferencedColumn: "Id", OrdinalPosition: ordinal}
	}
	// person names the account too, by a column of the table's.
	person := func(tab *tableMetadata) *tableMetadata {
		tab.ForeignKeys = append(tab.ForeignKeys, fk("UserId", "CrewSessionUsers"))
		tab.Columns["UserId"] = names("CrewSessionUsers", 3)

		return tab
	}
	// under is a table whose rows name one row of another table, and the group too where
	// group is set.
	under := func(column, table string, group bool) *tableMetadata {
		tab := &tableMetadata{PkCount: 1, Columns: map[string]columnMeta{"Id": pk}}
		if column != "" {
			tab.ForeignKeys = append(tab.ForeignKeys, fk(column, table))
			tab.Columns[column] = names(table, 1)
		}
		if group {
			tab.ForeignKeys = append(tab.ForeignKeys, fk("GroupId", "CrewGroups"))
			tab.Columns["GroupId"] = names("CrewGroups", 2)
		}

		return tab
	}
	nested := under("GroupId", "CrewGroups", false)
	nested.ForeignKeys = append([]foreignKeyMeta{fk("ParentId", "NestedAssignments")}, nested.ForeignKeys...)
	nested.Columns["ParentId"] = columnMeta{IsForeignKey: true, IsNullable: true, ReferencedTable: "NestedAssignments", ReferencedColumn: "Id", OrdinalPosition: 2}
	covers := under("GroupId", "CrewGroups", false)
	covers.ForeignKeys = append(covers.ForeignKeys, fk("BackupGroupId", "CrewGroups"))
	covers.Columns["BackupGroupId"] = names("CrewGroups", 2)
	badges := &tableMetadata{
		PkCount: 1,
		Columns: map[string]columnMeta{
			"Id":      pk,
			"GroupId": {IsForeignKey: true, ReferencedTable: "CrewGroupMembers", ReferencedColumn: "GroupId", OrdinalPosition: 1},
			"UserId":  {IsForeignKey: true, ReferencedTable: "CrewGroupMembers", ReferencedColumn: "UserId", OrdinalPosition: 2},
		},
		ForeignKeys: []foreignKeyMeta{{Name: "FK_PlaceBadges_Member", Columns: []string{"GroupId", "UserId"}, ReferencedTable: "CrewGroupMembers", ReferencedColumns: []string{"GroupId", "UserId"}, DeleteRule: "NO ACTION"}},
	}

	return map[string]*tableMetadata{
		"CrewGroups":        {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Name": {OrdinalPosition: 1}}},
		"CrewSessionUsers":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Username": {OrdinalPosition: 1}}},
		"CrewGroupMembers":  {PkCount: 2, ForeignKeys: []foreignKeyMeta{fk("GroupId", "CrewGroups")}, Columns: map[string]columnMeta{"GroupId": key(0), "UserId": key(1)}},
		"CrewGroupRoles":    {PkCount: 2, ForeignKeys: []foreignKeyMeta{fk("GroupId", "CrewGroups")}, Columns: map[string]columnMeta{"GroupId": key(0), "Role": key(1)}},
		"Tasks":             under("", "", false),
		"TaskAssignments":   person(under("TaskId", "Tasks", true)),
		"TaskWatches":       under("TaskId", "Tasks", true),
		"ShiftCovers":       covers,
		"NotedAssignments":  person(under("GroupId", "CrewGroups", false)),
		"AssignmentNotes":   under("AssignmentId", "NotedAssignments", false),
		"AssignmentHolds":   under("AssignmentId", "NotedAssignments", false),
		"HeldAssignments":   under("GroupId", "CrewGroups", false),
		"HoldSlips":         under("AssignmentId", "HeldAssignments", false),
		"NestedAssignments": nested,
		"PlaceBadges":       badges,
		"Projects":          {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Name": {OrdinalPosition: 1}}},
		"ProjectTasks":      under("ProjectId", "Projects", false),
		"LevelTasks":        {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "LevelId": {OrdinalPosition: 1}}},
		"Ranks":             under("GroupId", "CrewGroups", false),
	}
}

// deletedWithRun reads deletedwithfixture as a run reads it, over deletedWithTables: the
// crew's declarations through the instance, and the application's records the test names
// with none, which edit may change before the group's description is written a second
// time over them (resolveLibraryDeleteTrees). It answers the run's resources by name, the
// first description of each of the crew's records as its literal, and what the second
// build answered.
func deletedWithRun(t *testing.T, records []string, edit func(map[string]*resourceInfo)) (run map[string]*resourceInfo, first map[string]string, err error) {
	t.Helper()

	c := &client{tableMap: deletedWithTables(), resource: "pkg/resources"}
	var crewStructs, applicationStructs []*parser.Struct
	for _, s := range loadFixture(t, "deletedwithfixture").Structs {
		switch {
		case strings.HasPrefix(s.Name(), "Crew"):
			crewStructs = append(crewStructs, s)
		case slices.Contains(records, s.Name()):
			applicationStructs = append(applicationStructs, s)
		}
	}
	declared, err := c.instanceResources(&identityInstance{auth: "Crew", importPath: "example.com/acme/auth/crew"}, crewStructs)
	if err != nil {
		t.Fatalf("instanceResources() error = %v", err)
	}
	own, err := c.structsToResources(applicationStructs)
	if err != nil {
		t.Fatalf("structsToResources() error = %v", err)
	}
	run = make(map[string]*resourceInfo)
	first = make(map[string]string)
	for _, res := range declared {
		run[res.Name()] = res
		if res.DeleteTree != nil {
			first[res.Name()] = res.DeleteTreeLiteral()
		}
	}
	for _, res := range own {
		run[res.Name()] = res
	}
	if edit != nil {
		edit(run)
	}
	resources := slices.Concat(declared, own)
	sortResources(resources)

	return run, first, (&resourceGenerator{client: c}).resolveLibraryDeleteTrees(resources)
}

// libraryKind is what a test reads of a kind of a group's description: its name, its
// ways up to the group, its links to itself, and its marks.
type libraryKind struct {
	Name        string
	Paths       [][]deleteLink
	Within      []deleteLink
	Deletes     bool
	Trusted     bool
	AsCaller    bool
	WithinReach bool
	Package     string
}

// libraryKinds reads the kinds of one list.
func libraryKinds(kinds []*deleteKind) []libraryKind {
	var got []libraryKind
	for _, k := range kinds {
		got = append(got, libraryKind{Name: k.Name, Paths: k.Paths, Within: k.Within, Deletes: k.Deletes, Trusted: k.Trusted, AsCaller: k.AsCaller, WithinReach: k.WithinReach, Package: k.Package})
	}

	return got
}

// The group's own kinds, and the application's kinds as the group's description lists
// them.
var (
	groupStep   = link("GroupId", "CrewGroups")
	ownRole     = libraryKind{Name: "CrewGroupRole", Paths: [][]deleteLink{{groupStep}}, Deletes: true, Trusted: true}
	ownMember   = libraryKind{Name: "CrewGroupMember", Paths: [][]deleteLink{{groupStep}}, Deletes: true, Trusted: true}
	underMember = libraryKind{Name: "CrewGroupMember", Paths: [][]deleteLink{{groupStep}}}
	underRole   = libraryKind{Name: "CrewGroupRole", Paths: [][]deleteLink{{groupStep}}}
)

// takenWith is an application's kind the group's delete takes, by its ways up to the
// group.
func takenWith(name string, paths ...[]deleteLink) libraryKind {
	return libraryKind{Name: name, Paths: paths, Deletes: true, AsCaller: true, Package: "resources"}
}

// checkedWith is an application's kind under the group's Under or RefusedBy, by its ways
// up to the group.
func checkedWith(name string, paths ...[]deleteLink) libraryKind {
	return libraryKind{Name: name, Paths: paths, AsCaller: true}
}

// Test_deletedWith_groupDescription pins the group's description written a second time
// over the application's records: a record whose column says it goes with the group
// (@deletedWith) is taken before the group's own roles and members, deepest first, and is
// checked as whoever asked for the group's delete (AsCaller); what its own delete takes is
// taken with it, its way extended by the column, and what refuses its own delete refuses
// the group's. A record that names the group and says nothing is under Under alone, and
// so refuses the group's delete while a row names it. What names a member's row, and a
// table no resource declares, is in no list. A group no record of the application's names
// keeps its first description.
func Test_deletedWith_groupDescription(t *testing.T) {
	t.Parallel()

	noteWay := []deleteLink{link("AssignmentId", "NotedAssignments"), groupStep}
	slipWay := []deleteLink{link("AssignmentId", "HeldAssignments"), groupStep}
	parent := deleteLink{Columns: []string{"ParentId"}, Table: "NestedAssignments", References: []string{"Id"}, MayBeEmpty: true}
	nested := takenWith("NestedAssignment", []deleteLink{groupStep})
	nested.Within = []deleteLink{parent}

	tests := []struct {
		name          string
		records       []string
		wantTaken     []libraryKind
		wantRefusedBy []libraryKind
		wantUnder     []libraryKind
	}{
		{
			name:      "a link record that goes with the group is taken before the roles and the members",
			records:   []string{"Task", "TaskAssignment"},
			wantTaken: []libraryKind{takenWith("TaskAssignment", []deleteLink{groupStep}), ownRole, ownMember},
			wantUnder: []libraryKind{underMember, underRole, checkedWith("TaskAssignment", []deleteLink{groupStep})},
		},
		{
			name:      "a link record that says nothing is under Under alone",
			records:   []string{"Task", "TaskWatch"},
			wantTaken: []libraryKind{ownRole, ownMember},
			wantUnder: []libraryKind{underMember, underRole, checkedWith("TaskWatch", []deleteLink{groupStep})},
		},
		{
			name:      "of two columns that name the group, the taken kind's way is the marked one",
			records:   []string{"ShiftCover"},
			wantTaken: []libraryKind{takenWith("ShiftCover", []deleteLink{groupStep}), ownRole, ownMember},
			wantUnder: []libraryKind{underMember, underRole, checkedWith("ShiftCover", []deleteLink{groupStep}, []deleteLink{link("BackupGroupId", "CrewGroups")})},
		},
		{
			name:          "what the record's own delete takes is taken first, and what refuses it refuses the group's",
			records:       []string{"NotedAssignment", "AssignmentNote", "AssignmentHold"},
			wantTaken:     []libraryKind{takenWith("AssignmentNote", noteWay), takenWith("NotedAssignment", []deleteLink{groupStep}), ownRole, ownMember},
			wantRefusedBy: []libraryKind{checkedWith("AssignmentHold", []deleteLink{link("AssignmentId", "NotedAssignments"), groupStep})},
			wantUnder:     []libraryKind{underMember, underRole, checkedWith("NotedAssignment", []deleteLink{groupStep})},
		},
		{
			name:          "a kind under a record that declares nothing refuses the group's delete",
			records:       []string{"HeldAssignment", "HoldSlip"},
			wantTaken:     []libraryKind{takenWith("HeldAssignment", []deleteLink{groupStep}), ownRole, ownMember},
			wantRefusedBy: []libraryKind{checkedWith("HoldSlip", slipWay)},
			wantUnder:     []libraryKind{underMember, underRole, checkedWith("HeldAssignment", []deleteLink{groupStep})},
		},
		{
			name:      "a record whose delete takes its own kind is one kind, with its links to itself",
			records:   []string{"NestedAssignment"},
			wantTaken: []libraryKind{nested, ownRole, ownMember},
			wantUnder: []libraryKind{underMember, underRole, checkedWith("NestedAssignment", []deleteLink{groupStep})},
		},
		{
			name: "a record that names a member's row, and a table no resource declares, are in no list",
			// TaskAssignments names the group, and no record of this run declares it.
			records:   []string{"Task", "PlaceBadge"},
			wantTaken: []libraryKind{ownRole, ownMember},
			wantUnder: []libraryKind{underMember, underRole},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			run, _, err := deletedWithRun(t, tt.records, nil)
			if err != nil {
				t.Fatalf("resolveLibraryDeleteTrees() error = %v", err)
			}
			tree := run["CrewGroup"].DeleteTree
			if diff := cmp.Diff(tt.wantTaken, libraryKinds(tree.Taken)); diff != "" {
				t.Errorf("the group's taken kinds (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantRefusedBy, libraryKinds(tree.RefusedBy)); diff != "" {
				t.Errorf("the group's refusing kinds (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantUnder, libraryKinds(tree.Under)); diff != "" {
				t.Errorf("the group's kinds under it (-want +got):\n%s", diff)
			}
			for name, res := range run {
				if res.identity == nil && res.DeleteTree != nil && strings.Contains(res.DeleteTreeLiteral(), "CrewGroup") {
					t.Errorf("the description of %s, a record of the application's, names the group:\n%s", name, res.DeleteTreeLiteral())
				}
			}
		})
	}
}

// Test_deletedWith_groupLiteral pins how the group's description writes a kind of the
// application's it takes: by the function the record's own package declares for it,
// marked as checked as the caller, and not trusted; the group's own kinds are written as
// the first description writes them.
func Test_deletedWith_groupLiteral(t *testing.T) {
	t.Parallel()

	run, first, err := deletedWithRun(t, []string{"Task", "TaskAssignment"}, nil)
	if err != nil {
		t.Fatalf("resolveLibraryDeleteTrees() error = %v", err)
	}
	literal := run["CrewGroup"].DeleteTreeLiteral()
	for _, want := range []string{
		"Resource: \"TaskAssignments\",\nWords: resource.DeleteWords{One: \"task assignment\", Many: \"task assignments\"},",
		"AsCaller: true,\nRows: resources.TaskAssignmentTakenRows(),\n},",
	} {
		if !strings.Contains(literal, want) {
			t.Errorf("the group's description lacks %q:\n%s", want, literal)
		}
	}
	// The taken kind and the kind under Under; the roles and the members, twice each.
	if got := strings.Count(literal, "AsCaller: true,"); got != 2 {
		t.Errorf("the group's description writes AsCaller %d time(s), want twice:\n%s", got, literal)
	}
	if got, want := strings.Count(literal, "Trusted: true,"), strings.Count(first["CrewGroup"], "Trusted: true,"); got != want || got != 2 {
		t.Errorf("the group's description writes Trusted %d time(s), its first %d, want twice:\n%s", got, want, literal)
	}
	if !strings.Contains(literal, "Rows: resource.NewTakenRows(func(*resource.PatchSet[CrewGroupMember], *resource.Caller) {}),") {
		t.Errorf("the group's description changed how it takes its members:\n%s", literal)
	}
}

// Test_deletedWith_groupNoApplicationNames pins a group no record of the application's
// names: its second description is its first.
func Test_deletedWith_groupNoApplicationNames(t *testing.T) {
	t.Parallel()

	run, first, err := deletedWithRun(t, []string{"Task", "Project"}, nil)
	if err != nil {
		t.Fatalf("resolveLibraryDeleteTrees() error = %v", err)
	}
	if diff := cmp.Diff(first["CrewGroup"], run["CrewGroup"].DeleteTreeLiteral()); diff != "" {
		t.Errorf("the group's description changed (-first +second):\n%s", diff)
	}
}

// Test_deletedWith_refusals pins the places @deletedWith is refused, each in the sentence
// that says what to write: a column that names a record of the application's, the
// tenant record and a read-only table among them, a column that names no group and no
// account of an auth by a foreign key, a read-only table, and a column of the tenant
// record, whatever it names.
func Test_deletedWith_refusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		records []string
		edit    func(map[string]*resourceInfo)
		wantErr string
	}{
		{
			name:    "a column that names a record of the application's",
			records: []string{"Project", "ProjectTask"},
			wantErr: "@deletedWith on ProjectTask.ProjectID: ProjectID names a project, a record of this application. Say on Project that its project tasks go with it: @deleteCascade(ProjectTask).",
		},
		{
			name:    "a column that names the tenant record",
			records: []string{"Project", "ProjectTask"},
			edit: func(run map[string]*resourceInfo) {
				run["Project"].IsTenant = true
			},
			wantErr: "@deletedWith on ProjectTask.ProjectID: ProjectID names a tenant, and a tenant's delete takes every row under the tenant already. Remove @deletedWith.",
		},
		{
			name:    "a column that names a read-only table",
			records: []string{"Project", "ProjectTask"},
			edit: func(run map[string]*resourceInfo) {
				run["Project"].EnumerationType = "ProjectKind"
			},
			wantErr: "@deletedWith on ProjectTask.ProjectID: ProjectID names a project, a read-only table (@enumerate) whose rows are never deleted. Remove @deletedWith.",
		},
		{
			name:    "a column the schema holds to no group and no account",
			records: []string{"LevelTask"},
			wantErr: "@deletedWith on LevelTask.LevelID: the schema holds LevelId to no group and no account of an auth by a foreign key. @deletedWith goes on the column that names a group or an account of the library's; add the foreign key, or remove @deletedWith.",
		},
		{
			name:    "a column the schema holds to a member's row",
			records: []string{"PlaceBadge"},
			edit: func(run map[string]*resourceInfo) {
				markField(run, "PlaceBadge", "UserID", true)
			},
			wantErr: "@deletedWith on PlaceBadge.UserID: the schema holds UserId to no group and no account of an auth by a foreign key. @deletedWith goes on the column that names a group or an account of the library's; add the foreign key, or remove @deletedWith.",
		},
		{
			name:    "a read-only table",
			records: []string{"Rank"},
			edit: func(run map[string]*resourceInfo) {
				run["Rank"].EnumerationType = "RankType"
			},
			wantErr: "@deletedWith on Rank.GroupID: Rank is a read-only table (@enumerate): its rows are never deleted. Remove @deletedWith.",
		},
		{
			name:    "a column of the tenant record that names a group",
			records: []string{"Task", "TaskAssignment"},
			edit: func(run map[string]*resourceInfo) {
				run["TaskAssignment"].IsTenant = true
			},
			wantErr: "@deletedWith on TaskAssignment.GroupID: TaskAssignment is the tenant record, and a tenant is deleted by its own delete. Remove @deletedWith.",
		},
		{
			name:    "a column of the tenant record that names an account",
			records: []string{"Task", "TaskAssignment"},
			edit: func(run map[string]*resourceInfo) {
				run["TaskAssignment"].IsTenant = true
				markField(run, "TaskAssignment", "GroupID", false)
				markField(run, "TaskAssignment", "UserID", true)
			},
			wantErr: "@deletedWith on TaskAssignment.UserID: TaskAssignment is the tenant record, and a tenant is deleted by its own delete. Remove @deletedWith.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := deletedWithRun(t, tt.records, tt.edit)
			if err == nil {
				t.Fatalf("resolveLibraryDeleteTrees() = nil, want the refusal %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("resolveLibraryDeleteTrees() error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// renderResourceFile renders a record's generated resource file in the package named,
// formatted.
func renderResourceFile(t *testing.T, res *resourceInfo, pkg string) string {
	t.Helper()

	out, err := (&client{}).generateTemplateOutput("resourceFile", resourceFileTemplate, &resourceFileData{Source: "fixture", Package: pkg, Resource: res})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	formatted, err := format.Source(out)
	if err != nil {
		t.Fatalf("format.Source() error = %v on:\n%s", err, out)
	}

	return string(formatted)
}

// Test_deletedWith_takenRowsFunction pins the function a record's own file declares for
// the delete of a record of another package that takes its rows: its delete patch around
// the row's patch set, its delete rule registered, and armed against a caller that is not
// zero, with its questions asked as a removal where the record decides what a person may
// do. A record nothing of another package takes declares none.
func Test_deletedWith_takenRowsFunction(t *testing.T) {
	t.Parallel()

	run, _, err := deletedWithRun(t, []string{"Task", "TaskAssignment", "NotedAssignment", "AssignmentNote", "AssignmentHold"}, func(run map[string]*resourceInfo) {
		run["TaskAssignment"].ValidateDeleteType = "AssignmentDeleteRule"
	})
	if err != nil {
		t.Fatalf("resolveLibraryDeleteTrees() error = %v", err)
	}
	file := renderResourceFile(t, run["TaskAssignment"], "resources")
	for _, want := range []string{
		"// TaskAssignmentTakenRows is how a delete of a record of another package deletes a\n// TaskAssignment with it (resource.NewTakenRows)",
		"func TaskAssignmentTakenRows() resource.TakenRows {\n" +
			"\treturn resource.NewTakenRows(func(patchSet *resource.PatchSet[TaskAssignment], caller *resource.Caller) {\n" +
			"\t\tpatch := &TaskAssignmentDeletePatch{patchSet: patchSet}\n" +
			"\t\tpatch.registerValidateFunc()\n" +
			"\t\tif !caller.IsZero() {\n" +
			"\t\t\tpatch.Enforce(caller)\n" +
			"\t\t}\n" +
			"\t})\n" +
			"}\n",
	} {
		if !strings.Contains(file, want) {
			t.Errorf("TaskAssignment's file lacks %q:\n%s", want, file)
		}
	}
	// A kind taken through the record's own delete is taken by the group's too.
	if note := renderResourceFile(t, run["AssignmentNote"], "resources"); !strings.Contains(note, "func AssignmentNoteTakenRows() resource.TakenRows {") {
		t.Errorf("AssignmentNote's file lacks its TakenRows:\n%s", note)
	}
	for _, name := range []string{"Task", "AssignmentHold"} {
		if file := renderResourceFile(t, run[name], "resources"); strings.Contains(file, "TakenRows()") {
			t.Errorf("%s's file declares a TakenRows, and nothing of another package takes it:\n%s", name, file)
		}
	}

	// A record that decides what a person may do asks its questions as a removal.
	guarded := run["TaskAssignment"]
	guarded.subjectReadThroughs = []subjectReadThrough{{}}
	if got, want := guarded.TakenRowsLiteral(), "if !caller.IsZero() {\npatch.Enforce(caller)\npatchSet.RegisterDeleteCheckFunc(func(ctx context.Context, txn resource.ReadWriteTransaction) error {\nreturn RefuseImpersonatorAsTaskAssignmentSubject(ctx, txn, resource.OperationDelete, patchSet.PrimaryKey(), patchSet)\n})\n}"; !strings.Contains(got, want) {
		t.Errorf("TakenRowsLiteral() = %q, want it to hold %q", got, want)
	}
}

// Test_deletedWith_libraryRowsAlone pins the group's delete patch: it can leave the
// application's rows out (WithLibraryRowsAlone), whether or not a record of the
// application's names the group, and a record of the application's that takes rows of its
// own has no such choice. The group's description says what it lists.
func Test_deletedWith_libraryRowsAlone(t *testing.T) {
	t.Parallel()

	const alone = "func (p *CrewGroupDeletePatch) WithLibraryRowsAlone() *CrewGroupDeletePatch {\n\tp.patchSet.DeleteLibraryRowsAlone()\n\n\treturn p\n}\n"
	const said = "DeleteTree describes what a delete of a CrewGroup takes with it and what refuses it: the library's own kinds, and the application's records that name the CrewGroup, taken where their column says so (@deletedWith) and refusing its delete where it does not. The kinds taken are in the order their rows are deleted, deepest first, each with its ways up to the CrewGroup and what the delete of one of its rows costs."
	for name, records := range map[string][]string{
		"a group a record of the application's names": {"Task", "TaskAssignment", "NotedAssignment", "AssignmentNote", "AssignmentHold"},
		"a group nothing of the application's names":  {"Task"},
	} {
		run, _, err := deletedWithRun(t, records, nil)
		if err != nil {
			t.Fatalf("%s: resolveLibraryDeleteTrees() error = %v", name, err)
		}
		file := renderResourceFile(t, run["CrewGroup"], "crewresources")
		if !strings.Contains(file, alone) {
			t.Errorf("%s: the group's file lacks %q:\n%s", name, alone, file)
		}
		if flat := strings.ReplaceAll(file, "\n// ", " "); !strings.Contains(flat, said) {
			t.Errorf("%s: the group's file lacks the comment %q:\n%s", name, said, file)
		}
		if note, ok := run["NotedAssignment"]; ok {
			if file := renderResourceFile(t, note, "resources"); strings.Contains(file, "WithLibraryRowsAlone") || !strings.Contains(file, "WithEverythingUnder") {
				t.Errorf("%s: NotedAssignment's patch has WithLibraryRowsAlone, or no WithEverythingUnder:\n%s", name, file)
			}
		}
	}
}

// Test_deletedWith_accountRecord pins the account record of an auth's, which a column of
// the application's can name as it names a group: it goes by the word a person calls it,
// and its delete patch always takes rows when told, whether or not a record of the
// application's names it. It can leave the application's rows out
// (WithLibraryRowsAlone), and its WithEverythingUnder says what it takes.
func Test_deletedWith_accountRecord(t *testing.T) {
	t.Parallel()

	run, _, err := deletedWithRun(t, []string{"Task"}, nil)
	if err != nil {
		t.Fatalf("resolveLibraryDeleteTrees() error = %v", err)
	}
	account := run["CrewSessionUser"]
	c := &client{tableMap: deletedWithTables()}
	if one, many := c.deleteWords(account); one != "account" || many != "accounts" {
		t.Errorf("the account's words = %q, %q, want account, accounts", one, many)
	}
	if !account.TakesRowsUnder() {
		t.Error("the account record's delete takes no rows when told")
	}
	file := renderResourceFile(t, account, "crewresources")
	for _, want := range []string{
		"func (p *CrewSessionUserDeletePatch) WithEverythingUnder() *CrewSessionUserDeletePatch {",
		"func (p *CrewSessionUserDeletePatch) Left() *resource.DeleteLeft {",
		"func (p *CrewSessionUserDeletePatch) DeleteAlso(kinds ...resource.TakenKind) *CrewSessionUserDeletePatch {",
		"func (p *CrewSessionUserDeletePatch) WithLibraryRowsAlone() *CrewSessionUserDeletePatch {",
	} {
		if !strings.Contains(file, want) {
			t.Errorf("the account's file lacks %q:\n%s", want, file)
		}
	}
	const said = "WithEverythingUnder says the delete takes the rows under the CrewSessionUser with it: the kinds its description lists as taken, the application's rows that name it in a column that says so (@deletedWith) among them, each through its own delete. It takes the kinds handed to the delete as well (DeleteAlso). Without it, a delete of a CrewSessionUser that has such rows deletes nothing and is answered with the question a person is asked first (httpio.HasPreconditionRequired). Where the rows cost more than one save takes, the delete takes what fits, deepest first, and Left says what is left: buffer the delete again, in a transaction of its own, until Left answers nil."
	for name, file := range map[string]string{"account": file, "group": renderResourceFile(t, run["CrewGroup"], "crewresources")} {
		want := said
		if name == "group" {
			want = strings.ReplaceAll(said, "CrewSessionUser", "CrewGroup")
		}
		if flat := strings.ReplaceAll(file, "\n// ", " "); !strings.Contains(flat, want) {
			t.Errorf("the %s's WithEverythingUnder lacks the comment %q:\n%s", name, want, file)
		}
	}
	// A record of the application's that takes rows says what its @deleteCascade lists.
	if res := run["Task"]; res.TakesRowsUnder() {
		t.Errorf("Task takes rows under it, and it declares none")
	}
}

// markField marks the record's field as saying its row goes with what it names, or not
// (resourceField.deletedWith), as the annotation marks it.
func markField(run map[string]*resourceInfo, record, field string, marked bool) {
	for _, f := range run[record].Fields {
		if f.Name() == field {
			f.deletedWith = marked
		}
	}
}

// userStep is the step from a person's column up to the accounts table.
var userStep = link("UserId", "CrewSessionUsers")

// takenWithinReach is an application's kind an account's delete takes, by its ways up
// to the account: it goes on the permission to delete the account where it reaches.
func takenWithinReach(name string, paths ...[]deleteLink) libraryKind {
	return libraryKind{Name: name, Paths: paths, Deletes: true, WithinReach: true, Package: "resources"}
}

// Test_deletedWith_accountDescription pins the account record's description written a
// second time over the application's records, by a group's rules: a record whose person
// column says it goes with the account is taken, and what its own delete takes is taken
// too, each marked WithinReach and not AsCaller, since an account's rows go on the
// permission to delete it; what refuses its own delete refuses the account's, marked
// AsCaller; a record that names the account and says nothing is under Under alone,
// marked AsCaller. A record whose two columns say it goes with the group and with the
// account is a taken kind of each, marked for each. The group's lists are what a
// group's own rules give them.
func Test_deletedWith_accountDescription(t *testing.T) {
	t.Parallel()

	noteWay := []deleteLink{link("AssignmentId", "NotedAssignments"), userStep}
	tests := []struct {
		name          string
		records       []string
		edit          func(map[string]*resourceInfo)
		wantTaken     []libraryKind
		wantRefusedBy []libraryKind
		wantUnder     []libraryKind
		wantGroup     []libraryKind
	}{
		{
			name:    "the person column says the row goes with the account",
			records: []string{"Task", "TaskAssignment"},
			edit: func(run map[string]*resourceInfo) {
				markField(run, "TaskAssignment", "UserID", true)
				markField(run, "TaskAssignment", "GroupID", false)
			},
			wantTaken: []libraryKind{takenWithinReach("TaskAssignment", []deleteLink{userStep})},
			wantUnder: []libraryKind{checkedWith("TaskAssignment", []deleteLink{userStep})},
			wantGroup: []libraryKind{ownRole, ownMember},
		},
		{
			name:      "the person column says nothing",
			records:   []string{"Task", "TaskAssignment"},
			edit:      func(run map[string]*resourceInfo) { markField(run, "TaskAssignment", "GroupID", false) },
			wantUnder: []libraryKind{checkedWith("TaskAssignment", []deleteLink{userStep})},
			wantGroup: []libraryKind{ownRole, ownMember},
		},
		{
			name:      "both columns say so: a taken kind of the group's and of the account's",
			records:   []string{"Task", "TaskAssignment"},
			edit:      func(run map[string]*resourceInfo) { markField(run, "TaskAssignment", "UserID", true) },
			wantTaken: []libraryKind{takenWithinReach("TaskAssignment", []deleteLink{userStep})},
			wantUnder: []libraryKind{checkedWith("TaskAssignment", []deleteLink{userStep})},
			wantGroup: []libraryKind{takenWith("TaskAssignment", []deleteLink{groupStep}), ownRole, ownMember},
		},
		{
			name:          "what the record's own delete takes is within reach, and what refuses it is checked as the caller",
			records:       []string{"NotedAssignment", "AssignmentNote", "AssignmentHold"},
			edit:          func(run map[string]*resourceInfo) { markField(run, "NotedAssignment", "UserID", true) },
			wantTaken:     []libraryKind{takenWithinReach("AssignmentNote", noteWay), takenWithinReach("NotedAssignment", []deleteLink{userStep})},
			wantRefusedBy: []libraryKind{checkedWith("AssignmentHold", []deleteLink{link("AssignmentId", "NotedAssignments"), userStep})},
			wantUnder:     []libraryKind{checkedWith("NotedAssignment", []deleteLink{userStep})},
			wantGroup: []libraryKind{
				takenWith("AssignmentNote", []deleteLink{link("AssignmentId", "NotedAssignments"), groupStep}),
				takenWith("NotedAssignment", []deleteLink{groupStep}), ownRole, ownMember,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			run, _, err := deletedWithRun(t, tt.records, tt.edit)
			if err != nil {
				t.Fatalf("resolveLibraryDeleteTrees() error = %v", err)
			}
			tree := run["CrewSessionUser"].DeleteTree
			if tree == nil {
				t.Fatal("the account has no description of its delete")
			}
			if tree.Record.One != "account" || tree.Record.Many != "accounts" {
				t.Errorf("the account's words = %q, %q, want account, accounts", tree.Record.One, tree.Record.Many)
			}
			if diff := cmp.Diff(tt.wantTaken, libraryKinds(tree.Taken)); diff != "" {
				t.Errorf("the account's taken kinds (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantRefusedBy, libraryKinds(tree.RefusedBy)); diff != "" {
				t.Errorf("the account's refusing kinds (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantUnder, libraryKinds(tree.Under)); diff != "" {
				t.Errorf("the account's kinds under it (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantGroup, libraryKinds(run["CrewGroup"].DeleteTree.Taken)); diff != "" {
				t.Errorf("the group's taken kinds (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_deletedWith_accountLiteral pins how the account's description writes a kind of the
// application's it takes: by the function the record's own package declares for it,
// marked WithinReach and not AsCaller. A record that goes with the group and with the
// account declares that function once.
func Test_deletedWith_accountLiteral(t *testing.T) {
	t.Parallel()

	run, _, err := deletedWithRun(t, []string{"Task", "TaskAssignment"}, func(run map[string]*resourceInfo) {
		markField(run, "TaskAssignment", "UserID", true)
	})
	if err != nil {
		t.Fatalf("resolveLibraryDeleteTrees() error = %v", err)
	}
	if run["CrewSessionUser"].DeleteTree == nil {
		t.Fatal("the account has no description of its delete")
	}
	literal := run["CrewSessionUser"].DeleteTreeLiteral()
	if want := "Words: resource.DeleteWords{One: \"account\", Many: \"accounts\"},"; !strings.Contains(literal, want) {
		t.Errorf("the account's description lacks %q:\n%s", want, literal)
	}
	_, taken, found := strings.Cut(literal, "Taken:")
	if !found {
		t.Fatalf("the account's description lists no taken kinds:\n%s", literal)
	}
	taken, _, _ = strings.Cut(taken, "Under:")
	if want := "WithinReach: true,\nRows: resources.TaskAssignmentTakenRows(),\n},"; !strings.Contains(taken, want) || strings.Contains(taken, "AsCaller") {
		t.Errorf("the account's taken kinds are written\n%s\nwant %q and no AsCaller", taken, want)
	}
	if file := renderResourceFile(t, run["TaskAssignment"], "resources"); strings.Count(file, "func TaskAssignmentTakenRows() resource.TakenRows {") != 1 {
		t.Errorf("TaskAssignment's file declares its TakenRows other than once:\n%s", file)
	}
}

// Test_deletedWith_accountTenants pins the tenants an account's description names for its
// sentences: the tenant record's table, key and name column and the word for one tenant,
// where the application has a tenant record and a kind is within reach; Name empty where
// the record marks no name; none where the application has no tenant record, for an
// account no marked column names, and in a group's description.
func Test_deletedWith_accountTenants(t *testing.T) {
	t.Parallel()

	tenantRecord := func(named bool) func(map[string]*resourceInfo) {
		return func(run map[string]*resourceInfo) {
			project := run["Project"]
			project.IsTenant, project.TenantLabel = true, "company"
			if named {
				project.TenantNameField = project.fieldNamed("Name")
			}
		}
	}
	both := func(edits ...func(map[string]*resourceInfo)) func(map[string]*resourceInfo) {
		return func(run map[string]*resourceInfo) {
			for _, edit := range edits {
				edit(run)
			}
		}
	}
	markPerson := func(run map[string]*resourceInfo) { markField(run, "TaskAssignment", "UserID", true) }

	tests := []struct {
		name        string
		edit        func(map[string]*resourceInfo)
		wantAccount string
	}{
		{name: "a tenant record that marks its name", edit: both(tenantRecord(true), markPerson), wantAccount: `Tenants: &resource.DeleteTenants{Table: "Projects", Key: "Id", Name: "Name", Word: "company"},`},
		{name: "a tenant record that marks none", edit: both(tenantRecord(false), markPerson), wantAccount: `Tenants: &resource.DeleteTenants{Table: "Projects", Key: "Id", Name: "", Word: "company"},`},
		{name: "no tenant record", edit: markPerson},
		{name: "an account no marked column names", edit: tenantRecord(true)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			run, _, err := deletedWithRun(t, []string{"Task", "TaskAssignment", "Project"}, tt.edit)
			if err != nil {
				t.Fatalf("resolveLibraryDeleteTrees() error = %v", err)
			}
			var account string
			if run["CrewSessionUser"].DeleteTree != nil {
				account = run["CrewSessionUser"].DeleteTreeLiteral()
			}
			if tt.wantAccount != "" && !strings.Contains(account, tt.wantAccount) {
				t.Errorf("the account's description lacks %q:\n%s", tt.wantAccount, account)
			}
			if tt.wantAccount == "" && strings.Contains(account, "Tenants:") {
				t.Errorf("the account's description names the tenants:\n%s", account)
			}
			if group := run["CrewGroup"].DeleteTreeLiteral(); strings.Contains(group, "Tenants:") {
				t.Errorf("the group's description names the tenants:\n%s", group)
			}
		})
	}
}
