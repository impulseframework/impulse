package access

import (
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
)

// createWidgetShop is the fixture's action no role is given: registered in the global
// scope, and left out of List, the way the generated collection carries the action of
// the create-your-tenant route.
const createWidgetShop accesstypes.Resource = "CreateWidgetShop"

// unlistedActionCollection is grammarCollection with createWidgetShop registered and
// not listed, by a collection that does not say why.
type unlistedActionCollection struct{ grammarCollection }

func (unlistedActionCollection) Scope(res accesstypes.Resource) accesstypes.PermissionScope {
	if res == createWidgetShop {
		return accesstypes.GlobalPermissionScope
	}

	return grammarCollection{}.Scope(res)
}

// markedActionCollection is unlistedActionCollection saying what the action is, as
// the generated collection does.
type markedActionCollection struct{ unlistedActionCollection }

func (markedActionCollection) IsNoRoleAction(action accesstypes.Resource) bool {
	return action == createWidgetShop
}

func (markedActionCollection) NoRoleActions() []accesstypes.Resource {
	return []accesstypes.Resource{createWidgetShop}
}

// TestExpandRoleGrants_noRoleAction pins what the role file's validation does with a
// grant of an action no role is given: it is refused, in words that say what the
// action is and what to do, and the same grant is refused by a collection that does
// not say what the action is, since List does not hold it.
func TestExpandRoleGrants_noRoleAction(t *testing.T) {
	t.Parallel()

	const (
		why      = "role Tester grants Execute on CreateWidgetShop, which is an action no role is given: nothing asks for a grant of it, so the grant would do nothing, and the action is registered only so that a permission mask can refuse it to a session that acts as someone; remove the grant from the role"
		unlisted = "resource CreateWidgetShop does not require permission Execute"
	)
	tests := []struct {
		name    string
		store   PermissionCollection
		grants  map[accesstypes.Permission][]Grant
		wantErr string
	}{
		{
			name:    "a grant of the action is refused, saying why",
			store:   markedActionCollection{},
			grants:  map[accesstypes.Permission][]Grant{"Execute": {{Resource: createWidgetShop}}},
			wantErr: why,
		},
		{
			name:    "under any permission",
			store:   markedActionCollection{},
			grants:  map[accesstypes.Permission][]Grant{"Read": {{Resource: createWidgetShop}}},
			wantErr: "role Tester grants Read on CreateWidgetShop, which is an action no role is given",
		},
		{
			name:    "a collection that does not say what the action is refuses the grant all the same",
			store:   unlistedActionCollection{},
			grants:  map[accesstypes.Permission][]Grant{"Execute": {{Resource: createWidgetShop}}},
			wantErr: unlisted,
		},
		{
			name:   "an action a role is given expands as before",
			store:  markedActionCollection{},
			grants: map[accesstypes.Permission][]Grant{"Execute": {{Resource: "DoThing"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			role := &Role{Name: "Tester", Description: "Exercises the grant of an action no role is given.", Permissions: tt.grants}
			_, err := expandRoleGrants(tt.store, role, accesstypes.GlobalPermissionScope)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expandRoleGrants() error = %v, want none", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expandRoleGrants() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestUnionCollection_noRoleAction pins the mark across sites: an action one site's
// collection marks as no role's is no role's in the union, which answers what the
// action is, and a site that registers the same name for roles is refused.
func TestUnionCollection_noRoleAction(t *testing.T) {
	t.Parallel()

	portal := &stubCollection{list: registry("Read", "Gadgets")}

	t.Run("the mark of one site holds in the union", func(t *testing.T) {
		t.Parallel()

		union, err := UnionCollection(portal, markedActionCollection{})
		if err != nil {
			t.Fatalf("UnionCollection() error = %v", err)
		}
		if slices.Contains(union.List()[accesstypes.Execute], createWidgetShop) {
			t.Errorf("the union lists %s under Execute: a role file could grant it", createWidgetShop)
		}
		if !isNoRoleAction(union, createWidgetShop) {
			t.Errorf("isNoRoleAction(union, %s) = false, want true: the refusal of a grant would not say why", createWidgetShop)
		}
		if isNoRoleAction(union, "DoThing") {
			t.Error("isNoRoleAction(union, DoThing) = true, want false")
		}
		role := &Role{Name: "Tester", Description: "Exercises the union.", Permissions: map[accesstypes.Permission][]Grant{"Execute": {{Resource: createWidgetShop}}}}
		_, err = expandRoleGrants(union, role, accesstypes.GlobalPermissionScope)
		if want := "which is an action no role is given"; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("expandRoleGrants() over the union: error = %v, want containing %q", err, want)
		}
	})

	t.Run("a site that registers the name for roles is refused", func(t *testing.T) {
		t.Parallel()

		registers := &stubCollection{
			list:   registry("Execute", string(createWidgetShop)),
			scopes: map[accesstypes.Resource]accesstypes.PermissionScope{createWidgetShop: accesstypes.GlobalPermissionScope},
		}
		_, err := UnionCollection(registers, markedActionCollection{})
		want := `UnionCollection: collection 2 (in argument order) marks "CreateWidgetShop" as an action no role is given and collection 1 registers it for roles: a name is one or the other in every site, so rename one of the two`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("UnionCollection() error = %v, want containing %q", err, want)
		}
	})
}
