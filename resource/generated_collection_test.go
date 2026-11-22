package resource

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
)

func TestNewSetData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fields      []FieldTags
		permissions []accesstypes.Permission
		want        SetData
		wantErr     bool
	}{
		{
			name: "list request struct registers every field with the endpoint permission",
			fields: []FieldTags{
				{Field: "ID", JSON: "id", Perm: "-"},
				{Field: "Name", JSON: "name"},
				{Field: "Secret", JSON: "-", Perm: ""},
			},
			permissions: []accesstypes.Permission{accesstypes.List},
			want: SetData{
				Permissions: []accesstypes.Permission{accesstypes.List},
				TagPermissions: accesstypes.TagPermissions{
					"id":   {accesstypes.NullPermission},
					"name": {accesstypes.List},
				},
				ImmutableFields:  map[accesstypes.Tag]struct{}{},
				PositionalFields: map[accesstypes.Tag]struct{}{},
			},
		},
		{
			name: "a positional field is recorded by its tag",
			fields: []FieldTags{
				{Field: "ID", JSON: "id", Perm: "-"},
				{Field: "Fee", JSON: "fee", Masking: "positional"},
			},
			permissions: []accesstypes.Permission{accesstypes.List},
			want: SetData{
				Permissions: []accesstypes.Permission{accesstypes.List},
				TagPermissions: accesstypes.TagPermissions{
					"id":  {accesstypes.NullPermission},
					"fee": {accesstypes.List},
				},
				ImmutableFields:  map[accesstypes.Tag]struct{}{},
				PositionalFields: map[accesstypes.Tag]struct{}{"fee": {}},
			},
		},
		{
			name: "a masking value the generator never writes is rejected",
			fields: []FieldTags{
				{Field: "Fee", JSON: "fee", Masking: "sideways"},
			},
			permissions: []accesstypes.Permission{accesstypes.List},
			wantErr:     true,
		},
		{
			name: "patch request struct strips Update from the immutable tag",
			fields: []FieldTags{
				{Field: "Name", JSON: "name"},
				{Field: "Code", JSON: "code", Immutable: true},
			},
			permissions: []accesstypes.Permission{accesstypes.Create, accesstypes.Update, accesstypes.Delete},
			want: SetData{
				Permissions: []accesstypes.Permission{accesstypes.Create, accesstypes.Delete, accesstypes.Update},
				TagPermissions: accesstypes.TagPermissions{
					"name": {accesstypes.Create, accesstypes.Update},
					"code": {accesstypes.Create},
				},
				ImmutableFields:  map[accesstypes.Tag]struct{}{"code": {}},
				PositionalFields: map[accesstypes.Tag]struct{}{},
			},
		},
		{
			name: "stale permission tag is rejected",
			fields: []FieldTags{
				{Field: "Name", JSON: "name", Perm: "List"},
			},
			permissions: []accesstypes.Permission{accesstypes.List},
			wantErr:     true,
		},
		{
			name: "stale Delete tag is rejected",
			fields: []FieldTags{
				{Field: "Name", JSON: "name", Perm: "Delete"},
			},
			permissions: []accesstypes.Permission{accesstypes.Create},
			wantErr:     true,
		},
		{
			name: "mixed mutating and non-mutating permissions are rejected",
			fields: []FieldTags{
				{Field: "Name", JSON: "name"},
			},
			permissions: []accesstypes.Permission{accesstypes.Read, accesstypes.Update},
			wantErr:     true,
		},
		{
			name: "permission tag on json-hidden field is rejected",
			fields: []FieldTags{
				{Field: "Name", JSON: "-", Perm: "Read"},
			},
			permissions: []accesstypes.Permission{accesstypes.Read},
			wantErr:     true,
		},
		{
			name: "enforced field without json tag is rejected",
			fields: []FieldTags{
				{Field: "Name", JSON: ""},
			},
			permissions: []accesstypes.Permission{accesstypes.Read},
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NewSetData(tt.fields, tt.permissions...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewSetData() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("NewSetData() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCollectionBuilder_Data(t *testing.T) {
	t.Parallel()

	b := NewCollectionBuilder()

	listSet, err := NewSetData([]FieldTags{
		{Field: "ID", JSON: "id", Perm: "-"},
		{Field: "Name", JSON: "name"},
	}, accesstypes.List)
	if err != nil {
		t.Fatalf("NewSetData() error = %v", err)
	}
	readSet, err := NewSetData([]FieldTags{
		{Field: "ID", JSON: "id", Perm: "-"},
		{Field: "Name", JSON: "name"},
	}, accesstypes.Read)
	if err != nil {
		t.Fatalf("NewSetData() error = %v", err)
	}
	patchSet, err := NewSetData([]FieldTags{
		{Field: "Name", JSON: "name"},
		{Field: "Code", JSON: "code", Immutable: true},
	}, accesstypes.Create, accesstypes.Update, accesstypes.Delete)
	if err != nil {
		t.Fatalf("NewSetData() error = %v", err)
	}

	scope := accesstypes.GlobalPermissionScope
	for _, set := range []SetData{listSet, readSet, patchSet} {
		if err := b.AddResourceSet(scope, "Widgets", set); err != nil {
			t.Fatalf("AddResourceSet() error = %v", err)
		}
	}

	if err := b.AddMethodResource(scope, accesstypes.Execute, "DoThing"); err != nil {
		t.Fatalf("AddMethodResource() error = %v", err)
	}

	// Manual registration allows duplicates; Data must deduplicate them.
	for range 2 {
		if err := b.AddResource(scope, accesstypes.Read, "ManualThing"); err != nil {
			t.Fatalf("AddResource() error = %v", err)
		}
	}

	want := CollectionData{Resources: []CollectionResource{
		{
			Name:        "DoThing",
			Scope:       scope,
			Permissions: []accesstypes.Permission{accesstypes.Execute},
		},
		{
			Name:        "ManualThing",
			Scope:       scope,
			Permissions: []accesstypes.Permission{accesstypes.Read},
		},
		{
			Name:        "Widgets",
			Scope:       scope,
			Permissions: []accesstypes.Permission{accesstypes.Create, accesstypes.Delete, accesstypes.List, accesstypes.Read, accesstypes.Update},
			Tags: []TagData{
				{Name: "code", Permissions: []accesstypes.Permission{accesstypes.Create}},
				{Name: "id"},
				{Name: "name", Permissions: []accesstypes.Permission{accesstypes.Create, accesstypes.List, accesstypes.Read, accesstypes.Update}},
			},
			ImmutableTags: []accesstypes.Tag{"code"},
		},
	}}

	if diff := cmp.Diff(want, b.Data()); diff != "" {
		t.Errorf("CollectionBuilder.Data() mismatch (-want +got):\n%s", diff)
	}
}

func TestCollectionBuilder_duplicateErrors(t *testing.T) {
	t.Parallel()

	scope := accesstypes.GlobalPermissionScope

	tests := []struct {
		name string
		// run performs any valid setup on b (failing the test on setup errors) and
		// returns the error from the offending call.
		run func(t *testing.T, b *CollectionBuilder) error
	}{
		{
			name: "duplicate resource permission across sets",
			run: func(t *testing.T, b *CollectionBuilder) error {
				t.Helper()
				set, err := NewSetData([]FieldTags{{Field: "ID", JSON: "id"}}, accesstypes.List)
				if err != nil {
					t.Fatalf("NewSetData() error = %v", err)
				}
				if err := b.AddResourceSet(scope, "Widgets", set); err != nil {
					t.Fatalf("AddResourceSet() error = %v", err)
				}

				return b.AddResourceSet(scope, "Widgets", set)
			},
		},
		{
			name: "duplicate method resource",
			run: func(t *testing.T, b *CollectionBuilder) error {
				t.Helper()
				if err := b.AddMethodResource(scope, accesstypes.Execute, "DoThing"); err != nil {
					t.Fatalf("AddMethodResource() error = %v", err)
				}

				return b.AddMethodResource(scope, accesstypes.Execute, "DoThing")
			},
		},
		{
			name: "null permission",
			run: func(t *testing.T, b *CollectionBuilder) error {
				t.Helper()

				return b.AddResource(scope, accesstypes.NullPermission, "Widgets")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tt.run(t, NewCollectionBuilder()); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestNewGeneratedCollection_validation(t *testing.T) {
	t.Parallel()

	scope := accesstypes.GlobalPermissionScope

	tests := []struct {
		name    string
		data    CollectionData
		wantErr bool
	}{
		{
			name: "valid",
			data: CollectionData{Resources: []CollectionResource{{
				Name:        "Widgets",
				Scope:       scope,
				Permissions: []accesstypes.Permission{accesstypes.List},
				Tags:        []TagData{{Name: "id", Permissions: []accesstypes.Permission{accesstypes.List}}, {Name: "name", Permissions: []accesstypes.Permission{}}},
			}}},
		},
		{
			name: "positional masking on a registered tag",
			data: CollectionData{Resources: []CollectionResource{{
				Name:        "Widgets",
				Scope:       scope,
				Permissions: []accesstypes.Permission{accesstypes.List},
				Tags:        []TagData{{Name: "id"}, {Name: "fee", Permissions: []accesstypes.Permission{accesstypes.List}, Masking: MaskingPositional}},
				Order:       []accesstypes.Tag{"fee"},
				QueryKeys:   []accesstypes.Tag{"fee"},
			}}},
		},
		{
			name: "concealing spelled out is the default",
			data: CollectionData{Resources: []CollectionResource{{
				Name:        "Widgets",
				Scope:       scope,
				Permissions: []accesstypes.Permission{accesstypes.List},
				Tags:        []TagData{{Name: "fee", Permissions: []accesstypes.Permission{accesstypes.List}, Masking: MaskingConcealing}},
			}}},
		},
		{
			name: "an unknown masking behavior",
			data: CollectionData{Resources: []CollectionResource{{
				Name:        "Widgets",
				Scope:       scope,
				Permissions: []accesstypes.Permission{accesstypes.List},
				Tags:        []TagData{{Name: "fee", Permissions: []accesstypes.Permission{accesstypes.List}, Masking: "sideways"}},
			}}},
			wantErr: true,
		},
		{
			name: "an order naming a tag the resource lacks",
			data: CollectionData{Resources: []CollectionResource{{
				Name:        "Widgets",
				Scope:       scope,
				Permissions: []accesstypes.Permission{accesstypes.List},
				Tags:        []TagData{{Name: "fee", Permissions: []accesstypes.Permission{accesstypes.List}}},
				Order:       []accesstypes.Tag{"deadline"},
			}}},
			wantErr: true,
		},
		{
			name: "a query key naming a tag the resource lacks",
			data: CollectionData{Resources: []CollectionResource{{
				Name:        "Widgets",
				Scope:       scope,
				Permissions: []accesstypes.Permission{accesstypes.List},
				Tags:        []TagData{{Name: "fee", Permissions: []accesstypes.Permission{accesstypes.List}}},
				QueryKeys:   []accesstypes.Tag{"deadline"},
			}}},
			wantErr: true,
		},
		{
			name: "a field a create must carry is one of the resource's tags that carry Create",
			data: CollectionData{Resources: []CollectionResource{{
				Name:         "Widgets",
				Scope:        scope,
				Permissions:  []accesstypes.Permission{accesstypes.Create},
				Tags:         []TagData{{Name: "id"}, {Name: "name", Permissions: []accesstypes.Permission{accesstypes.Create}}},
				RequiredTags: []accesstypes.Tag{"name"},
			}}},
		},
		{
			name: "a required field naming a tag the resource lacks",
			data: CollectionData{Resources: []CollectionResource{{
				Name:         "Widgets",
				Scope:        scope,
				Permissions:  []accesstypes.Permission{accesstypes.Create},
				Tags:         []TagData{{Name: "name", Permissions: []accesstypes.Permission{accesstypes.Create}}},
				RequiredTags: []accesstypes.Tag{"weight"},
			}}},
			wantErr: true,
		},
		{
			name: "a required field naming a tag no grant names for Create",
			data: CollectionData{Resources: []CollectionResource{{
				Name:         "Widgets",
				Scope:        scope,
				Permissions:  []accesstypes.Permission{accesstypes.Create, accesstypes.Read},
				Tags:         []TagData{{Name: "name", Permissions: []accesstypes.Permission{accesstypes.Create}}, {Name: "photo", Permissions: []accesstypes.Permission{accesstypes.Read}}},
				RequiredTags: []accesstypes.Tag{"photo"},
			}}},
			wantErr: true,
		},
		{
			name:    "empty resource name",
			data:    CollectionData{Resources: []CollectionResource{{Scope: scope}}},
			wantErr: true,
		},
		{
			name:    "empty scope",
			data:    CollectionData{Resources: []CollectionResource{{Name: "Widgets"}}},
			wantErr: true,
		},
		{
			name: "duplicate resource",
			data: CollectionData{Resources: []CollectionResource{
				{Name: "Widgets", Scope: scope, Permissions: []accesstypes.Permission{accesstypes.List}},
				{Name: "Widgets", Scope: scope, Permissions: []accesstypes.Permission{accesstypes.Read}},
			}},
			wantErr: true,
		},
		{
			name: "null resource permission",
			data: CollectionData{Resources: []CollectionResource{{
				Name: "Widgets", Scope: scope, Permissions: []accesstypes.Permission{accesstypes.NullPermission},
			}}},
			wantErr: true,
		},
		{
			name: "duplicate tag",
			data: CollectionData{Resources: []CollectionResource{{
				Name: "Widgets", Scope: scope,
				Tags: []TagData{{Name: "id"}, {Name: "id"}},
			}}},
			wantErr: true,
		},
		{
			name: "duplicate tag permission",
			data: CollectionData{Resources: []CollectionResource{{
				Name: "Widgets", Scope: scope,
				Tags: []TagData{{Name: "id", Permissions: []accesstypes.Permission{accesstypes.List, accesstypes.List}}},
			}}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewGeneratedCollection(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewGeneratedCollection() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGeneratedCollection_roundTrip(t *testing.T) {
	t.Parallel()

	b := NewCollectionBuilder()
	set, err := NewSetData([]FieldTags{
		{Field: "ID", JSON: "id", Perm: "-"},
		{Field: "Name", JSON: "name"},
	}, accesstypes.List)
	if err != nil {
		t.Fatalf("NewSetData() error = %v", err)
	}
	if err := b.AddResourceSet(accesstypes.GlobalPermissionScope, "Widgets", set); err != nil {
		t.Fatalf("AddResourceSet() error = %v", err)
	}
	if err := b.AddMethodResource(accesstypes.GlobalPermissionScope, accesstypes.Execute, "DoThing"); err != nil {
		t.Fatalf("AddMethodResource() error = %v", err)
	}
	if err := b.AddResourceSet(accesstypes.GlobalPermissionScope, "Summaries", set); err != nil {
		t.Fatalf("AddResourceSet() error = %v", err)
	}
	b.SetResourceComputed(accesstypes.GlobalPermissionScope, "Summaries")
	if err := b.AddMethodResource(accesstypes.GlobalPermissionScope, accesstypes.Execute, "ShipWidget"); err != nil {
		t.Fatalf("AddMethodResource() error = %v", err)
	}
	b.SetMethodTransition(accesstypes.GlobalPermissionScope, "ShipWidget", TransitionData{Target: "Widgets", From: []string{"packed", "labeled"}, To: "shipped"})
	listed, err := NewSetData([]FieldTags{
		{Field: "ID", JSON: "id", Perm: "-"},
		{Field: "Name", JSON: "name"},
		{Field: "Fee", JSON: "fee", Masking: "positional"},
		{Field: "Deadline", JSON: "deadline"},
	}, accesstypes.List)
	if err != nil {
		t.Fatalf("NewSetData() error = %v", err)
	}
	if err := b.AddResourceSet(accesstypes.DomainPermissionScope, "Missions", listed); err != nil {
		t.Fatalf("AddResourceSet() error = %v", err)
	}
	b.SetResourceQueryKeys(accesstypes.DomainPermissionScope, "Missions", []accesstypes.Tag{"deadline"}, []accesstypes.Tag{"fee", "name"})

	data := b.Data()
	g, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	if diff := cmp.Diff(data, g.Data()); diff != "" {
		t.Errorf("GeneratedCollection.Data() round trip mismatch (-want +got):\n%s", diff)
	}

	missions := data.Resources[slices.IndexFunc(data.Resources, func(r CollectionResource) bool { return r.Name == "Missions" })]
	wantMissions := CollectionResource{
		Name:        "Missions",
		Scope:       accesstypes.DomainPermissionScope,
		Permissions: []accesstypes.Permission{accesstypes.List},
		Tags: []TagData{
			{Name: "deadline", Permissions: []accesstypes.Permission{accesstypes.List}},
			{Name: "fee", Permissions: []accesstypes.Permission{accesstypes.List}, Masking: MaskingPositional},
			{Name: "id"},
			{Name: "name", Permissions: []accesstypes.Permission{accesstypes.List}},
		},
		Order:     []accesstypes.Tag{"deadline"},
		QueryKeys: []accesstypes.Tag{"fee", "name"},
	}
	if diff := cmp.Diff(wantMissions, missions); diff != "" {
		t.Errorf("Missions collection data mismatch (-want +got):\n%s", diff)
	}
	order, keys := g.ConcealingKeys(accesstypes.DomainPermissionScope, "Missions")
	if diff := cmp.Diff([]accesstypes.Tag{"deadline"}, order); diff != "" {
		t.Errorf("ConcealingKeys(Missions) order mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]accesstypes.Tag{"name"}, keys); diff != "" {
		t.Errorf("ConcealingKeys(Missions) keys mismatch (-want +got):\n%s", diff)
	}
	if order, keys := g.ConcealingKeys(accesstypes.GlobalPermissionScope, "Widgets"); order != nil || keys != nil {
		t.Errorf("ConcealingKeys(Widgets) = %v, %v, want none for a resource declaring no keys", order, keys)
	}
	if got := g.FieldMasking(accesstypes.DomainPermissionScope, "Missions", "fee"); got != MaskingPositional {
		t.Errorf("FieldMasking(fee) = %q, want positional", got)
	}
	if got := g.FieldMasking(accesstypes.DomainPermissionScope, "Missions", "deadline"); got != MaskingConcealing {
		t.Errorf("FieldMasking(deadline) = %q, want concealing", got)
	}

	wantTransitions := []TransitionMethod{{Method: "ShipWidget", Transition: TransitionData{Target: "Widgets", From: []string{"packed", "labeled"}, To: "shipped"}}}
	if diff := cmp.Diff(wantTransitions, g.TransitionsOnto("Widgets")); diff != "" {
		t.Errorf("TransitionsOnto(Widgets) mismatch (-want +got):\n%s", diff)
	}
	if got := g.TransitionsOnto("Summaries"); got != nil {
		t.Errorf("TransitionsOnto(Summaries) = %v, want none", got)
	}

	tests := []struct {
		name string
		res  accesstypes.Resource
		want bool
	}{
		{name: "computed resource answers true", res: "Summaries", want: true},
		{name: "computed field resource answers as its base", res: "Summaries.total", want: true},
		{name: "table-backed resource answers false", res: "Widgets", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := g.IsComputedResource(accesstypes.GlobalPermissionScope, tt.res); got != tt.want {
				t.Errorf("IsComputedResource(%s) = %t, want %t", tt.res, got, tt.want)
			}
		})
	}
}

func TestGeneratedCollection_readMethods(t *testing.T) {
	t.Parallel()

	// What the builder took and what a load of its Data() answers must agree for every
	// read method: a declaration a setter takes and Data() drops is found here. The
	// builder's own state is read (b.g), since GeneratedCollection() is itself a load of
	// Data().
	listSet, err := NewSetData([]FieldTags{
		{Field: "ID", JSON: "id", Perm: "-"},
		{Field: "Name", JSON: "name"},
	}, accesstypes.List)
	if err != nil {
		t.Fatalf("NewSetData() error = %v", err)
	}
	patchSet, err := NewSetData([]FieldTags{
		{Field: "Name", JSON: "name"},
		{Field: "Code", JSON: "code", Immutable: true},
	}, accesstypes.Create, accesstypes.Update, accesstypes.Delete)
	if err != nil {
		t.Fatalf("NewSetData() error = %v", err)
	}

	b := NewCollectionBuilder()
	if err := b.AddResourceSet(accesstypes.GlobalPermissionScope, "Widgets", listSet); err != nil {
		t.Fatalf("registering list set: %v", err)
	}
	if err := b.AddResourceSet(accesstypes.GlobalPermissionScope, "Widgets", patchSet); err != nil {
		t.Fatalf("registering patch set: %v", err)
	}
	if err := b.AddMethodResource(accesstypes.GlobalPermissionScope, accesstypes.Execute, "DoThing"); err != nil {
		t.Fatalf("AddMethodResource() error = %v", err)
	}

	direct := b.g
	fromData, err := NewGeneratedCollection(b.Data())
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	if diff := cmp.Diff(direct.Resources(), fromData.Resources()); diff != "" {
		t.Errorf("Resources() mismatch (-direct +fromData):\n%s", diff)
	}
	// List()'s value ordering follows map iteration and is not deterministic; its
	// consumers are order-insensitive.
	if diff := cmp.Diff(direct.List(), fromData.List(), cmpopts.SortSlices(func(a, b accesstypes.Resource) bool { return a < b })); diff != "" {
		t.Errorf("List() mismatch (-direct +fromData):\n%s", diff)
	}
	if diff := cmp.Diff(direct.TypescriptData(), fromData.TypescriptData()); diff != "" {
		t.Errorf("TypescriptData() mismatch (-direct +fromData):\n%s", diff)
	}
	if got, want := fromData.Scope("Widgets"), direct.Scope("Widgets"); got != want {
		t.Errorf("Scope() = %v, want %v", got, want)
	}
	if !direct.ResourceExists("Widgets") {
		t.Error("ResourceExists(Widgets) = false, want true")
	}
	if direct.ResourceExists("DoThing") {
		t.Error("ResourceExists(DoThing) = true, want false (Execute-only resources are methods)")
	}
	if !direct.IsResourceImmutable(accesstypes.GlobalPermissionScope, accesstypes.Resource("Widgets").ResourceWithTag("code")) {
		t.Error("IsResourceImmutable(Widgets.code) = false, want true")
	}
	if direct.IsResourceImmutable(accesstypes.GlobalPermissionScope, accesstypes.Resource("Widgets").ResourceWithTag("name")) {
		t.Error("IsResourceImmutable(Widgets.name) = true, want false")
	}
}

// TestGeneratedCollection_TypescriptDataExcluding pins the outlet filter's collection
// half: excluding a resource removes it, its tags, and any permission or scope only
// it carried, while excluding nothing returns exactly TypescriptData.
func TestGeneratedCollection_TypescriptDataExcluding(t *testing.T) {
	t.Parallel()

	widgetSet, err := NewSetData([]FieldTags{
		{Field: "ID", JSON: "id", Perm: "-"},
		{Field: "Name", JSON: "name"},
	}, accesstypes.List)
	if err != nil {
		t.Fatalf("NewSetData() error = %v", err)
	}
	gadgetSet, err := NewSetData([]FieldTags{
		{Field: "Code", JSON: "code"},
	}, accesstypes.Update)
	if err != nil {
		t.Fatalf("NewSetData() error = %v", err)
	}

	b := NewCollectionBuilder()
	if err := b.AddResourceSet(accesstypes.GlobalPermissionScope, "Widgets", widgetSet); err != nil {
		t.Fatalf("registering Widgets: %v", err)
	}
	if err := b.AddResourceSet(accesstypes.DomainPermissionScope, "Gadgets", gadgetSet); err != nil {
		t.Fatalf("registering Gadgets: %v", err)
	}
	if err := b.AddMethodResource(accesstypes.GlobalPermissionScope, accesstypes.Execute, "DoThing"); err != nil {
		t.Fatalf("AddMethodResource() error = %v", err)
	}
	g, err := b.GeneratedCollection()
	if err != nil {
		t.Fatalf("GeneratedCollection() error = %v", err)
	}

	tests := []struct {
		name     string
		excluded []accesstypes.Resource
		want     *TypescriptData
	}{
		{
			name: "excluding nothing returns the unfiltered data",
			want: g.TypescriptData(),
		},
		{
			name:     "excluding the sole domain resource drops its tags; the permission and scope vocabulary stays whole",
			excluded: []accesstypes.Resource{"Gadgets"},
			want: &TypescriptData{
				Permissions:      []accesstypes.Permission{accesstypes.Create, accesstypes.Delete, accesstypes.Execute, accesstypes.List, accesstypes.Read, accesstypes.Update},
				Resources:        []accesstypes.Resource{"Widgets"},
				Methods:          []accesstypes.Resource{"DoThing"},
				ResourceTags:     map[accesstypes.Resource][]accesstypes.Tag{"Widgets": {"id", "name"}},
				PermissionScopes: []accesstypes.PermissionScope{accesstypes.DomainPermissionScope, accesstypes.GlobalPermissionScope},
			},
		},
		{
			name:     "excluding a method drops it from the methods; the permission vocabulary stays whole",
			excluded: []accesstypes.Resource{"DoThing"},
			want: &TypescriptData{
				Permissions:      []accesstypes.Permission{accesstypes.Create, accesstypes.Delete, accesstypes.Execute, accesstypes.List, accesstypes.Read, accesstypes.Update},
				Resources:        []accesstypes.Resource{"Gadgets", "Widgets"},
				Methods:          []accesstypes.Resource{},
				ResourceTags:     map[accesstypes.Resource][]accesstypes.Tag{"Widgets": {"id", "name"}, "Gadgets": {"code"}},
				PermissionScopes: []accesstypes.PermissionScope{accesstypes.DomainPermissionScope, accesstypes.GlobalPermissionScope},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, g.TypescriptDataExcluding(tt.excluded...)); diff != "" {
				t.Errorf("TypescriptDataExcluding() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGeneratedCollection_HasPermission(t *testing.T) {
	t.Parallel()

	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{
		{Name: "Widgets", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Read}},
	}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	tests := []struct {
		name       string
		scope      accesstypes.PermissionScope
		permission accesstypes.Permission
		resource   accesstypes.Resource
		want       bool
	}{
		{name: "registered permission", scope: accesstypes.GlobalPermissionScope, permission: accesstypes.Read, resource: "Widgets", want: true},
		{name: "unregistered permission", scope: accesstypes.GlobalPermissionScope, permission: accesstypes.Update, resource: "Widgets", want: false},
		{name: "wrong scope", scope: accesstypes.DomainPermissionScope, permission: accesstypes.Read, resource: "Widgets", want: false},
		{name: "unknown resource", scope: accesstypes.GlobalPermissionScope, permission: accesstypes.Read, resource: "Missing", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := g.HasPermission(tt.scope, tt.permission, tt.resource); got != tt.want {
				t.Errorf("HasPermission(%s, %s, %s) = %v, want %v", tt.scope, tt.permission, tt.resource, got, tt.want)
			}
		})
	}
}

// TestGeneratedCollection_methodTargets pins the @target carriage (§12): a
// transition registers its target implicitly, a plain target registers
// directly, MethodTarget answers per scope, and MethodsTargeting merges both
// forms sorted by method — the capability envelope's Execute candidates.
func TestGeneratedCollection_methodTargets(t *testing.T) {
	t.Parallel()

	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{
		{
			Name:        "Tasks",
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.Read},
		},
		{
			Name:        "CloseTask",
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.Execute},
			Transition:  &TransitionData{Target: "Tasks", From: []string{"open"}, To: "closed"},
		},
		{
			Name:        "NudgeTask",
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.Execute},
			Target:      "Tasks",
		},
		{
			Name:        "RunReport",
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.Execute},
		},
	}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	if target, ok := g.MethodTarget(accesstypes.DomainPermissionScope, "CloseTask"); !ok || target != "Tasks" {
		t.Errorf("MethodTarget(CloseTask) = %q, %v; want Tasks, true (a transition registers its target)", target, ok)
	}
	if target, ok := g.MethodTarget(accesstypes.DomainPermissionScope, "NudgeTask"); !ok || target != "Tasks" {
		t.Errorf("MethodTarget(NudgeTask) = %q, %v; want Tasks, true", target, ok)
	}
	if _, ok := g.MethodTarget(accesstypes.DomainPermissionScope, "RunReport"); ok {
		t.Error("MethodTarget(RunReport) = true, want false (no @target row)")
	}
	if _, ok := g.MethodTarget(accesstypes.GlobalPermissionScope, "CloseTask"); ok {
		t.Error("MethodTarget answered across scopes, want per-scope")
	}

	methods := g.MethodsTargeting("Tasks")
	if len(methods) != 2 || methods[0].Method != "CloseTask" || methods[1].Method != "NudgeTask" {
		t.Fatalf("MethodsTargeting(Tasks) = %+v, want [CloseTask NudgeTask]", methods)
	}
	if methods[0].Transition == nil || methods[0].Transition.To != "closed" {
		t.Errorf("MethodsTargeting(Tasks)[0].Transition = %+v, want the declared edge", methods[0].Transition)
	}
	if methods[1].Transition != nil {
		t.Errorf("MethodsTargeting(Tasks)[1].Transition = %+v, want nil for the plain form", methods[1].Transition)
	}

	// The serializable round trip carries both forms.
	data := collectionDataFrom(g)
	var closeTask, nudge *CollectionResource
	for i := range data.Resources {
		switch data.Resources[i].Name {
		case "CloseTask":
			closeTask = &data.Resources[i]
		case "NudgeTask":
			nudge = &data.Resources[i]
		}
	}
	if closeTask == nil || closeTask.Target != "Tasks" || closeTask.Transition == nil {
		t.Errorf("round-tripped CloseTask = %+v, want Target and Transition", closeTask)
	}
	if nudge == nil || nudge.Target != "Tasks" || nudge.Transition != nil {
		t.Errorf("round-tripped NudgeTask = %+v, want Target only", nudge)
	}
}

// TestGeneratedCollection_methodCallerEnters pins the @callerEnters carriage: a
// method the builder marks comes back marked from MethodsTargeting, through Data()
// and a load of that data, beside an unmarked method on the same row that does not;
// and a load refuses a marked method that names no row.
func TestGeneratedCollection_methodCallerEnters(t *testing.T) {
	t.Parallel()

	b := NewCollectionBuilder()
	if err := b.AddResource(accesstypes.DomainPermissionScope, accesstypes.Read, "Tasks"); err != nil {
		t.Fatalf("AddResource(Tasks) error = %v", err)
	}
	for _, method := range []accesstypes.Resource{"CloseTask", "NudgeTask", "ReopenTask"} {
		if err := b.AddMethodResource(accesstypes.DomainPermissionScope, accesstypes.Execute, method); err != nil {
			t.Fatalf("AddMethodResource(%s) error = %v", method, err)
		}
	}
	b.SetMethodTransition(accesstypes.DomainPermissionScope, "CloseTask", TransitionData{Target: "Tasks", From: []string{"open"}, To: "closed"})
	b.SetMethodTarget(accesstypes.DomainPermissionScope, "NudgeTask", "Tasks")
	b.SetMethodTarget(accesstypes.DomainPermissionScope, "ReopenTask", "Tasks")
	b.SetMethodCallerEnters(accesstypes.DomainPermissionScope, "CloseTask")
	b.SetMethodCallerEnters(accesstypes.DomainPermissionScope, "NudgeTask")

	want := map[accesstypes.Resource]bool{"CloseTask": true, "NudgeTask": true, "ReopenTask": false}
	marks := func(g *GeneratedCollection) map[accesstypes.Resource]bool {
		got := map[accesstypes.Resource]bool{}
		for _, tm := range g.MethodsTargeting("Tasks") {
			got[tm.Method] = tm.CallerEnters
		}

		return got
	}

	data := b.Data()
	for _, res := range data.Resources {
		if res.Name == "ReopenTask" && res.CallerEnters {
			t.Error("Data() marks ReopenTask, which the builder did not mark")
		}
	}
	loaded, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection(Data()) error = %v", err)
	}
	if diff := cmp.Diff(want, marks(loaded)); diff != "" {
		t.Errorf("MethodsTargeting(Tasks) CallerEnters after a load (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(want, marks(b.g)); diff != "" {
		t.Errorf("MethodsTargeting(Tasks) CallerEnters on the builder's own state (-want +got):\n%s", diff)
	}

	_, err = NewGeneratedCollection(CollectionData{Resources: []CollectionResource{{
		Name:         "RunReport",
		Scope:        accesstypes.DomainPermissionScope,
		Permissions:  []accesstypes.Permission{accesstypes.Execute},
		CallerEnters: true,
	}}})
	wantErr := `method resource "RunReport" is offered only to a caller who enters its row's tenants but names no target row`
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Errorf("NewGeneratedCollection() of a marked method with no target error = %v, want %q", err, wantErr)
	}
}

// TestGeneratedCollection_methodOfferedOn pins the carriage of the rows that offer a
// method that names no row (@callerEnters(Row, ...)): a method the builder gives rows comes
// back from MethodsOfferedOn for each of them and for no other resource, through Data(),
// which hands the rows back sorted, through a load of that data and in the builder's own
// state; and a load refuses rows given to a method that names its row, a row
// with no name and a row named twice.
func TestGeneratedCollection_methodOfferedOn(t *testing.T) {
	t.Parallel()

	b := NewCollectionBuilder()
	for _, res := range []accesstypes.Resource{"Docks", "Keepers", "Cranes"} {
		if err := b.AddResource(accesstypes.DomainPermissionScope, accesstypes.Read, res); err != nil {
			t.Fatalf("AddResource(%s) error = %v", res, err)
		}
	}
	for _, method := range []accesstypes.Resource{"SweepKeepers", "CloseDock"} {
		if err := b.AddMethodResource(accesstypes.DomainPermissionScope, accesstypes.Execute, method); err != nil {
			t.Fatalf("AddMethodResource(%s) error = %v", method, err)
		}
	}
	b.SetMethodTarget(accesstypes.DomainPermissionScope, "CloseDock", "Docks")
	b.SetMethodOfferedOn(accesstypes.DomainPermissionScope, "SweepKeepers", []accesstypes.Resource{"Cranes"})
	b.SetMethodOfferedOn(accesstypes.DomainPermissionScope, "SweepKeepers", []accesstypes.Resource{"Keepers", "Docks"})

	data := b.Data()
	for _, res := range data.Resources {
		var want []accesstypes.Resource
		if res.Name == "SweepKeepers" {
			want = []accesstypes.Resource{"Docks", "Keepers"}
		}
		if diff := cmp.Diff(want, res.OfferedOn); diff != "" {
			t.Errorf("Data() OfferedOn of %s mismatch (-want +got):\n%s", res.Name, diff)
		}
	}
	loaded, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection(Data()) error = %v", err)
	}
	want := map[accesstypes.Resource][]accesstypes.Resource{"Docks": {"SweepKeepers"}, "Keepers": {"SweepKeepers"}, "Cranes": nil, "SweepKeepers": nil}
	for name, g := range map[string]*GeneratedCollection{"a load of Data()": loaded, "the builder's own state": b.g} {
		for row, methods := range want {
			if diff := cmp.Diff(methods, g.MethodsOfferedOn(row)); diff != "" {
				t.Errorf("%s: MethodsOfferedOn(%s) mismatch (-want +got):\n%s", name, row, diff)
			}
		}
	}

	tests := []struct {
		name    string
		res     CollectionResource
		wantErr string
	}{
		{
			name: "rows given to a method that names its row",
			res: CollectionResource{
				Name: "CloseDock", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute},
				Target: "Docks", OfferedOn: []accesstypes.Resource{"Docks", "Keepers"},
			},
			wantErr: `method resource "CloseDock" is offered on the rows of Docks, Keepers and names a target row: a method that names its row is offered on that row alone`,
		},
		{
			name: "rows given to a transition",
			res: CollectionResource{
				Name: "CloseDock", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute},
				Transition: &TransitionData{Target: "Docks", From: []string{"open"}, To: "closed"}, OfferedOn: []accesstypes.Resource{"Keepers"},
			},
			wantErr: `method resource "CloseDock" is offered on the rows of Keepers and names a target row: a method that names its row is offered on that row alone`,
		},
		{
			name: "a row with no name",
			res: CollectionResource{
				Name: "SweepKeepers", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute},
				OfferedOn: []accesstypes.Resource{"Keepers", ""},
			},
			wantErr: `method resource "SweepKeepers" is offered on the rows of a resource with no name`,
		},
		{
			name: "a row named twice",
			res: CollectionResource{
				Name: "SweepKeepers", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute},
				OfferedOn: []accesstypes.Resource{"Keepers", "Keepers"},
			},
			wantErr: `method resource "SweepKeepers" is offered on the rows of "Keepers" twice`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{tt.res}})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("NewGeneratedCollection() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// TestGeneratedCollection_membersOf pins the workflow-membership surface the
// create-under-parent affordance rides (§11): MembersOf answers the immediate
// hop only, sorted, and the Parent field round-trips.
func TestGeneratedCollection_membersOf(t *testing.T) {
	t.Parallel()

	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{
		{
			Name:        "Tasks",
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.Read},
		},
		{
			Name:        "TaskNotes",
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.Create},
			Parent:      "Tasks",
		},
		{
			Name:        "TaskLines",
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.Create},
			Parent:      "Tasks",
		},
		{
			// A second-level member: its affordance rides TaskLines, never Tasks.
			Name:        "TaskLineItems",
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.Create},
			Parent:      "TaskLines",
		},
	}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	if got := g.MembersOf("Tasks"); len(got) != 2 || got[0] != "TaskLines" || got[1] != "TaskNotes" {
		t.Errorf("MembersOf(Tasks) = %v, want [TaskLines TaskNotes] (immediate hop only, sorted)", got)
	}
	if got := g.MembersOf("TaskLines"); len(got) != 1 || got[0] != "TaskLineItems" {
		t.Errorf("MembersOf(TaskLines) = %v, want [TaskLineItems]", got)
	}
	if got := g.MembersOf("TaskNotes"); len(got) != 0 {
		t.Errorf("MembersOf(TaskNotes) = %v, want empty", got)
	}

	data := collectionDataFrom(g)
	for i := range data.Resources {
		if data.Resources[i].Name == "TaskLines" && data.Resources[i].Parent != "Tasks" {
			t.Errorf("round-tripped TaskLines.Parent = %q, want Tasks", data.Resources[i].Parent)
		}
	}
}

// TestNewGeneratedCollection_targetMismatch pins the consistency check: a
// method whose Target disagrees with its transition's target is invalid data.
func TestNewGeneratedCollection_targetMismatch(t *testing.T) {
	t.Parallel()

	_, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{{
		Name:        "CloseTask",
		Scope:       accesstypes.DomainPermissionScope,
		Permissions: []accesstypes.Permission{accesstypes.Execute},
		Transition:  &TransitionData{Target: "Tasks", From: []string{"open"}, To: "closed"},
		Target:      "Widgets",
	}}})
	if err == nil || !strings.Contains(err.Error(), "its transition targets") {
		t.Errorf("NewGeneratedCollection() error = %v, want the target-mismatch rejection", err)
	}
}

