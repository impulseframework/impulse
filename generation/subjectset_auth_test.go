package generation

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
)

// TestResolveSubjectSetAuths pins what a run holds a set to on whose people it
// describes, driven through structsToResources and the run's resolution as a generation
// run drives them. An application with one auth leaves the part out, and every set is
// that auth's. An application with several says it on every set it writes, since nothing
// else tells the generator which auth's people a user column holds, and a name that is
// no auth of the application is refused. The set of a person's groups, which the
// generator declares for an auth, is asked nothing.
func TestResolveSubjectSetAuths(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	staff := []*identityInstance{{auth: "Staff", policy: access.Tables{Groups: "StaffGroups"}, groupSet: "staffGroups"}}

	tests := []struct {
		name     string
		structs  []string
		identity []*identityInstance
		auths    []string
		want     map[string]string
		wantErr  []string
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
				"say whose people UserID holds with auth: (auth: patrons)",
			},
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
			name:    "a run that is told of no auth takes the name as written",
			structs: []string{"BayKeeper", "TeamMember"},
			want:    map[string]string{"bays": "staff", "teams": ""},
		},
		{
			name:     "several auths, and the set of a person's groups an auth declares says nothing",
			structs:  []string{"StaffGroup", "StaffGroupMember", "BayKeeper"},
			identity: staff,
			auths:    []string{"patrons", "staff"},
			want:     map[string]string{"bays": "staff", "staffGroups": ""},
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
			if err := c.resolveRunSubjectSets(resources); err != nil {
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
