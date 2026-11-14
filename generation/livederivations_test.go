package generation

import (
	"go/format"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// The generated record next to the resources: a package's entries as the types over the
// tables with their origin said, and the application's assembly of the packages' values.

func Test_liveDerivationsPackageTemplate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		data         *liveDerivationsPackageData
		wantContains []string
	}{
		{
			name: "a root and its tables read off the query, a rootless global resource declared, the resources package qualified",
			data: &liveDerivationsPackageData{Source: "pkg/resources", Package: "virtualresources", ResourcesQualifier: "resources.", Resources: []derivedResourceData{
				{Type: "MissionBoard", Comment: "MissionBoards: the tables its query reads.", Root: "Mission", Tables: []sourceTableData{{Type: "Client", Global: true}, {Type: "Squadron"}}},
				{Type: "Roster", Comment: "Rosters: the tables declared with @changedBy.", Global: true, Tables: []sourceTableData{{Type: "Pilot"}}},
			}},
			wantContains: []string{
				"package virtualresources",
				"var LiveDerivations = &live.Derivations{",
				"// MissionBoards: the tables its query reads.\n",
				"{Resource: MissionBoard{}.Resource(), Root: resources.Mission{}.Resource(), Tables: []live.SourceTable{",
				"{Name: resources.Client{}.Resource(), Global: true},",
				"{Name: resources.Squadron{}.Resource()},",
				"// Rosters: the tables declared with @changedBy.\n",
				"{Resource: Roster{}.Resource(), Global: true, Tables: []live.SourceTable{",
				"{Name: resources.Pilot{}.Resource()},",
			},
		},
		{
			name: "in the resources package itself the types are bare",
			data: &liveDerivationsPackageData{Source: "pkg/resources", Package: "resources", Resources: []derivedResourceData{
				{Type: "MissionBoard", Comment: "MissionBoards: the tables its query reads.", Root: "Mission", Tables: []sourceTableData{{Type: "Squadron"}}},
			}},
			wantContains: []string{
				"{Resource: MissionBoard{}.Resource(), Root: Mission{}.Resource(), Tables: []live.SourceTable{",
				"{Name: Squadron{}.Resource()},",
			},
		},
		{
			name: "a table of an auth is named as text beside the types, and the file says why",
			data: &liveDerivationsPackageData{Source: "pkg/resources", Package: "virtualresources", ResourcesQualifier: "resources.", Resources: []derivedResourceData{
				{
					Type:    "SectorCrew",
					Comment: "SectorCrews: the tables its query reads. CrewSessionUsers, CrewUserRoles are named as text: an auth's generated management declares the types over them inside its tree, where this package cannot import them.",
					Tables:  []sourceTableData{{Table: "CrewSessionUsers", Global: true}, {Table: "CrewUserRoles", Global: true}, {Type: "Sector", Global: true}},
				},
				{Type: "SeatLog", Comment: "SeatLogs: the tables declared with @changedBy.", Global: true, RootTable: "CrewUserRoles"},
			}},
			wantContains: []string{
				"// A table of an auth is the exception to the naming: the generated management declares\n",
				"{Resource: SectorCrew{}.Resource(), Tables: []live.SourceTable{",
				`{Name: "CrewSessionUsers", Global: true},`,
				`{Name: "CrewUserRoles", Global: true},`,
				"{Name: resources.Sector{}.Resource(), Global: true},",
				`{Resource: SeatLog{}.Resource(), Global: true, Root: "CrewUserRoles"},`,
			},
		},
		{
			name: "the views of an auth's generated management, recorded in the auth's handlers package: the declarations package qualifies the views and the auth's own tables, the resources package the application's",
			data: &liveDerivationsPackageData{Source: `Auth("example.com/acme/beacon/pkg/auth/crew")`, Package: "crewhandlers", ResourcesQualifier: "resources.", Auth: "Crew", TypesQualifier: "crewresources.", Resources: []derivedResourceData{
				{
					Type:    "CrewShipUser",
					Comment: "CrewShipUsers: the tables its query reads.",
					Tables:  []sourceTableData{{Type: "CrewSessionUser", Own: true, Global: true}, {Type: "CrewUserRole", Own: true, Global: true}, {Type: "Ship", Global: true}},
				},
				{Type: "CrewShipLog", Comment: "CrewShipLogs: the tables its query reads.", Root: "CrewUserRole", RootOwn: true},
			}},
			wantContains: []string{
				"package crewhandlers",
				"// LiveDerivations records, for each view the Crew auth's generated management\n",
				"{Resource: crewresources.CrewShipUser{}.Resource(), Tables: []live.SourceTable{",
				"{Name: crewresources.CrewSessionUser{}.Resource(), Global: true},",
				"{Name: crewresources.CrewUserRole{}.Resource(), Global: true},",
				"{Name: resources.Ship{}.Resource(), Global: true},",
				"{Resource: crewresources.CrewShipLog{}.Resource(), Root: crewresources.CrewUserRole{}.Resource()},",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out := renderFormatted(t, "liveDerivationsPackageTemplate", liveDerivationsPackageTemplate, tt.data)
			for _, want := range tt.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			// The file's comment is the package's own, or the auth's.
			if own := "for each published resource of this package"; strings.Contains(out, own) != (tt.data.Auth == "") {
				t.Errorf("output speaks of the package's own resources = %v, want %v:\n%s", strings.Contains(out, own), tt.data.Auth == "", out)
			}
			// The exception is said only in a file that carries it.
			if exception := "A table of an auth is the exception"; strings.Contains(out, exception) != tt.data.NamesTablesAsText() {
				t.Errorf("output says the naming exception = %v, want %v:\n%s", strings.Contains(out, exception), tt.data.NamesTablesAsText(), out)
			}
		})
	}
}

