package resource

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestGeneratedCollection_bindings pins the binding carriage: CollectionData
// round-trips through NewGeneratedCollection and Data() in canonical form
// (vocabulary sorted by name, hop order preserved), a binding-only resource
// survives with no permissions, and Bindings answers per resource.
func TestGeneratedCollection_bindings(t *testing.T) {
	t.Parallel()

	data := CollectionData{Resources: []CollectionResource{
		{
			Name:        "MaintenanceTasks",
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.Read},
			Attributes: []AttributeData{
				{Name: "crew", Column: "CrewId", Type: AttributeTypeString},
				{Name: "sector", Column: "BerthId", Type: AttributeTypeString, Path: []BindingHop{
					{Table: "Berths", JoinColumn: "Id", Column: "StationId"},
					{Table: "Stations", JoinColumn: "Id", Column: "Sector"},
				}},
			},
			Domain: &DomainBindingData{Column: "StationId"},
		},
		{
			// Binding-only: the vocabulary describes the data model, so a
			// resource with no permission registrations still carries it.
			Name:        "UserProfiles",
			Scope:       accesstypes.GlobalPermissionScope,
			SubjectSets: []SubjectBindingData{{Name: "crews", UserColumn: "UserId", Column: "CrewId", Type: AttributeTypeString}},
			SubjectValues: []SubjectBindingData{
				{Name: "approvalLimit", UserColumn: "UserId", Column: "Limit", Type: AttributeTypeNumber},
			},
		},
	}}

	g, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	bindings, ok := g.Bindings(accesstypes.DomainPermissionScope, "MaintenanceTasks")
	if !ok {
		t.Fatal("Bindings(MaintenanceTasks) ok = false, want true")
	}
	if len(bindings.Attributes) != 2 || bindings.Domain == nil {
		t.Errorf("Bindings(MaintenanceTasks) = %+v, want two attributes and the domain binding", bindings)
	}
	if _, ok := g.Bindings(accesstypes.DomainPermissionScope, "Berths"); ok {
		t.Error("Bindings(Berths) ok = true for a resource declaring none")
	}

	roundTripped, err := NewGeneratedCollection(g.Data())
	if err != nil {
		t.Fatalf("NewGeneratedCollection(round trip) error = %v", err)
	}
	if diff := cmp.Diff(g.Data(), roundTripped.Data()); diff != "" {
		t.Errorf("Data() round trip mismatch (-first +second):\n%s", diff)
	}

	profile, ok := roundTripped.Bindings(accesstypes.GlobalPermissionScope, "UserProfiles")
	if !ok {
		t.Fatal("Bindings(UserProfiles) ok = false after round trip, want true")
	}
	if len(profile.SubjectSets) != 1 || len(profile.SubjectValues) != 1 {
		t.Errorf("Bindings(UserProfiles) = %+v, want the subject vocabulary intact", profile)
	}
}