// TestCollectionBuilder_requiredOnCreate pins the fact deploy-time role validation reads
// for the required-field warning: the builder keeps, of the fields it is told a create
// must be given, the ones a grant names for Create, sorted; the data carries them and
// reads back the same; and a resource told nothing answers none.
func TestCollectionBuilder_requiredOnCreate(t *testing.T) {
	t.Parallel()

	const scope = accesstypes.DomainPermissionScope

	b := NewCollectionBuilder()
	created, err := NewSetData([]FieldTags{
		{Field: "ID", JSON: "id", Perm: "-"},
		{Field: "Instructions", JSON: "instructions"},
		{Field: "Done", JSON: "done"},
		{Field: "Notes", JSON: "notes"},
	}, accesstypes.Create)
	if err != nil {
		t.Fatalf("NewSetData() error = %v", err)
	}
	if err := b.AddResourceSet(scope, "Tasks", created); err != nil {
		t.Fatalf("AddResourceSet() error = %v", err)
	}
	read, err := NewSetData([]FieldTags{{Field: "ID", JSON: "id", Perm: "-"}, {Field: "Photo", JSON: "photo"}}, accesstypes.Read)
	if err != nil {
		t.Fatalf("NewSetData() error = %v", err)
	}
	if err := b.AddResourceSet(scope, "Tasks", read); err != nil {
		t.Fatalf("AddResourceSet() error = %v", err)
	}
	// Told in no order, with a field no Create grant can name (photo) and one twice.
	b.SetResourceRequiredTags(scope, "Tasks", []accesstypes.Tag{"instructions", "photo", "done", "instructions"})

	data := b.Data()
	tasks := data.Resources[slices.IndexFunc(data.Resources, func(r CollectionResource) bool { return r.Name == "Tasks" })]
	want := []accesstypes.Tag{"done", "instructions"}
	if diff := cmp.Diff(want, tasks.RequiredTags); diff != "" {
		t.Errorf("Tasks RequiredTags mismatch (-want +got):\n%s", diff)
	}

	g, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}
	if diff := cmp.Diff(data, g.Data()); diff != "" {
		t.Errorf("GeneratedCollection.Data() round trip mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(want, g.RequiredOnCreate(scope, "Tasks")); diff != "" {
		t.Errorf("RequiredOnCreate(Tasks) mismatch (-want +got):\n%s", diff)
	}
	if got := g.RequiredOnCreate(scope, "Tasks.done"); got != nil {
		t.Errorf("RequiredOnCreate(Tasks.done) = %v, want none for a field resource", got)
	}
	if got := g.RequiredOnCreate(accesstypes.GlobalPermissionScope, "Tasks"); got != nil {
		t.Errorf("RequiredOnCreate(Tasks) in another scope = %v, want none", got)
	}
}

