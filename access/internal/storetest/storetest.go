// Package storetest is the shared conformance suite for access.Store
// implementations. Each store package owns its containers and schema setup
// and hands a ready store to Run; the contract assertions live here once, so
// every store is held to exactly the same behavior.
package storetest

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// Fixture names shared by the suite's ordered phases. Scopes are variables
// because accesstypes.PolicyScope is a struct.
var (
	tenant1     = accesstypes.DomainPolicyScope("tenant1")
	tenant2     = accesstypes.DomainPolicyScope("tenant2")
	globalScope = accesstypes.GlobalPolicyScope()
	everyDomain = accesstypes.EveryDomainPolicyScope()
)

const (
	editor = accesstypes.Role("Editor")
	admin  = accesstypes.Role("Admin")
	viewer = accesstypes.Role("Viewer")
	// chief is a release default role: it has no row anywhere, and
	// memberships name it by name alone.
	chief = accesstypes.Role("Chief")

	alice = accesstypes.User("alice")
	bob   = accesstypes.User("bob")
	carol = accesstypes.User("carol")

	readPerm   = accesstypes.Permission("Read")
	updatePerm = accesstypes.Permission("Update")

	employees = "employees"
	widgets   = "widgets"

	nameField      = "name"
	priceField     = "price"
	salaryField    = "salary"
	ownerCondition = "owner = @subject"
	priceCondition = "price < 100"

	// The descriptions the custom roles are created with: opaque text to the
	// store, written with the row and read back with it.
	editorDescription = "Edits the records of the tenant."
	adminDescription  = "Administers the tenant."
	viewerDescription = "Reads the records of the tenant."
)

// Run exercises the full access.Store contract against an empty, ready store.
// The suite is one ordered scenario: later phases build on earlier writes.
func Run(t *testing.T, store access.Store) {
	t.Helper()

	t.Run("roles", func(t *testing.T) { runRoles(t, store) })
	t.Run("memberships", func(t *testing.T) { runMemberships(t, store) })
	t.Run("user memberships", func(t *testing.T) { runUserMemberships(t, store) })
	t.Run("grants", func(t *testing.T) { runGrants(t, store) })
	t.Run("change grants", func(t *testing.T) { runChangeGrants(t, store) })
	t.Run("global and every domain", func(t *testing.T) { runPartitions(t, store) })
	t.Run("delete role", func(t *testing.T) { runDeleteRole(t, store) })
	t.Run("read policy", func(t *testing.T) { runReadPolicy(t, store) })
}

func runRoles(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	if exists, err := store.RoleExists(ctx, tenant1, editor); err != nil || exists {
		t.Fatalf("RoleExists() on empty store = (%v, %v), want (false, nil)", exists, err)
	}

	for _, r := range []policy.RoleRow{
		{Name: editor, Description: editorDescription},
		{Name: admin, Description: adminDescription},
		{Name: viewer, Description: viewerDescription},
	} {
		if err := store.InsertRole(ctx, tenant1, r.Name, r.Description); err != nil {
			t.Fatalf("InsertRole(%q) error = %v", r.Name, err)
		}
	}
	// A re-insert is the no-op whatever description it carries: the stored
	// one stays.
	if err := store.InsertRole(ctx, tenant1, editor, "A second description, which must not land."); err != nil {
		t.Fatalf("InsertRole() re-insert must be a no-op, got error = %v", err)
	}
	if err := store.InsertRole(ctx, tenant2, editor, editorDescription); err != nil {
		t.Fatalf("InsertRole(tenant2) error = %v", err)
	}

	if exists, err := store.RoleExists(ctx, tenant1, editor); err != nil || !exists {
		t.Fatalf("RoleExists() = (%v, %v), want (true, nil)", exists, err)
	}
	if exists, err := store.RoleExists(ctx, tenant2, admin); err != nil || exists {
		t.Fatalf("RoleExists() must be domain-scoped: got (%v, %v), want (false, nil)", exists, err)
	}

	roles, err := store.ListRoles(ctx, tenant1)
	if err != nil {
		t.Fatalf("ListRoles() error = %v", err)
	}
	want := []policy.RoleRow{
		{Name: admin, Description: adminDescription},
		{Name: editor, Description: editorDescription},
		{Name: viewer, Description: viewerDescription},
	}
	if diff := cmp.Diff(want, roles); diff != "" {
		t.Errorf("ListRoles() must be sorted and domain-scoped, each role with the description it was created with (-want +got):\n%s", diff)
	}
}

