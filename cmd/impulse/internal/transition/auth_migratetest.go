package transition

import (
	"fmt"
	"go/format"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
)

// The anchors of the base's migrate test that a second inviting auth changes: the line a
// release prints when somebody holds the role, and the preparation that gives the first
// auth a holder.
var (
	roleHeldRE   = regexp.MustCompile(`(?m)^(\troleHeld\s+= "[^"\n]*)"$`)
	withHolderRE = regexp.MustCompile(`(?s)(\nfunc withHolder\(t \*testing\.T, db \*initiator\.SpannerDB\) \{\n.*?)(\n\}\n)`)
)

// holdMigrateTest brings the migrate command's test to a release that invites the added
// auth's first administrator too, so the application the command leaves passes its own
// tests. The test is the application's own file, changed where it is as the command
// writes it and nowhere else. The base's test gives the first auth a holder of the Super
// Administrator role in the cases whose release goes to its end; those cases now give the
// added auth a holder too, and expect the line a release prints for each auth. The test
// of a start whose directory gives every role proves that a release invites nobody and
// that the command has no flag for a fresh invitation; a release now stops for the added
// auth's first administrator, and the flag takes that auth's name alone, so the two
// cases say that, and the first names the added auth's package, which the test then
// imports; the base's cases reach the added auth through the application's data alone and
// import nothing more. It returns what it did, or nothing where the test is neither.
func (au Auth) holdMigrateTest(a *app.App, src *authSource) (did string, err error) {
	data, mode, err := readFile(a, migrateTest)
	if err != nil {
		return "", err
	}
	text := string(data)
	var out string
	var named bool
	switch {
	case roleHeldRE.MatchString(text) && withHolderRE.MatchString(text):
		out, did = au.holderCases(text, src)
	default:
		out, did = au.directoryCases(text, src)
		named = true
	}
	if did == "" {
		return "", nil
	}
	written := []byte(out)
	if named {
		written, err = app.AddImport(migrateTest, written, path.Join(modulePath(a), src.AuthDir, au.Name))
		if err != nil {
			return "", err
		}
	}
	formatted, err := format.Source(written)
	if err != nil {
		return "", errors.Wrap(err, "format.Source()")
	}
	if err := os.WriteFile(a.Abs(migrateTest), formatted, mode); err != nil {
		return "", errors.Wrap(err, "os.WriteFile()")
	}

	return did, nil
}

// holderCases is the base's test with a holder of the role in the added auth wherever a
// case gives the first auth one, or nothing where the test names the first auth's holder
// some other way.
func (au Auth) holderCases(text string, src *authSource) (out, did string) {
	held := "Somebody holds the Super Administrator role in the %s auth: no first administrator invited\\n"
	anchors := []string{
		fmt.Sprintf(held, src.Name) + `"`,
		fmt.Sprintf("spanner.Insert(%q, ", src.Pascal+"SessionUsers"),
		fmt.Sprintf("openData(t).%s().Access().UserManager()", src.Pascal),
		"accesstypes.User(someone), accesstypes.SuperAdministrator)",
	}
	for _, anchor := range anchors {
		if !strings.Contains(text, anchor) {
			return "", ""
		}
	}
	helper := "with" + au.Pascal() + "Holder"
	text = roleHeldRE.ReplaceAllString(text, "${1}"+fmt.Sprintf(held, au.Name)+`"`)
	text = withHolderRE.ReplaceAllString(text, "${1}\n\t"+helper+"(t, db)${2}")
	text = strings.TrimRight(text, "\n") + fmt.Sprintf(`

// %[1]s gives the %[2]s auth one user who holds the Super Administrator role, on the
// database withHolder migrated: somebody administers that auth too, so a release invites
// nobody to it and goes to its end.
func %[1]s(t *testing.T, db *initiator.SpannerDB) {
	t.Helper()

	mutation := spanner.Insert(%[3]q, []string{"Id", "Username", "CreatedAt"}, []any{someone, "someone", spanner.CommitTimestamp})
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{mutation}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %%v", err)
	}
	manager := openData(t).%[4]s().Access().UserManager()
	if err := manager.AddUserRoles(t.Context(), accesstypes.GlobalPolicyScope(), accesstypes.User(someone), accesstypes.SuperAdministrator); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles() error = %%v", err)
	}
}
`, helper, au.Name, au.Pascal()+"SessionUsers", au.Pascal())

	return text, fmt.Sprintf("%s: the cases that give the %s auth a holder of the Super Administrator role give the %s auth one too (%s), and expect the line a release prints for each auth, since a release now invites the %s auth's first administrator and stops while nobody of the auth holds the role and %s names no address", migrateTest, src.Name, au.Name, helper, au.Name, authpkg.AdministratorVariable(au.Name))
}

