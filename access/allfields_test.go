package access

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// stampedWidgets is the permission collection of a release, as the generator writes
// one, whose Widgets carry a field that never changes once the row is made: the
// collection marks createdBy immutable and lists no Update on it, since the generator
// takes Update off an immutable field (resource.NewSetData).
func stampedWidgets(t *testing.T) PermissionCollection {
	t.Helper()

	every := []accesstypes.Permission{accesstypes.Create, accesstypes.Read, accesstypes.Update}

	return generated(t, resource.CollectionData{
		Resources: []resource.CollectionResource{{
			Name:        "Widgets",
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: every,
			Tags: []resource.TagData{
				{Name: "createdBy", Permissions: []accesstypes.Permission{accesstypes.Create, accesstypes.Read}},
				{Name: "name", Permissions: every},
			},
			ImmutableTags: []accesstypes.Tag{"createdBy"},
		}},
	})
}

// validated is the role file's check of the roles against what the release declares.
func validated(store PermissionCollection, roles ScopedRoles) ([]Warning, error) {
	return ValidateRoles(store, &RoleConfig{Roles: roles}, "")
}

// TestAllFieldsUpdate_leavesOutAnImmutableField pins that Update on all of a resource's
// fields ("Widgets.*") is not Update on a field the release marks immutable, whether a
// role file gives the grant to a default role or a write gives it to a custom one: a
// check of the immutable field answers not granted and the digest shows no Update on
// it, while every other field of the resource, one no grant names among them, is
// covered. The collection is one in the generator's shape.
func TestAllFieldsUpdate_leavesOutAnImmutableField(t *testing.T) {
	t.Parallel()

	const editor accesstypes.Role = "Editor"
	tests := []struct {
		name string
		// roleFile declares the role as a default; nil where the role is a custom one.
		roleFile RoleFile
		// give makes the custom role and writes its grants; nil where the file declares it.
		give func(ctx context.Context, manager UserManager) error
	}{
		{
			name: "a default role whose file gives it Update on all the fields",
			roleFile: RoleFile(`{"roles": {"domain": [{"name": "Editor", "description": "Edits the widgets.", "permissions": {
				"Read": [{"resource": "Widgets", "fields": ["createdBy", "name"]}],
				"Update": [{"resource": "Widgets", "fields": ["*"]}]
			}}]}}`),
		},
		{
			name: "a custom role written Update on all the fields",
			give: func(ctx context.Context, manager UserManager) error {
				if err := manager.AddRole(ctx, tenant1Policy, editor, aDescription); err != nil {
					return fmt.Errorf("AddRole(%s): %w", editor, err)
				}
				err := manager.AddRoleGrants(ctx, tenant1Policy, editor,
					GrantRow{Permission: accesstypes.Read, Resource: "Widgets"},
					GrantRow{Permission: accesstypes.Read, Resource: "Widgets.createdBy"},
					GrantRow{Permission: accesstypes.Read, Resource: "Widgets.name"},
					GrantRow{Permission: accesstypes.Update, Resource: "Widgets"},
					GrantRow{Permission: accesstypes.Update, Resource: "Widgets.*"},
				)
				if err != nil {
					return fmt.Errorf("AddRoleGrants(%s): %w", editor, err)
				}

				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			var opts []Option
			if tt.roleFile != nil {
				opts = append(opts, WithDefaultRoles(tt.roleFile, ""))
			}
			client := newPolicyClient(t, newFakeStore(), stampedWidgets(t), opts...)
			manager := client.UserManager()
			if tt.give != nil {
				if err := tt.give(ctx, manager); err != nil {
					t.Fatalf("giving the role its grants error = %v", err)
				}
			}
			if err := manager.AddUserRoles(ctx, tenant1Policy, "dana", editor); err != nil {
				t.Fatalf("AddUserRoles() error = %v", err)
			}

			asked := []accesstypes.Resource{"Widgets", "Widgets.*", "Widgets.createdBy", "Widgets.later", "Widgets.name"}
			decisions, err := client.CheckUserResources(ctx, accesstypes.NewEnvironment(), "dana", tenant1Scope, accesstypes.Update, asked...)
			if err != nil {
				t.Fatalf("CheckUserResources() error = %v", err)
			}
			var granted []accesstypes.Resource
			for _, res := range asked {
				if decisions[res].IsGranted() {
					granted = append(granted, res)
				}
			}
			wantGranted := []accesstypes.Resource{"Widgets", "Widgets.*", "Widgets.later", "Widgets.name"}
			if diff := cmp.Diff(wantGranted, granted); diff != "" {
				t.Errorf("CheckUserResources() Update granted (-want +got):\n%s", diff)
			}

			digest, err := client.UserPermissionDigest(ctx, "dana", tenant1Scope)
			if err != nil {
				t.Fatalf("UserPermissionDigest() error = %v", err)
			}
			wantFields := accesstypes.PermissionDigest{
				"Widgets.createdBy": {accesstypes.Read: accesstypes.DigestGranted},
				"Widgets.name":      {accesstypes.Read: accesstypes.DigestGranted, accesstypes.Update: accesstypes.DigestGranted},
			}
			gotFields := accesstypes.PermissionDigest{
				"Widgets.createdBy": digest["Widgets.createdBy"],
				"Widgets.name":      digest["Widgets.name"],
			}
			if diff := cmp.Diff(wantFields, gotFields); diff != "" {
				t.Errorf("UserPermissionDigest() of the two fields (-want +got):\n%s", diff)
			}
		})
	}
}

