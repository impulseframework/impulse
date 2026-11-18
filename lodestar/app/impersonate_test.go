package app

import (
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
		{Name: "act", Grant: "ActAsUser", Widest: "everything except credential actions"},
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