// TestGeneratedCollection_listsAcrossTenants pins the declaration a list across tenants
// rests on: the collection answers it for the resources the data marks and for no other,
// the builder's mark survives the data, a nil collection declares none, and a resource
// that is no tenant's cannot be marked.
func TestGeneratedCollection_listsAcrossTenants(t *testing.T) {
	t.Parallel()

	builder := NewCollectionBuilder()
	for _, res := range []accesstypes.Resource{"Hangars", "Docks", "Berths"} {
		if err := builder.AddResource(accesstypes.DomainPermissionScope, accesstypes.List, res); err != nil {
			t.Fatalf("AddResource(%s) error = %v", res, err)
		}
	}
	if err := builder.AddResource(accesstypes.GlobalPermissionScope, accesstypes.List, "Ports"); err != nil {
		t.Fatalf("AddResource(Ports) error = %v", err)
	}
	builder.SetResourceListsAcrossTenants("Hangars")
	builder.SetResourceListsAcrossTenants("Berths")

	data := builder.Data()
	for i := range data.Resources {
		res := &data.Resources[i]
		if want := res.Name == "Hangars" || res.Name == "Berths"; res.ListsAcrossTenants != want {
			t.Errorf("the data marks %s as listed across tenants = %t, want %t", res.Name, res.ListsAcrossTenants, want)
		}
	}

	g, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}
	if diff := cmp.Diff([]accesstypes.Resource{"Berths", "Hangars"}, g.ListedAcrossTenants()); diff != "" {
		t.Errorf("ListedAcrossTenants() mismatch (-want +got):\n%s", diff)
	}
	for res, want := range map[accesstypes.Resource]bool{"Hangars": true, "Berths": true, "Docks": false, "Ports": false, "Unknown": false} {
		if got := g.ListsAcrossTenants(res); got != want {
			t.Errorf("ListsAcrossTenants(%s) = %t, want %t", res, got, want)
		}
	}

	var none *GeneratedCollection
	if none.ListsAcrossTenants("Hangars") || len(none.ListedAcrossTenants()) != 0 {
		t.Error("a nil collection declares a resource for listing across tenants, want none")
	}

	_, err = NewGeneratedCollection(CollectionData{Resources: []CollectionResource{
		{Name: "Ports", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.List}, ListsAcrossTenants: true},
	}})
	want := `resource "Ports" is declared for listing across tenants and is not a tenant's resource: its permission scope is "global"`
	if err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("NewGeneratedCollection() error = %v, want it to end %q", err, want)
	}
}

