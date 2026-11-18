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

// identityMethodActs lists every method the generated management of an auth writes, by
// its act (the method's name without the auth's and the tenant record's), and says of
// each whether it changes how a person signs in. One that does carries the class
// credential (identityCredentialClass), so a permission mask that refuses the class
// refuses it.
//
// The list is the test's whole input: a method added to the generated management fails
// Test_prepareIdentity_credentialMethods until its act is listed here with a decision,
// and a method listed as a credential method fails it until its template carries the
// class. To change the set, change the line and the template together.
var identityMethodActs = map[string]bool{
	// A person's credentials: what they sign in with, and the username and the address
	// they sign in under or recover the account through.
	"SetTemporaryPassword":  true,
	"RequirePasswordChange": true,
	"RemovePassword":        true,
	"UnlinkSessionIdentity": true,
	"RenameUser":            true,
	"SetUserEmail":          true,

	// Whether a person has an account and may use it, and the name people read.
	"CreateUser":  false,
	"DeleteUser":  false,
	"DisableUser": false,
	"EnableUser":  false,
	"SetUserName": false,

	// What a person holds beside their credentials: sessions, application tokens,
	// remembered browsers, and the count of failed sign-ins kept under their name.
	"EndSession":               false,
	"EndSessionImpersonation":  false,
	"EndUserSessions":          false,
	"RevokeSessionToken":       false,
	"RevokeUserTrustedDevices": false,
	"UnlockSignIn":             false,

	// Who is let in: invitations, and whether a method is open to sign-up.
	"InviteUser":            false,
	"WithdrawInvitation":    false,
	"InviteTenant":          false,
	"WithdrawInvitedTenant": false,
	"OpenSignUp":            false,
	"MakeInviteOnly":        false,

	// What a person may do: roles, and the groups that hold them.
	"AssignRole":        false,
	"RemoveRole":        false,
	"CreateGroup":       false,
	"DescribeGroup":     false,
	"DeleteGroup":       false,
	"AddGroupMember":    false,
	"RemoveGroupMember": false,
	"GiveGroupRole":     false,
	"TakeGroupRole":     false,
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
// group of types, from the text after its @rpc annotation.
var identityMethodStruct = regexp.MustCompile(`(?m)^\s*(?:type\s+)?(\w+) struct \{`)

// Test_prepareIdentity_credentialMethods lists the methods the generated management
// writes and holds each to identityMethodActs: every method is listed there with a
// decision, the ones that change how a person signs in carry the class credential, and
// no other does.
func Test_prepareIdentity_credentialMethods(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, crewGroupTables, membersIdentityTables, membersGroupTables)...)
	r.resource = "pkg/resources"
	r.tableMap["Ships"] = shipsTable()
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": shipTenantSource})
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	classLine := strings.TrimPrefix(identityCredentialClass, "\n")
	credential := make(map[string][]string)
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

		wantsClass, listed := identityMethodActs[act]
		carries := strings.Contains(string(source), classLine+"\n")
		switch {
		case !listed:
			t.Errorf("%s (%s) is not listed in identityMethodActs: say whether it changes how a person signs in, and give its template the class if it does", method, file)
		case wantsClass && !carries:
			t.Errorf("%s (%s) changes how a person signs in and its source carries no %q: end its template's annotations with identityCredentialClass", method, file, classLine)
		case !wantsClass && carries:
			t.Errorf("%s (%s) carries %q and is not listed as a credential method in identityMethodActs", method, file, classLine)
		}
		if carries {
			credential[act] = append(credential[act], method)
		}
	}

	// The credential methods the fixture's two auths write: the whole application's and
	// one tenant's of each act. The members sign in through a directory alone, so they
	// hold no password to set, require or remove.
	want := map[string][]string{
		"SetTemporaryPassword":  {"SetCrewShipTemporaryPassword", "SetCrewTemporaryPassword"},
		"RequirePasswordChange": {"RequireCrewPasswordChange", "RequireCrewShipPasswordChange"},
		"RemovePassword":        {"RemoveCrewPassword", "RemoveCrewShipPassword"},
		"UnlinkSessionIdentity": {"UnlinkCrewSessionIdentity", "UnlinkCrewShipSessionIdentity"},
		"RenameUser":            {"RenameCrewShipUser", "RenameCrewUser", "RenameMembersShipUser", "RenameMembersUser"},
		"SetUserEmail":          {"SetCrewShipUserEmail", "SetCrewUserEmail", "SetMembersShipUserEmail", "SetMembersUserEmail"},
	}
	for _, methods := range credential {
		slices.Sort(methods)
	}

	if diff := cmp.Diff(want, credential); diff != "" {
		t.Errorf("the methods that carry the class %s (-want +got):\n%s", accesstypes.ClassCredential, diff)
	}

	// An act listed and never written is a line nobody decides any more.
	for act := range identityMethodActs {
		if !seen[act] {
			t.Errorf("identityMethodActs lists %s, which the generated management does not write for the fixture: remove the line, or widen the fixture", act)
		}
	}
}
