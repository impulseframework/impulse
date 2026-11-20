package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/router"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
)

// credentialActions are the actions of the console and the portal that change how a
// person signs in, by name: the generated identity methods of the crew and of the
// members, for the whole application and for a sector. The generated collection classes
// each credential, which is what the kind that acts as a person refuses. When the
// generated set gains, loses or renames a method, this list changes with it: a method
// that changes how a person signs in and is missing from the collection's class is
// open to a session that acts as a person.
var credentialActions = []accesstypes.Resource{
	"RemoveCrewPassword",
	"RemoveCrewSectorPassword",
	"RenameCrewSectorUser",
	"RenameCrewUser",
	"RenameMembersSectorUser",
	"RenameMembersUser",
	"RequireCrewPasswordChange",
	"RequireCrewSectorPasswordChange",
	"SendCrewSectorUserPasswordResetLink",
	"SendCrewUserPasswordResetLink",
	"SetCrewSectorTemporaryPassword",
	"SetCrewSectorUserEmail",
	"SetCrewTemporaryPassword",
	"SetCrewUserEmail",
	"SetMembersSectorUserEmail",
	"SetMembersUserEmail",
	"UnlinkCrewSectorSessionIdentity",
	"UnlinkCrewSessionIdentity",
	"UnlinkMembersSectorSessionIdentity",
	"UnlinkMembersSessionIdentity",
}

// accessActions are the actions of the console and the portal that give or take away
// what someone may do, by name: the generated identity methods that give and remove a
// role, take one for a set time, make and delete a group, put a person in one and take them out, give a group a
// role and take it, send and withdraw an invitation, the one that makes a sector among
// them, make an account, and open a sign-in method to
// sign-up or make it invite only, for the whole application and for a sector; the
// setting that lets people ask for a reset link at the sign-in page; the four methods
// the generator writes for the sector record's directory, which open it to the
// directory's people with a role and make it invite only again, and which make the
// sector sign in with its directory alone and end that; CreateSector, the action of the
// generated route by which a person creates a sector of their own; and the one action
// of Lodestar's own that does, SetSectorPilotClearance, which rates a pilot. The
// generated collection classes each access, which the kind that acts as a person
// refuses beside credential. An account disabled, enabled or deleted is not among them:
// those carry no class (TestAccountSwitchesCarryNoClass). When the generated set gains,
// loses or renames such a method, this list changes with it.
var accessActions = []accesstypes.Resource{
	"AddCrewGroupMember",
	"AddCrewSectorGroupMember",
	"AssignCrewRole",
	"AssignCrewSectorRole",
	"CreateCrewGroup",
	"CreateCrewSectorGroup",
	"CreateCrewSectorUser",
	"CreateCrewUser",
	"CreateSector",
	"DeleteCrewGroup",
	"DeleteCrewSectorGroup",
	"EndSectorDirectoryOnly",
	"GiveCrewGroupRole",
	"GiveCrewSectorGroupRole",
	"InviteCrewSector",
	"InviteCrewSectorUser",
	"InviteCrewUser",
	"MakeCrewInviteOnly",
	"MakeSectorDirectoryOnly",
	"MakeSectorInviteOnly",
	"OpenCrewSignUp",
	"OpenSectorToDirectory",
	"RemoveCrewGroupMember",
	"RemoveCrewRole",
	"RemoveCrewSectorGroupMember",
	"RemoveCrewSectorRole",
	"SetCrewSelfServiceReset",
	"SetSectorPilotClearance",
	"TakeCrewGroupRole",
	"TakeCrewRole",
	"TakeCrewSectorGroupRole",
	"WithdrawCrewInvitation",
	"WithdrawCrewInvitedSector",
	"WithdrawCrewSectorInvitation",
}

// TestAccessActions pins the generated collection's list of the actions classed access,
// by name, as TestCredentialActions pins the credential ones: the mask of the kind that
// acts as a person refuses the class, so the list is what such a session cannot do to
// who may do what.
func TestAccessActions(t *testing.T) {
	t.Parallel()

	got := router.Collection().ActionsOfClass(router.ClassAccess)
	if !slices.Equal(got, accessActions) {
		t.Errorf("the actions the generated collection classes access = %v, want %v", got, accessActions)
	}
	for _, action := range got {
		if slices.Contains(credentialActions, action) {
			t.Errorf("%s is classed both access and credential: an action that changes how a person signs in is the one class, one that changes who may do what the other", action)
		}
	}
}

