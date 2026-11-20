package access

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
)

// setsCollection is the grammar fixture with its subject sets declared: each set by
// name, with the sets its who: part builds on.
type setsCollection struct {
	grammarCollection
	sets map[string][]string
}

func (c setsCollection) SubjectSets() map[string][]string {
	return c.sets
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
// set the application declares that no grant reads, silence for a set read only through
// another set's who: part, and the role files of the application's other auths counted
// beside the one validated.
func TestValidateRoles_unreadSets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		sets   map[string][]string
		roles  []*Role
		others []*RoleConfig
		want   []Warning
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
			name:   "a set another auth's role file reads is read",
			sets:   map[string][]string{"crews": nil, "hazardBands": nil},
			roles:  []*Role{readsCrews()},
			others: []*RoleConfig{{Roles: ScopedRoles{Domain: []*Role{readsBands()}}}},
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

			got, err := ValidateRoles(setsCollection{sets: tt.sets}, &RoleConfig{Roles: ScopedRoles{Domain: tt.roles}}, tt.others...)
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

	want := "subject set wings is declared (@subjectSet) and no role reads it: no grant's condition names subject.wings, and no set a grant reads builds on it in its who: part, so nobody holds anything through it. Write the grant that reads it, or accept this warning where the role file is validated until that grant is written. If another auth's role file reads it, hand that file to WithDefaultRoles and to ValidateRoles beside this one."
	if got := (UnreadSetWarning{Set: "wings"}).String(); got != want {
		t.Errorf("UnreadSetWarning.String() =\n  %s\nwant\n  %s", got, want)
	}
}

// TestValidateRoles_unreadSetsAnotherFileUnparsed refuses another auth's role file whose
// condition does not parse, rather than counting nothing for it.
func TestValidateRoles_unreadSetsAnotherFileUnparsed(t *testing.T) {
	t.Parallel()

	other := &RoleConfig{Roles: ScopedRoles{Domain: []*Role{{Name: "Broken", Description: "Carries a condition that does not parse.", Permissions: map[accesstypes.Permission][]Grant{
		"Read": {{Resource: "Widgets", Condition: "owner IN"}},
	}}}}}
	_, err := ValidateRoles(setsCollection{sets: map[string][]string{"crews": nil}}, &RoleConfig{}, other)
	if err == nil {
		t.Fatal("ValidateRoles() error = nil, want the other file's condition refused")
	}
}