// TestSnapshot_conditionalAllFieldsUpdateLeavesOutAnImmutableField pins the same for a
// grant under a condition: the immutable field is covered under no condition of an
// all-fields Update, and every other field under the grant's own. The grant is a stored
// row, compiled against a collection written by hand that marks the field immutable.
func TestSnapshot_conditionalAllFieldsUpdateLeavesOutAnImmutableField(t *testing.T) {
	t.Parallel()

	records := &policy.Records{Grants: []policy.Grant{
		{Scope: tenant1Policy, Subject: userSubject("dana"), Perm: accesstypes.Update, Resource: "Widgets", Field: "*", Condition: "owner = subject"},
	}}
	snap, findings, err := newSnapshot(records, nil, immutablePriceCollection{}, time.Now())
	if err != nil {
		t.Fatalf("newSnapshot() error = %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("newSnapshot() findings = %v, want none: the grant is one the release declares", findings)
	}

	asked := []accesstypes.Resource{"Widgets.*", "Widgets.name", "Widgets.price"}
	got := make(map[accesstypes.Resource][]string, len(asked))
	for i, decision := range snap.decideUserResources("dana", tenant1Scope, accesstypes.Update, asked...) {
		if decision.granted {
			t.Errorf("Update on %s is granted outright, want it under the grant's condition or not at all", asked[i])
		}
		got[asked[i]] = decision.conditions
	}
	want := map[accesstypes.Resource][]string{
		"Widgets.*":     {"owner = subject"},
		"Widgets.name":  {"owner = subject"},
		"Widgets.price": nil,
	}
	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("the conditions Update is held under (-want +got):\n%s", diff)
	}
}

