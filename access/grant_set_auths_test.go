package access

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// authSetsCollection is setsCollection with a third set beside the grammar fixture's
// two: docks, a set of strings.
type authSetsCollection struct {
	setsCollection
}

// SubjectSetComparisonType answers for docks, and for the grammar fixture's sets as it
// does.
func (c authSetsCollection) SubjectSetComparisonType(name string) (accesstypes.AttributeType, bool) {
	if name == "docks" {
		return accesstypes.AttributeTypeString, true
	}

	return c.setsCollection.SubjectSetComparisonType(name)
}

// customRoleSetsRelease is the release the custom-role tests run against: crews
// describes the people of crew, hazardBands the people of staff, and docks names no
// auth. auths are the auths its generation knew.
func customRoleSetsRelease(auths ...string) PermissionCollection {
	return authSetsCollection{setsCollection{
		sets:     map[string][]string{"docks": nil},
		authSets: map[string]map[string][]string{"crew": {"crews": nil}, "staff": {"hazardBands": nil}},
		auths:    auths,
	}}
}

// customRoleSetGrants are the grants a custom role is given on Widgets, each with the
// set of another auth's people its condition reads, if it reads one, for a role of crew.
var customRoleSetGrants = []struct {
	name string
	cond string
	// otherSet is the set of staff's people the condition reads; empty where it reads
	// none.
	otherSet string
}{
	{name: "a set labeled with the role's own auth", cond: "owner IN subject.crews"},
	{name: "a set that names no auth", cond: "owner IN subject.docks"},
	{name: "a set labeled with another auth, with IN", cond: "price IN subject.hazardBands", otherSet: "hazardBands"},
	{name: "a set labeled with another auth, with NOT IN", cond: "price NOT IN subject.hazardBands", otherSet: "hazardBands"},
	{name: "the role's own auth's set and another auth's in one condition", cond: "owner IN subject.crews AND price IN subject.hazardBands", otherSet: "hazardBands"},
}

// customRoleEngines are the ways a Client is opened over customRoleSetsRelease: told the
// auth crew where the release knows crew and staff, and, where it knows crew alone,
// told no auth and opened with no role file. Only the first holds a custom role to its
// auth's sets. Where the release knows several auths the other two do not open
// (TestNew_authName).
var customRoleEngines = []struct {
	name string
	// auths are the auths the engine's release knows.
	auths  []string
	opts   []Option
	heldTo bool
}{
	{name: "the engine of crew", auths: []string{"crew", "staff"}, opts: []Option{WithDefaultRoles(crewsRoleFile, "crew")}, heldTo: true},
	{name: "an engine told no auth", auths: []string{"crew"}, opts: []Option{WithDefaultRoles(crewsRoleFile, "")}},
	{name: "an engine opened with no role file", auths: []string{"crew"}},
}

// crewRoleReadsStaffSet is the sentence a grant of Clerk, a role of crew, on Widgets is
// refused with when its condition reads set, a set of staff's people.
func crewRoleReadsStaffSet(set string) string {
	return "role Clerk: Read grant on Widgets: the condition reads subject." + set + ", which describes the people of staff (auth: staff in the set's @subjectSet), and this is a role of crew. The set is looked up by the ids of staff's people, so it says nothing about the people who hold this role: read a set that describes the people of crew, or give the grant to a role of staff"
}

// TestUserManager_customRoleGrantHeldToItsAuthsSets pins that a write to a custom role
// is held to the sets of the engine's auth as the role file is: a grant that reads a set
// of another auth's people is a bad request in the role file's sentence, by each write
// that adds a grant, and writes nothing. A set of the role's own auth, a set that names
// no auth, and any set in an engine told no auth are taken.
func TestUserManager_customRoleGrantHeldToItsAuthsSets(t *testing.T) {
	t.Parallel()

	const clerk accesstypes.Role = "Clerk"
	writes := []struct {
		name  string
		write func(ctx context.Context, m UserManager, cond string) error
	}{
		{name: "AddRoleGrant", write: func(ctx context.Context, m UserManager, cond string) error {
			return m.AddRoleGrant(ctx, tenant1Policy, clerk, accesstypes.Read, "Widgets", cond)
		}},
		{name: "AddRoleGrants", write: func(ctx context.Context, m UserManager, cond string) error {
			return m.AddRoleGrants(ctx, tenant1Policy, clerk, GrantRow{Permission: accesstypes.Read, Resource: "Widgets", Condition: cond})
		}},
		{name: "ChangeRoleGrants", write: func(ctx context.Context, m UserManager, cond string) error {
			return m.ChangeRoleGrants(ctx, tenant1Policy, clerk, nil, []GrantRow{{Permission: accesstypes.Read, Resource: "Widgets", Condition: cond}})
		}},
	}
	for _, engine := range customRoleEngines {
		for _, grant := range customRoleSetGrants {
			refused := engine.heldTo && grant.otherSet != ""
			for _, w := range writes {
				// A grant that is taken is written by AddRoleGrant alone; a refused one by
				// every write that adds a grant.
				if !refused && w.name != "AddRoleGrant" {
					continue
				}
				t.Run(engine.name+"/"+grant.name+"/"+w.name, func(t *testing.T) {
					t.Parallel()
					ctx := t.Context()

					manager := newPolicyClient(t, newFakeStore(), customRoleSetsRelease(engine.auths...), engine.opts...).UserManager()
					if err := manager.AddRole(ctx, tenant1Policy, clerk, aDescription); err != nil {
						t.Fatalf("AddRole() error = %v", err)
					}
					err := w.write(ctx, manager, grant.cond)
					want := map[accesstypes.Permission]map[accesstypes.Resource][]RoleGrant{accesstypes.Read: {"Widgets": {{Condition: grant.cond}}}}
					if refused {
						want = nil
						switch {
						case !httpio.HasBadRequest(err):
							t.Errorf("%s() error = %v, want a bad request", w.name, err)
						case httpio.Message(err) != crewRoleReadsStaffSet(grant.otherSet):
							t.Errorf("%s() refusal = %q, want %q", w.name, httpio.Message(err), crewRoleReadsStaffSet(grant.otherSet))
						}
					} else if err != nil {
						t.Errorf("%s() error = %v, want the grant written", w.name, err)
					}
					got, err := manager.RoleGrants(ctx, tenant1Policy, clerk)
					if err != nil {
						t.Fatalf("RoleGrants() error = %v", err)
					}
					if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
						t.Errorf("RoleGrants() after %s() mismatch (-want +got):\n%s", w.name, diff)
					}
				})
			}
		}
	}
}