// TestGeneratedCollection_actionClasses pins the classes an action carries: what the
// builder records reaches the data sorted and once each, the data builds a collection
// that answers them in the action's own scope, and the collection names the classes in
// use and the actions of each.
func TestGeneratedCollection_actionClasses(t *testing.T) {
	t.Parallel()

	b := NewCollectionBuilder()
	for _, method := range []struct {
		scope   accesstypes.PermissionScope
		name    accesstypes.Resource
		classes []accesstypes.ActionClass
	}{
		{scope: accesstypes.GlobalPermissionScope, name: "SetPassword", classes: []accesstypes.ActionClass{"sensitive", accesstypes.ClassCredential, "sensitive"}},
		{scope: accesstypes.GlobalPermissionScope, name: "Launch"},
		{scope: accesstypes.DomainPermissionScope, name: "RenameUser", classes: []accesstypes.ActionClass{accesstypes.ClassCredential}},
		{scope: accesstypes.DomainPermissionScope, name: "WireFunds", classes: []accesstypes.ActionClass{"money_movement"}},
	} {
		if err := b.AddMethodResource(method.scope, accesstypes.Execute, method.name); err != nil {
			t.Fatalf("AddMethodResource(%s) error = %v", method.name, err)
		}
		b.SetActionClasses(method.scope, method.name, method.classes)
	}

	data := b.Data()
	wantData := map[accesstypes.Resource][]accesstypes.ActionClass{
		"SetPassword": {accesstypes.ClassCredential, "sensitive"},
		"Launch":      nil,
		"RenameUser":  {accesstypes.ClassCredential},
		"WireFunds":   {"money_movement"},
	}
	gotData := make(map[accesstypes.Resource][]accesstypes.ActionClass, len(data.Resources))
	for _, res := range data.Resources {
		gotData[res.Name] = res.Classes
	}
	if diff := cmp.Diff(wantData, gotData, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("CollectionData classes mismatch (-want +got):\n%s", diff)
	}

	g, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}
	for _, tt := range []struct {
		scope  accesstypes.PermissionScope
		action accesstypes.Resource
		want   []accesstypes.ActionClass
	}{
		{scope: accesstypes.GlobalPermissionScope, action: "SetPassword", want: []accesstypes.ActionClass{accesstypes.ClassCredential, "sensitive"}},
		{scope: accesstypes.DomainPermissionScope, action: "SetPassword"},
		{scope: accesstypes.GlobalPermissionScope, action: "Launch"},
		{scope: accesstypes.DomainPermissionScope, action: "RenameUser", want: []accesstypes.ActionClass{accesstypes.ClassCredential}},
		{scope: accesstypes.GlobalPermissionScope, action: "RenameUser"},
		{scope: accesstypes.GlobalPermissionScope, action: "NoSuchAction"},
	} {
		if diff := cmp.Diff(tt.want, g.ActionClasses(tt.scope, tt.action), cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("ActionClasses(%s, %s) mismatch (-want +got):\n%s", tt.scope, tt.action, diff)
		}
	}
	if diff := cmp.Diff([]accesstypes.ActionClass{accesstypes.ClassCredential, "money_movement", "sensitive"}, g.ClassesInUse()); diff != "" {
		t.Errorf("ClassesInUse() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]accesstypes.Resource{"RenameUser", "SetPassword"}, g.ActionsOfClass(accesstypes.ClassCredential)); diff != "" {
		t.Errorf("ActionsOfClass(credential) mismatch (-want +got):\n%s", diff)
	}
	if got := g.ActionsOfClass("unused"); len(got) != 0 {
		t.Errorf("ActionsOfClass(unused) = %v, want none", got)
	}
	if diff := cmp.Diff(data, g.Data()); diff != "" {
		t.Errorf("the data does not survive the round trip (-want +got):\n%s", diff)
	}
}