// TestSnapshot_allFieldsUpdateDigestLeavesOutAnImmutableField pins that the digest
// tells Update on all of a resource's fields over the fields the release declares,
// though no grant names them, and leaves out the one the release marks immutable, as a
// check of it does.
func TestSnapshot_allFieldsUpdateDigestLeavesOutAnImmutableField(t *testing.T) {
	t.Parallel()

	records := &policy.Records{Grants: []policy.Grant{
		{Scope: tenant1Policy, Subject: userSubject("dana"), Perm: accesstypes.Update, Resource: "Widgets", Field: "*"},
	}}
	snap, findings, err := newSnapshot(records, nil, immutablePriceCollection{}, time.Now())
	if err != nil {
		t.Fatalf("newSnapshot() error = %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("newSnapshot() findings = %v, want none: the grant is one the release declares", findings)
	}

	want := accesstypes.PermissionDigest{
		"Widgets.name": {accesstypes.Update: accesstypes.DigestGranted},
	}
	if diff := cmp.Diff(want, snap.userDigest(tenant1Scope, "dana")); diff != "" {
		t.Errorf("userDigest() mismatch (-want +got):\n%s", diff)
	}
}

// TestAllFieldsGrant_roleFileAndWriteGiveTheSameSet pins that a role file and a write
// to a custom role accept the same grants of all of a resource's fields and refuse the
// same ones, so a role made at run time holds nothing a default role could not: the
// grant is held to the resource's own name, by both.
func TestAllFieldsGrant_roleFileAndWriteGiveTheSameSet(t *testing.T) {
	t.Parallel()

	const tester accesstypes.Role = "Tester"
	const ghost = `role Tester: Read grant on Widgets: condition references "ghost", which is not an attribute of Widgets`
	tests := []struct {
		name       string
		collection PermissionCollection
		permission accesstypes.Permission
		// file is the grant as a role file writes it; rows are the same grant as a write
		// spells it.
		file Grant
		rows []GrantRow
		// wantFile and wantWrite are the refusals; both empty where the grant is given.
		wantFile, wantWrite string
	}{
		{
			name:       "all the fields of a resource the release declares, as a field set",
			collection: markedActionCollection{},
			permission: accesstypes.Read,
			file:       Grant{Resource: "Widgets", Fields: []accesstypes.Tag{"*"}},
			rows:       []GrantRow{{Permission: accesstypes.Read, Resource: "Widgets"}, {Permission: accesstypes.Read, Resource: "Widgets.*"}},
		},
		{
			name:       "all the fields of a resource the release declares, as a name",
			collection: markedActionCollection{},
			permission: accesstypes.Read,
			file:       Grant{Resource: "Widgets.*"},
			rows:       []GrantRow{{Permission: accesstypes.Read, Resource: "Widgets.*"}},
		},
		{
			name:       "Update on all the fields of a resource with an immutable field",
			collection: immutablePriceCollection{},
			permission: accesstypes.Update,
			file:       Grant{Resource: "Widgets.*"},
			rows:       []GrantRow{{Permission: accesstypes.Update, Resource: "Widgets.*"}},
		},
		{
			name:       "all the fields under a condition the resource's attributes answer",
			collection: markedActionCollection{},
			permission: accesstypes.Read,
			file:       Grant{Resource: "Widgets", Fields: []accesstypes.Tag{"*"}, Condition: "owner = subject"},
			rows: []GrantRow{
				{Permission: accesstypes.Read, Resource: "Widgets", Condition: "owner = subject"},
				{Permission: accesstypes.Read, Resource: "Widgets.*", Condition: "owner = subject"},
			},
		},
		{
			name:       "all the fields under a condition that reads what the resource does not declare",
			collection: markedActionCollection{},
			permission: accesstypes.Read,
			file:       Grant{Resource: "Widgets", Fields: []accesstypes.Tag{"*"}, Condition: "ghost = subject"},
			rows: []GrantRow{
				{Permission: accesstypes.Read, Resource: "Widgets", Condition: "ghost = subject"},
				{Permission: accesstypes.Read, Resource: "Widgets.*", Condition: "ghost = subject"},
			},
			wantFile:  ghost,
			wantWrite: ghost,
		},
		{
			name:       "all the fields of a resource the release does not declare",
			collection: markedActionCollection{},
			permission: accesstypes.Read,
			file:       Grant{Resource: "Gadgets.*"},
			rows:       []GrantRow{{Permission: accesstypes.Read, Resource: "Gadgets.*"}},
			wantFile:   "resource Gadgets does not require a permission or does not exist",
			wantWrite:  declaredWhy("Read on Gadgets.*", "this release declares no Read on Gadgets"),
		},
		{
			name:       "a permission the resource does not take",
			collection: markedActionCollection{},
			permission: accesstypes.Execute,
			file:       Grant{Resource: "Widgets.*"},
			rows:       []GrantRow{{Permission: accesstypes.Execute, Resource: "Widgets.*"}},
			wantFile:   "resource Widgets does not require permission Execute",
			wantWrite:  declaredWhy("Execute on Widgets.*", "this release declares no Execute on Widgets"),
		},
		{
			name:       "all the fields of a resource of another scope",
			collection: markedActionCollection{},
			permission: accesstypes.Execute,
			file:       Grant{Resource: "DoThing.*"},
			rows:       []GrantRow{{Permission: accesstypes.Execute, Resource: "DoThing.*"}},
			wantFile:   "role Tester is a domain role but grants Execute on DoThing, a global-scoped resource — a role's grants live at its declared scope; move the grant to a global role",
			wantWrite:  "role Tester is a domain role but grants Execute on DoThing, a global-scoped resource — a role's grants live at its declared scope; move the grant to a global role",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, fileErr := validated(tt.collection, ScopedRoles{Domain: []*Role{{
				Name: tester, Description: aDescription, Permissions: map[accesstypes.Permission][]Grant{tt.permission: {tt.file}},
			}}})
			var gotFile string
			if fileErr != nil {
				gotFile = plainText(fileErr)
			}
			if diff := cmp.Diff(tt.wantFile, gotFile); diff != "" {
				t.Errorf("the role file's refusal mismatch (-want +got):\n%s", diff)
			}

			ctx := context.Background()
			manager := newPolicyClient(t, newFakeStore(), tt.collection).UserManager()
			if err := manager.AddRole(ctx, tenant1Policy, tester, aDescription); err != nil {
				t.Fatalf("AddRole() error = %v", err)
			}
			writeErr := manager.AddRoleGrants(ctx, tenant1Policy, tester, tt.rows...)
			var gotWrite string
			switch {
			case writeErr != nil && !httpio.HasBadRequest(writeErr):
				t.Fatalf("AddRoleGrants() error = %v, want the grant written or a bad request", writeErr)
			case writeErr != nil:
				gotWrite = httpio.Message(writeErr)
			}
			if diff := cmp.Diff(tt.wantWrite, gotWrite); diff != "" {
				t.Errorf("the write's refusal mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestValidateRoles_allFieldsGrantWarnings pins what the role file's warnings read of a
// grant of all of a resource's fields: it covers every field, so a Create that carries
// it leaves no required field out, and a List that carries it covers each sort or
// filter key under its conditions, or outright where it has none.
func TestValidateRoles_allFieldsGrantWarnings(t *testing.T) {
	t.Parallel()

	domain := accesstypes.DomainPermissionScope
	clerk := func(permissions map[accesstypes.Permission][]Grant) ScopedRoles {
		return ScopedRoles{Domain: []*Role{{Name: "Clerk", Description: "Keeps the records.", Permissions: permissions}}}
	}
	tests := []struct {
		name  string
		store PermissionCollection
		roles ScopedRoles
		want  []Warning
	}{
		{
			name:  "a Create of all the fields leaves no required field out",
			store: requiredCollection{},
			roles: clerk(map[accesstypes.Permission][]Grant{
				"Create": {{Resource: "Missions", Fields: []accesstypes.Tag{"*"}}},
			}),
		},
		{
			name:  "a List of all the fields under one condition masks nothing the row filter does not prove",
			store: concealingCollection{},
			roles: clerk(map[accesstypes.Permission][]Grant{
				"List": {{Resource: "Missions", Fields: []accesstypes.Tag{"*"}, Condition: completed}},
			}),
		},
		{
			name:  "a List of all the fields under a condition masks each key where another field is listed outright",
			store: concealingCollection{},
			roles: clerk(map[accesstypes.Permission][]Grant{
				"List": {
					{Resource: "Missions", Fields: []accesstypes.Tag{"title"}},
					{Resource: "Missions", Fields: []accesstypes.Tag{"*"}, Condition: completed},
				},
			}),
			want: []Warning{
				ConcealingKeyWarning{Role: "Clerk", Scope: domain, Resource: "Missions", Field: "deadline", Conditions: []string{completed}, DefaultOrder: true, Unconditional: true},
				ConcealingKeyWarning{Role: "Clerk", Scope: domain, Resource: "Missions", Field: "fee", Conditions: []string{completed}, Unconditional: true},
			},
		},
		{
			name:  "a List of all the fields with no condition leaves a key's own condition nothing to mask",
			store: concealingCollection{},
			roles: clerk(map[accesstypes.Permission][]Grant{
				"List": {
					{Resource: "Missions", Fields: []accesstypes.Tag{"*"}},
					{Resource: "Missions", Fields: []accesstypes.Tag{"fee"}, Condition: completed},
				},
			}),
		},
		{
			name:  "a key listed under its own condition and under the all-fields one carries both",
			store: concealingCollection{},
			roles: clerk(map[accesstypes.Permission][]Grant{
				"List": {
					{Resource: "Missions", Fields: []accesstypes.Tag{"title"}},
					{Resource: "Missions", Fields: []accesstypes.Tag{"fee"}, Condition: closedStates},
					{Resource: "Missions", Fields: []accesstypes.Tag{"*"}, Condition: completed},
				},
			}),
			want: []Warning{
				ConcealingKeyWarning{Role: "Clerk", Scope: domain, Resource: "Missions", Field: "deadline", Conditions: []string{completed}, DefaultOrder: true, Unconditional: true, Uncovered: []string{closedStates}},
				ConcealingKeyWarning{Role: "Clerk", Scope: domain, Resource: "Missions", Field: "fee", Conditions: []string{completed, closedStates}, Unconditional: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := validated(tt.store, tt.roles)
			if err != nil {
				t.Fatalf("ValidateRoles() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ValidateRoles() warnings mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