// TestGeneratedCollection_subjectComparisonTypes pins the typed subject
// accessors deploy validation reads: the comparison type of the column a set
// or value yields, and ok false for a name the collection does not declare in
// that form.
func TestGeneratedCollection_subjectComparisonTypes(t *testing.T) {
	t.Parallel()

	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{{
		Name:        "UserProfiles",
		Scope:       accesstypes.GlobalPermissionScope,
		SubjectSets: []SubjectBindingData{{Name: "crews", UserColumn: "UserId", Column: "CrewId", Type: AttributeTypeString}},
		SubjectValues: []SubjectBindingData{
			{Name: "approvalLimit", UserColumn: "UserId", Column: "Limit", Type: AttributeTypeNumber},
			{Name: "homeSector", UserColumn: "UserId", Column: "StationId", Type: AttributeTypeString, Path: []BindingHop{{Table: "Stations", JoinColumn: "Id", Column: "Sector"}}},
		},
	}}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	tests := []struct {
		name     string
		lookup   func(string) (accesstypes.AttributeType, bool)
		arg      string
		wantType accesstypes.AttributeType
		wantOK   bool
	}{
		{name: "set carries its column's type", lookup: g.SubjectSetComparisonType, arg: "crews", wantType: AttributeTypeString, wantOK: true},
		{name: "value carries its column's type", lookup: g.SubjectValueComparisonType, arg: "approvalLimit", wantType: AttributeTypeNumber, wantOK: true},
		{name: "dotted value carries its terminal's type", lookup: g.SubjectValueComparisonType, arg: "homeSector", wantType: AttributeTypeString, wantOK: true},
		{name: "undeclared set", lookup: g.SubjectSetComparisonType, arg: "ghosts"},
		{name: "a value is not a set", lookup: g.SubjectSetComparisonType, arg: "approvalLimit"},
		{name: "a set is not a value", lookup: g.SubjectValueComparisonType, arg: "crews"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			typ, ok := tt.lookup(tt.arg)
			if typ != tt.wantType || ok != tt.wantOK {
				t.Errorf("lookup(%q) = (%q, %v), want (%q, %v)", tt.arg, typ, ok, tt.wantType, tt.wantOK)
			}
		})
	}
}