// directoryCases is the test of a start whose directory gives every role with its two
// cases about the first administrator said for an application that now has an auth that
// invites one, or nothing where the test does not hold the two cases as the start wrote
// them.
func (au Auth) directoryCases(text string, src *authSource) (out, did string) {
	edits := []struct{ old, new string }{
		{
			fmt.Sprintf("\t\t\tname:      \"the migrations with the seed apply every schema and data migration and invite nobody: the %s auth's directory gives every role\",\n\t\t\targs:      []string{\"-seed\"},\n", src.Name),
			fmt.Sprintf("\t\t\tname:      \"the migrations with the seed apply every schema and data migration; nobody is invited to the %[1]s auth, whose directory gives every role, and the release stops for the %[2]s auth's first administrator, whom nobody names\",\n\t\t\targs:      []string{\"-seed\"},\n\t\t\twantCode:  exitFailed,\n\t\t\twantErr:   \"no first administrator is named\",\n\t\t\twantAlso:  []string{%[2]s.AdministratorVariable},\n", src.Name, au.Name),
		},
		{
			fmt.Sprintf("\t\t\tname:     \"a fresh invitation is no flag of the command: nobody is invited to the %[1]s auth\",\n\t\t\tmissing:  true,\n\t\t\targs:     []string{\"-reinvite-administrator\", %[1]s.Name},\n\t\t\twantCode: exitRefused,\n\t\t\twantErr:  \"flag provided but not defined: -reinvite-administrator\",\n", src.Name),
			fmt.Sprintf("\t\t\tname:     \"a fresh invitation goes to an auth that invites a first administrator: the %[1]s auth, whose directory gives every role, is refused by name\",\n\t\t\tmissing:  true,\n\t\t\targs:     []string{\"-reinvite-administrator\", %[1]s.Name},\n\t\t\twantCode: exitRefused,\n\t\t\twantErr:  \"no auth of that name invites a first administrator; name one of %[2]s\",\n", src.Name, au.Name),
		},
	}
	for _, edit := range edits {
		if strings.Count(text, edit.old) != 1 {
			return "", ""
		}
		text = strings.Replace(text, edit.old, edit.new, 1)
	}
	text = strings.Replace(text,
		"so a\n// release invites no first administrator and asks for no mailbox on that account, and\n// the command has no -reinvite-administrator flag.",
		fmt.Sprintf("so a\n// release invites no first administrator of it. The %s auth invites one: a release stops\n// until an address is named for it or somebody of it holds the Super Administrator role,\n// and -reinvite-administrator takes that auth's name alone.", au.Name), 1)

	return text, fmt.Sprintf("%s: the case that proved a release invites nobody now proves it stops for the %s auth's first administrator while %s names no address, and the case that proved the command has no -reinvite-administrator flag now proves the flag refuses the %s auth's name, since the %s auth invites a first administrator and the %s auth's directory gives every role", migrateTest, au.Name, authpkg.AdministratorVariable(au.Name), src.Name, au.Name, src.Name)
}