// TestCollectionBuilder_SetActionClasses_joins pins that a name given classes twice
// carries them all: a method registered with one class, and registered again by hand
// under another permission with another, answers both, so the later registration takes
// no class away and a mask that refuses the first still refuses the action.
func TestCollectionBuilder_SetActionClasses_joins(t *testing.T) {
	t.Parallel()

	b := NewCollectionBuilder()
	if err := b.AddMethodResource(accesstypes.GlobalPermissionScope, accesstypes.Execute, "SetPassword"); err != nil {
		t.Fatalf("AddMethodResource() error = %v", err)
	}
	b.SetActionClasses(accesstypes.GlobalPermissionScope, "SetPassword", []accesstypes.ActionClass{accesstypes.ClassCredential})
	if err := b.AddResource(accesstypes.GlobalPermissionScope, accesstypes.List, "SetPassword"); err != nil {
		t.Fatalf("AddResource() error = %v", err)
	}
	b.SetActionClasses(accesstypes.GlobalPermissionScope, "SetPassword", []accesstypes.ActionClass{"sensitive"})
	b.SetActionClasses(accesstypes.GlobalPermissionScope, "SetPassword", nil)

	want := []accesstypes.ActionClass{accesstypes.ClassCredential, "sensitive"}
	if diff := cmp.Diff(want, b.g.ActionClasses(accesstypes.GlobalPermissionScope, "SetPassword")); diff != "" {
		t.Errorf("ActionClasses() after two registrations mismatch (-want +got):\n%s", diff)
	}
	for _, res := range b.Data().Resources {
		if res.Name != "SetPassword" {
			continue
		}
		if diff := cmp.Diff(want, res.Classes); diff != "" {
			t.Errorf("CollectionData classes after two registrations mismatch (-want +got):\n%s", diff)
		}
	}
}

