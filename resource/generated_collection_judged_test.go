package resource

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// judgedMethodResources registers res with the owner, state and role attributes and its
// tenant column, RateThing, a method that names no row and whose request declares
// role, SendThingAgain, which names a row of res, is an action no role is given and is
// judged by the grant of RateThing, and GreetKeeper, which names a row of res and is
// judged by its own grant.
func judgedMethodResources(res accesstypes.Resource) []CollectionResource {
	execute := []accesstypes.Permission{accesstypes.Execute}
	row := heldRowResource(res)
	row.Attributes = append(row.Attributes, AttributeData{Name: "role", Column: "Role", Type: AttributeTypeString})

	return []CollectionResource{
		row,
		{Name: "GreetKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: execute, Target: res},
		{
			Name: "RateThing", Scope: accesstypes.DomainPermissionScope, Permissions: execute,
			Attributes: []AttributeData{{Name: "role", Column: "Role", Type: AttributeTypeString}},
		},
		{Name: "SendThingAgain", Scope: accesstypes.DomainPermissionScope, Permissions: execute, Target: res, NoRole: true, JudgedBy: "RateThing"},
	}
}

// judgedMethodCollection is the collection of judgedMethodResources.
func judgedMethodCollection(t *testing.T, res accesstypes.Resource) *GeneratedCollection {
	t.Helper()

	g, err := NewGeneratedCollection(CollectionData{Resources: judgedMethodResources(res)})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// TestGeneratedCollection_JudgedBy pins the mark of a method that another method's
// grant judges: the builder sets it, a second call replacing the first, the data
// carries it and the collection reads it back, and JudgedBy answers it for the marked
// method alone.
func TestGeneratedCollection_JudgedBy(t *testing.T) {
	t.Parallel()

	b := NewCollectionBuilder()
	for _, method := range []accesstypes.Resource{"GreetKeeper", "RateThing", "SendThingAgain", "ScoreThing"} {
		if err := b.AddMethodResource(accesstypes.DomainPermissionScope, accesstypes.Execute, method); err != nil {
			t.Fatalf("AddMethodResource(%s) error = %v", method, err)
		}
	}
	if err := b.AddResource(accesstypes.DomainPermissionScope, accesstypes.Read, heldResourceName); err != nil {
		t.Fatalf("AddResource(%s) error = %v", heldResourceName, err)
	}
	b.SetResourceBindings(accesstypes.DomainPermissionScope, heldResourceName, &Bindings{
		Attributes: []AttributeData{{Name: "role", Column: "Role", Type: AttributeTypeString}},
		Domain:     &DomainBindingData{Column: "Station"},
	})
	b.SetResourceBindings(accesstypes.DomainPermissionScope, "RateThing", &Bindings{Attributes: []AttributeData{{Name: "role", Column: "Role", Type: AttributeTypeString}}})
	b.SetMethodTarget(accesstypes.DomainPermissionScope, "GreetKeeper", heldResourceName)
	b.SetMethodTarget(accesstypes.DomainPermissionScope, "SendThingAgain", heldResourceName)
	b.SetActionNoRole(accesstypes.DomainPermissionScope, "SendThingAgain")
	b.SetMethodJudgedBy(accesstypes.DomainPermissionScope, "SendThingAgain", "ScoreThing")
	b.SetMethodJudgedBy(accesstypes.DomainPermissionScope, "SendThingAgain", "RateThing")
	data := b.Data()
	g, err := b.GeneratedCollection()
	if err != nil {
		t.Fatalf("CollectionBuilder.GeneratedCollection() error = %v", err)
	}

	t.Run("the data carries the mark, and it survives the round trip", func(t *testing.T) {
		t.Parallel()

		marked := map[accesstypes.Resource]accesstypes.Resource{}
		for _, res := range data.Resources {
			marked[res.Name] = res.JudgedBy
		}
		want := map[accesstypes.Resource]accesstypes.Resource{"GreetKeeper": "", "RateThing": "", "ScoreThing": "", "SendThingAgain": "RateThing", heldResourceName: ""}
		if diff := cmp.Diff(want, marked); diff != "" {
			t.Errorf("CollectionData JudgedBy mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(data, g.Data()); diff != "" {
			t.Errorf("the data does not survive the round trip (-want +got):\n%s", diff)
		}
	})

	t.Run("the collection says which method judges the marked one", func(t *testing.T) {
		t.Parallel()

		if by, judged := g.JudgedBy("SendThingAgain"); by != "RateThing" || !judged {
			t.Errorf("JudgedBy(SendThingAgain) = (%q, %v), want (RateThing, true)", by, judged)
		}
		for _, name := range []accesstypes.Resource{"GreetKeeper", "RateThing", heldResourceName, "NoSuchMethod"} {
			if by, judged := g.JudgedBy(name); by != "" || judged {
				t.Errorf("JudgedBy(%s) = (%q, %v), want (\"\", false)", name, by, judged)
			}
		}
		var none *GeneratedCollection
		if by, judged := none.JudgedBy("SendThingAgain"); by != "" || judged {
			t.Errorf("a nil collection's JudgedBy(SendThingAgain) = (%q, %v), want (\"\", false)", by, judged)
		}
	})
}

// TestNewGeneratedCollection_JudgedByValidation pins what the collection refuses of the
// mark at start-up, each refusal naming the judged method and the judging one: the
// judged method is an action no role is given that names a row, and the judging one
// registers Execute in the same scope, is granted by a role file, names no row, and
// declares no request field the row lacks.
func TestNewGeneratedCollection_JudgedByValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// change alters judgedMethodResources: the row first, then GreetKeeper, RateThing and
		// SendThingAgain, in that order.
		change  func(resources []CollectionResource) []CollectionResource
		wantErr string
	}{
		{
			name:   "a method no role is given, judged by a method that names no row",
			change: func(resources []CollectionResource) []CollectionResource { return resources },
		},
		{
			name: "the judged method is given to roles",
			change: func(resources []CollectionResource) []CollectionResource {
				resources[3].NoRole = false

				return resources
			},
			wantErr: `method resource "SendThingAgain" is judged by the grant of "RateThing" and is not marked an action no role is given: a grant of its own would be asked by nothing`,
		},
		{
			name: "the judged method names no row",
			change: func(resources []CollectionResource) []CollectionResource {
				resources[3].Target = ""

				return resources
			},
			wantErr: `method resource "SendThingAgain" is judged by the grant of "RateThing" and names no target row: the grant's request fields are read from the row the method names`,
		},
		{
			name: "the judging method registers its Execute in the other scope",
			change: func(resources []CollectionResource) []CollectionResource {
				resources[2].Scope = accesstypes.GlobalPermissionScope

				return resources
			},
			wantErr: `method resource "SendThingAgain" is judged by the grant of "RateThing", which registers no Execute in the same scope`,
		},
		{
			name: "the judging method is not registered",
			change: func(resources []CollectionResource) []CollectionResource {
				return append(resources[:2], resources[3])
			},
			wantErr: `method resource "SendThingAgain" is judged by the grant of "RateThing", which registers no Execute in the same scope`,
		},
		{
			name: "the judging method is given to no role",
			change: func(resources []CollectionResource) []CollectionResource {
				resources[2].NoRole = true

				return resources
			},
			wantErr: `method resource "SendThingAgain" is judged by the grant of "RateThing", which no role is given: a method is judged by one a role file grants`,
		},
		{
			name: "the judging method is itself judged by another",
			change: func(resources []CollectionResource) []CollectionResource {
				resources[3].JudgedBy = "ZoneThing"

				return append(resources, CollectionResource{
					Name: "ZoneThing", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, JudgedBy: "RateThing",
				})
			},
			wantErr: `method resource "SendThingAgain" is judged by the grant of "ZoneThing", which no role is given: a method is judged by one a role file grants`,
		},
		{
			name: "the judging method names a target row of its own",
			change: func(resources []CollectionResource) []CollectionResource {
				resources[3].JudgedBy = "GreetKeeper"

				return resources
			},
			wantErr: `method resource "SendThingAgain" is judged by the grant of "GreetKeeper", which names a target row of its own: a method that names no row judges another`,
		},
		{
			name: "the judging method's request field is not on the row",
			change: func(resources []CollectionResource) []CollectionResource {
				resources[0].Attributes = resources[0].Attributes[:2]

				return resources
			},
			wantErr: `method resource "SendThingAgain" is judged by the grant of "RateThing", whose request field role is no attribute of the same type on heldResources: a condition that reads new.role would have nothing to read on the row`,
		},
		{
			name: "the row's attribute of the name has another type",
			change: func(resources []CollectionResource) []CollectionResource {
				resources[0].Attributes[2].Type = AttributeTypeNumber

				return resources
			},
			wantErr: `method resource "SendThingAgain" is judged by the grant of "RateThing", whose request field role is no attribute of the same type on heldResources`,
		},
		{
			name: "the row's attribute of the name is a list where the request field is one value",
			change: func(resources []CollectionResource) []CollectionResource {
				resources[0].Attributes[2].List = true

				return resources
			},
			wantErr: `method resource "SendThingAgain" is judged by the grant of "RateThing", whose request field role is no attribute of the same type on heldResources`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewGeneratedCollection(CollectionData{Resources: tt.change(judgedMethodResources(heldResourceName))})
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
