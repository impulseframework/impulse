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
// it, and beside them the application's tables name a group, a task, a project, an
// assignment or a member's row, each by a foreign key the database does not follow.
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
		"CrewGroupMembers":  {PkCount: 2, ForeignKeys: []foreignKeyMeta{fk("GroupId", "CrewGroups")}, Columns: map[string]columnMeta{"GroupId": key(0), "UserId": key(1)}},
		"CrewGroupRoles":    {PkCount: 2, ForeignKeys: []foreignKeyMeta{fk("GroupId", "CrewGroups")}, Columns: map[string]columnMeta{"GroupId": key(0), "Role": key(1)}},
		"Tasks":             under("", "", false),
		"TaskAssignments":   under("TaskId", "Tasks", true),
		"TaskWatches":       under("TaskId", "Tasks", true),
		"ShiftCovers":       covers,
		"NotedAssignments":  under("GroupId", "CrewGroups", false),
		"AssignmentNotes":   under("AssignmentId", "NotedAssignments", false),
		"AssignmentHolds":   under("AssignmentId", "NotedAssignments", false),
		"HeldAssignments":   under("GroupId", "CrewGroups", false),
		"HoldSlips":         under("AssignmentId", "HeldAssignments", false),
		"NestedAssignments": nested,
		"PlaceBadges":       badges,
		"Projects":          under("", "", false),
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

	return run, first, c.resolveLibraryDeleteTrees(resources)
}

// libraryKind is what a test reads of a kind of a group's description: its name, its
// ways up to the group, its links to itself, and its marks.
type libraryKind struct {
	Name     string
	Paths    [][]deleteLink
	Within   []deleteLink
	Deletes  bool
	Trusted  bool
	AsCaller bool
	Package  string
}

// libraryKinds reads the kinds of one list.
func libraryKinds(kinds []*deleteKind) []libraryKind {
	var got []libraryKind
	for _, k := range kinds {
		got = append(got, libraryKind{Name: k.Name, Paths: k.Paths, Within: k.Within, Deletes: k.Deletes, Trusted: k.Trusted, AsCaller: k.AsCaller, Package: k.Package})
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

// Test_deletedWith_refusals pins the three places @deletedWith is refused, each in the
// sentence that says what to write: a column that names a record of the application's,
// a column that names no group of an auth by a foreign key, and a read-only table.
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
			name:    "a column the schema holds to no group",
			records: []string{"LevelTask"},
			wantErr: "@deletedWith on LevelTask.LevelID: the schema holds LevelId to no group of an auth by a foreign key. @deletedWith goes on the column that names a group of the library's; add the foreign key, or remove @deletedWith.",
		},
		{
			name:    "a read-only table",
			records: []string{"Rank"},
			edit: func(run map[string]*resourceInfo) {
				run["Rank"].EnumerationType = "RankType"
			},
			wantErr: "@deletedWith on Rank.GroupID: Rank is a read-only table (@enumerate): its rows are never deleted. Remove @deletedWith.",
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