// accountSwitches are the generated methods that disable, enable and delete an account,
// for the crew and the members, for the whole application and for a sector.
var accountSwitches = []accesstypes.Resource{
	"DeleteCrewSectorUser",
	"DeleteCrewUser",
	"DeleteMembersSectorUser",
	"DeleteMembersUser",
	"DisableCrewSectorUser",
	"DisableCrewUser",
	"DisableMembersSectorUser",
	"DisableMembersUser",
	"EnableCrewSectorUser",
	"EnableCrewUser",
	"EnableMembersSectorUser",
	"EnableMembersUser",
}

// TestAccountSwitchesCarryNoClass pins that disabling, enabling and deleting an account
// are registered actions that carry neither of the library's classes: they are the work
// a person who stands in for an administrator is there to do, so the kind that acts as
// a person, whose mask refuses both classes, keeps them. The rule that nobody is the
// subject of what they do in such a session is what refuses them on the real person's
// own account.
func TestAccountSwitchesCarryNoClass(t *testing.T) {
	t.Parallel()

	collection := router.Collection()
	for _, action := range accountSwitches {
		if collection.Scope(action) == "" {
			t.Errorf("%s is not an action of the generated collection: the list of the account switches names a method the generator no longer writes", action)
		}
		for _, class := range []accesstypes.ActionClass{router.ClassAccess, router.ClassCredential} {
			if slices.Contains(collection.ActionsOfClass(class), action) {
				t.Errorf("%s is classed %s: disabling, enabling and deleting an account carry no class, so a session that acts as a person does them", action, class)
			}
		}
	}
}

// TestCredentialActions pins the generated collection's list of the actions classed
// credential, by name. The mask of the kind that acts as a person refuses the class, so
// the list is exactly what such a session cannot do: an action that drops out of it,
// because a template lost its @class line or the generator dropped the class on the way
// to the collection, would be open to the session without any other test noticing.
func TestCredentialActions(t *testing.T) {
	t.Parallel()

	got := router.Collection().ActionsOfClass(router.ClassCredential)
	if !slices.Equal(got, credentialActions) {
		t.Errorf("the actions the generated collection classes credential = %v, want %v", got, credentialActions)
	}
}

// TestImpersonationKinds pins the impersonation the console offers, held to the
// generated collection as the application holds it when it starts: three kinds, each
// under a grant of its own, with the widest mask that grant gives.
func TestImpersonationKinds(t *testing.T) {
	t.Parallel()

	declared, err := session.NewImpersonations(router.Collection(), impersonationKinds()...)
	if err != nil {
		t.Fatalf("session.NewImpersonations() refuses the console's declaration: %v", err)
	}

	type kind struct {
		Name   string
		Grant  accesstypes.Resource
		AsRole bool
		Widest string
	}
	want := []kind{
		{Name: "view", Grant: "ViewAsUser", Widest: "reading only"},
		{
			Name: "act", Grant: "ActAsUser",
			Widest: "everything except access actions (still allowed: writing HangarPasses) and credential actions",
		},
		{Name: "role", Grant: "AssumeRole", AsRole: true, Widest: "everything"},
	}
	var got []kind
	for _, k := range declared.Kinds() {
		got = append(got, kind{Name: k.Name(), Grant: k.Grant(), AsRole: k.AsRole(), Widest: k.Widest().Words()})
	}
	if !slices.Equal(got, want) {
		t.Errorf("the kinds of impersonation the console offers = %+v, want %+v", got, want)
	}
}