func runMemberships(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	// A membership names its role by name alone: the release's default roles
	// have no row, so no row is required.
	if err := store.InsertUserRole(ctx, tenant1, alice, chief); err != nil {
		t.Fatalf("InsertUserRole() of a role with no row must succeed, got error = %v", err)
	}

	for _, m := range []struct {
		user accesstypes.User
		role accesstypes.Role
	}{
		{alice, editor},
		{alice, viewer},
		{bob, editor},
	} {
		if err := store.InsertUserRole(ctx, tenant1, m.user, m.role); err != nil {
			t.Fatalf("InsertUserRole(%q, %q) error = %v", m.user, m.role, err)
		}
	}
	if err := store.InsertUserRole(ctx, tenant1, alice, editor); err != nil {
		t.Fatalf("InsertUserRole() re-insert must be a no-op, got error = %v", err)
	}

	userRoles, err := store.ListUserRoles(ctx, tenant1, alice)
	if err != nil {
		t.Fatalf("ListUserRoles() error = %v", err)
	}
	if diff := cmp.Diff([]accesstypes.Role{chief, editor, viewer}, userRoles); diff != "" {
		t.Errorf("ListUserRoles() (-want +got):\n%s", diff)
	}
	if roles, err := store.ListUserRoles(ctx, tenant2, alice); err != nil || len(roles) != 0 {
		t.Errorf("ListUserRoles() must be domain-scoped: got (%v, %v)", roles, err)
	}

	roleUsers, err := store.ListRoleUsers(ctx, tenant1, editor)
	if err != nil {
		t.Fatalf("ListRoleUsers() error = %v", err)
	}
	if diff := cmp.Diff([]accesstypes.User{alice, bob}, roleUsers); diff != "" {
		t.Errorf("ListRoleUsers() (-want +got):\n%s", diff)
	}

	if err := store.DeleteUserRole(ctx, tenant1, bob, editor); err != nil {
		t.Fatalf("DeleteUserRole() error = %v", err)
	}
	if err := store.DeleteUserRole(ctx, tenant1, bob, editor); err != nil {
		t.Fatalf("DeleteUserRole() of absent row must be a no-op, got error = %v", err)
	}
	if users, err := store.ListRoleUsers(ctx, tenant1, editor); err != nil || len(users) != 1 || users[0] != alice {
		t.Errorf("ListRoleUsers() after delete = (%v, %v), want ([%s], nil)", users, err, alice)
	}
	if err := store.DeleteUserRole(ctx, tenant1, alice, chief); err != nil {
		t.Fatalf("DeleteUserRole() error = %v", err)
	}
}

