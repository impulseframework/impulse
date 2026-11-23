package generation

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// The class an act of the generated management carries, in the table below: the class
// of the acts that change how a person signs in, the class of the acts that give or
// take away what someone may do, and none.
const (
	changesSignIn = accesstypes.ClassCredential
	changesAccess = accesstypes.ClassAccess
	unclassed     = accesstypes.ActionClass("")
)

// identityMethodActs lists every method the generated management of an auth writes, by
// its act (the method's name without the auth's and the tenant record's), and says of
// each which class it carries. An act that changes how a person signs in carries the
// class credential (identityCredentialClass), and one that gives or takes away what
// someone may do carries the class access (identityAccessClass), so a permission mask
// that refuses a class refuses every act of it.
//
// The list is the test's whole input: a method added to the generated management fails
// Test_prepareIdentity_classedMethods until its act is listed here with a decision, and
// a method listed with a class fails it until its template carries the class. To change
// a set, change the line and the template together.
var identityMethodActs = map[string]accesstypes.ActionClass{
	// A person's credentials: what they sign in with, the link that lets them choose a
	// new password, and the username and the address they sign in under or recover the
	// account through. The address changes once the person confirms the new one; asking
	// for the change is the act.
	"RequirePasswordChange":     changesSignIn,
	"RemovePassword":            changesSignIn,
	"SendUserPasswordResetLink": changesSignIn,
	"UnlinkSessionIdentity":     changesSignIn,
	"RenameUser":                changesSignIn,
	"SetUserEmail":              changesSignIn,

	// What a person may do: the roles they hold, one at a time or all of their own in a
	// tenant at once (a tenant's RemoveUser, with their places in its groups), and the
	// groups that hold roles for the people in them. A group made is a new way to hold
	// roles and one deleted takes its roles from everyone in it.
	"AssignRole":        changesAccess,
	"RemoveRole":        changesAccess,
	"RemoveUser":        changesAccess,
	"CreateGroup":       changesAccess,
	"DeleteGroup":       changesAccess,
	"AddGroupMember":    changesAccess,
	"RemoveGroupMember": changesAccess,
	"GiveGroupRole":     changesAccess,
	"TakeGroupRole":     changesAccess,

	// A role a person takes themselves for a set time (the Break glass page) is a role
	// given, to the caller. The method refuses every session that acts as someone else
	// by itself (identity.Manager.TakeRole), and carries the class beside that, so a
	// mask that refuses the class names every act that gives a role.
	"TakeRole": changesAccess,

	// Who is let in, and with what: an invitation carries the roles its person will
	// hold, or makes them the administrator of a new tenant, and its withdrawal takes
	// that away; a sign-in method opened to sign-up gives a role to everyone who signs
	// up through it, and made invite only gives it to nobody.
	"InviteUser":            changesAccess,
	"WithdrawInvitation":    changesAccess,
	"InviteTenant":          changesAccess,
	"WithdrawInvitedTenant": changesAccess,
	"OpenSignUp":            changesAccess,
	"MakeInviteOnly":        changesAccess,

	// Whether a person may ask for their own reset link at the sign-in page: a setting
	// of the whole auth, which decides who can get back into an account without an
	// administrator. It is no act on one person's credentials, so it is with the
	// settings that say who is let in. So are the two settings of what every sign-in
	// asks for: whether everyone is asked for a second factor, and for how long a
	// remembered browser skips it.
	"SetSelfServiceReset":       changesAccess,
	"RequireSecondFactor":       changesAccess,
	"StopRequiringSecondFactor": changesAccess,
	"SetRememberedBrowsers":     changesAccess,

	// An account disabled, enabled or deleted: the work a person who stands in for an
	// administrator is there to do, which a mask that refuses the class access leaves to
	// them. Nobody does it to themselves: the rule that the person acting is never the
	// subject of what they do (identity.RefuseImpersonatorAsSubject) refuses each on the
	// real person's own account, whatever the mask says.
	"DeleteUser":  unclassed,
	"DisableUser": unclassed,
	"EnableUser":  unclassed,

	// The name people read, and what a group is described as: words, which change
	// nothing anybody may do.
	"SetUserName":   unclassed,
	"DescribeGroup": unclassed,

	// What a person holds beside their credentials and their roles: sessions,
	// application tokens, remembered browsers, and the count of failed sign-ins kept
	// under their name. Ending one of them ends a sign-in already made, or lets one be
	// tried again; the person signs in as before and may do what they could.
	"EndSession":               unclassed,
	"EndSessionImpersonation":  unclassed,
	"EndUserSessions":          unclassed,
	"RevokeSessionToken":       unclassed,
	"RevokeUserTrustedDevices": unclassed,
	"UnlockSignIn":             unclassed,
}

