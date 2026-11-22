package generation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
)

// TestResolveSubjectSetAuths pins what a run holds a set to on whose people it
// describes, driven through structsToResources and the run's resolution as a generation
// run drives them. An application with one auth may leave the part out, and every set is
// that auth's. An application with several says it on every set it writes, since nothing
// else tells the generator which auth's people a user column holds, and a name that is
// no auth of the application is refused. The set of a person's groups, which the
// generator declares for an auth, names that auth as the generated declaration writes
// it, so a set of another auth's people that follows a person through it is refused as
// one that follows a person through any other set of that auth's.
func TestResolveSubjectSetAuths(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	staff := []*identityInstance{{auth: "Staff", policy: access.Tables{Groups: "StaffGroups"}, groupSet: "staffGroups"}}

	tests := []struct {
		name       string
		structs    []string
		identity   []*identityInstance
		auths      []string
		want       map[string]string
		wantRunErr []string
		wantErr    []string
		// notInErr are words the refusal must not say: a label it would pick for the
		// author.
		notInErr []string
	}{
		{
			name:    "one auth and nothing written: the set names no auth",
			structs: []string{"TeamMember"},
			auths:   []string{"staff"},
			want:    map[string]string{"teams": ""},
		},
		{
			name:    "one auth, and the set names it",
			structs: []string{"BayKeeper"},
			auths:   []string{"staff"},
			want:    map[string]string{"bays": "staff"},
		},
		{
			name:    "several auths, and each set says whose people it describes",
			structs: []string{"BayKeeper", "LoungeGuest"},
			auths:   []string{"patrons", "staff"},
			want:    map[string]string{"bays": "staff", "lounges": "patrons"},
		},
		{
			name:    "several auths, and a set that says none",
			structs: []string{"TeamMember", "BayKeeper"},
			auths:   []string{"patrons", "staff"},
			wantErr: []string{
				"struct TeamMember field UserID: @subjectSet(teams): the application has several auths (patrons, staff), and a set describes the people of one",
				"say whose people UserID holds by adding auth: and that auth's name to the annotation",
			},
			// The refusal lists the auths and picks none: a label pasted from the message
			// onto a set of the other auth's people would pass generation.
			notInErr: []string{"auth: patrons", "auth: staff"},
		},
		{
			name:    "a name that is no auth of the application",
			structs: []string{"BayKeeper", "LoungeGuest"},
			auths:   []string{"crew", "patrons"},
			wantErr: []string{"struct BayKeeper field UserID: @subjectSet(bays): auth: names staff, which is no auth of the application; its auths are: crew, patrons"},
		},
		{
			name:    "a name that is no auth of an application with one",
			structs: []string{"BayKeeper"},
			auths:   []string{"crew"},
			wantErr: []string{"struct BayKeeper field UserID: @subjectSet(bays): auth: names staff, which is no auth of the application; its auths are: crew"},
		},
		{
			name:    "a run that is told of no auth, and a set that names one",
			structs: []string{"BayKeeper", "TeamMember"},
			wantErr: []string{"struct BayKeeper field UserID: @subjectSet(bays): auth: names staff, and this run knows no auth to hold the name to: name the auth on the outlet of the generate program that serves its people, or take auth: off the set"},
		},
		{
			name:    "a run that is told of no auth, and a set that names none",
			structs: []string{"TeamMember"},
			want:    map[string]string{"teams": ""},
		},
		{
			name:     "several auths, and the set of a person's groups an auth declares names that auth",
			structs:  []string{"StaffGroup", "StaffGroupMember", "BayKeeper", "BayGroup"},
			identity: staff,
			auths:    []string{"patrons", "staff"},
			want:     map[string]string{"bays": "staff", "baysOpen": "staff", "staffGroups": "staff"},
		},
		{
			name:     "several auths, and a set of one's people that follows a person through the groups of another's",
			structs:  []string{"StaffGroup", "StaffGroupMember", "WhoOtherAuthGroups"},
			identity: staff,
			auths:    []string{"patrons", "staff"},
			wantRunErr: []string{
				"struct WhoOtherAuthGroups field UserID: @subjectSet(viaStaffGroups): who: tests GroupID against subject.staffGroups, which describes the people of staff (declared on StaffGroupMember, auth: staff), and viaStaffGroups describes the people of patrons (auth: patrons)",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := subjectSetFixtureClient(t, structs)
			c.identity = tt.identity
			resources, err := c.structsToResources(subjectSetStructs(t, structs, tt.structs...))
			if err != nil {
				t.Fatalf("structsToResources() error = %v", err)
			}
			err = c.resolveRunSubjectSets(resources)
			if len(tt.wantRunErr) > 0 {
				if err == nil {
					t.Fatal("resolveRunSubjectSets() error = nil, want a refusal")
				}
				for _, want := range tt.wantRunErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("resolveRunSubjectSets() error = %q, want it to contain %q", err.Error(), want)
					}
				}

				return
			}
			if err != nil {
				t.Fatalf("resolveRunSubjectSets() error = %v", err)
			}

			err = resolveSubjectSetAuths(resources, tt.auths)
			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatal("resolveSubjectSetAuths() error = nil, want a refusal")
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("resolveSubjectSetAuths() error = %q, want it to contain %q", err.Error(), want)
					}
				}
				for _, unwanted := range tt.notInErr {
					if strings.Contains(err.Error(), unwanted) {
						t.Errorf("resolveSubjectSetAuths() error = %q, want it not to contain %q", err.Error(), unwanted)
					}
				}

				return
			}
			if err != nil {
				t.Fatalf("resolveSubjectSetAuths() error = %v", err)
			}
			got := make(map[string]string)
			for _, res := range resources {
				for _, set := range res.SubjectSets {
					got[set.Name] = set.Auth
				}
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the auth each set names mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestExtractResourcesBesideTheApplications_subjectSetAuths drives the same rule through
// the step of a run that applies it, with the auths the run learns for itself and no
// name handed in by the test: a run that skipped the check, or held the sets to no auth,
// fails here. Two outlets that sign two auths in make an application of several auths,
// so a set that says none is refused naming both, and a set that names neither is
// refused as no auth of the application; one outlet makes an application of one auth,
// whose sets need say nothing. A run whose generate program declares one site's outlet
// still knows every auth of the application, from the auth packages in the application's
// tree: beside another site's auth package, a set that says none is refused naming both
// auths, and a set labeled for the other site's auth is taken.
func TestExtractResourcesBesideTheApplications_subjectSetAuths(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))

	tests := []struct {
		name string
		// auths are the auths the run's outlets declare, the first on the default outlet.
		auths []string
		// tree are the auth packages in the application's tree; none leaves the run with
		// no tree to read.
		tree    []string
		structs []string
		want    map[string]string
		wantErr []string
	}{
		{
			name:    "two outlets with an auth each, and every set says whose people it describes",
			auths:   []string{"staff", "patrons"},
			structs: []string{"BayKeeper", "LoungeGuest"},
			want:    map[string]string{"bays": "staff", "lounges": "patrons"},
		},
		{
			name:    "two outlets with an auth each, and a set that says none",
			auths:   []string{"staff", "patrons"},
			structs: []string{"TeamMember", "BayKeeper"},
			wantErr: []string{"struct TeamMember field UserID: @subjectSet(teams): the application has several auths (patrons, staff), and a set describes the people of one"},
		},
		{
			name:    "two outlets with an auth each, and a set that names neither",
			auths:   []string{"crew", "patrons"},
			structs: []string{"BayKeeper", "LoungeGuest"},
			wantErr: []string{"struct BayKeeper field UserID: @subjectSet(bays): auth: names staff, which is no auth of the application; its auths are: crew, patrons"},
		},
		{
			name:    "one outlet with an auth, and a set that says none",
			auths:   []string{"staff"},
			structs: []string{"TeamMember", "BayKeeper"},
			want:    map[string]string{"bays": "staff", "teams": ""},
		},
		{
			name:    "one outlet with an auth whose package is the tree's one auth, and a set that says none",
			auths:   []string{"staff"},
			tree:    []string{"staff"},
			structs: []string{"TeamMember", "BayKeeper"},
			want:    map[string]string{"bays": "staff", "teams": ""},
		},
		{
			name:    "one outlet with an auth beside another site's auth package, and a set that says none",
			auths:   []string{"staff"},
			tree:    []string{"staff", "patrons"},
			structs: []string{"TeamMember", "BayKeeper"},
			wantErr: []string{"struct TeamMember field UserID: @subjectSet(teams): the application has several auths (patrons, staff), and a set describes the people of one"},
		},
		{
			name:    "one outlet with an auth beside another site's auth package, and a set for each auth",
			auths:   []string{"staff"},
			tree:    []string{"staff", "patrons"},
			structs: []string{"BayKeeper", "LoungeGuest"},
			want:    map[string]string{"bays": "staff", "lounges": "patrons"},
		},
		{
			name:    "no outlet with an auth beside two auth packages, and a set that names neither",
			tree:    []string{"crew", "patrons"},
			structs: []string{"BayKeeper"},
			wantErr: []string{"struct BayKeeper field UserID: @subjectSet(bays): auth: names staff, which is no auth of the application; its auths are: crew, patrons"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &resourceGenerator{client: subjectSetFixtureClient(t, structs), genRoutes: true, routePrefix: "api"}
			if len(tt.tree) > 0 {
				r.moduleRoot = t.TempDir()
				for _, auth := range tt.tree {
					writeAuthPackage(t, r.moduleRoot, auth)
				}
			}
			for i, auth := range tt.auths {
				outlet := routerOutlet{auth: &outletAuth{importPath: "example.com/acme/beacon/pkg/auth/" + auth, methods: []AuthMethod{PasswordMethod()}}}
				if i == 0 {
					r.defaultOutlet = outlet

					continue
				}
				outlet.name, outlet.prefix, outlet.servesSessions = auth, auth+"/api", true
				r.extraOutlets = append(r.extraOutlets, outlet)
			}
			resources, err := r.structsToResources(subjectSetStructs(t, structs, tt.structs...))
			if err != nil {
				t.Fatalf("structsToResources() error = %v", err)
			}
			r.resources = resources

			err = r.extractResourcesBesideTheApplications(nil, nil)
			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatal("extractResourcesBesideTheApplications() error = nil, want a refusal")
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("extractResourcesBesideTheApplications() error = %q, want it to contain %q", err.Error(), want)
					}
				}

				return
			}
			if err != nil {
				t.Fatalf("extractResourcesBesideTheApplications() error = %v", err)
			}
			got := make(map[string]string)
			for _, res := range r.resources {
				for _, set := range res.SubjectSets {
					got[set.Name] = set.Auth
				}
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the auth each set names mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// writeAuthPackage writes an auth package of the name into the application tree at root,
// where the application keeps its auths: a package under pkg/auth that constructs a
// session Auth.
func writeAuthPackage(t *testing.T, root, name string) {
	t.Helper()

	dir := filepath.Join(root, "pkg", "auth", name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	src := "package " + name + "\n\nimport \"github.com/impulseframework/impulse/session\"\n\nfunc New() {\n\t_, _ = session.NewAuth(nil)\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "auth.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
}