// runUserMemberships holds ListUserMemberships to its contract: every
// membership the user holds, wherever it is held — one domain, the global
// partition, every domain — sorted by scope then role, and nothing of other
// users'. It leaves the memberships as runMemberships did.
func runUserMemberships(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	for _, m := range []struct {
		scope accesstypes.PolicyScope
		role  accesstypes.Role
	}{
		{tenant2, viewer},
		{globalScope, admin},
		{everyDomain, chief},
	} {
		if err := store.InsertUserRole(ctx, m.scope, alice, m.role); err != nil {
			t.Fatalf("InsertUserRole(%v, %q) error = %v", m.scope, m.role, err)
		}
	}
	if err := store.InsertUserRole(ctx, everyDomain, carol, chief); err != nil {
		t.Fatalf("InsertUserRole() error = %v", err)
	}

	got, err := store.ListUserMemberships(ctx, alice)
	if err != nil {
		t.Fatalf("ListUserMemberships() error = %v", err)
	}
	member := policy.Subject{Kind: policy.SubjectUser, Name: string(alice)}
	want := []policy.Membership{
		{Scope: tenant1, Member: member, Role: editor},
		{Scope: tenant1, Member: member, Role: viewer},
		{Scope: tenant2, Member: member, Role: viewer},
		{Scope: everyDomain, Member: member, Role: chief},
		{Scope: globalScope, Member: member, Role: admin},
	}
	if diff := cmp.Diff(want, got, cmpopts.EquateComparable(accesstypes.PolicyScope{})); diff != "" {
		t.Errorf("ListUserMemberships() must list every scope, sorted by scope then role (-want +got):\n%s", diff)
	}
	if got, err := store.ListUserMemberships(ctx, "nobody"); err != nil || len(got) != 0 {
		t.Errorf("ListUserMemberships() of an unknown user = (%v, %v), want none", got, err)
	}

	for _, m := range []struct {
		scope accesstypes.PolicyScope
		user  accesstypes.User
		role  accesstypes.Role
	}{
		{tenant2, alice, viewer},
		{globalScope, alice, admin},
		{everyDomain, alice, chief},
		{everyDomain, carol, chief},
	} {
		if err := store.DeleteUserRole(ctx, m.scope, m.user, m.role); err != nil {
			t.Fatalf("DeleteUserRole(%v, %q, %q) error = %v", m.scope, m.user, m.role, err)
		}
	}
}

func runGrants(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	if err := store.InsertGrant(ctx, tenant1, "Ghost", readPerm, employees, "", ""); err == nil {
		t.Fatal("InsertGrant() with absent role must fail (parent enforcement), got nil")
	}

	// Condition is opaque expression text and part of the row's identity: the
	// same (permission, resource, field) holds one row per condition, "" being
	// the unconditional row. The store never interprets the text.
	grants := []policy.RoleGrant{
		{Perm: readPerm, Resource: employees, Field: ""},
		{Perm: readPerm, Resource: employees, Field: "*"},
		{Perm: readPerm, Resource: employees, Field: nameField},
		{Perm: readPerm, Resource: employees, Field: salaryField, Condition: ownerCondition},
		{Perm: readPerm, Resource: employees, Field: salaryField, Condition: "region = 'west'"},
		{Perm: updatePerm, Resource: widgets, Field: ""},
	}
	for _, g := range grants {
		if err := store.InsertGrant(ctx, tenant1, editor, g.Perm, g.Resource, g.Field, g.Condition); err != nil {
			t.Fatalf("InsertGrant(%v) error = %v", g, err)
		}
	}
	if err := store.InsertGrant(ctx, tenant1, editor, readPerm, employees, "", ""); err != nil {
		t.Fatalf("InsertGrant() re-insert must be a no-op, got error = %v", err)
	}
	if err := store.InsertGrant(ctx, tenant1, editor, readPerm, employees, salaryField, ownerCondition); err != nil {
		t.Fatalf("InsertGrant() re-insert with the same condition must be a no-op, got error = %v", err)
	}

	got, err := store.ListRoleGrants(ctx, tenant1, editor)
	if err != nil {
		t.Fatalf("ListRoleGrants() error = %v", err)
	}
	if diff := cmp.Diff(grants, got); diff != "" {
		t.Errorf("ListRoleGrants() must be sorted with one row per condition (-want +got):\n%s", diff)
	}

	runInsertGrants(t, store, grants)

	// DeleteGrant addresses exactly one row: the other condition on the same
	// (permission, resource, field) survives.
	if err := store.DeleteGrant(ctx, tenant1, editor, readPerm, employees, salaryField, "region = 'west'"); err != nil {
		t.Fatalf("DeleteGrant() error = %v", err)
	}
	if err := store.DeleteGrant(ctx, tenant1, editor, readPerm, employees, salaryField, "region = 'west'"); err != nil {
		t.Fatalf("DeleteGrant() of absent row must be a no-op, got error = %v", err)
	}
	got, err = store.ListRoleGrants(ctx, tenant1, editor)
	if err != nil {
		t.Fatalf("ListRoleGrants() error = %v", err)
	}
	want := append(append([]policy.RoleGrant{}, grants[:4]...), grants[5])
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ListRoleGrants() after deleting one condition's row (-want +got):\n%s", diff)
	}

	// DeleteGrants removes every condition's row for the (permission,
	// resource, field), and only those.
	if err := store.InsertGrant(ctx, tenant1, editor, readPerm, employees, salaryField, "region = 'west'"); err != nil {
		t.Fatalf("InsertGrant() error = %v", err)
	}
	if err := store.DeleteGrants(ctx, tenant1, editor, readPerm, employees, salaryField); err != nil {
		t.Fatalf("DeleteGrants() error = %v", err)
	}
	if err := store.DeleteGrants(ctx, tenant1, editor, "Update", widgets, ""); err != nil {
		t.Fatalf("DeleteGrants() error = %v", err)
	}
	if err := store.DeleteGrants(ctx, tenant1, editor, "Update", widgets, ""); err != nil {
		t.Fatalf("DeleteGrants() of absent rows must be a no-op, got error = %v", err)
	}
	got, err = store.ListRoleGrants(ctx, tenant1, editor)
	if err != nil {
		t.Fatalf("ListRoleGrants() error = %v", err)
	}
	if diff := cmp.Diff(grants[:3], got); diff != "" {
		t.Errorf("ListRoleGrants() after DeleteGrants (-want +got):\n%s", diff)
	}
}