// TestNewGeneratedCollection_oneScopePerName pins that collection data built by hand
// lists a name in one scope: the collection finds a resource's scope by its name alone
// (Scope), so a name of two scopes was answered with either from one call to the next.
// The rule holds whatever either entry registers; the first scope said is the earlier
// entry's.
func TestNewGeneratedCollection_oneScopePerName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		resources []CollectionResource
		wantErr   string
	}{
		{
			name: "a record listed for the whole application and for a tenant",
			resources: []CollectionResource{
				{Name: "Widgets", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Read}},
				{Name: "Widgets", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Read}},
			},
			wantErr: `resource "Widgets" is listed in the scope "global" and in the scope "domain": a name is of one scope, which a role's grant and a check find by the name alone. ` +
				`List it in one scope, or give one of the two a name of its own`,
		},
		{
			name: "a classed action for the whole application and an action of the same name for a tenant",
			resources: []CollectionResource{
				{Name: "SetPassword", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Classes: []accesstypes.ActionClass{accesstypes.ClassCredential}},
				{Name: "SetPassword", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}},
			},
			wantErr: `resource "SetPassword" is listed in the scope "global" and in the scope "domain": a name is of one scope, which a role's grant and a check find by the name alone. ` +
				`List it in one scope, or give one of the two a name of its own`,
		},
		{
			name: "a classed action for the whole application and a record of the same name for a tenant",
			resources: []CollectionResource{
				{Name: "SetPassword", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Classes: []accesstypes.ActionClass{accesstypes.ClassCredential}},
				{Name: "SetPassword", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.List, accesstypes.Read}},
			},
			wantErr: `resource "SetPassword" is listed in the scope "global" and in the scope "domain": a name is of one scope, which a role's grant and a check find by the name alone. ` +
				`List it in one scope, or give one of the two a name of its own`,
		},
		{
			name: "an entry that registers no permission for a tenant and a record of the same name for the whole application",
			resources: []CollectionResource{
				{Name: "Widgets", Scope: accesstypes.DomainPermissionScope, Attributes: []AttributeData{{Name: "status", Column: "Status", Type: AttributeTypeString}}},
				{Name: "Widgets", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Read}},
			},
			wantErr: `resource "Widgets" is listed in the scope "domain" and in the scope "global": a name is of one scope, which a role's grant and a check find by the name alone. ` +
				`List it in one scope, or give one of the two a name of its own`,
		},
		{
			name: "one name twice in one scope",
			resources: []CollectionResource{
				{Name: "Widgets", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Read}},
				{Name: "Widgets", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Update}},
			},
			wantErr: `duplicate resource "Widgets" in scope "domain"`,
		},
		{
			name: "two names, one in each scope",
			resources: []CollectionResource{
				{Name: "Widgets", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Read}},
				{Name: "Gadgets", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Read}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g, err := NewGeneratedCollection(CollectionData{Resources: tt.resources})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("NewGeneratedCollection() error = %v", err)
				}
				for _, res := range tt.resources {
					if got := g.Scope(res.Name); got != res.Scope {
						t.Errorf("Scope(%s) = %q, want %q", res.Name, got, res.Scope)
					}
				}

				return
			}
			if err == nil || !strings.HasSuffix(err.Error(), tt.wantErr) {
				t.Errorf("NewGeneratedCollection() error = %v, want one ending in %q", err, tt.wantErr)
			}
		})
	}
}