func Test_liveDerivationsTemplate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		data            *liveDerivationsAssemblyData
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:            "no package publishes: the empty value, still handed to every publish",
			data:            &liveDerivationsAssemblyData{Source: "pkg/resources", Package: "app"},
			wantContains:    []string{"package app", "var liveDerivations = live.Merge()"},
			wantNotContains: []string{"LiveDerivations,"},
		},
		{
			name: "two packages publish, one of them the handler package itself",
			data: &liveDerivationsAssemblyData{Source: "pkg/resources", Package: "app", Values: []string{"LiveDerivations", "computedresources.LiveDerivations", "virtualresources.LiveDerivations"}},
			wantContains: []string{
				"var liveDerivations = live.Merge(\n\tLiveDerivations,\n\tcomputedresources.LiveDerivations,\n\tvirtualresources.LiveDerivations,\n)",
			},
			wantNotContains: []string{"generated management"},
		},
		{
			name: "an auth's record among the values, which the comment names",
			data: &liveDerivationsAssemblyData{Source: "pkg/resources", Package: "app", Values: []string{"crewhandlers.LiveDerivations", "virtualresources.LiveDerivations"}, AuthViews: true},
			wantContains: []string{
				"and of every auth whose generated management declares views, generated in\n// the auth's handlers package,",
				"var liveDerivations = live.Merge(\n\tcrewhandlers.LiveDerivations,\n\tvirtualresources.LiveDerivations,\n)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out := renderFormatted(t, "liveDerivationsTemplate", liveDerivationsTemplate, tt.data)
			for _, want := range tt.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			for _, notWant := range tt.wantNotContains {
				if strings.Contains(out, notWant) {
					t.Errorf("output carries %q:\n%s", notWant, out)
				}
			}
		})
	}
}

// renderFormatted renders a Go file template and formats it as the writer does, so the
// expectations read the formatted text.
func renderFormatted(t *testing.T, templateName, fileTemplate string, data any) string {
	t.Helper()

	c := &client{}
	rendered, err := c.generateTemplateOutput(templateName, fileTemplate, data)
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	out, err := format.Source(rendered)
	if err != nil {
		t.Fatalf("format.Source() error = %v:\n%s", err, rendered)
	}

	return string(out)
}