// runInsertGrants holds InsertGrants to InsertGrant's contract as one write: an
// absent role is refused, rows already present are left alone, a repeated row
// is written once, and an empty list is a no-op. The list afterwards is the
// union; the rows it added are removed again so the phases after it see the
// grants they expect.
func runInsertGrants(t *testing.T, store access.Store, grants []policy.RoleGrant) {
	t.Helper()
	ctx := t.Context()

	if err := store.InsertGrants(ctx, tenant1, "Ghost", []policy.RoleGrant{{Perm: readPerm, Resource: employees}}); err == nil {
		t.Fatal("InsertGrants() with absent role must fail (parent enforcement), got nil")
	}
	batch := []policy.RoleGrant{
		{Perm: readPerm, Resource: employees, Field: ""},
		{Perm: readPerm, Resource: employees, Field: salaryField, Condition: ownerCondition},
		{Perm: updatePerm, Resource: widgets, Field: nameField},
		{Perm: updatePerm, Resource: widgets, Field: nameField},
		{Perm: updatePerm, Resource: widgets, Field: priceField, Condition: priceCondition},
	}
	if err := store.InsertGrants(ctx, tenant1, editor, batch); err != nil {
		t.Fatalf("InsertGrants() over present and absent rows error = %v", err)
	}
	if err := store.InsertGrants(ctx, tenant1, editor, nil); err != nil {
		t.Fatalf("InsertGrants() with no rows must be a no-op, got error = %v", err)
	}
	wantAfterBatch := append(slices.Clone(grants),
		policy.RoleGrant{Perm: updatePerm, Resource: widgets, Field: nameField},
		policy.RoleGrant{Perm: updatePerm, Resource: widgets, Field: priceField, Condition: priceCondition},
	)
	got, err := store.ListRoleGrants(ctx, tenant1, editor)
	if err != nil {
		t.Fatalf("ListRoleGrants() error = %v", err)
	}
	if diff := cmp.Diff(wantAfterBatch, got); diff != "" {
		t.Errorf("ListRoleGrants() after InsertGrants (-want +got):\n%s", diff)
	}
	for _, g := range []policy.RoleGrant{{Perm: updatePerm, Resource: widgets, Field: nameField}, {Perm: updatePerm, Resource: widgets, Field: priceField, Condition: priceCondition}} {
		if err := store.DeleteGrant(ctx, tenant1, editor, g.Perm, g.Resource, g.Field, g.Condition); err != nil {
			t.Fatalf("DeleteGrant(%v) error = %v", g, err)
		}
	}
}