// identityAct names a generated method by its act: the method's name without the
// auth's name, and without the tenant record's where the method is a tenant's variant
// of an act. A method whose object is the tenant itself (the invitation that makes one)
// keeps it, as Tenant.
func identityAct(method string) string {
	act := method
	for _, auth := range []string{"Crew", "Members"} {
		if strings.Contains(act, auth) {
			act = strings.Replace(act, auth, "", 1)

			break
		}
	}
	if name, ok := strings.CutSuffix(act, "Ship"); ok {
		return name + "Tenant"
	}

	return strings.Replace(act, "Ship", "", 1)
}

// identityMethodStruct reads the struct an @rpc source declares, on its own or in a
// group of types, from the text after its @rpc annotation: one with fields, or one with
// none (struct{}).
var identityMethodStruct = regexp.MustCompile(`(?m)^\s*(?:type\s+)?(\w+) struct ?\{`)

// Test_prepareIdentity_classedMethods lists the methods the generated management writes
// and holds each to identityMethodActs: every method is listed there with a decision,
// the ones that change how a person signs in carry the class credential, the ones that
// give or take away what someone may do carry the class access, and no method carries a
// class it is not listed with.
func Test_prepareIdentity_classedMethods(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, crewGroupTables, membersIdentityTables, membersGroupTables)...)
	r.resource = "pkg/resources"
	r.tableMap["Ships"] = shipsTable()
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": shipTenantSource})
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	classLines := map[accesstypes.ActionClass]string{
		changesSignIn: strings.TrimPrefix(identityCredentialClass, "\n") + "\n",
		changesAccess: strings.TrimPrefix(identityAccessClass, "\n") + "\n",
	}
	classed := map[accesstypes.ActionClass]map[string][]string{changesSignIn: {}, changesAccess: {}}
	seen := make(map[string]bool)
	for _, file := range identityTreeFiles(t, "pkg/identity") {
		source, err := os.ReadFile(filepath.Join("pkg", "identity", file))
		if err != nil {
			t.Fatalf("os.ReadFile() error = %v", err)
		}
		// A method's annotations open with @rpc on a line of its own; a file that
		// declares none is no method's.
		_, declaration, found := strings.Cut(string(source), "// @rpc\n")
		if !found {
			continue
		}
		match := identityMethodStruct.FindStringSubmatch(declaration)
		if match == nil {
			t.Errorf("%s declares a method and no struct the test can read", file)

			continue
		}
		method := match[1]
		act := identityAct(method)
		seen[act] = true

		want, listed := identityMethodActs[act]
		if !listed {
			t.Errorf("%s (%s) is not listed in identityMethodActs: say whether it changes how a person signs in (credential), gives or takes away what someone may do (access), or neither, and give its template the class's line", method, file)

			continue
		}
		for class, line := range classLines {
			carries := strings.Contains(string(source), line)
			switch {
			case want == class && !carries:
				t.Errorf("%s (%s) is listed with the class %s and its source carries no %q: end its template's annotations with the class's line (identityCredentialClass, identityAccessClass)", method, file, class, strings.TrimSpace(line))
			case want != class && carries:
				t.Errorf("%s (%s) carries %q and identityMethodActs does not list it with the class %s", method, file, strings.TrimSpace(line), class)
			}
			if carries {
				classed[class][act] = append(classed[class][act], method)
			}
		}
		if strings.Count(string(source), "// @"+classKeyword+"(") != len(strings.Fields(string(want))) {
			t.Errorf("%s (%s) carries a class annotation the list does not account for: it is listed with %q", method, file, want)
		}
	}

	// The classed methods the fixture's two auths write: the whole application's and one
	// tenant's of each act. The members sign in through a directory alone, so they hold
	// no password to set, require, remove or reset, and nobody is invited to their auth
	// or signs up to it.
	want := map[accesstypes.ActionClass]map[string][]string{
		changesSignIn: {
			"RequirePasswordChange":     {"RequireCrewPasswordChange", "RequireCrewShipPasswordChange"},
			"RemovePassword":            {"RemoveCrewPassword", "RemoveCrewShipPassword"},
			"SendUserPasswordResetLink": {"SendCrewShipUserPasswordResetLink", "SendCrewUserPasswordResetLink"},
			"UnlinkSessionIdentity":     {"UnlinkCrewSessionIdentity", "UnlinkCrewShipSessionIdentity"},
			"RenameUser":                {"RenameCrewShipUser", "RenameCrewUser", "RenameMembersShipUser", "RenameMembersUser"},
			"SetUserEmail":              {"SetCrewShipUserEmail", "SetCrewUserEmail", "SetMembersShipUserEmail", "SetMembersUserEmail"},
		},
		changesAccess: wantAccessMethods,
	}
	for _, acts := range classed {
		for _, methods := range acts {
			slices.Sort(methods)
		}
	}
	if diff := cmp.Diff(want, classed); diff != "" {
		t.Errorf("the methods that carry a class (-want +got):\n%s", diff)
	}

	// An act listed and never written is a line nobody decides any more.
	for act := range identityMethodActs {
		if !seen[act] {
			t.Errorf("identityMethodActs lists %s, which the generated management does not write for the fixture: remove the line, or widen the fixture", act)
		}
	}
}