// TestGeneratedCollection_noDotInAResourceName pins that a resource's name holds no dot,
// however the name is handed over: a dot separates a resource from one of its fields, so
// the name would be read as a field of another resource and would answer for it.
// Collection data is refused such an entry whether it registers permissions or none,
// and the builder refuses it at each registration. A name with two dots is refused the
// same, with no panic.
func TestGeneratedCollection_noDotInAResourceName(t *testing.T) {
	t.Parallel()

	refusal := func(name string) string {
		return fmt.Sprintf("resource %q: a resource's name holds no dot. "+
			"A dot separates a resource from one of its fields, so this name would be read as a field of another resource and would answer for it. "+
			"Give the resource a name without a dot; a field is listed with its resource", name)
	}
	read := []accesstypes.Permission{accesstypes.Read}
	tests := []struct {
		name     string
		resource accesstypes.Resource
		register func(accesstypes.Resource) error
	}{
		{name: "collection data, an entry with permissions", resource: "Widgets.name", register: func(name accesstypes.Resource) error {
			_, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{{Name: name, Scope: accesstypes.DomainPermissionScope, Permissions: read}}})

			return err
		}},
		{name: "collection data, an entry with no permission", resource: "Widgets.name", register: func(name accesstypes.Resource) error {
			_, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{{
				Name: name, Scope: accesstypes.DomainPermissionScope,
				Attributes: []AttributeData{{Name: "status", Column: "Status", Type: AttributeTypeString}},
			}}})

			return err
		}},
		{name: "collection data, a name with two dots", resource: "Widgets.name.first", register: func(name accesstypes.Resource) error {
			_, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{{Name: name, Scope: accesstypes.DomainPermissionScope, Permissions: read}}})

			return err
		}},
		{name: "the builder's AddResource", resource: "Widgets.name", register: func(name accesstypes.Resource) error {
			return NewCollectionBuilder().AddResource(accesstypes.DomainPermissionScope, accesstypes.Read, name)
		}},
		{name: "the builder's AddMethodResource", resource: "Widgets.name", register: func(name accesstypes.Resource) error {
			return NewCollectionBuilder().AddMethodResource(accesstypes.GlobalPermissionScope, accesstypes.Execute, name)
		}},
		{name: "the builder's AddResourceSet", resource: "Widgets.name", register: func(name accesstypes.Resource) error {
			return NewCollectionBuilder().AddResourceSet(accesstypes.DomainPermissionScope, name, SetData{Permissions: read})
		}},
		{name: "the builder's AddResource, a name with two dots", resource: "Widgets.name.first", register: func(name accesstypes.Resource) error {
			return NewCollectionBuilder().AddResource(accesstypes.DomainPermissionScope, accesstypes.Read, name)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.register(tt.resource)
			if want := refusal(string(tt.resource)); err == nil || !strings.HasSuffix(err.Error(), want) {
				t.Errorf("registering %q: error = %v, want one ending in %q", tt.resource, err, want)
			}
		})
	}
}