// refusedGrant is a row every store refuses: its permission is 65 characters,
// over the Spanner column's 64, and carries a NUL byte, which PostgreSQL
// rejects in text. A change that carries it fails whole, which is how the
// suite proves that a failed change leaves the role's grants as they were.
var refusedGrant = policy.RoleGrant{Perm: accesstypes.Permission(strings.Repeat("x", 64) + "\x00"), Resource: employees}

// runChangeGrants holds ChangeGrants to its contract: removals and additions
// land together, so a condition edit never shows the role with neither row; a
// change the store refuses leaves the grants as they were; additions already
// present and removals already absent are no-ops, as is nothing to change; and
// the role must exist for additions. It leaves the grants as runGrants did.
func runChangeGrants(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	base := []policy.RoleGrant{
		{Perm: readPerm, Resource: employees, Field: ""},
		{Perm: readPerm, Resource: employees, Field: "*"},
		{Perm: readPerm, Resource: employees, Field: nameField},
	}
	old := policy.RoleGrant{Perm: readPerm, Resource: employees, Field: salaryField, Condition: ownerCondition}
	edited := policy.RoleGrant{Perm: readPerm, Resource: employees, Field: salaryField, Condition: "region = 'west'"}
	list := func(step string) []policy.RoleGrant {
		got, err := store.ListRoleGrants(ctx, tenant1, editor)
		if err != nil {
			t.Fatalf("ListRoleGrants() after %s error = %v", step, err)
		}

		return got
	}

	// Additions only, one of them already present: the present row is left
	// alone and the other lands.
	if err := store.ChangeGrants(ctx, tenant1, editor, nil, []policy.RoleGrant{base[0], old}); err != nil {
		t.Fatalf("ChangeGrants() with additions only error = %v", err)
	}
	if diff := cmp.Diff(append(slices.Clone(base), old), list("the additions")); diff != "" {
		t.Errorf("ListRoleGrants() after additions (-want +got):\n%s", diff)
	}

	// A condition edit: the old row goes and the new one comes in one call.
	if err := store.ChangeGrants(ctx, tenant1, editor, []policy.RoleGrant{old}, []policy.RoleGrant{edited}); err != nil {
		t.Fatalf("ChangeGrants() condition edit error = %v", err)
	}
	if diff := cmp.Diff(append(slices.Clone(base), edited), list("the condition edit")); diff != "" {
		t.Errorf("ListRoleGrants() after the condition edit (-want +got):\n%s", diff)
	}

	// A change the store refuses leaves the role as it was: the removal did
	// not stick without its addition.
	if err := store.ChangeGrants(ctx, tenant1, editor, []policy.RoleGrant{edited}, []policy.RoleGrant{refusedGrant}); err == nil {
		t.Fatal("ChangeGrants() with a row the store refuses must fail, got nil")
	}
	if diff := cmp.Diff(append(slices.Clone(base), edited), list("the refused change")); diff != "" {
		t.Errorf("ListRoleGrants() after a refused change must be unchanged (-want +got):\n%s", diff)
	}

	// Removing an absent row and changing nothing are no-ops.
	if err := store.ChangeGrants(ctx, tenant1, editor, []policy.RoleGrant{old}, nil); err != nil {
		t.Fatalf("ChangeGrants() removing an absent row must be a no-op, got error = %v", err)
	}
	if err := store.ChangeGrants(ctx, tenant1, editor, nil, nil); err != nil {
		t.Fatalf("ChangeGrants() with nothing to change must be a no-op, got error = %v", err)
	}
	if diff := cmp.Diff(append(slices.Clone(base), edited), list("the no-ops")); diff != "" {
		t.Errorf("ListRoleGrants() after the no-ops (-want +got):\n%s", diff)
	}

	// Additions need the role.
	if err := store.ChangeGrants(ctx, tenant1, "Ghost", nil, []policy.RoleGrant{{Perm: readPerm, Resource: employees}}); err == nil {
		t.Fatal("ChangeGrants() with absent role must fail (parent enforcement), got nil")
	}

	// Back to what runGrants left.
	if err := store.ChangeGrants(ctx, tenant1, editor, []policy.RoleGrant{edited}, nil); err != nil {
		t.Fatalf("ChangeGrants() removal error = %v", err)
	}
	if diff := cmp.Diff(base, list("the removal")); diff != "" {
		t.Errorf("ListRoleGrants() after the removal (-want +got):\n%s", diff)
	}
}

