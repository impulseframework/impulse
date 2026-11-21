package access

import (
	"maps"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
)

// setsCollection is the grammar fixture with its subject sets declared: each set by
// name, with the sets its who: part builds on. sets are the sets that name no auth,
// and authSets the sets that describe one auth's people, by the auth's name. auths
// are the auths the collection's generation knew, none unless a test names them.
type setsCollection struct {
	grammarCollection
	sets     map[string][]string
	authSets map[string]map[string][]string
	auths    []string
}

// Auths lists the auths the fixture is told its generation knew.
func (c setsCollection) Auths() []string {
	return c.auths
}

// SubjectSets lists the sets for one auth as a generated collection does: those that
// name no auth and those of the auth asked for, and every set for an empty auth.
func (c setsCollection) SubjectSets(auth string) map[string][]string {
	listed := maps.Clone(c.sets)
	if listed == nil {
		listed = make(map[string][]string)
	}
	for of, sets := range c.authSets {
		if auth == "" || of == auth {
			maps.Copy(listed, sets)
		}
	}

	return listed
}

// SubjectSetAuth names the auth a set is declared under in authSets, and none for a
// set that names no auth.
func (c setsCollection) SubjectSetAuth(name string) string {
	for of, sets := range c.authSets {
		if _, ok := sets[name]; ok {
			return of
		}
	}

	return ""
}

// readsCrews and readsBands are a domain role each: one reads subject.crews, the other
// subject.hazardBands.
func readsCrews() *Role {
	return &Role{Name: "Quartermaster", Description: "Reads the widgets of their crews.", Permissions: map[accesstypes.Permission][]Grant{
		"Read": {{Resource: "Widgets", Condition: "owner IN subject.crews"}},
	}}
}

func readsBands() *Role {
	return &Role{Name: "Inspector", Description: "Reads the widgets in their hazard bands.", Permissions: map[accesstypes.Permission][]Grant{
		"Read": {{Resource: "Widgets", Condition: "price IN subject.hazardBands"}},
	}}
}

// readsNoSet is a domain role whose grants name no subject set.
func readsNoSet() *Role {
	return &Role{Name: "Clerk", Description: "Reads every widget.", Permissions: map[accesstypes.Permission][]Grant{
		"Read": {{Resource: "Widgets"}},
	}}
}