// TestNewGeneratedCollection_bindingValidation pins the runtime vocabulary
// rules: names unique within one resource, subject names unique across the
// whole collection, every attribute and subject entry typed from the
// vocabulary, and a subject entry's own way to a row's tenant carried only by
// a subject set of a resource that is no tenant's.
func TestNewGeneratedCollection_bindingValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		data        CollectionData
		wantContain string
	}{
		{
			name: "duplicate name within one resource",
			data: CollectionData{Resources: []CollectionResource{{
				Name:  "Widgets",
				Scope: accesstypes.GlobalPermissionScope,
				Attributes: []AttributeData{
					{Name: "crew", Column: "CrewId", Type: AttributeTypeString},
					{Name: "crew", Column: "TeamId", Type: AttributeTypeString},
				},
			}}},
			wantContain: `binding name "crew" twice`,
		},
		{
			name: "attribute and subject name colliding on one resource",
			data: CollectionData{Resources: []CollectionResource{{
				Name:        "Widgets",
				Scope:       accesstypes.GlobalPermissionScope,
				Attributes:  []AttributeData{{Name: "crew", Column: "CrewId", Type: AttributeTypeString}},
				SubjectSets: []SubjectBindingData{{Name: "crew", UserColumn: "UserId", Column: "CrewId", Type: AttributeTypeString}},
			}}},
			wantContain: `binding name "crew" twice`,
		},
		{
			name: "subject name is one application-wide namespace",
			data: CollectionData{Resources: []CollectionResource{
				{
					Name:        "CrewMembers",
					Scope:       accesstypes.GlobalPermissionScope,
					SubjectSets: []SubjectBindingData{{Name: "crews", UserColumn: "UserId", Column: "CrewId", Type: AttributeTypeString}},
				},
				{
					Name:        "TeamMembers",
					Scope:       accesstypes.DomainPermissionScope,
					SubjectSets: []SubjectBindingData{{Name: "crews", UserColumn: "UserId", Column: "TeamId", Type: AttributeTypeString}},
				},
			}},
			wantContain: "application-wide namespace",
		},
		{
			name: "attribute with a type outside the vocabulary",
			data: CollectionData{Resources: []CollectionResource{{
				Name:       "Widgets",
				Scope:      accesstypes.GlobalPermissionScope,
				Attributes: []AttributeData{{Name: "crew", Column: "CrewId", Type: "uuid"}},
			}}},
			wantContain: `attribute "crew" carries comparison type "uuid"`,
		},
		{
			name: "subject set without a type",
			data: CollectionData{Resources: []CollectionResource{{
				Name:        "CrewMembers",
				Scope:       accesstypes.GlobalPermissionScope,
				SubjectSets: []SubjectBindingData{{Name: "crews", UserColumn: "UserId", Column: "CrewId"}},
			}}},
			wantContain: `subject binding "crews" carries comparison type ""`,
		},
		{
			name: "subject value with a type outside the vocabulary",
			data: CollectionData{Resources: []CollectionResource{{
				Name:          "UserProfiles",
				Scope:         accesstypes.GlobalPermissionScope,
				SubjectValues: []SubjectBindingData{{Name: "approvalLimit", UserColumn: "UserId", Column: "Limit", Type: "bytes"}},
			}}},
			wantContain: `subject binding "approvalLimit" carries comparison type "bytes"`,
		},
		{
			name: "a subject set that names its own way to a tenant on a tenant's resource",
			data: CollectionData{Resources: []CollectionResource{{
				Name:  "CrewMembers",
				Scope: accesstypes.DomainPermissionScope,
				SubjectSets: []SubjectBindingData{{
					Name: "crews", UserColumn: "UserId", Column: "CrewId", Type: AttributeTypeString,
					Domain: &DomainBindingData{Column: "Region"},
				}},
				Domain: &DomainBindingData{Column: "StationId"},
			}}},
			wantContain: `resource "CrewMembers" subject set "crews" writes a domain:, which says where a row's tenant is found on a resource that is no tenant's; "CrewMembers" is a tenant's resource`,
		},
		{
			name: "a subject set that names its own way to a tenant beside a domain binding the resource carries",
			data: CollectionData{Resources: []CollectionResource{{
				Name:  "CrewMembers",
				Scope: accesstypes.GlobalPermissionScope,
				SubjectSets: []SubjectBindingData{{
					Name: "crews", UserColumn: "UserId", Column: "CrewId", Type: AttributeTypeString,
					Domain: &DomainBindingData{Column: "CrewId", Path: []BindingHop{{Table: "Crews", JoinColumn: "Id", Column: "Region"}}},
				}},
				Domain: &DomainBindingData{Column: "StationId"},
			}}},
			wantContain: `its own domain binding keeps the set to the request's tenant — remove the set's domain`,
		},
		{
			name: "a subject value that names a way to a tenant",
			data: CollectionData{Resources: []CollectionResource{{
				Name:  "UserProfiles",
				Scope: accesstypes.GlobalPermissionScope,
				SubjectValues: []SubjectBindingData{{
					Name: "approvalLimit", UserColumn: "UserId", Column: "Limit", Type: AttributeTypeNumber,
					Domain: &DomainBindingData{Column: "StationId"},
				}},
			}}},
			wantContain: `resource "UserProfiles" subject value "approvalLimit" writes a domain:, which is a part of a subject set alone`,
		},
		{
			name: "empty binding name",
			data: CollectionData{Resources: []CollectionResource{{
				Name:       "Widgets",
				Scope:      accesstypes.GlobalPermissionScope,
				Attributes: []AttributeData{{Column: "CrewId"}},
			}}},
			wantContain: "empty name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewGeneratedCollection(tt.data)
			if err == nil {
				t.Fatalf("NewGeneratedCollection() expected an error containing %q, got nil", tt.wantContain)
			}
			if !strings.Contains(err.Error(), tt.wantContain) {
				t.Errorf("NewGeneratedCollection() error = %q, want containing %q", err, tt.wantContain)
			}
		})
	}
}