// runPartitions pins that where a row is held is structural: the global
// partition, every domain, and tenants literally named "global" or "every"
// are four distinct places, and a delete in one touches nothing in the
// others.
func runPartitions(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	tenantNamedGlobal := accesstypes.DomainPolicyScope("global")
	tenantNamedEvery := accesstypes.DomainPolicyScope("every")

	for _, scope := range []accesstypes.PolicyScope{globalScope, everyDomain, tenantNamedGlobal, tenantNamedEvery} {
		if err := store.InsertRole(ctx, scope, admin, adminDescription); err != nil {
			t.Fatalf("InsertRole(%v) error = %v", scope, err)
		}
	}
	for _, scope := range []accesstypes.PolicyScope{globalScope, everyDomain, tenantNamedGlobal, tenantNamedEvery} {
		if exists, err := store.RoleExists(ctx, scope, admin); err != nil || !exists {
			t.Fatalf("RoleExists(%v) = (%v, %v), want (true, nil)", scope, exists, err)
		}
	}
	for _, scope := range []accesstypes.PolicyScope{tenantNamedGlobal, tenantNamedEvery} {
		if deleted, err := store.DeleteRole(ctx, scope, admin); err != nil || !deleted {
			t.Fatalf("DeleteRole(%v) = (%v, %v), want (true, nil)", scope, deleted, err)
		}
	}
	for _, scope := range []accesstypes.PolicyScope{globalScope, everyDomain} {
		if exists, err := store.RoleExists(ctx, scope, admin); err != nil || !exists {
			t.Fatalf("RoleExists(%v) after the tenant deletes = (%v, %v), want (true, nil): deleting a tenant's row must not touch the structural partitions", scope, exists, err)
		}
	}
	if roles, err := store.ListRoles(ctx, everyDomain); err != nil || !slices.Equal(roles, []policy.RoleRow{{Name: admin, Description: adminDescription}}) {
		t.Errorf("ListRoles(every domain) = (%v, %v), want [%s] with its description", roles, err, admin)
	}

	// A scope-wide grant is stored as an empty resource+field row — a spot no
	// real resource can occupy — and lists back exactly that way.
	if err := store.InsertGrant(ctx, globalScope, admin, "Export", "", "", ""); err != nil {
		t.Fatalf("InsertGrant(scope-wide) error = %v", err)
	}
	grants, err := store.ListRoleGrants(ctx, globalScope, admin)
	if err != nil {
		t.Fatalf("ListRoleGrants(global scope) error = %v", err)
	}
	want := []policy.RoleGrant{{Perm: "Export", Resource: "", Field: ""}}
	if diff := cmp.Diff(want, grants); diff != "" {
		t.Errorf("ListRoleGrants(global scope) (-want +got):\n%s", diff)
	}

	// A grant on a role held in every domain lists under every domain alone.
	if err := store.InsertGrant(ctx, everyDomain, admin, readPerm, widgets, "", ""); err != nil {
		t.Fatalf("InsertGrant(every domain) error = %v", err)
	}
	if grants, err := store.ListRoleGrants(ctx, tenant1, admin); err != nil || len(grants) != 0 {
		t.Errorf("ListRoleGrants(tenant1, Admin) = (%v, %v), want none: an every-domain row is not a tenant's row", grants, err)
	}
}