// Test_liveDerivationsByPackage pins the grouping: a published virtual resource in the
// virtual package and a declared computed one in the computed package, each table as the
// type over it, a table no resource backs named in the comment alone.
func Test_liveDerivationsByPackage(t *testing.T) {
	t.Parallel()

	loaded, parsed := loadFixturePackage(t, "changedbyfixture")
	structs := fixtureStructs(parsed)
	c := changedByClient(loaded)
	c.resources = []*resourceInfo{fixtureTable(t, structs, "Berth", "DockID", "Slot"), fixtureTable(t, structs, "Dock", "ID")}
	roster := fixtureVirtualPublished(t, structs, "Roster")
	tally := fixtureComputedPublished(t, structs, "Tally")
	if err := c.resolvePublished([]*resourceInfo{roster}, []*computedResource{tally}); err != nil {
		t.Fatalf("resolvePublished() error = %v", err)
	}
	c.resources = append(c.resources, roster)
	c.computedResources = []*computedResource{tally}
	c.virtual, c.computed = "pkg/virtualresources", "pkg/computedresources"
	r := &resourceGenerator{client: c}

	got, err := r.liveDerivationsByPackage()
	if err != nil {
		t.Fatalf("liveDerivationsByPackage() error = %v", err)
	}
	want := []derivationsPackage{
		{dir: "pkg/computedresources", Resources: []derivedResourceData{
			{Type: "Tally", Comment: "Tallies: the tables declared with @changedBy. The set also names Visits, which no resource backs: no write to it is published, so it is left out.", Global: true, Tables: []sourceTableData{{Type: "Dock", Global: true}}},
		}},
		{dir: "pkg/virtualresources", Resources: []derivedResourceData{
			{Type: "Roster", Comment: "Rosters: the tables its query reads.", Global: true, Root: "Berth", Tables: []sourceTableData{{Type: "Dock", Global: true}}},
		}},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(derivationsPackage{})); diff != "" {
		t.Errorf("liveDerivationsByPackage() mismatch (-want +got):\n%s", diff)
	}
}

