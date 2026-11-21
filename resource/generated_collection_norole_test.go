package resource

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// noRoleCollection is a collection with one action no role is given (CreateShop,
// classed access, as the generated create-your-tenant route registers its own) beside
// an action a role is given and a resource.
func noRoleCollection(t *testing.T) (*GeneratedCollection, CollectionData) {
	t.Helper()

	b := NewCollectionBuilder()
	for _, action := range []accesstypes.Resource{"CreateShop", "Launch"} {
		if err := b.AddMethodResource(accesstypes.GlobalPermissionScope, accesstypes.Execute, action); err != nil {
			t.Fatalf("AddMethodResource(%s) error = %v", action, err)
		}
	}
	b.SetActionClasses(accesstypes.GlobalPermissionScope, "CreateShop", []accesstypes.ActionClass{accesstypes.ClassAccess})
	b.SetActionNoRole(accesstypes.GlobalPermissionScope, "CreateShop")
	if err := b.AddResource(accesstypes.DomainPermissionScope, accesstypes.Read, "Widgets"); err != nil {
		t.Fatalf("AddResource(Widgets) error = %v", err)
	}
	data := b.Data()
	g, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g, data
}

// TestGeneratedCollection_noRoleAction pins what the mark of an action no role is given
// does: the action is in nothing that says what a role may be given (List, which the
// role file's validation reads, and the browser constants), and it stays registered
// for the masked check, which reads its scope, its permission and its classes.
func TestGeneratedCollection_noRoleAction(t *testing.T) {
	t.Parallel()

	g, data := noRoleCollection(t)

	t.Run("the data carries the mark, and it survives the round trip", func(t *testing.T) {
		t.Parallel()

		marked := map[accesstypes.Resource]bool{}
		for _, res := range data.Resources {
			marked[res.Name] = res.NoRole
		}
		if diff := cmp.Diff(map[accesstypes.Resource]bool{"CreateShop": true, "Launch": false, "Widgets": false}, marked); diff != "" {
			t.Errorf("CollectionData NoRole mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(data, g.Data()); diff != "" {
			t.Errorf("the data does not survive the round trip (-want +got):\n%s", diff)
		}
	})

	t.Run("the collection says which actions are marked", func(t *testing.T) {
		t.Parallel()

		if !g.IsNoRoleAction("CreateShop") {
			t.Error("IsNoRoleAction(CreateShop) = false, want true")
		}
		for _, name := range []accesstypes.Resource{"Launch", "Widgets", "NoSuchAction"} {
			if g.IsNoRoleAction(name) {
				t.Errorf("IsNoRoleAction(%s) = true, want false", name)
			}
		}
		if diff := cmp.Diff([]accesstypes.Resource{"CreateShop"}, g.NoRoleActions()); diff != "" {
			t.Errorf("NoRoleActions() mismatch (-want +got):\n%s", diff)
		}
		var none *GeneratedCollection
		if none.IsNoRoleAction("CreateShop") || len(none.NoRoleActions()) != 0 {
			t.Error("a nil collection marks an action, want none")
		}
	})

	t.Run("List leaves the action out, so no role file can grant it", func(t *testing.T) {
		t.Parallel()

		executable := g.List()[accesstypes.Execute]
		slices.Sort(executable)
		if diff := cmp.Diff([]accesstypes.Resource{"Launch"}, executable); diff != "" {
			t.Errorf("List()[Execute] mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("the browser constants leave the action out", func(t *testing.T) {
		t.Parallel()

		if diff := cmp.Diff([]accesstypes.Resource{"Launch"}, g.TypescriptData().Methods); diff != "" {
			t.Errorf("TypescriptData().Methods mismatch (-want +got):\n%s", diff)
		}
		if slices.Contains(g.TypescriptData().Resources, "CreateShop") {
			t.Error("TypescriptData().Resources holds CreateShop, want it in neither list")
		}
	})

	t.Run("the action stays registered for the masked check", func(t *testing.T) {
		t.Parallel()

		if got := g.Scope("CreateShop"); got != accesstypes.GlobalPermissionScope {
			t.Errorf("Scope(CreateShop) = %q, want %q", got, accesstypes.GlobalPermissionScope)
		}
		if !g.HasPermission(accesstypes.GlobalPermissionScope, accesstypes.Execute, "CreateShop") {
			t.Error("HasPermission(global, Execute, CreateShop) = false, want true")
		}
		if diff := cmp.Diff([]accesstypes.ActionClass{accesstypes.ClassAccess}, g.ActionClasses(accesstypes.GlobalPermissionScope, "CreateShop")); diff != "" {
			t.Errorf("ActionClasses(CreateShop) mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]accesstypes.Resource{"CreateShop"}, g.ActionsOfClass(accesstypes.ClassAccess)); diff != "" {
			t.Errorf("ActionsOfClass(access) mismatch (-want +got):\n%s", diff)
		}
	})
}

// TestNewGeneratedCollection_noRoleValidation pins what the collection refuses of the
// mark at start-up: it is for an action, a name that registers Execute alone.
func TestNewGeneratedCollection_noRoleValidation(t *testing.T) {
	t.Parallel()

	const refusal = "is marked an action no role is given and is not an action"
	tests := []struct {
		name    string
		res     CollectionResource
		wantErr string
	}{
		{
			name: "the mark on an action",
			res:  CollectionResource{Name: "CreateShop", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, NoRole: true},
		},
		{
			name:    "the mark on a resource",
			res:     CollectionResource{Name: "Widgets", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Read}, NoRole: true},
			wantErr: refusal,
		},
		{
			name:    "the mark on a name registered under Execute and another permission",
			res:     CollectionResource{Name: "CreateShop", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute, accesstypes.List}, NoRole: true},
			wantErr: refusal,
		},
		{
			name:    "the mark on a name that registers nothing",
			res:     CollectionResource{Name: "CreateShop", Scope: accesstypes.GlobalPermissionScope, NoRole: true},
			wantErr: refusal,
		},
		{
			name: "the mark on a name with fields",
			res: CollectionResource{
				Name: "CreateShop", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, NoRole: true,
				Tags: []TagData{{Name: "name", Permissions: []accesstypes.Permission{accesstypes.Execute}}},
			},
			wantErr: refusal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{tt.res}})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("NewGeneratedCollection() error = %v, want none", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewGeneratedCollection() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
