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
