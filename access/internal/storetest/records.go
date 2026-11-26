package storetest

import (
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// The fixtures of the record phase, kept apart from the other phases': a tenant of its
// own with two projects and a client, two people and two groups. The phase removes what
// it writes, so the policy read after it finds what the phases before it left.
var (
	recordTenant = accesstypes.DomainPolicyScope("records")

	projectOne   = accesstypes.NewRecordScope("records", "Projects", "p-1")
	projectTwo   = accesstypes.NewRecordScope("records", "Projects", "p-2")
	clientOne    = accesstypes.NewRecordScope("records", "Clients", "p-1")
	otherProject = accesstypes.NewRecordScope("elsewhere", "Projects", "p-1")
)

const (
	projectEditor = accesstypes.Role("ProjectEditor")
	projectViewer = accesstypes.Role("ProjectViewer")

	lee = accesstypes.User("lee")
	ned = accesstypes.User("ned")

	projectCrew   = accesstypes.Group("7f2f6a52-6c63-4a7a-9c1e-00000000000b")
	projectGuests = accesstypes.Group("7f2f6a52-6c63-4a7a-9c1e-00000000000c")
	// noGroup is an id no group is kept under.
	noGroup = accesstypes.Group("7f2f6a52-6c63-4a7a-9c1e-00000000000d")
)

// equateRecords compares RecordScope, a comparable struct with unexported fields, by ==.
var equateRecords = cmpopts.EquateComparable(accesstypes.RecordScope{})

// runRecordRoles holds the record tables to their contract: a role held on a record is
// listed on that record alone; given again it is one row with the new period; the listing
// leaves out one that has ended and keeps one that has not begun; a group's needs the
// group's row and is listed with the group's name, and goes with the group; removing every
// role held on a record leaves every other record's; and the policy read returns every
// row with its period, its hash moving with each.
func runRecordRoles(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	for _, group := range []*policy.GroupRow{
		{Group: policy.Group{ID: projectCrew, Scope: recordTenant}, Name: "Crew", Description: "Who works on the projects."},
		{Group: policy.Group{ID: projectGuests, Scope: recordTenant}, Name: "Guests", Description: "Who looks in on a project."},
	} {
		if taken, err := store.InsertGroup(ctx, group); err != nil || taken {
			t.Fatalf("InsertGroup(%s) = (%v, %v), want it written", group.Name, taken, err)
		}
	}

	p := newPeriods()
	t.Run("a person's role", func(t *testing.T) { recordUserRoles(t, store, &p) })
	t.Run("a group's role", func(t *testing.T) { recordGroupRoles(t, store, &p) })
	t.Run("the policy read", func(t *testing.T) { recordPolicy(t, store, &p) })
	t.Run("a record's roles go together", func(t *testing.T) { recordRolesDeleted(t, store) })
	t.Run("a group's go with the group", func(t *testing.T) { recordGroupDeleted(t, store) })
}

// leeHolds is a fixture shorthand for lee's role on a record as the listing returns it.
func leeHolds(at accesstypes.RecordScope, role accesstypes.Role, period accesstypes.Period) policy.RecordAssignment {
	return policy.RecordAssignment{RecordMembership: policy.RecordMembership{Scope: at, Member: policy.Subject{Kind: policy.SubjectUser, Name: string(lee)}, Role: role, Period: period}}
}

// heldByGroup is a fixture shorthand for a group's role on a record as the listing returns
// it, with the group's name.
func heldByGroup(at accesstypes.RecordScope, group accesstypes.Group, name string, role accesstypes.Role, period accesstypes.Period) policy.RecordAssignment {
	return policy.RecordAssignment{
		RecordMembership: policy.RecordMembership{Scope: at, Member: policy.Subject{Kind: policy.SubjectGroup, Name: string(group)}, Role: role, Period: period},
		GroupName:        name,
	}
}

// listRecord lists the roles held on a record, failing the test on an error.
func listRecord(t *testing.T, store access.Store, at accesstypes.RecordScope) []policy.RecordAssignment {
	t.Helper()
	held, err := store.ListRecordRoles(t.Context(), at)
	if err != nil {
		t.Fatalf("ListRecordRoles(%v) error = %v", at, err)
	}

	return held
}

// recordUserRoles holds a person's role on a record to its contract. It leaves lee holding
// ProjectEditor on the first project with no period and ProjectViewer there not yet
// begun, and ned ProjectViewer there ended.
func recordUserRoles(t *testing.T, store access.Store, p *periods) {
	t.Helper()
	ctx := t.Context()

	if err := store.InsertRecordUserRole(ctx, projectOne, lee, projectEditor, p.current); err != nil {
		t.Fatalf("InsertRecordUserRole() error = %v", err)
	}
	if diff := cmp.Diff([]policy.RecordAssignment{leeHolds(projectOne, projectEditor, p.current)}, listRecord(t, store, projectOne), equateRecords); diff != "" {
		t.Errorf("ListRecordRoles(the first project) (-want +got):\n%s", diff)
	}
	// Listed on that record and no other: another record of the kind, another kind with
	// the same key, and another tenant with the same kind and key.
	for _, other := range []accesstypes.RecordScope{projectTwo, clientOne, otherProject} {
		if held := listRecord(t, store, other); len(held) != 0 {
			t.Errorf("ListRecordRoles(%v) = %v, want none: the role is held on the first project alone", other, held)
		}
	}

	// Given again with another period it is one row, with the new period.
	if err := store.InsertRecordUserRole(ctx, projectOne, lee, projectEditor, accesstypes.Period{}); err != nil {
		t.Fatalf("InsertRecordUserRole() given again error = %v", err)
	}
	if diff := cmp.Diff([]policy.RecordAssignment{leeHolds(projectOne, projectEditor, accesstypes.Period{})}, listRecord(t, store, projectOne), equateRecords); diff != "" {
		t.Errorf("ListRecordRoles() after the role was given again with no period (-want +got):\n%s", diff)
	}

	// One that has not begun is listed, one that has ended is not; people by user then role.
	if err := store.InsertRecordUserRole(ctx, projectOne, lee, projectViewer, p.upcoming); err != nil {
		t.Fatalf("InsertRecordUserRole() error = %v", err)
	}
	if err := store.InsertRecordUserRole(ctx, projectOne, ned, projectViewer, p.ended); err != nil {
		t.Fatalf("InsertRecordUserRole() error = %v", err)
	}
	want := []policy.RecordAssignment{
		leeHolds(projectOne, projectEditor, accesstypes.Period{}),
		leeHolds(projectOne, projectViewer, p.upcoming),
	}
	if diff := cmp.Diff(want, listRecord(t, store, projectOne), equateRecords); diff != "" {
		t.Errorf("ListRecordRoles() with one not begun and one ended (-want +got):\n%s", diff)
	}

	// A delete removes the one row, and a delete of a row that is not there is a no-op.
	if err := store.InsertRecordUserRole(ctx, projectTwo, ned, projectEditor, accesstypes.Period{}); err != nil {
		t.Fatalf("InsertRecordUserRole() error = %v", err)
	}
	if err := store.DeleteRecordUserRole(ctx, projectTwo, ned, projectEditor); err != nil {
		t.Fatalf("DeleteRecordUserRole() error = %v", err)
	}
	if err := store.DeleteRecordUserRole(ctx, projectTwo, ned, projectEditor); err != nil {
		t.Errorf("DeleteRecordUserRole() of a row that is gone: error = %v, want none", err)
	}
	if held := listRecord(t, store, projectTwo); len(held) != 0 {
		t.Errorf("ListRecordRoles(the second project) after its one role was removed = %v, want none", held)
	}
}

// recordGroupRoles holds a group's role on a record to its contract. It leaves the crew
// holding ProjectEditor on the first project and the guests ProjectViewer there.
func recordGroupRoles(t *testing.T, store access.Store, p *periods) {
	t.Helper()
	ctx := t.Context()

	err := store.InsertRecordGroupRole(ctx, noGroup, projectOne, projectEditor, accesstypes.Period{})
	if !errors.Is(err, policy.ErrNoGroup) {
		t.Errorf("InsertRecordGroupRole() for a group that is not there: error = %v, want policy.ErrNoGroup", err)
	}
	for _, g := range []struct {
		group accesstypes.Group
		role  accesstypes.Role
	}{{projectGuests, projectViewer}, {projectCrew, projectEditor}} {
		if err := store.InsertRecordGroupRole(ctx, g.group, projectOne, g.role, accesstypes.Period{}); err != nil {
			t.Fatalf("InsertRecordGroupRole(%s) error = %v", g.group, err)
		}
	}
	// The people first, then the groups by name: Crew before Guests.
	want := []policy.RecordAssignment{
		leeHolds(projectOne, projectEditor, accesstypes.Period{}),
		leeHolds(projectOne, projectViewer, p.upcoming),
		heldByGroup(projectOne, projectCrew, "Crew", projectEditor, accesstypes.Period{}),
		heldByGroup(projectOne, projectGuests, "Guests", projectViewer, accesstypes.Period{}),
	}
	if diff := cmp.Diff(want, listRecord(t, store, projectOne), equateRecords); diff != "" {
		t.Errorf("ListRecordRoles() with two groups (-want +got):\n%s", diff)
	}
	for _, other := range []accesstypes.RecordScope{projectTwo, clientOne, otherProject} {
		if held := listRecord(t, store, other); len(held) != 0 {
			t.Errorf("ListRecordRoles(%v) = %v, want none", other, held)
		}
	}

	if err := store.InsertRecordGroupRole(ctx, projectCrew, projectTwo, projectEditor, accesstypes.Period{}); err != nil {
		t.Fatalf("InsertRecordGroupRole() error = %v", err)
	}
	if err := store.DeleteRecordGroupRole(ctx, projectCrew, projectTwo, projectEditor); err != nil {
		t.Fatalf("DeleteRecordGroupRole() error = %v", err)
	}
	if err := store.DeleteRecordGroupRole(ctx, projectCrew, projectTwo, projectEditor); err != nil {
		t.Errorf("DeleteRecordGroupRole() of a row that is gone: error = %v, want none", err)
	}
	if held := listRecord(t, store, projectTwo); len(held) != 0 {
		t.Errorf("ListRecordRoles(the second project) after the group's role was removed = %v, want none", held)
	}
}

// recordRows reads the policy and answers the roles held on the records of the phase's
// tenant, and the records' hash.
func recordRows(t *testing.T, store access.Store) (held []policy.RecordMembership, hash [32]byte) {
	t.Helper()
	records, err := store.ReadPolicy(t.Context())
	if err != nil {
		t.Fatalf("ReadPolicy() error = %v", err)
	}
	for i := range records.RecordMemberships {
		if m := &records.RecordMemberships[i]; m.Scope.Domain() == "records" {
			held = append(held, *m)
		}
	}

	return held, records.Hash()
}

// recordPolicy holds the policy read to every row held on a record, the ended one among
// them, each with its period, and the hash to each row and each period.
func recordPolicy(t *testing.T, store access.Store, p *periods) {
	t.Helper()
	ctx := t.Context()

	held, before := recordRows(t, store)
	byMember := make(map[string]policy.RecordMembership, len(held))
	for i := range held {
		byMember[held[i].Member.Name+"/"+string(held[i].Role)] = held[i]
	}
	if len(held) != 5 {
		t.Errorf("ReadPolicy() holds %d roles on the phase's records, want 5 (lee's two, ned's ended one, and the two groups'): %v", len(held), held)
	}
	if neds, ok := byMember[string(ned)+"/"+string(projectViewer)]; !ok || !neds.Period.End.Equal(p.ended.End) || !neds.Period.Start.Equal(p.ended.Start) || neds.Scope != projectOne || neds.Member.Kind != policy.SubjectUser {
		t.Errorf("ReadPolicy() ned's ended role = (%v, %v), want it on the first project with its end", neds, ok)
	}
	if crews, ok := byMember[string(projectCrew)+"/"+string(projectEditor)]; !ok || crews.Member.Kind != policy.SubjectGroup || crews.Scope != projectOne {
		t.Errorf("ReadPolicy() the crew's role = (%v, %v), want a group's role on the first project", crews, ok)
	}

	if err := store.InsertRecordUserRole(ctx, clientOne, ned, projectViewer, accesstypes.Period{}); err != nil {
		t.Fatalf("InsertRecordUserRole() error = %v", err)
	}
	_, added := recordRows(t, store)
	if added == before {
		t.Error("the records' hash did not change when a role was given on a record")
	}
	if err := store.InsertRecordUserRole(ctx, clientOne, ned, projectViewer, p.current); err != nil {
		t.Fatalf("InsertRecordUserRole() error = %v", err)
	}
	if _, moved := recordRows(t, store); moved == added {
		t.Error("the records' hash did not change when the period of a role held on a record changed")
	}
	if err := store.DeleteRecordUserRole(ctx, clientOne, ned, projectViewer); err != nil {
		t.Fatalf("DeleteRecordUserRole() error = %v", err)
	}
	if _, after := recordRows(t, store); after != before {
		t.Error("the records' hash is not what it was once the role was removed again")
	}
}

// recordRolesDeleted removes every role held on the first project and holds the second
// project's to stay. It leaves the crew holding ProjectEditor on the second project.
func recordRolesDeleted(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	if err := store.InsertRecordUserRole(ctx, projectTwo, lee, projectViewer, accesstypes.Period{}); err != nil {
		t.Fatalf("InsertRecordUserRole() error = %v", err)
	}
	if err := store.InsertRecordGroupRole(ctx, projectCrew, projectTwo, projectEditor, accesstypes.Period{}); err != nil {
		t.Fatalf("InsertRecordGroupRole() error = %v", err)
	}
	if err := store.DeleteRecordRoles(ctx, projectOne); err != nil {
		t.Fatalf("DeleteRecordRoles() error = %v", err)
	}
	held, _ := recordRows(t, store)
	for i := range held {
		if held[i].Scope == projectOne {
			t.Errorf("ReadPolicy() after DeleteRecordRoles(the first project) holds %v: every row of the record goes, the ended one among them", held[i])
		}
	}
	want := []policy.RecordAssignment{
		leeHolds(projectTwo, projectViewer, accesstypes.Period{}),
		heldByGroup(projectTwo, projectCrew, "Crew", projectEditor, accesstypes.Period{}),
	}
	if diff := cmp.Diff(want, listRecord(t, store, projectTwo), equateRecords); diff != "" {
		t.Errorf("ListRecordRoles(the second project) after the first's were removed (-want +got):\n%s", diff)
	}
	if err := store.DeleteRecordRoles(ctx, projectOne); err != nil {
		t.Errorf("DeleteRecordRoles() of a record that holds none: error = %v, want none", err)
	}
	if err := store.DeleteRecordUserRole(ctx, projectTwo, lee, projectViewer); err != nil {
		t.Fatalf("DeleteRecordUserRole() error = %v", err)
	}
}

// recordGroupDeleted deletes the phase's groups and holds their rows to go with them. It
// leaves nothing of the phase.
func recordGroupDeleted(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	for _, group := range []accesstypes.Group{projectCrew, projectGuests} {
		if deleted, err := store.DeleteGroup(ctx, group); err != nil || !deleted {
			t.Fatalf("DeleteGroup(%s) = (%v, %v), want it deleted", group, deleted, err)
		}
	}
	if held := listRecord(t, store, projectTwo); len(held) != 0 {
		t.Errorf("ListRecordRoles(the second project) after the crew was deleted = %v, want none", held)
	}
	if held, _ := recordRows(t, store); len(held) != 0 {
		t.Errorf("ReadPolicy() after the phase = %v, want no role held on its records", held)
	}
}