// TestValidateRoles_customRoleWriteSaysTheRoleFilesSentence pins that a write to a custom
// role and the role file say one sentence for the same grant of the same role: the role
// file of crew that gives Clerk the grant is refused with the words the write is.
func TestValidateRoles_customRoleWriteSaysTheRoleFilesSentence(t *testing.T) {
	t.Parallel()

	for _, grant := range customRoleSetGrants {
		if grant.otherSet == "" {
			continue
		}
		t.Run(grant.name, func(t *testing.T) {
			t.Parallel()

			clerk := &Role{Name: "Clerk", Description: aDescription, Permissions: map[accesstypes.Permission][]Grant{
				accesstypes.Read: {{Resource: "Widgets", Condition: grant.cond}},
			}}
			_, err := ValidateRoles(customRoleSetsRelease("crew", "staff"), &RoleConfig{Roles: ScopedRoles{Domain: []*Role{clerk}}}, "crew")
			if err == nil {
				t.Fatal("ValidateRoles() error = nil, want the grant refused")
			}
			if got := plainText(err); got != crewRoleReadsStaffSet(grant.otherSet) {
				t.Errorf("ValidateRoles() refusal = %q, want %q", got, crewRoleReadsStaffSet(grant.otherSet))
			}
		})
	}
}

// TestClient_storedGrantHeldToItsAuthsSets pins that a load of the policy holds a custom
// role's stored grant to the sets of the engine's auth as the write does: a grant found
// in the store that reads a set of another auth's people is skipped, reported through
// CheckPolicy and the reload hook with the write's reason, given to nobody, and marked
// in the role's listing. In an engine told no auth the same row is carried.
func TestClient_storedGrantHeldToItsAuthsSets(t *testing.T) {
	t.Parallel()

	const clerk accesstypes.Role = "Clerk"
	for _, engine := range customRoleEngines {
		for _, grant := range customRoleSetGrants {
			t.Run(engine.name+"/"+grant.name, func(t *testing.T) {
				t.Parallel()
				ctx := t.Context()

				wantReason := ""
				if engine.heldTo && grant.otherSet != "" {
					wantReason = strings.TrimPrefix(crewRoleReadsStaffSet(grant.otherSet), "role Clerk: Read grant on Widgets: ")
				}
				store := newFakeStore()
				for _, err := range []error{
					store.InsertRole(ctx, tenant1Policy, clerk, aDescription),
					store.InsertGrant(ctx, tenant1Policy, clerk, accesstypes.Read, "Widgets", "", grant.cond),
					store.InsertUserRole(ctx, tenant1Policy, "dana", clerk, accesstypes.Period{}, ""),
				} {
					if err != nil {
						t.Fatalf("seeding fake store: %v", err)
					}
				}
				hook := &reloadErrors{}
				client := newPolicyClient(t, store, customRoleSetsRelease(engine.auths...), slices.Concat(engine.opts, []Option{WithReloadErrorHandler(hook.collect)})...)

				foothold, err := client.UserHasGrants(ctx, "dana", tenant1Scope)
				if err != nil {
					t.Fatalf("UserHasGrants() error = %v", err)
				}
				if foothold != (wantReason == "") {
					t.Errorf("UserHasGrants() = %v, want %v", foothold, wantReason == "")
				}

				var want []string
				if wantReason != "" {
					want = []string{"skipped a grant this release cannot use, so it is denied: role Clerk in scope " + tenant1Policy.String() + `, Read on Widgets under condition "` + grant.cond + `": ` + wantReason}
				}
				warnings, err := client.CheckPolicy(ctx)
				if err != nil {
					t.Fatalf("CheckPolicy() error = %v", err)
				}
				var listed []string
				for _, warning := range warnings {
					if _, ok := warning.(*SkippedGrant); ok {
						listed = append(listed, warning.String())
					}
				}
				if diff := cmp.Diff(want, listed); diff != "" {
					t.Errorf("CheckPolicy() skipped grants mismatch (-want +got):\n%s", diff)
				}
				settle(client.snapEngine)
				var reported []string
				for _, err := range hook.list() {
					var skipped *SkippedGrant
					if errors.As(err, &skipped) {
						reported = append(reported, skipped.Error())
					}
				}
				if diff := cmp.Diff(want, reported); diff != "" {
					t.Errorf("the hook's skipped grants mismatch (-want +got):\n%s", diff)
				}

				grants, err := client.UserManager().RoleGrants(ctx, tenant1Policy, clerk)
				if err != nil {
					t.Fatalf("RoleGrants() error = %v", err)
				}
				wantGrants := map[accesstypes.Permission]map[accesstypes.Resource][]RoleGrant{
					accesstypes.Read: {"Widgets": {{Condition: grant.cond, Skipped: wantReason}}},
				}
				if diff := cmp.Diff(wantGrants, grants); diff != "" {
					t.Errorf("RoleGrants() mismatch (-want +got):\n%s", diff)
				}
			})
		}
	}
}