// Test_liveDerivationsByPackage_authTables pins the entry of a view that reads tables an
// auth's generated management declares: the declarations are internal to the auth's
// tree, so the view's package cannot name their types, and the entry names each such
// table as text, the @rowsOf root included, beside the application's own by type.
func Test_liveDerivationsByPackage_authTables(t *testing.T) {
	t.Parallel()

	loaded, parsed := loadFixturePackage(t, "changedbyfixture")
	structs := fixtureStructs(parsed)
	crew := &identityInstance{auth: "Crew", resources: "pkg/identity/internal/crewresources"}

	tests := []struct {
		name       string
		authTables []string
		want       derivedResourceData
	}{
		{
			name:       "a table the query reads",
			authTables: []string{"Dock"},
			want: derivedResourceData{
				Type:    "Roster",
				Comment: "Rosters: the tables its query reads. Docks is named as text: an auth's generated management declares the type over it inside its tree, where this package cannot import it.",
				Global:  true,
				Root:    "Berth",
				Tables:  []sourceTableData{{Table: "Docks", Global: true}},
			},
		},
		{
			name:       "the root and a table",
			authTables: []string{"Berth", "Dock"},
			want: derivedResourceData{
				Type:      "Roster",
				Comment:   "Rosters: the tables its query reads. Berths, Docks are named as text: an auth's generated management declares the types over them inside its tree, where this package cannot import them.",
				Global:    true,
				RootTable: "Berths",
				Tables:    []sourceTableData{{Table: "Docks", Global: true}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := changedByClient(loaded)
			c.resources = []*resourceInfo{fixtureTable(t, structs, "Berth", "DockID", "Slot"), fixtureTable(t, structs, "Dock", "ID")}
			for _, res := range c.resources {
				if slices.Contains(tt.authTables, res.Name()) {
					res.identity = crew
				}
			}
			roster := fixtureVirtualPublished(t, structs, "Roster")
			if err := c.resolvePublished([]*resourceInfo{roster}, nil); err != nil {
				t.Fatalf("resolvePublished() error = %v", err)
			}
			c.resources = append(c.resources, roster)
			c.virtual = "pkg/virtualresources"
			r := &resourceGenerator{client: c}

			got, err := r.liveDerivationsByPackage()
			if err != nil {
				t.Fatalf("liveDerivationsByPackage() error = %v", err)
			}
			want := []derivationsPackage{{dir: "pkg/virtualresources", Resources: []derivedResourceData{tt.want}}}
			if diff := cmp.Diff(want, got, cmp.AllowUnexported(derivationsPackage{})); diff != "" {
				t.Errorf("liveDerivationsByPackage() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_liveDerivationsByPackage_identityViews pins the entry of a view an auth's
// generated management declares itself: its record is the auth's handlers package's, the
// auth's own tables are named by type and marked as declared beside the view, the
// application's by type, and nothing as text; a view there that reads another auth's
// table is refused as a fault of the generator.
func Test_liveDerivationsByPackage_identityViews(t *testing.T) {
	t.Parallel()

	loaded, parsed := loadFixturePackage(t, "changedbyfixture")
	structs := fixtureStructs(parsed)
	crew := &identityInstance{auth: "Crew", importPath: crewAuthPath, resources: "pkg/identity/internal/crewresources", handlers: "pkg/identity/crewhandlers"}
	members := &identityInstance{auth: "Members", importPath: membersAuthPath, resources: "pkg/identity/internal/membersresources", handlers: "pkg/identity/membershandlers"}

	tests := []struct {
		name    string
		owners  map[string]*identityInstance
		want    derivedResourceData
		wantErr string
	}{
		{
			name:   "the root is the auth's own, the other table the application's",
			owners: map[string]*identityInstance{"Berth": crew},
			want:   derivedResourceData{Type: "Roster", Comment: "Rosters: the tables its query reads.", Global: true, Root: "Berth", RootOwn: true, Tables: []sourceTableData{{Type: "Dock", Global: true}}},
		},
		{
			name:   "both are the auth's own",
			owners: map[string]*identityInstance{"Berth": crew, "Dock": crew},
			want:   derivedResourceData{Type: "Roster", Comment: "Rosters: the tables its query reads.", Global: true, Root: "Berth", RootOwn: true, Tables: []sourceTableData{{Type: "Dock", Own: true, Global: true}}},
		},
		{
			name:    "a table of another auth",
			owners:  map[string]*identityInstance{"Berth": crew, "Dock": members},
			wantErr: `struct Roster: a view of the generated management of Auth("example.com/acme/beacon/pkg/auth/crew") reads Docks, a table of Auth("example.com/acme/beacon/pkg/auth/members")`,
		},
		{
			name:    "a root of another auth",
			owners:  map[string]*identityInstance{"Berth": members},
			wantErr: `reads Berths, a table of Auth("example.com/acme/beacon/pkg/auth/members")`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := changedByClient(loaded)
			c.resources = []*resourceInfo{fixtureTable(t, structs, "Berth", "DockID", "Slot"), fixtureTable(t, structs, "Dock", "ID")}
			for _, res := range c.resources {
				res.identity = tt.owners[res.Name()]
			}
			roster := fixtureVirtualPublished(t, structs, "Roster")
			if err := c.resolvePublished([]*resourceInfo{roster}, nil); err != nil {
				t.Fatalf("resolvePublished() error = %v", err)
			}
			roster.identity = crew
			c.resources = append(c.resources, roster)
			c.virtual = "pkg/virtualresources"
			r := &resourceGenerator{client: c}

			got, err := r.liveDerivationsByPackage()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), "fault of the generator") {
					t.Fatalf("liveDerivationsByPackage() error = %v, want one containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("liveDerivationsByPackage() error = %v", err)
			}
			want := []derivationsPackage{{dir: "pkg/identity/crewhandlers", instance: crew, Resources: []derivedResourceData{tt.want}}}
			if diff := cmp.Diff(want, got, cmp.AllowUnexported(derivationsPackage{}, identityInstance{})); diff != "" {
				t.Errorf("liveDerivationsByPackage() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_derivationComment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		resource string
		declared bool
		unbacked []string
		byName   []string
		want     string
	}{
		{name: "read off the query", resource: "MissionBoards", want: "MissionBoards: the tables its query reads."},
		{name: "declared", resource: "SectorHazardBoards", declared: true, want: "SectorHazardBoards: the tables declared with @changedBy."},
		{name: "one table no resource backs", resource: "Rosters", unbacked: []string{"AuditTrail"}, want: "Rosters: the tables its query reads. The set also names AuditTrail, which no resource backs: no write to it is published, so it is left out."},
		{name: "two tables no resource backs", resource: "Rosters", declared: true, unbacked: []string{"AuditTrail", "Ledger"}, want: "Rosters: the tables declared with @changedBy. The set also names AuditTrail, Ledger, which no resource backs: no write to them is published, so they are left out."},
		{name: "one table of an auth", resource: "SectorCrews", byName: []string{"CrewUserRoles"}, want: "SectorCrews: the tables its query reads. CrewUserRoles is named as text: an auth's generated management declares the type over it inside its tree, where this package cannot import it."},
		{
			name: "a table no resource backs and two of an auth", resource: "SectorCrews", declared: true, unbacked: []string{"CrewSessionSignIns"}, byName: []string{"CrewSessionUsers", "CrewUserRoles"},
			want: "SectorCrews: the tables declared with @changedBy. The set also names CrewSessionSignIns, which no resource backs: no write to it is published, so it is left out. CrewSessionUsers, CrewUserRoles are named as text: an auth's generated management declares the types over them inside its tree, where this package cannot import them.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, derivationComment(tt.resource, tt.declared, tt.unbacked, tt.byName)); diff != "" {
				t.Errorf("derivationComment() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_handlerHome_LiveDerivations pins what a handler hands to every publish, by where
// it is written: the application's handlers name the assembly in their own package, and
// an auth's name the field that keeps the value the application constructed them with,
// so a commit of the auth's management reaches the application's derived resources.
func Test_handlerHome_LiveDerivations(t *testing.T) {
	t.Parallel()

	r := &resourceGenerator{client: &client{}}
	r.handler, r.applicationName, r.receiverName = "app", "App", "a"
	crew := &identityInstance{auth: "Crew", resources: "pkg/identity/internal/crewresources", methods: "pkg/identity/crewidentity", handlers: "pkg/identity/crewhandlers"}

	tests := []struct {
		name string
		home *handlerHome
		want string
	}{
		{name: "the application's handler package", home: r.homeOf(nil), want: "liveDerivations"},
		{name: "no home is the application's", home: nil, want: "liveDerivations"},
		{name: "an auth's handlers", home: r.homeOf(crew), want: "h.liveDerivations"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.home.LiveDerivations(); got != tt.want {
				t.Errorf("LiveDerivations() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Test_homeOf_views pins the package a home's handlers name a view in: the application's
// virtual package for its own, and an auth's declarations package, which holds the
// auth's views beside its tables.
func Test_homeOf_views(t *testing.T) {
	t.Parallel()

	r := &resourceGenerator{client: &client{}}
	r.handler, r.virtual = "app", "pkg/virtualresources"
	crew := &identityInstance{auth: "Crew", resources: "pkg/identity/internal/crewresources", methods: "pkg/identity/crewidentity", handlers: "pkg/identity/crewhandlers"}

	if got := r.homeOf(nil).views; got != "pkg/virtualresources" {
		t.Errorf("the application's views = %q, want its virtual package", got)
	}
	if got := r.homeOf(crew).views; got != "pkg/identity/internal/crewresources" {
		t.Errorf("an auth's views = %q, want its declarations package", got)
	}
}

// Test_identityDelegatesTemplate_liveDerivations pins the hand-over: every delegating
// method constructs the auth's handlers with the application's live derivations.
func Test_identityDelegatesTemplate_liveDerivations(t *testing.T) {
	t.Parallel()

	out := renderFormatted(t, "identityDelegatesTemplate", identityDelegatesTemplate, &identityDelegatesData{
		Source:          "pkg/identity",
		Package:         "app",
		ApplicationName: "App",
		ReceiverName:    "a",
		Instances: []*identityDelegates{{
			Auth: "Crew", AuthPackage: "crew", MethodsPackage: "crewidentity", HandlersPackage: "crewhandlers",
			Handlers: []identityDelegate{{Name: "AssignCrewRole"}, {Name: "CrewSessionUsers"}},
		}},
	})
	for _, want := range []string{
		"var _ crewhandlers.Deps = (*App)(nil)",
		"func (a *App) AssignCrewRole() http.HandlerFunc {\n\treturn crewhandlers.New(a, liveDerivations).AssignCrewRole()\n}",
		"func (a *App) CrewSessionUsers() http.HandlerFunc {\n\treturn crewhandlers.New(a, liveDerivations).CrewSessionUsers()\n}",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func Test_packageQualifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		from, to packageDir
		want     string
	}{
		{name: "another package is qualified by its name", from: "app", to: "pkg/virtualresources", want: "virtualresources."},
		{name: "the same package, spelled differently, is bare", from: "pkg/resources/", to: "pkg/resources", want: ""},
		{name: "a package named by a file in it", from: "app", to: "pkg/resources/resources.go", want: "resources."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := packageQualifier(tt.from, tt.to); got != tt.want {
				t.Errorf("packageQualifier() = %q, want %q", got, tt.want)
			}
		})
	}
}