// TestGeneratedCollection_Auths pins that the collection answers with the auths its
// generation run knew, in name order whatever order the data lists them in, and with
// none for a run that declared no auth; the data read back from the collection carries
// the same list.
func TestGeneratedCollection_Auths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		auths   []string
		want    []string
		wantErr string
	}{
		{name: "a run that declared no auth"},
		{name: "one auth", auths: []string{"staff"}, want: []string{"staff"}},
		{name: "several auths, listed out of order", auths: []string{"members", "crew"}, want: []string{"crew", "members"}},
		{name: "an auth listed twice", auths: []string{"crew", "crew"}, wantErr: `auth "crew" is listed twice`},
		{name: "an empty name", auths: []string{"crew", ""}, wantErr: "an auth with an empty name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g, err := NewGeneratedCollection(CollectionData{Auths: tt.auths})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NewGeneratedCollection() error = %v, want it to contain %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("NewGeneratedCollection() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, g.Auths(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Auths() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.want, collectionDataFrom(g).Auths, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("the data read back: Auths mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestGeneratedCollection_SubjectSets pins what the role-file check of one auth is told
// of the application's subject sets: each set the application declares for that auth's
// people or for no auth, with the sets its who: part builds on, no set that describes
// another auth's people, and no set the generator declares for an auth.
func TestGeneratedCollection_SubjectSets(t *testing.T) {
	t.Parallel()

	data := CollectionData{Resources: []CollectionResource{
		{
			Name:  "CrewMembers",
			Scope: accesstypes.GlobalPermissionScope,
			SubjectSets: []SubjectBindingData{
				{Name: "crews", UserColumn: "UserId", Column: "CrewId", Type: AttributeTypeString, Auth: "crew"},
				{Name: "crewGroups", UserColumn: "UserId", Column: "GroupId", Type: AttributeTypeString, Offered: true, Auth: "crew"},
			},
		},
		{
			Name:  "DockPasses",
			Scope: accesstypes.GlobalPermissionScope,
			SubjectSets: []SubjectBindingData{{
				Name: "docks", UserColumn: "UserId", Column: "DockId", Type: AttributeTypeString, Auth: "crew",
				Who: []SubjectWhoData{{Column: "UserId"}, {Column: "CrewId", Set: "crews"}, {Column: "GroupId", Set: "crewGroups"}},
			}},
		},
		{
			Name:  "AccountHolders",
			Scope: accesstypes.GlobalPermissionScope,
			SubjectSets: []SubjectBindingData{
				{Name: "accounts", UserColumn: "UserId", Column: "AccountId", Type: AttributeTypeString, Auth: "members"},
			},
		},
		{
			Name:  "Watches",
			Scope: accesstypes.GlobalPermissionScope,
			SubjectSets: []SubjectBindingData{
				{Name: "watches", UserColumn: "UserId", Column: "WatchId", Type: AttributeTypeString},
			},
		},
	}}
	g, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	tests := []struct {
		name string
		auth string
		want map[string][]string
	}{
		{
			name: "an auth is listed its own sets and the sets that name no auth",
			auth: "crew",
			want: map[string][]string{"crews": nil, "docks": {"crews", "crewGroups"}, "watches": nil},
		},
		{
			name: "another auth is listed its own, and none of the first's",
			auth: "members",
			want: map[string][]string{"accounts": nil, "watches": nil},
		},
		{
			name: "an auth no set names is listed the sets that name no auth",
			auth: "partners",
			want: map[string][]string{"watches": nil},
		},
		{
			name: "an empty auth is listed every set",
			want: map[string][]string{"accounts": nil, "crews": nil, "docks": {"crews", "crewGroups"}, "watches": nil},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, g.SubjectSets(tt.auth), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("SubjectSets(%q) mismatch (-want +got):\n%s", tt.auth, diff)
			}
		})
	}

	// Whose people a set describes is answered for every set, the generator's among
	// them, which the listing leaves out.
	wantAuths := map[string]string{"crews": "crew", "docks": "crew", "crewGroups": "crew", "accounts": "members", "watches": "", "berths": ""}
	for name, want := range wantAuths {
		if got := g.SubjectSetAuth(name); got != want {
			t.Errorf("SubjectSetAuth(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestGeneratedCollection_SubjectSetDefinition pins the parts a union of collections
// compares a subject set by: where the set is declared, the column that holds its
// person, the value with each hop it takes, its type, its who: and where: parts, and
// the way its rows reach a tenant, the anchor's own or the set's, each part said the
// same way whichever collection answers. The auth the set names is in no part. A name
// no set has is answered nil.
func TestGeneratedCollection_SubjectSetDefinition(t *testing.T) {
	t.Parallel()

	g, err := NewGeneratedCollection(CollectionData{Auths: []string{"crew"}, Resources: []CollectionResource{
		{
			Name:  "HangarPasses",
			Scope: accesstypes.GlobalPermissionScope,
			SubjectSets: []SubjectBindingData{{
				Name: "hangarsRun", UserColumn: "UserId", Column: "BayId", Type: AttributeTypeString, Auth: "crew",
				Path: []BindingHop{{Table: "Bays", JoinColumn: "Id", Column: "DeckId"}, {Table: "Decks", JoinColumn: "Id", Column: "HangarId"}},
				Who:  []SubjectWhoData{{Column: "UserId"}, {Column: "SquadronId", Set: "squadrons"}},
				Where: &SubjectWhereData{
					Condition: "LevelID = 'run' AND (ExpiresAt IS NULL OR ExpiresAt > now)",
					Columns: []SubjectColumnData{
						{Name: "LevelID", Column: "LevelId", Type: AttributeTypeString},
						{Name: "ExpiresAt", Column: "ExpiresAt", Type: AttributeTypeTimestamp},
					},
				},
				Domain: &DomainBindingData{Column: "GroupId", Path: []BindingHop{{Table: "Groups", JoinColumn: "Id", Column: "DomainId"}}},
			}},
		},
		{
			Name:        "Watches",
			Scope:       accesstypes.GlobalPermissionScope,
			SubjectSets: []SubjectBindingData{{Name: "watches", UserColumn: "UserId", Column: "WatchId", Type: AttributeTypeString}},
		},
		{
			Name:        "SquadronMemberships",
			Scope:       accesstypes.DomainPermissionScope,
			Domain:      &DomainBindingData{Column: "SectorId"},
			SubjectSets: []SubjectBindingData{{Name: "squadrons", UserColumn: "UserId", Column: "SquadronId", Type: AttributeTypeString}},
		},
	}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	tests := []struct {
		name string
		set  string
		want []string
	}{
		{
			name: "a set with a path, both kinds of who: test, a where: with its columns and a way of its own to a tenant",
			set:  "hangarsRun",
			want: []string{
				"declared on HangarPasses (global)",
				"person: UserId",
				"value: BayId > Bays.DeckId by Id > Decks.HangarId by Id",
				"type: string",
				"who: UserId, SquadronId IN subject.squadrons",
				"where: LevelID = 'run' AND (ExpiresAt IS NULL OR ExpiresAt > now) [LevelID=LevelId (string), ExpiresAt=ExpiresAt (timestamp)]",
				"tenant: GroupId > Groups.DomainId by Id",
			},
		},
		{
			name: "a set that writes neither part, on a table of no tenant",
			set:  "watches",
			want: []string{
				"declared on Watches (global)",
				"person: UserId",
				"value: WatchId",
				"type: string",
				"who: (none)",
				"where: (none)",
				"tenant: (none)",
			},
		},
		{
			name: "a set on a tenant's table, whose rows reach their tenant by the table's own way",
			set:  "squadrons",
			want: []string{
				"declared on SquadronMemberships (domain)",
				"person: UserId",
				"value: SquadronId",
				"type: string",
				"who: (none)",
				"where: (none)",
				"tenant: SectorId",
			},
		},
		{
			name: "a name no set has",
			set:  "ghosts",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, g.SubjectSetDefinition(tt.set)); diff != "" {
				t.Errorf("SubjectSetDefinition(%q) mismatch (-want +got):\n%s", tt.set, diff)
			}
		})
	}
}
