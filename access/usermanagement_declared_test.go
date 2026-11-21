package access

import (
	"context"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// declaredWhy is the refusal of a grant the release does not declare, written to a
// role: what was asked, the reason in the words a load of the policy skips a stored
// grant with, and what a role is given.
func declaredWhy(asked, reason string) string {
	return asked + " cannot be given to a role: " + reason + ". A role is given a permission the application declares on a resource or a field; check the permission and the name"
}

// TestUserManager_grantsHeldToTheCollection pins that a grant written to a custom role
// is one the running release declares, on each of the five writes that add one. A
// permission, a resource or a field the collection does not list, and an action no role
// is given, are refused as a bad request that says which and why, and nothing of the
// call is written, the rows beside the refused one included. A name the collection
// lists is written, a grant of all of a resource's fields among them. A removal is held
// to nothing, so a grant the release no longer declares can be taken away.
func TestUserManager_grantsHeldToTheCollection(t *testing.T) {
	t.Parallel()

	const tester accesstypes.Role = "Tester"
	read := func(res accesstypes.Resource) GrantRow {
		return GrantRow{Permission: accesstypes.Read, Resource: res}
	}

	tests := []struct {
		name  string
		write func(ctx context.Context, manager UserManager) error
		// wantRefused is the bad request's message; empty when the write is made.
		wantRefused string
		// want are the grants the role holds afterwards, as "permission resource.field".
		want []string
	}{
		{
			name: "AddRoleGrant: a resource the release declares",
			write: func(ctx context.Context, manager UserManager) error {
				return manager.AddRoleGrant(ctx, tenant1Policy, tester, accesstypes.Read, "Widgets", "")
			},
			want: []string{"Read Widgets."},
		},
		{
			name: "AddRoleGrant: a field the release declares",
			write: func(ctx context.Context, manager UserManager) error {
				return manager.AddRoleGrant(ctx, tenant1Policy, tester, accesstypes.Update, "Widgets.price", "")
			},
			want: []string{"Update Widgets.price"},
		},
		{
			name: "AddRoleGrant: all the fields of a resource the release declares",
			write: func(ctx context.Context, manager UserManager) error {
				return manager.AddRoleGrant(ctx, tenant1Policy, tester, accesstypes.Read, "Widgets.*", "")
			},
			want: []string{"Read Widgets.*"},
		},
		{
			name: "AddRoleGrant: a resource the release does not declare",
			write: func(ctx context.Context, manager UserManager) error {
				return manager.AddRoleGrant(ctx, tenant1Policy, tester, accesstypes.Read, "Gadgets", "")
			},
			wantRefused: declaredWhy("Read on Gadgets", "this release declares no Read on Gadgets"),
		},
		{
			name: "AddRoleGrant: a permission the release does not have",
			write: func(ctx context.Context, manager UserManager) error {
				return manager.AddRoleGrant(ctx, tenant1Policy, tester, "Fly", "Widgets", "")
			},
			wantRefused: declaredWhy("Fly on Widgets", "this release has no permission Fly"),
		},
		{
			name: "AddRoleGrant: a permission the release does not declare on the field",
			write: func(ctx context.Context, manager UserManager) error {
				return manager.AddRoleGrant(ctx, tenant1Policy, tester, accesstypes.Update, "Widgets.name", "")
			},
			wantRefused: declaredWhy("Update on Widgets.name", "this release declares no Update on Widgets.name"),
		},
		{
			name: "AddRoleGrant: a permission the release declares on another resource",
			write: func(ctx context.Context, manager UserManager) error {
				return manager.AddRoleGrant(ctx, tenant1Policy, tester, accesstypes.Read, "DoThing", "")
			},
			wantRefused: declaredWhy("Read on DoThing", "this release declares no Read on DoThing"),
		},
		{
			name: "AddRoleGrant: an action no role is given",
			write: func(ctx context.Context, manager UserManager) error {
				return manager.AddRoleGrant(ctx, tenant1Policy, tester, accesstypes.Execute, createWidgetShop, "")
			},
			wantRefused: "Execute on CreateWidgetShop cannot be given to a role: CreateWidgetShop is an action no role is given. Nothing asks for a grant of it, so the grant would do nothing",
		},
		{
			name: "AddRolePermission: a permission the release does not have is refused for the whole of a scope too",
			write: func(ctx context.Context, manager UserManager) error {
				return manager.AddRolePermission(ctx, tenant1Policy, tester, "Frobnicate")
			},
			wantRefused: "Frobnicate cannot be given to a role: this release has no permission Frobnicate. A role is given a permission the application declares; check the permission's name",
		},
		{
			name: "AddRolePermission: a permission the release has",
			write: func(ctx context.Context, manager UserManager) error {
				return manager.AddRolePermission(ctx, tenant1Policy, tester, accesstypes.Read)
			},
			want: []string{"Read ."},
		},
		{
			name: "AddRoleGrants: one row the release does not declare refuses the call, and nothing is written",
			write: func(ctx context.Context, manager UserManager) error {
				return manager.AddRoleGrants(ctx, tenant1Policy, tester, read("Widgets"), read("Gadgets"), read("Widgets.name"))
			},
			wantRefused: declaredWhy("Read on Gadgets", "this release declares no Read on Gadgets"),
		},
		{
			name: "AddRoleGrants: rows the release declares",
			write: func(ctx context.Context, manager UserManager) error {
				return manager.AddRoleGrants(ctx, tenant1Policy, tester, read("Widgets"), read("Widgets.name"))
			},
			want: []string{"Read Widgets.", "Read Widgets.name"},
		},
		{
			name: "AddRolePermissionResources: one resource the release does not declare refuses the call, and nothing is written",
			write: func(ctx context.Context, manager UserManager) error {
				return manager.AddRolePermissionResources(ctx, tenant1Policy, tester, accesstypes.Read, "Widgets", "Gadgets.name")
			},
			wantRefused: declaredWhy("Read on Gadgets.name", "this release declares no Read on Gadgets.name"),
		},
		{
			name: "ChangeRoleGrants: an addition the release does not declare refuses the call",
			write: func(ctx context.Context, manager UserManager) error {
				return manager.ChangeRoleGrants(ctx, tenant1Policy, tester, nil, []GrantRow{read("Widgets"), read("Gadgets")})
			},
			wantRefused: declaredWhy("Read on Gadgets", "this release declares no Read on Gadgets"),
		},
		{
			name: "ChangeRoleGrants: a removal is held to nothing",
			write: func(ctx context.Context, manager UserManager) error {
				return manager.ChangeRoleGrants(ctx, tenant1Policy, tester, []GrantRow{read("Gadgets")}, []GrantRow{read("Widgets")})
			},
			want: []string{"Read Widgets."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			store := newFakeStore()
			manager := newPolicyClient(t, store, markedActionCollection{}).UserManager()
			if err := manager.AddRole(ctx, tenant1Policy, tester, aDescription); err != nil {
				t.Fatalf("AddRole() error = %v", err)
			}

			err := tt.write(ctx, manager)
			switch {
			case tt.wantRefused == "" && err != nil:
				t.Fatalf("the write error = %v, want it made", err)
			case tt.wantRefused != "" && !httpio.HasBadRequest(err):
				t.Fatalf("the write error = %v, want a bad request", err)
			case tt.wantRefused != "":
				if diff := cmp.Diff(tt.wantRefused, httpio.Message(err)); diff != "" {
					t.Errorf("the refusal's message mismatch (-want +got):\n%s", diff)
				}
			}

			records, err := store.ReadPolicy(ctx)
			if err != nil {
				t.Fatalf("ReadPolicy() error = %v", err)
			}
			var got []string
			for _, grant := range records.Grants {
				got = append(got, string(grant.Perm)+" "+grant.Resource+"."+grant.Field)
			}
			slices.Sort(got)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the role's grants mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// immutablePriceCollection is the fixture's collection listing Update on a field the
// release marks immutable, as a collection written by hand can: the generated one
// takes Update off an immutable field.
type immutablePriceCollection struct{ markedActionCollection }

func (immutablePriceCollection) IsResourceImmutable(_ accesstypes.PermissionScope, res accesstypes.Resource) bool {
	return res == "Widgets.price"
}

// scopelessGadgetsCollection is the fixture's collection listing a resource it holds
// in no scope, as a collection written by hand can.
type scopelessGadgetsCollection struct{ markedActionCollection }

func (c scopelessGadgetsCollection) List() map[accesstypes.Permission][]accesstypes.Resource {
	listed := c.markedActionCollection.List()
	listed[accesstypes.Read] = append(listed[accesstypes.Read], "Gadgets")

	return listed
}

// TestUserManager_grantsHeldToTheRoleFilesRule pins that a grant written to a custom
// role is held to the whole of the rule the role file's grants are held to, past the
// names: the grant is of the role's own scope, its condition parses and reads only
// what the release declares, and Update is not given on what the release marks
// immutable. Each is refused as a bad request in the role file's own sentence, on
// each of the four writes that add a grant on a resource, and nothing of the call is
// written. A removal is held to nothing.
func TestUserManager_grantsHeldToTheRoleFilesRule(t *testing.T) {
	t.Parallel()

	const tester accesstypes.Role = "Tester"
	const (
		globalResource = "role Tester is a domain role but grants Execute on DoThing, a global-scoped resource — a role's grants live at its declared scope; move the grant to a global role"
		domainResource = "role Tester is a global role but grants Read on Widgets, a domain-scoped resource — a role's grants live at its declared scope; move the grant to a domain role"
		ghost          = `role Tester: Read grant on Widgets: condition references "ghost", which is not an attribute of Widgets`
	)
	_, parseErr := accesstypes.NewCondition("owner = ")
	if parseErr == nil {
		t.Fatal(`accesstypes.NewCondition("owner = ") error = nil, want the condition refused`)
	}
	row := func(permission accesstypes.Permission, res accesstypes.Resource, condition string) GrantRow {
		return GrantRow{Permission: permission, Resource: res, Condition: condition}
	}

	tests := []struct {
		name string
		// collection is what the release declares; nil is the fixture's own.
		collection PermissionCollection
		// held is where the role is held.
		held  accesstypes.PolicyScope
		write func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error
		// wantRefused is the bad request's message; empty when the write is made.
		wantRefused string
		// want are the grants the role holds afterwards, as
		// "permission resource.field under condition".
		want []string
	}{
		{
			name: "AddRoleGrant: a condition that reads what the resource declares",
			held: tenant1Policy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.AddRoleGrant(ctx, held, tester, accesstypes.Read, "Widgets", "owner = subject")
			},
			want: []string{"Read Widgets. under owner = subject"},
		},
		{
			name: "AddRoleGrant: a condition naming an attribute the resource does not declare",
			held: tenant1Policy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.AddRoleGrant(ctx, held, tester, accesstypes.Read, "Widgets", "ghost = subject")
			},
			wantRefused: ghost,
		},
		{
			name: "AddRoleGrant: a condition that does not parse",
			held: tenant1Policy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.AddRoleGrant(ctx, held, tester, accesstypes.Read, "Widgets", "owner = ")
			},
			wantRefused: "role Tester: Read grant on Widgets: " + plainText(parseErr),
		},
		{
			name: "AddRoleGrant: a field's condition is held to the resource's attributes",
			held: tenant1Policy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.AddRoleGrant(ctx, held, tester, accesstypes.Read, "Widgets.name", "ghost = subject")
			},
			wantRefused: ghost,
		},
		{
			name: "AddRoleGrants: one row whose condition names an undeclared attribute refuses the call, and nothing is written",
			held: tenant1Policy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.AddRoleGrants(ctx, held, tester, row(accesstypes.Read, "Widgets", "owner = subject"), row(accesstypes.Read, "Widgets.name", "ghost = subject"))
			},
			wantRefused: ghost,
		},
		{
			name: "ChangeRoleGrants: an addition whose condition names an undeclared attribute refuses the call",
			held: tenant1Policy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.ChangeRoleGrants(ctx, held, tester, nil, []GrantRow{row(accesstypes.Read, "Widgets", "ghost = subject")})
			},
			wantRefused: ghost,
		},
		{
			name: "ChangeRoleGrants: a removal is held to nothing",
			held: tenant1Policy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				removals := []GrantRow{row(accesstypes.Read, "Widgets", "ghost = subject"), row(accesstypes.Execute, "DoThing", "")}

				return manager.ChangeRoleGrants(ctx, held, tester, removals, []GrantRow{row(accesstypes.Read, "Widgets", "")})
			},
			want: []string{"Read Widgets. under "},
		},
		{
			name: "AddRoleGrant: a domain role given a global resource",
			held: tenant1Policy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.AddRoleGrant(ctx, held, tester, accesstypes.Execute, "DoThing", "")
			},
			wantRefused: globalResource,
		},
		{
			name: "AddRoleGrants: a domain role given a global resource refuses the call, and nothing is written",
			held: tenant1Policy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.AddRoleGrants(ctx, held, tester, row(accesstypes.Read, "Widgets", ""), row(accesstypes.Execute, "DoThing", ""))
			},
			wantRefused: globalResource,
		},
		{
			name: "AddRolePermissionResources: a domain role given a global resource refuses the call, and nothing is written",
			held: tenant1Policy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.AddRolePermissionResources(ctx, held, tester, accesstypes.Execute, "DoThing")
			},
			wantRefused: globalResource,
		},
		{
			name: "ChangeRoleGrants: an addition that gives a domain role a global resource refuses the call",
			held: tenant1Policy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.ChangeRoleGrants(ctx, held, tester, nil, []GrantRow{row(accesstypes.Read, "Widgets", ""), row(accesstypes.Execute, "DoThing", "")})
			},
			wantRefused: globalResource,
		},
		{
			name: "AddRoleGrant: a role held in every domain given a global resource",
			held: everyDomainPolicy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.AddRoleGrant(ctx, held, tester, accesstypes.Execute, "DoThing", "")
			},
			wantRefused: globalResource,
		},
		{
			name: "AddRoleGrant: a role held in every domain given a domain's resource",
			held: everyDomainPolicy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.AddRoleGrant(ctx, held, tester, accesstypes.Read, "Widgets.*", "")
			},
			want: []string{"Read Widgets.* under "},
		},
		{
			name: "AddRoleGrant: a global role given a global resource",
			held: globalPolicy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.AddRoleGrant(ctx, held, tester, accesstypes.Execute, "DoThing", "")
			},
			want: []string{"Execute DoThing. under "},
		},
		{
			name: "AddRoleGrant: a global role given a domain's resource",
			held: globalPolicy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.AddRoleGrant(ctx, held, tester, accesstypes.Read, "Widgets", "")
			},
			wantRefused: domainResource,
		},
		{
			name:       "AddRoleGrant: Update on a field the release marks immutable",
			collection: immutablePriceCollection{},
			held:       tenant1Policy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.AddRoleGrant(ctx, held, tester, accesstypes.Update, "Widgets.price", "")
			},
			wantRefused: "role Tester cannot have update permission on immutable resource Widgets.price",
		},
		{
			name:       "AddRoleGrant: a resource the collection lists and holds in no scope",
			collection: scopelessGadgetsCollection{},
			held:       tenant1Policy,
			write: func(ctx context.Context, manager UserManager, held accesstypes.PolicyScope) error {
				return manager.AddRoleGrant(ctx, held, tester, accesstypes.Read, "Gadgets", "")
			},
			wantRefused: "resource Gadgets does not require a permission or does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			store := newFakeStore()
			collection := tt.collection
			if collection == nil {
				collection = markedActionCollection{}
			}
			manager := newPolicyClient(t, store, collection).UserManager()
			if err := manager.AddRole(ctx, tt.held, tester, aDescription); err != nil {
				t.Fatalf("AddRole() error = %v", err)
			}

			err := tt.write(ctx, manager, tt.held)
			switch {
			case tt.wantRefused == "" && err != nil:
				t.Fatalf("the write error = %v, want it made", err)
			case tt.wantRefused != "" && !httpio.HasBadRequest(err):
				t.Fatalf("the write error = %v, want a bad request", err)
			case tt.wantRefused != "":
				if diff := cmp.Diff(tt.wantRefused, httpio.Message(err)); diff != "" {
					t.Errorf("the refusal's message mismatch (-want +got):\n%s", diff)
				}
			}

			records, err := store.ReadPolicy(ctx)
			if err != nil {
				t.Fatalf("ReadPolicy() error = %v", err)
			}
			var got []string
			for _, grant := range records.Grants {
				got = append(got, string(grant.Perm)+" "+grant.Resource+"."+grant.Field+" under "+grant.Condition)
			}
			slices.Sort(got)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the role's grants mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