// TestGeneratedCollection_noDotInAFieldName pins that a field's name and a former name
// hold no dot, however they are handed over: a dot separates a resource from one of its
// fields, so the collection could not name such a field, and a former name with a dot
// would be read as a field of another resource. Collection data and the builder's
// AddResourceSet refuse each before anything is stored, so a tag a.b no longer reaches
// List, which could not name it.
func TestGeneratedCollection_noDotInAFieldName(t *testing.T) {
	t.Parallel()

	fieldName := func(tag, res string) string {
		return fmt.Sprintf("tag %q under resource %q: a field's name holds no dot. "+
			"A dot separates a resource from one of its fields, so the collection could not name this one. "+
			"Give the field a name without a dot", tag, res)
	}
	formerTag := func(tag, res, former string) string {
		return fmt.Sprintf("tag %q under resource %q names %q as its former name: a former name holds no dot. "+
			"Give the former name as the field was called, without a dot", tag, res, former)
	}
	read := []accesstypes.Permission{accesstypes.Read}
	widgets := func(tags ...TagData) CollectionData {
		return CollectionData{Resources: []CollectionResource{{Name: "Widgets", Scope: accesstypes.DomainPermissionScope, Permissions: read, Tags: tags}}}
	}
	tests := []struct {
		name     string
		register func() error
		want     string
	}{
		{name: "collection data, a tag's name", want: fieldName("name.first", "Widgets"), register: func() error {
			_, err := NewGeneratedCollection(widgets(TagData{Name: "name.first", Permissions: read}))

			return err
		}},
		{name: "collection data, a tag's former name", want: formerTag("title", "Widgets", "old.title"), register: func() error {
			_, err := NewGeneratedCollection(widgets(TagData{Name: "title", Permissions: read, Formerly: "old.title"}))

			return err
		}},
		{
			name: "collection data, a resource's former name",
			want: `resource "Reissue" names "Old.Reissue" as its former name: a former name holds no dot. ` +
				"A dot separates a resource from one of its fields, so the former name would be read as a field of another resource. " +
				"Give the former name as the method was called, without a dot",
			register: func() error {
				_, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{{
					Name: "Reissue", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Formerly: "Old.Reissue",
				}}})

				return err
			},
		},
		{name: "the builder's AddResourceSet, a tag's name", want: fieldName("name.first", "Widgets"), register: func() error {
			return NewCollectionBuilder().AddResourceSet(accesstypes.DomainPermissionScope, "Widgets", SetData{
				Permissions: read, TagPermissions: accesstypes.TagPermissions{"name.first": read},
			})
		}},
		{name: "the builder's AddResourceSet, a former tag name", want: formerTag("title", "Widgets", "old.title"), register: func() error {
			return NewCollectionBuilder().AddResourceSet(accesstypes.DomainPermissionScope, "Widgets", SetData{
				Permissions: read, TagPermissions: accesstypes.TagPermissions{"title": read}, FormerTags: map[accesstypes.Tag]accesstypes.Tag{"title": "old.title"},
			})
		}},
		{name: "collection data with a tag a.b is refused before List could read it", want: fieldName("a.b", "Widgets"), register: func() error {
			g, err := NewGeneratedCollection(widgets(TagData{Name: "a.b", Permissions: read}))
			if err == nil {
				g.List()
			}

			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tt.register(); err == nil || !strings.HasSuffix(err.Error(), tt.want) {
				t.Errorf("error = %v, want one ending in %q", err, tt.want)
			}
		})
	}
}

// TestGeneratedCollection_noEmptyOrStarName pins that a field has a name and is not
// named *, and that a resource has a name, however they are handed over: Resource.
// names no field, which a grant reads as the resource itself, and Resource.* is how a
// grant names all of a resource's fields. Collection data and the builder's
// AddResourceSet refuse the two field names, and each of the builder's registrations
// refuses the empty resource name, as collection data does.
func TestGeneratedCollection_noEmptyOrStarName(t *testing.T) {
	t.Parallel()

	noName := func(res string) string {
		return fmt.Sprintf("resource %q lists a field with no name. Give the field a name", res)
	}
	star := func(res string) string {
		return fmt.Sprintf("tag %q under resource %q: a field is not named *. "+
			"Resource.* is how a grant names all of a resource's fields, so the collection could not name this field alone. "+
			"Give the field another name", "*", res)
	}
	const emptyResource = "resource with empty name"
	read := []accesstypes.Permission{accesstypes.Read}
	widgets := func(tags ...TagData) CollectionData {
		return CollectionData{Resources: []CollectionResource{{Name: "Widgets", Scope: accesstypes.DomainPermissionScope, Permissions: read, Tags: tags}}}
	}
	tests := []struct {
		name     string
		register func() error
		want     string
	}{
		{name: "collection data, a tag with no name", want: noName("Widgets"), register: func() error {
			_, err := NewGeneratedCollection(widgets(TagData{Name: "", Permissions: read}))

			return err
		}},
		{name: "collection data, a tag named *", want: star("Widgets"), register: func() error {
			_, err := NewGeneratedCollection(widgets(TagData{Name: "*", Permissions: read}))

			return err
		}},
		{name: "the builder's AddResourceSet, a tag with no name", want: noName("Widgets"), register: func() error {
			return NewCollectionBuilder().AddResourceSet(accesstypes.DomainPermissionScope, "Widgets", SetData{
				Permissions: read, TagPermissions: accesstypes.TagPermissions{"": read},
			})
		}},
		{name: "the builder's AddResourceSet, a tag named *", want: star("Widgets"), register: func() error {
			return NewCollectionBuilder().AddResourceSet(accesstypes.DomainPermissionScope, "Widgets", SetData{
				Permissions: read, TagPermissions: accesstypes.TagPermissions{"*": read},
			})
		}},
		{name: "the builder's AddResourceSet, a resource with no name", want: emptyResource, register: func() error {
			return NewCollectionBuilder().AddResourceSet(accesstypes.DomainPermissionScope, "", SetData{Permissions: read})
		}},
		{name: "the builder's AddResource, a resource with no name", want: emptyResource, register: func() error {
			return NewCollectionBuilder().AddResource(accesstypes.DomainPermissionScope, accesstypes.Read, "")
		}},
		{name: "the builder's AddMethodResource, a resource with no name", want: emptyResource, register: func() error {
			return NewCollectionBuilder().AddMethodResource(accesstypes.GlobalPermissionScope, accesstypes.Execute, "")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tt.register(); err == nil || !strings.HasSuffix(err.Error(), tt.want) {
				t.Errorf("error = %v, want one ending in %q", err, tt.want)
			}
		})
	}
}

// TestNewGeneratedCollection_actionClassValidation pins what the collection refuses of
// an action's classes at start-up.
func TestNewGeneratedCollection_actionClassValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		res     CollectionResource
		wantErr string
	}{
		{
			name: "a class on an action",
			res:  CollectionResource{Name: "SetPassword", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Classes: []accesstypes.ActionClass{accesstypes.ClassCredential}},
		},
		{
			name:    "a class named as no class is",
			res:     CollectionResource{Name: "SetPassword", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Classes: []accesstypes.ActionClass{"Credential Actions"}},
			wantErr: `carries the class "Credential Actions", which is not a class's name`,
		},
		{
			name:    "a class carried twice",
			res:     CollectionResource{Name: "SetPassword", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Classes: []accesstypes.ActionClass{accesstypes.ClassCredential, accesstypes.ClassCredential}},
			wantErr: `carries the class "credential" twice`,
		},
		{
			name:    "a class on something no permission is checked on",
			res:     CollectionResource{Name: "SetPassword", Scope: accesstypes.GlobalPermissionScope, Classes: []accesstypes.ActionClass{accesstypes.ClassCredential}},
			wantErr: `resource "SetPassword" carries the classes [credential] and registers []: a class is carried by an action alone`,
		},
		{
			name:    "a class on a record's permission",
			res:     CollectionResource{Name: "Passwords", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Update}, Classes: []accesstypes.ActionClass{accesstypes.ClassCredential}},
			wantErr: `resource "Passwords" carries the classes [credential] and registers [Update]: a class is carried by an action alone, a name that registers Execute and nothing else. A record's writes are given a class by the generator alone, where the record decides what a person may do; any other record is refused to an impersonated session by its name (accesstypes.RefusePermission, accesstypes.RefuseWrites)`,
		},
		{
			name:    "a class on a name that is an action and a record at once",
			res:     CollectionResource{Name: "SetPassword", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute, accesstypes.Update}, Classes: []accesstypes.ActionClass{accesstypes.ClassCredential}},
			wantErr: `resource "SetPassword" carries the classes [credential] and registers [Execute Update]: a class is carried by an action alone`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{tt.res}})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("NewGeneratedCollection() error = %v", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("NewGeneratedCollection() error = %v, want one carrying %q", err, tt.wantErr)
			}
		})
	}
}