// wantAccessMethods are the methods of the fixture's two auths that carry the class
// access, by act: the whole application's and one tenant's of each, and a tenant's alone
// of the removal of a person from it.
var wantAccessMethods = map[string][]string{
	"AssignRole":            {"AssignCrewRole", "AssignCrewShipRole", "AssignMembersRole", "AssignMembersShipRole"},
	"RemoveRole":            {"RemoveCrewRole", "RemoveCrewShipRole", "RemoveMembersRole", "RemoveMembersShipRole"},
	"RemoveUser":            {"RemoveCrewShipUser", "RemoveMembersShipUser"},
	"CreateGroup":           {"CreateCrewGroup", "CreateCrewShipGroup", "CreateMembersGroup", "CreateMembersShipGroup"},
	"DeleteGroup":           {"DeleteCrewGroup", "DeleteCrewShipGroup", "DeleteMembersGroup", "DeleteMembersShipGroup"},
	"AddGroupMember":        {"AddCrewGroupMember", "AddCrewShipGroupMember", "AddMembersGroupMember", "AddMembersShipGroupMember"},
	"RemoveGroupMember":     {"RemoveCrewGroupMember", "RemoveCrewShipGroupMember", "RemoveMembersGroupMember", "RemoveMembersShipGroupMember"},
	"GiveGroupRole":         {"GiveCrewGroupRole", "GiveCrewShipGroupRole", "GiveMembersGroupRole", "GiveMembersShipGroupRole"},
	"TakeGroupRole":         {"TakeCrewGroupRole", "TakeCrewShipGroupRole", "TakeMembersGroupRole", "TakeMembersShipGroupRole"},
	"TakeRole":              {"TakeCrewRole", "TakeMembersRole"},
	"InviteUser":            {"InviteCrewShipUser", "InviteCrewUser"},
	"WithdrawInvitation":    {"WithdrawCrewInvitation", "WithdrawCrewShipInvitation"},
	"InviteTenant":          {"InviteCrewShip"},
	"WithdrawInvitedTenant": {"WithdrawCrewInvitedShip"},
	"OpenSignUp":            {"OpenCrewSignUp"},
	"MakeInviteOnly":        {"MakeCrewInviteOnly"},
	"SetSelfServiceReset":   {"SetCrewSelfServiceReset"},

	// The members' schema holds no sign-in settings table in the fixture.
	"RequireSecondFactor":       {"RequireCrewSecondFactor"},
	"StopRequiringSecondFactor": {"StopRequiringCrewSecondFactor"},
	"SetRememberedBrowsers":     {"SetCrewRememberedBrowsers"},
}
