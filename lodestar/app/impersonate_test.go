package app

import (
	"fmt"
	"slices"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/router"
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
// role, make and delete a group, put a person in one and take them out, give a group a
// role and take it, send and withdraw an invitation, the one that makes a sector among
// them, make, disable, enable and delete an account, and open a sign-in method to
// sign-up or make it invite only, for the whole application and for a sector; and the
// two methods the generator writes for the sector record's directory, which open it to
// the directory's people with a role and make it invite only again; and the one action
// of Lodestar's own that does, SetSectorPilotClearance, which rates a pilot. The
// generated collection classes each access, which the kind that acts as a person
// refuses beside credential. When the generated set gains, loses or renames such a
// method, this list changes with it.
var accessActions = []accesstypes.Resource{
	"AddCrewGroupMember",
	"AddCrewSectorGroupMember",
	"AssignCrewRole",
	"AssignCrewSectorRole",
	"CreateCrewGroup",
	"CreateCrewSectorGroup",
	"CreateCrewSectorUser",
	"CreateCrewUser",
	"DeleteCrewGroup",
	"DeleteCrewSectorGroup",
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
	"GiveCrewGroupRole",
	"GiveCrewSectorGroupRole",
	"InviteCrewSector",
	"InviteCrewSectorUser",
	"InviteCrewUser",
	"MakeCrewInviteOnly",
	"MakeSectorInviteOnly",
	"OpenCrewSignUp",
	"OpenSectorToDirectory",
	"RemoveCrewGroupMember",
	"RemoveCrewRole",
	"RemoveCrewSectorGroupMember",
	"RemoveCrewSectorRole",
	"SetSectorPilotClearance",
	"TakeCrewGroupRole",
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
			Widest: "everything except access actions, credential actions, writing ClientContacts, writing HangarPasses, " +
				"writing PilotCertifications, writing Pilots, writing SquadronMemberships and changing Squadrons",
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
// what, for the records of Lodestar's own. A record the policy reads as a subject value
// or a subject set decides what the person each row names may do, so the kind's mask
// names its three writes; and a record a dotted value is read through (the squadron,
// whose row says which wing its members are in) decides it for every person who reaches
// it, so the mask names its change. The list is read off the generated collection, so a
// record that gains a @subjectValue or a @subjectSet, or a value that takes a new path,
// fails here until the declaration of the kind names it. A write the application does
// not offer needs no name: the places in a group of an auth carry a subject set too,
// and only the identity library's own actions write them, each classed access.
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

	var decide, kept []string
	need := func(perm accesstypes.Permission, record accesstypes.Resource) {
		write := fmt.Sprintf("%s on %s", perm, record)
		if slices.Contains(decide, write) || !collection.HasPermission(collection.Scope(record), perm, record) {
			return
		}
		decide = append(decide, write)
		if _, refused := act.Refuses(perm, record); !refused {
			kept = append(kept, write)
		}
	}
	for _, record := range collection.Resources() {
		bindings, _ := collection.Bindings(collection.Scope(record), record)
		subjects := slices.Concat(bindings.SubjectValues, bindings.SubjectSets)
		if len(subjects) == 0 {
			continue
		}
		for _, perm := range []accesstypes.Permission{accesstypes.Create, accesstypes.Update, accesstypes.Delete} {
			need(perm, record)
		}
		for _, subject := range subjects {
			for _, hop := range subject.Path {
				need(accesstypes.Update, accesstypes.Resource(hop.Table))
			}
		}
	}

	want := []string{
		"Create on ClientContacts", "Update on ClientContacts", "Delete on ClientContacts",
		"Create on HangarPasses", "Update on HangarPasses", "Delete on HangarPasses",
		"Create on PilotCertifications", "Update on PilotCertifications", "Delete on PilotCertifications",
		"Create on Pilots", "Update on Pilots", "Delete on Pilots",
		"Create on SquadronMemberships", "Update on SquadronMemberships", "Delete on SquadronMemberships", "Update on Squadrons",
	}
	if !slices.Equal(decide, want) {
		t.Errorf("the writes that decide what a person may do, read off the generated collection = %q, want %q", decide, want)
	}
	if len(kept) != 0 {
		t.Errorf("the kind that acts as a person keeps %q: each changes who may do what, so the kind's mask names it", kept)
	}
}