// TestActKindRefusesTheRecordsThatDecideWhatAPersonMayDo holds the kind that acts as a
// person to the promise its role makes, that a stand-in does not change who may do
// what, for the records of Lodestar's own. The kind names no record to refuse it: its
// mask refuses the class access, and the generator gives that class to the writes of
// every record the policy reads as a subject value or a subject set, and to the change
// of a record a dotted value is read through (the squadron, whose row says which wing
// its members are in). The writes are read off the generated collection, so the list
// here is the generator's and changes when a record gains a @subjectValue or a
// @subjectSet, or a value takes a new path; the kind refuses each without an edit to
// its declaration.
// It names one record to keep it: the writes of a hangar pass are the kind's exception
// to the class, so they reach policy, and every other write of the class stays refused,
// whatever the generator adds to the class later.
func TestActKindRefusesTheRecordsThatDecideWhatAPersonMayDo(t *testing.T) {
	t.Parallel()

	collection := router.Collection()
	declared, err := session.NewImpersonations(collection, impersonationKinds()...)
	if err != nil {
		t.Fatalf("session.NewImpersonations() refuses the console's declaration: %v", err)
	}
	var act accesstypes.PermissionMask
	for _, k := range declared.Kinds() {
		if k.Name() == kindAct {
			act = k.Widest()
		}
	}
	for _, refusal := range act.Refusals() {
		if perm, record, named := refusal.Named(); named {
			t.Errorf("the kind that acts as a person refuses %s on %s by name: the class access is what holds it off a record that decides what a person may do", perm, record)
		}
	}

	// The places in a group of the crew carry a subject set too. The application
	// offers no write of that record, since the identity library's own actions write
	// it, each classed access; the record's writes carry the class all the same, so a
	// route written by hand that registers one is refused with them.
	want := []string{
		"Create on ClientContacts", "Update on ClientContacts", "Delete on ClientContacts",
		"Create on CrewGroupMembers", "Update on CrewGroupMembers", "Delete on CrewGroupMembers",
		"Create on HangarPasses", "Update on HangarPasses", "Delete on HangarPasses",
		"Create on PilotCertifications", "Update on PilotCertifications", "Delete on PilotCertifications",
		"Create on Pilots", "Update on Pilots", "Delete on Pilots",
		"Create on SquadronMemberships", "Update on SquadronMemberships", "Delete on SquadronMemberships",
		"Update on Squadrons",
	}
	classed := collection.WritesOfClass(router.ClassAccess)
	if !slices.Equal(classed, want) {
		t.Errorf("the writes the generator classes access, read off the generated collection = %q, want %q", classed, want)
	}

	// What the kind keeps out of the class: the three writes of a hangar pass.
	kept := []string{"Create on HangarPasses", "Update on HangarPasses", "Delete on HangarPasses"}
	for _, write := range kept {
		if !slices.Contains(classed, write) {
			t.Errorf("the kind keeps %s out of the class access, and the generator does not class it: the exception names what the class does not cover", write)
		}
	}

	for _, record := range collection.Resources() {
		for _, perm := range []accesstypes.Permission{accesstypes.Create, accesstypes.Update, accesstypes.Delete} {
			write := fmt.Sprintf("%s on %s", perm, record)
			refused := slices.Contains(classed, write) && !slices.Contains(kept, write)
			policy := &policyAsked{}
			masked := resource.Masked(policy, act, collection)
			if _, err := masked.Check(t.Context(), accesstypes.NewEnvironment(), accesstypes.GlobalScope(), perm, record); err != nil {
				t.Fatalf("Check(%s, %s) error = %v", perm, record, err)
			}
			if asked := len(policy.asked) != 0; asked == refused {
				t.Errorf("%s: policy asked = %t and the kind refuses the write = %t: the kind refuses the writes classed access, but for the hangar pass's, and leaves the rest to policy", write, asked, refused)
			}
		}
	}
}

// policyAsked stands for policy behind a masked check: it keeps the names that reach it
// and grants each. A check asks it who the person is and nothing else, so the rest is
// the embedded value's, which is nil.
type policyAsked struct {
	resource.UserPermissions

	asked []accesstypes.Resource
}

func (p *policyAsked) User() accesstypes.User {
	return "a person acted as"
}

func (p *policyAsked) Check(
	_ context.Context, _ accesstypes.Environment, _ accesstypes.Scope, _ accesstypes.Permission, resources ...accesstypes.Resource,
) (accesstypes.Decisions, error) {
	p.asked = append(p.asked, resources...)
	decisions := make(accesstypes.Decisions, len(resources))
	for _, res := range resources {
		decisions[res] = accesstypes.Granted()
	}

	return decisions, nil
}

