package transition

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/app"
)

// The tests half of add tenancy. An application with tenants asks more of the two test
// harnesses than the base's give: the configurer answers the tenant roster and what each
// tenant declares, the auth is handed the generated maker, and the identity client is
// built over it. So the base's harnesses stop compiling the moment the application gains
// tenants, and with them every suite of the two packages. Where the application's
// harnesses are the base's still, the command writes them again with what the tenants
// need, so the application passes go vet and its own tests as it was made; where somebody
// has changed one, both are left as they are and the brief says what each builds.

// The base's tests of sign-up, which an application with tenants has no ground for: both
// are taken out where they are the base's still.
var tenancyRemoved = []removedSuite{
	{signUpSuite, "it proves sign-up in an application without tenants, where opening a sign-in method names the role each new person gets; an application with tenants opens a method with no role, and a person who signs up creates a tenant of their own"},
	{signUpRoleSuite, "it proves the list the role of a person who signs up is picked from, which the generator writes no more once the application has tenants"},
}

// tenancyBaseTests are the base's tests the command writes again or takes out: the two
// harnesses, and the two tests of sign-up, whose file also holds what the base's other
// suites share (the browser's first request, the mailed link, the invited person).
func tenancyBaseTests() []string {
	tests := slices.Clone(tenancyHarnesses)
	for _, suite := range tenancyRemoved {
		tests = append(tests, suite.file)
	}

	return tests
}

// baseTestsAuth names the auth the application's tests are the base's for: the auth
// impulse new rendered them with. It is empty where a harness or a test of sign-up is no
// longer what the base wrote, since the harness the command writes takes over what the
// other suites shared with the tests of sign-up, and a changed one would declare it twice.
func (tn Tenancy) baseTestsAuth(a *app.App) string {
	for i := range a.AuthPackages {
		auth := a.AuthPackages[i].Name
		if !slices.ContainsFunc(tenancyBaseTests(), func(rel string) bool {
			return !asTheBaseRenders(a, rel, auth)
		}) {
			return auth
		}
	}

	return ""
}

// writeSuites brings the tests the application carries from the base up to an application
// with tenants, where they are the base's still: the two harnesses and the test of the
// role files are written again, the two tests of sign-up are taken out, and the test of
// the way back in to an account opens sign-up as an application with tenants takes it. It
// returns the files it settled, so the change lists what is left of the base's tests alone.
func (tn Tenancy) writeSuites(a *app.App, ch *Change) (map[string]bool, error) {
	settled := map[string]bool{}
	if err := tn.writeHarnesses(a, ch, settled); err != nil {
		return nil, err
	}
	if err := tn.derivePasswordReset(a, ch, settled); err != nil {
		return nil, err
	}

	return settled, nil
}