func runDeleteRole(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	// alice still holds Editor in tenant1: memberships held in the scope block
	// the delete, checked in the delete's own transaction.
	if _, err := store.DeleteRole(ctx, tenant1, editor); err == nil {
		t.Fatal("DeleteRole() with members must fail, got nil")
	}
	if exists, err := store.RoleExists(ctx, tenant1, editor); err != nil || !exists {
		t.Fatalf("RoleExists() after blocked delete = (%v, %v), want (true, nil)", exists, err)
	}

	// A membership in another scope does not block.
	if err := store.InsertUserRole(ctx, tenant2, bob, editor); err != nil {
		t.Fatalf("InsertUserRole() error = %v", err)
	}
	if err := store.DeleteUserRole(ctx, tenant1, alice, editor); err != nil {
		t.Fatalf("DeleteUserRole() error = %v", err)
	}
	deleted, err := store.DeleteRole(ctx, tenant1, editor)
	if err != nil || !deleted {
		t.Fatalf("DeleteRole() = (%v, %v), want (true, nil)", deleted, err)
	}

	// Grants cascaded with the role; the same role name in another domain is
	// untouched (the delete is domain-scoped), and so is the membership
	// there.
	if grants, err := store.ListRoleGrants(ctx, tenant1, editor); err != nil || len(grants) != 0 {
		t.Errorf("ListRoleGrants() after role delete = (%v, %v), want no grants", grants, err)
	}
	if exists, err := store.RoleExists(ctx, tenant2, editor); err != nil || !exists {
		t.Errorf("RoleExists(tenant2) after tenant1 delete = (%v, %v), want (true, nil)", exists, err)
	}
	if users, err := store.ListRoleUsers(ctx, tenant2, editor); err != nil || !slices.Equal(users, []accesstypes.User{bob}) {
		t.Errorf("ListRoleUsers(tenant2) after tenant1 delete = (%v, %v), want [%s]", users, err, bob)
	}

	deleted, err = store.DeleteRole(ctx, tenant1, editor)
	if err != nil || deleted {
		t.Fatalf("DeleteRole() of absent role = (%v, %v), want (false, nil)", deleted, err)
	}

	// Memberships block by name: a membership naming a role with no row still
	// refuses the delete, and once it is gone the delete of the absent role
	// is the no-op.
	if err := store.InsertUserRole(ctx, tenant1, alice, chief); err != nil {
		t.Fatalf("InsertUserRole() error = %v", err)
	}
	if _, err := store.DeleteRole(ctx, tenant1, chief); err == nil {
		t.Fatal("DeleteRole() of a role with no row but a member must fail, got nil")
	}
	if err := store.DeleteUserRole(ctx, tenant1, alice, chief); err != nil {
		t.Fatalf("DeleteUserRole() error = %v", err)
	}
	if deleted, err := store.DeleteRole(ctx, tenant1, chief); err != nil || deleted {
		t.Fatalf("DeleteRole() of a role with no row = (%v, %v), want (false, nil)", deleted, err)
	}
	if err := store.DeleteUserRole(ctx, tenant2, bob, editor); err != nil {
		t.Fatalf("DeleteUserRole() error = %v", err)
	}
}

