package access

import (
	"maps"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
)

// setsCollection is the grammar fixture with its subject sets declared: each set by
// name, with the sets its who: part builds on. sets are the sets that name no auth,
// and authSets the sets that describe one auth's people, by the auth's name.
type setsCollection struct {
	grammarCollection
	sets     map[string][]string
	authSets map[string]map[string][]string
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
			name:     "a grant of one auth that names another auth's set does not read it for that auth",
			authSets: map[string]map[string][]string{"crew": {"crews": nil}, "members": {"hazardBands": nil}},
			auth:     "members",
			roles:    []*Role{readsCrews()},
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

// TestUnreadSetWarning_String pins the line a deploy prints.
func TestUnreadSetWarning_String(t *testing.T) {
	t.Parallel()

	want := "subject set wings is declared (@subjectSet) and no role reads it: no grant's condition names subject.wings, and no set a grant reads builds on it in its who: part, so nobody holds anything through it. Write the grant that reads it, or accept this warning where the role file is validated until that grant is written. If the set describes another auth's people, say so on the set (auth: in its @subjectSet) and hand this auth's name to WithDefaultRoles and to ValidateRoles, and this auth's check no longer looks at it."
	if got := (UnreadSetWarning{Set: "wings"}).String(); got != want {
		t.Errorf("UnreadSetWarning.String() =\n  %s\nwant\n  %s", got, want)
	}
}