// writeHarnesses writes the application's two test harnesses and its test of the role
// files for an application with tenants, in the place of the base's, and takes out the
// base's two tests of sign-up. It writes nothing where the harnesses build the roster
// already, as the ones a start with no password writes do, or where they are not the
// base's.
func (tn Tenancy) writeHarnesses(a *app.App, ch *Change, settled map[string]bool) error {
	if tn.harnessesBuilt(a) {
		return nil
	}
	auth := tn.baseTestsAuth(a)
	if auth == "" {
		return nil
	}
	data := &suiteData{Record: tn.Record(), OneTenant: tn.OneTenantPerPerson, Directories: !tn.NoDirectories, North: devTenantNorth}
	files, err := renderSuites(passwordTenantsSuites, data, modulePath(a), auth)
	if err != nil {
		return err
	}
	written := make([]string, 0, len(files))
	for rel := range files {
		if !slices.Contains(tenancyHarnesses, rel) && !asTheBaseRenders(a, rel, auth) {
			ch.skipf("%s: not as the base writes it, so it was left as it is: where it refuses a role held in a %s, hold that role's grants to the engine in a %s of the development seed instead, as the tenanted skeleton's test of the same name does", rel, tn.oneLower(), tn.oneLower())

			continue
		}
		written = append(written, rel)
	}
	sort.Strings(written)
	for _, rel := range written {
		if err := os.WriteFile(a.Abs(rel), files[rel], 0o644); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
		settled[rel] = true
	}
	ch.didf("%s: the base's tests written again for an application with %s, each proving what it proved: the authorization suite's harness builds the %s roster over the test client, adds the generated matrix's %s to it and builds the %s auth's identity client over the generated maker and the roles a %s's administrator holds; the integration harness applies the development seed beside the schema, builds the roster over the test database and starts it, hands the auth the maker and what each %s declares, and carries what the base's other tests shared with its tests of sign-up (the browser's first request, the mailed link, the invited person); and the test of the role files holds a role held in a %s to the engine in a %s of the seed", strings.Join(written, ", "), tn.manyLower(), tn.oneLower(), tn.oneLower(), auth, tn.oneLower(), tn.oneLower(), tn.oneLower(), tn.oneLower())
	gone := make([]string, 0, len(tenancyRemoved))
	for _, suite := range tenancyRemoved {
		if err := os.Remove(a.Abs(suite.file)); err != nil {
			return errors.Wrap(err, "os.Remove()")
		}
		settled[suite.file] = true
		gone = append(gone, fmt.Sprintf("%s (%s)", suite.file, suite.why))
	}
	ch.didf("the base's tests of what an application with %s does not have were taken out: %s", tn.manyLower(), strings.Join(gone, "; "))
	// The first administrator signs in as before, with the password the harness still
	// gives its logins, and the test asks nothing of a tenant, so it holds as it is.
	settled[firstAdministratorSuite] = true

	return nil
}

// derivePasswordReset writes the base's test of the way back in to an account for an
// application with tenants, from the application's own file. The base's test opens the
// password method to sign-up, to prove that the choice of a person's own reset stands
// through it, and names the role each new person gets as it does; an application with
// tenants opens a method with no role and refuses a request that names one. A file that
// names no such role is left as it is, and so is one that is not as the base writes it,
// which the change then says.
func (tn Tenancy) derivePasswordReset(a *app.App, ch *Change, settled map[string]bool) error {
	if !exists(a, passwordResetSuite) {
		return nil
	}
	src, mode, err := readFile(a, passwordResetSuite)
	if err != nil {
		return err
	}
	if !bytes.Contains(src, []byte("defaultRole")) {
		return nil
	}
	out, err := derivedPasswordResetTest(src)
	if err != nil {
		ch.skipf("%s: not as the base writes it (%v), so it was left as it is: where it opens a sign-in method to sign-up with a role (defaultRole), open it with none, since an application with %s refuses a request that names one", passwordResetSuite, errors.Cause(err), tn.manyLower())

		return nil
	}
	if err := os.WriteFile(a.Abs(passwordResetSuite), out, mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	settled[passwordResetSuite] = true
	ch.didf("%s: the test opens the password method to sign-up with no role where it named one (defaultRole), since an application with %s gives a person who signs up no role and refuses a request that names one; what it proves of the way back in to an account is as it was", passwordResetSuite, tn.manyLower())

	return nil
}

// derivedPasswordResetTest is the base's test of the way back in to an account as an
// application with tenants takes it: the method is opened to sign-up with no role, and
// the list of the sign-in methods is read back for the method being open alone.
func derivedPasswordResetTest(src []byte) ([]byte, error) {
	text := string(src)
	edits := []struct{ old, new string }{
		{
			"proves, on the served stack of an application without\n// tenants, how",
			"proves, on the served stack of an application with\n// tenants, how",
		},
		{
			"[]byte(`{\"name\":\"password\",\"defaultRole\":\"FeatureAdministrator\"}`)",
			"[]byte(`{\"name\":\"password\"}`)",
		},
		{
			"len(got) != 1 || !got[0].OpenSignUp || got[0].DefaultRole != \"FeatureAdministrator\" {",
			"len(got) != 1 || !got[0].OpenSignUp {",
		},
		{
			"want the password method still open with its role\"",
			"want the password method still open\"",
		},
	}
	for _, edit := range edits {
		if strings.Count(text, edit.old) != 1 {
			return nil, errors.Newf("%q is not there once", edit.old)
		}
		text = strings.Replace(text, edit.old, edit.new, 1)
	}

	return finishSource(passwordResetSuite, src, []byte(text))
}