// TestActKindKnowsEveryNameTheApplicationChecks holds the generated collection to the
// rule the masked check makes of a mask that refuses a class: a permission on a name
// the application registers nothing under is refused before policy is asked, since
// nobody can say what the name carries. The kind that acts as a person refuses two
// classes, so the rule is asked of every check its sessions make. Every permission the
// collection lists, on a record, on a field and on an action, is checked under the
// kind's mask for a sector and for the whole application, and reaches policy unless the
// mask itself refuses it, by the class the action or the record's write carries: the
// rule refuses nothing a generated handler asks, which is always a name of the
// collection. A record's name spelled wrong is refused whatever the permission, and a
// permission a record does not register is policy's, since the name is the
// application's.
func TestActKindKnowsEveryNameTheApplicationChecks(t *testing.T) {
	t.Parallel()

	collection := router.Collection()
	declared, err := session.NewImpersonations(collection, impersonationKinds()...)
	if err != nil {
		t.Fatalf("session.NewImpersonations() refuses the console's declaration: %v", err)
	}
	var act accesstypes.PermissionMask
	for _, k := range declared.Kinds() {
		if k.Name() == kindAct {
			act = k.Widest()
		}
	}
	// asked answers the names a check of perm reaches policy with, under the kind's mask.
	asked := func(t *testing.T, scope accesstypes.Scope, perm accesstypes.Permission, names ...accesstypes.Resource) []accesstypes.Resource {
		t.Helper()

		policy := &policyAsked{}
		if _, err := resource.Masked(policy, act, collection).Check(t.Context(), accesstypes.NewEnvironment(), scope, perm, names...); err != nil {
			t.Fatalf("Check(%s) error = %v", perm, err)
		}

		return policy.asked
	}
	scopes := []accesstypes.Scope{accesstypes.GlobalScope(), accesstypes.DomainScope("a sector")}

	listed := collection.List()
	for _, perm := range []accesstypes.Permission{
		accesstypes.Create, accesstypes.Read, accesstypes.List, accesstypes.Update, accesstypes.Delete, accesstypes.Execute,
	} {
		names := listed[perm]
		if len(names) == 0 {
			t.Errorf("the generated collection lists nothing under %s, want every permission checked", perm)

			continue
		}
		// The mask's own answer knows nothing of the collection: a name it lets through
		// and the check does not is one the rule took for a name of nobody's.
		var want []accesstypes.Resource
		for _, name := range names {
			record, _, _ := strings.Cut(string(name), ".")
			classes := slices.Concat(
				collection.ActionClasses(accesstypes.GlobalPermissionScope, name), collection.ActionClasses(accesstypes.DomainPermissionScope, name),
				collection.WriteClasses(accesstypes.GlobalPermissionScope, perm, accesstypes.Resource(record)),
				collection.WriteClasses(accesstypes.DomainPermissionScope, perm, accesstypes.Resource(record)),
			)
			if _, refused := act.Refuses(perm, name, classes...); !refused {
				want = append(want, name)
			}
		}
		if len(want) == 0 {
			t.Errorf("the kind that acts as a person keeps nothing under %s, want a permission it keeps checked", perm)
		}
		for _, scope := range scopes {
			if got := asked(t, scope, perm, names...); !slices.Equal(got, want) {
				t.Errorf("%s in %s: policy was asked about %d of the %d names the collection lists, want the %d the mask keeps: got %v, want %v",
					perm, scope, len(got), len(names), len(want), got, want)
			}
		}
	}

	for _, perm := range []accesstypes.Permission{accesstypes.Create, accesstypes.Read, accesstypes.List, accesstypes.Update, accesstypes.Delete} {
		for _, scope := range scopes {
			if got := asked(t, scope, perm, "Missionz", "Missionz.title", "missions"); len(got) != 0 {
				t.Errorf("%s on names the collection does not hold, in %s: policy was asked about %v, want the mask to refuse them first", perm, scope, got)
			}
			// Hangars is a record of the application whatever the permission asked.
			if got := asked(t, scope, perm, "Hangars"); !slices.Equal(got, []accesstypes.Resource{"Hangars"}) {
				t.Errorf("%s on Hangars in %s: policy was asked about %v, want the record left to policy", perm, scope, got)
			}
		}
	}
}