func runReadPolicy(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	// State accumulated above: roles tenant1/{Admin,Viewer}, tenant2/Editor,
	// global/Admin with a scope-wide Export grant, every-domain/Admin with
	// Read on widgets; membership alice->Viewer in tenant1. Add grants to a
	// surviving role so the read covers grants too, one of them conditional,
	// and a membership of a role with no row.
	if err := store.InsertGrant(ctx, tenant1, viewer, "List", widgets, "*", ""); err != nil {
		t.Fatalf("InsertGrant() error = %v", err)
	}
	if err := store.InsertGrant(ctx, tenant1, viewer, readPerm, widgets, "name", "owner = @subject"); err != nil {
		t.Fatalf("InsertGrant() error = %v", err)
	}
	if err := store.InsertUserRole(ctx, everyDomain, carol, chief); err != nil {
		t.Fatalf("InsertUserRole() error = %v", err)
	}

	records, err := store.ReadPolicy(ctx)
	if err != nil {
		t.Fatalf("ReadPolicy() error = %v", err)
	}

	roleSubject := func(role accesstypes.Role) policy.Subject {
		return policy.Subject{Kind: policy.SubjectRole, Name: string(role)}
	}
	userSubject := func(user accesstypes.User) policy.Subject {
		return policy.Subject{Kind: policy.SubjectUser, Name: string(user)}
	}
	want := &policy.Records{
		Roles: []policy.Role{
			{Scope: tenant1, Name: admin},
			{Scope: tenant1, Name: viewer},
			{Scope: tenant2, Name: editor},
			{Scope: everyDomain, Name: admin},
			{Scope: globalScope, Name: admin},
		},
		Grants: []policy.Grant{
			{Scope: tenant1, Subject: roleSubject(viewer), Perm: "List", Resource: widgets, Field: "*"},
			{Scope: tenant1, Subject: roleSubject(viewer), Perm: readPerm, Resource: widgets, Field: nameField, Condition: ownerCondition},
			{Scope: everyDomain, Subject: roleSubject(admin), Perm: readPerm, Resource: widgets},
			{Scope: globalScope, Subject: roleSubject(admin), Perm: "Export", Resource: "", Field: ""},
		},
		Memberships: []policy.Membership{
			{Scope: tenant1, Member: userSubject(alice), Role: viewer},
			{Scope: everyDomain, Member: userSubject(carol), Role: chief},
		},
	}
	sortRecords(records)
	sortRecords(want)
	// PolicyScope is comparable with unexported fields; compare it by ==.
	if diff := cmp.Diff(want, records, cmpopts.EquateComparable(accesstypes.PolicyScope{})); diff != "" {
		t.Errorf("ReadPolicy() (-want +got):\n%s", diff)
	}
}

// sortRecords orders records canonically so set comparisons are stable
// regardless of row order.
func sortRecords(r *policy.Records) {
	compareSubjects := func(a, b policy.Subject) int {
		if c := int(a.Kind) - int(b.Kind); c != 0 {
			return c
		}

		return strings.Compare(a.Name, b.Name)
	}
	cmpChain := func(results ...int) int {
		for _, c := range results {
			if c != 0 {
				return c
			}
		}

		return 0
	}

	slices.SortFunc(r.Roles, func(a, b policy.Role) int {
		return cmpChain(policy.CompareScopes(a.Scope, b.Scope), strings.Compare(string(a.Name), string(b.Name)))
	})
	slices.SortFunc(r.Grants, func(a, b policy.Grant) int {
		return cmpChain(
			policy.CompareScopes(a.Scope, b.Scope),
			compareSubjects(a.Subject, b.Subject),
			strings.Compare(string(a.Perm), string(b.Perm)),
			strings.Compare(a.Resource, b.Resource),
			strings.Compare(a.Field, b.Field),
		)
	})
	slices.SortFunc(r.Memberships, func(a, b policy.Membership) int {
		return cmpChain(
			policy.CompareScopes(a.Scope, b.Scope),
			compareSubjects(a.Member, b.Member),
			strings.Compare(string(a.Role), string(b.Role)),
		)
	})
}