// TestValidateRoles_unreadSets pins the fourth warning kind: one line for each subject
// set the application declares that no grant reads, and silence for a set read only
// through another set's who: part.
func TestValidateRoles_unreadSets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		sets  map[string][]string
		roles []*Role
		want  []Warning
	}{
		{
			name:  "a declared set no grant reads is one warning",
			sets:  map[string][]string{"crews": nil, "hazardBands": nil},
			roles: []*Role{readsCrews()},
			want:  []Warning{UnreadSetWarning{Set: "hazardBands"}},
		},
		{
			name:  "every declared set is read",
			sets:  map[string][]string{"crews": nil, "hazardBands": nil},
			roles: []*Role{readsCrews(), readsBands()},
		},
		{
			name:  "a set read only through another set's who: part is read",
			sets:  map[string][]string{"crews": {"hazardBands"}, "hazardBands": nil},
			roles: []*Role{readsCrews()},
		},
		{
			name:  "a chain of who: parts is followed to its end",
			sets:  map[string][]string{"crews": {"watches"}, "watches": {"hazardBands"}, "hazardBands": nil},
			roles: []*Role{readsCrews()},
		},
		{
			name:  "two sets that build on each other and are read by nothing both warn",
			sets:  map[string][]string{"crews": {"hazardBands"}, "hazardBands": {"crews"}},
			roles: []*Role{readsNoSet()},
			want:  []Warning{UnreadSetWarning{Set: "crews"}, UnreadSetWarning{Set: "hazardBands"}},
		},
		{
			name:  "a set that builds on a read set is not read by that",
			sets:  map[string][]string{"crews": nil, "hazardBands": {"crews"}},
			roles: []*Role{readsCrews()},
			want:  []Warning{UnreadSetWarning{Set: "hazardBands"}},
		},
		{
			name: "a role file with no role warns on every declared set",
			sets: map[string][]string{"crews": nil},
			want: []Warning{UnreadSetWarning{Set: "crews"}},
		},
		{
			name:  "an application that declares no set raises nothing",
			roles: []*Role{readsNoSet()},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ValidateRoles(setsCollection{sets: tt.sets}, &RoleConfig{Roles: ScopedRoles{Domain: tt.roles}}, fixtureAuth)
			if err != nil {
				t.Fatalf("ValidateRoles() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ValidateRoles() warnings mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestValidateRoles_unreadSetsOfOneAuth pins whose sets an auth's check looks at: the
// sets that describe its own people and the sets that name no auth, and never a set
// that describes another auth's people, which only that auth's roles can read. An
// application with a staff auth and a customer auth validates each role file with its
// auth's name, and neither is told of the other's sets.
func TestValidateRoles_unreadSetsOfOneAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sets     map[string][]string
		authSets map[string]map[string][]string
		auth     string
		roles    []*Role
		want     []Warning
	}{
		{
			name:     "a set that describes another auth's people is not looked at",
			authSets: map[string]map[string][]string{"crew": {"crews": nil}, "members": {"hazardBands": nil}},
			auth:     "crew",
			roles:    []*Role{readsCrews()},
		},
		{
			name:     "the other auth's check is told of its own unread set, and not of the first's",
			authSets: map[string]map[string][]string{"crew": {"crews": nil}, "members": {"hazardBands": nil}},
			auth:     "members",
			roles:    []*Role{readsNoSet()},
			want:     []Warning{UnreadSetWarning{Set: "hazardBands"}},
		},
		{
			name:     "the auth's own unread set warns, and another auth's beside it does not",
			authSets: map[string]map[string][]string{"crew": {"crews": nil, "watches": nil}, "members": {"hazardBands": nil}},
			auth:     "crew",
			roles:    []*Role{readsCrews()},
			want:     []Warning{UnreadSetWarning{Set: "watches"}},
		},
		{
			name:     "a set that names no auth is looked at by every auth's check",
			sets:     map[string][]string{"hazardBands": nil},
			authSets: map[string]map[string][]string{"crew": {"crews": nil}},
			auth:     "members",
			roles:    []*Role{readsNoSet()},
			want:     []Warning{UnreadSetWarning{Set: "hazardBands"}},
		},
		{
			name:     "a check that names no auth looks at every set",
			authSets: map[string]map[string][]string{"crew": {"crews": nil}, "members": {"hazardBands": nil}},
			roles:    []*Role{readsNoSet()},
			want:     []Warning{UnreadSetWarning{Set: "crews"}, UnreadSetWarning{Set: "hazardBands"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := setsCollection{sets: tt.sets, authSets: tt.authSets}
			got, err := ValidateRoles(store, &RoleConfig{Roles: ScopedRoles{Domain: tt.roles}}, tt.auth)
			if err != nil {
				t.Fatalf("ValidateRoles() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ValidateRoles() warnings mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestValidateRoles_grantThroughAnotherAuthsSet pins that a role file is refused a grant
// whose condition reads a set that describes another auth's people: the set is looked up
// by that auth's ids, so the grant gives the role's holders nothing unless an id of theirs
// is also an id of the other auth's, and then it gives them another person's rows. A set
// of the file's own auth, a set that names no auth, and a file handed over with no name
// are accepted.
func TestValidateRoles_grantThroughAnotherAuthsSet(t *testing.T) {
	t.Parallel()

	labeled := map[string]map[string][]string{"crew": {"crews": nil}, "members": {"hazardBands": nil}}
	tests := []struct {
		name     string
		sets     map[string][]string
		authSets map[string]map[string][]string
		auth     string
		roles    ScopedRoles
		wantErr  string
	}{
		{
			name:     "a grant of one auth's role through a set that describes another auth's people",
			authSets: labeled,
			auth:     "members",
			roles:    ScopedRoles{Domain: []*Role{readsNoSet(), readsCrews()}},
			wantErr:  "role Quartermaster: Read grant on Widgets: the condition reads subject.crews, which describes the people of crew (auth: crew in the set's @subjectSet), and this role file is handed over as the roles of members; the set is looked up by the ids of crew's people, so the role's holders get nothing of their own through it: read a set that describes the people of members, or move the grant to a role of crew",
		},
		{
			name:     "the other auth's file is refused the first's set the same way",
			authSets: labeled,
			auth:     "crew",
			roles:    ScopedRoles{Domain: []*Role{readsBands()}},
			wantErr:  "role Inspector: Read grant on Widgets: the condition reads subject.hazardBands, which describes the people of members (auth: members in the set's @subjectSet), and this role file is handed over as the roles of crew",
		},
		{
			name:     "a grant through a set of the file's own auth",
			authSets: labeled,
			auth:     "crew",
			roles:    ScopedRoles{Domain: []*Role{readsCrews()}},
		},
		{
			name:     "a grant through a set that names no auth",
			sets:     map[string][]string{"crews": nil},
			authSets: map[string]map[string][]string{"members": {"hazardBands": nil}},
			auth:     "members",
			roles:    ScopedRoles{Domain: []*Role{readsCrews(), readsBands()}},
		},
		{
			name:     "a file handed over with no name is held to no auth's sets",
			authSets: labeled,
			roles:    ScopedRoles{Domain: []*Role{readsCrews(), readsBands()}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := setsCollection{sets: tt.sets, authSets: tt.authSets}
			_, err := ValidateRoles(store, &RoleConfig{Roles: tt.roles}, tt.auth)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateRoles() error = %v, want none", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateRoles() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// crewsRoleFile is a role file over setsCollection whose one domain role reads
// subject.crews.
var crewsRoleFile = RoleFile(`{
  "roles": {
    "domain": [
      {"name": "Quartermaster", "description": "Reads the widgets of their crews.", "permissions": {"Read": [{"resource": "Widgets", "condition": "owner IN subject.crews"}]}}
    ]
  }
}`)

// TestNew_grantThroughAnotherAuthsSet pins that New refuses the same grant when the role
// file is handed over with WithDefaultRoles, and opens with the file of the set's own auth.
func TestNew_grantThroughAnotherAuthsSet(t *testing.T) {
	t.Parallel()

	collection := setsCollection{authSets: map[string]map[string][]string{"crew": {"crews": nil}, "members": {"hazardBands": nil}}}
	tests := []struct {
		name    string
		auth    string
		wantErr []string
	}{
		{name: "the file of the set's own auth", auth: "crew"},
		{
			name: "the file of another auth",
			auth: "members",
			wantErr: []string{
				"access.WithDefaultRoles(): the role file does not validate",
				"role Quartermaster: Read grant on Widgets: the condition reads subject.crews, which describes the people of crew (auth: crew in the set's @subjectSet), and this role file is handed over as the roles of members",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, err := New(newFakeStore(), collection, WithDefaultRoles(crewsRoleFile, tt.auth))
			if client != nil {
				t.Cleanup(func() {
					if err := client.Close(); err != nil {
						t.Errorf("Client.Close() error = %v", err)
					}
				})
			}
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("New() error = %v, want none", err)
				}

				return
			}
			for _, want := range tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("New() error = %v, want it to contain %q", err, want)
				}
			}
		})
	}
}

// TestUnreadSetWarning_String pins the line a deploy prints.
func TestUnreadSetWarning_String(t *testing.T) {
	t.Parallel()

	want := "subject set wings is declared (@subjectSet) and no role reads it: no grant's condition names subject.wings, and no set a grant reads builds on it in its who: part, so nobody holds anything through it. Write the grant that reads it, or accept this warning where the role file is validated until that grant is written. If the set describes another auth's people, say so on the set (auth: in its @subjectSet) and hand this auth's name to WithDefaultRoles and to ValidateRoles, and this auth's check no longer looks at it."
	if got := (UnreadSetWarning{Set: "wings"}).String(); got != want {
		t.Errorf("UnreadSetWarning.String() =\n  %s\nwant\n  %s", got, want)
	}
}

// TestClient_CheckPolicy_unreadSetsOfARoleFile pins that the sets no role reads are a
// role file's warning, raised for the auth the file was handed over with. A Client opened
// without a role file has no file to write the grant in, so it is told of no set, where
// one handed a role file is told of each set of its own auth that the file does not read,
// and of no other auth's.
func TestClient_CheckPolicy_unreadSetsOfARoleFile(t *testing.T) {
	t.Parallel()

	collection := setsCollection{authSets: map[string]map[string][]string{"crew": {"crews": nil}, "members": {"hazardBands": nil}}}
	tests := []struct {
		name string
		opts []Option
		want []Warning
	}{
		{name: "a Client opened without a role file is told of no set"},
		{
			name: "a Client handed a role file is told of its own auth's unread set",
			opts: []Option{WithDefaultRoles(grammarRoleFile, "crew")},
			want: []Warning{UnreadSetWarning{Set: "crews"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newPolicyClient(t, newFakeStore(), collection, tt.opts...)
			warnings, err := client.CheckPolicy(t.Context())
			if err != nil {
				t.Fatalf("CheckPolicy() error = %v", err)
			}
			var got []Warning
			for _, w := range warnings {
				if _, unread := w.(UnreadSetWarning); unread {
					got = append(got, w)
				}
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("CheckPolicy() unread sets mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
