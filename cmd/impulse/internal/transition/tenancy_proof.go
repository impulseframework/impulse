package transition

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/ettle/strcase"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/app"
)

// The proof half of the roles add tenancy writes. The tenanted reference application
// holds each grant that changes a person, or shows more of one, to a condition, which
// keeps a tenant's administrator from an account the tenant does not manage; impulse
// check holds every conditional grant to a test that proves it over seeded rows
// (conditions-proven). So the role is written with those grants only where the test
// that proves them is written with it: the reference's own test of a tenant's
// people, in the application's names, signing people in the way the application's other
// tests do. The test stands on the harness the command writes, so it is written where
// that harness is the command's still and the conditions are the ones the test proves;
// anywhere else the role is written without the conditional grants, and the change says
// which they are and why the test was not written.

// The tests of the integration suite the proof is written among or stands on.
const (
	tenantPeopleTest  = integrationSuite + "/tenant_people_test.go"
	grantsSuite       = integrationSuite + "/grants_test.go"
	secondFactorSuite = integrationSuite + "/second_factor_test.go"
	directorySignIn   = integrationSuite + "/sign_in_test.go"
)

// conditionalGrant is a grant the role a tenant's administrator holds carries under a
// condition, in the application's names.
type conditionalGrant struct {
	permission string
	grant      access.Grant
}

// String names the grant as a line of the change does.
func (c conditionalGrant) String() string {
	return fmt.Sprintf("%s on %s under %q", c.permission, c.grant.Resource, c.grant.Condition)
}

// provenGrant is a conditional grant as a test names the one it proves: the coordinates
// impulse check reads from a provesGrant call.
type provenGrant struct {
	role, permission, resource, condition string
}

// String names the grant as a line of the change does.
func (p provenGrant) String() string {
	return fmt.Sprintf("%s on %s under %q", p.permission, p.resource, p.condition)
}

// conditionalGrants lists a role's grants that hold under a condition, in the file's order.
func conditionalGrants(role *roleEntry) []conditionalGrant {
	var conditional []conditionalGrant
	for i := range role.Permissions {
		for _, grant := range role.Permissions[i].Grants {
			if grant.Condition != "" {
				conditional = append(conditional, conditionalGrant{permission: role.Permissions[i].Permission, grant: grant})
			}
		}
	}

	return conditional
}

// withoutConditions are a role's grants that hold under no condition, in the file's order.
func withoutConditions(permissions rolePermissions) rolePermissions {
	free := rolePermissions{}
	for i := range permissions {
		for _, grant := range permissions[i].Grants {
			if grant.Condition == "" {
				free.add(permissions[i].Permission, grant)
			}
		}
	}

	return free
}

// authHasPassword reports whether the auth of a package registers the password method.
func authHasPassword(a *app.App, pkg *app.AuthPackage) bool {
	for i := range a.Auths {
		if path.Dir(a.Auths[i].File) != pkg.Dir {
			continue
		}
		for j := range a.Auths[i].Methods {
			if a.Auths[i].Methods[j].Kind == app.MethodPassword {
				return true
			}
		}
	}

	return false
}

// peopleProof renders the test that proves the conditions on the grants of the role a
// tenant's administrator holds, for the auth whose people make tenants. It returns the
// files the proof is written as, each by its path: the test, and the harness it stands on
// as the command writes it beside the test, whose provesGrant says to the application's
// lint that every call names one role. It returns no file, and why in words a line of the
// change carries, where the command cannot write a test that holds as the application is:
// a test of that name is there already, the tests it stands on are not the ones the
// command writes, the conditions are not the ones it proves, or another test of the
// package declares one of its names.
func (tn Tenancy) peopleProof(a *app.App, seat *app.AuthPackage, conditional []conditionalGrant) (proof map[string][]byte, why string, err error) {
	if exists(a, tenantPeopleTest) {
		return nil, fmt.Sprintf("%s is there already, and the command writes over nobody's test", tenantPeopleTest), nil
	}
	password := authHasPassword(a, seat)
	if why, err := tn.proofStands(a, seat.Name, password); why != "" || err != nil {
		return nil, why, err
	}
	data := &suiteData{
		Record: tn.Record(), OneTenant: tn.OneTenantPerPerson, Kebab: strcase.ToKebab(tn.Record()), Segment: tn.Segment(),
		South: devTenantSouth, Password: password,
	}
	files, err := renderSuites(tenantPeopleSuites, data, modulePath(a), seat.Name)
	if err != nil {
		return nil, "", err
	}
	test := files[tenantPeopleTest]
	proven, declared, err := readProof(test)
	if err != nil {
		return nil, "", err
	}
	if why := tn.unprovenWords(conditional, proven); why != "" {
		return nil, why, nil
	}
	if why := declaredAlready(a, declared); why != "" {
		return nil, why, nil
	}
	set, _ := tn.proofSuites(seat.Name, password)
	beside, err := tn.standing(a, set, seat.Name, true)
	if err != nil {
		return nil, "", err
	}

	return map[string][]byte{tenantPeopleTest: test, integrationHarness: beside[integrationHarness]}, "", nil
}

// proofSuites names the set of tests the proof stands on, by the sign-in the auth has: the
// ones add tenancy writes for a password sign-in, or the ones the start whose people sign
// in through their tenant's directory writes. It names none, and says why, for an auth
// with neither.
func (tn Tenancy) proofSuites(auth string, password bool) (set, why string) {
	switch {
	case password:
		return passwordTenantsSuites, ""
	case tn.NoDirectories:
		return "", fmt.Sprintf("the %s auth has no password sign-in and no %s has a directory, and the command writes no tests that sign a person in to such an application", auth, tn.oneLower())
	}

	return byDirectorySuites, ""
}

// standing renders the tests of a set that the proof stands on, each by its path, as the
// command writes them for the auth: the harness that serves the stack, the test whose
// helper waits for the engine, and the helpers that sign a person in. With proven they
// are as the command writes them beside the proof.
func (tn Tenancy) standing(a *app.App, set, auth string, proven bool) (map[string][]byte, error) {
	module := modulePath(a)
	data := &suiteData{Record: tn.Record(), OneTenant: tn.OneTenantPerPerson, Directories: !tn.NoDirectories, North: devTenantNorth, Proven: proven}
	files, err := renderSuites(set, data, module, auth)
	if err != nil {
		return nil, err
	}
	stands := map[string][]byte{integrationHarness: files[integrationHarness], grantsSuite: files[grantsSuite]}
	if set == passwordTenantsSuites {
		// The base's second factor signs a person in with the password the harness gives.
		secondFactor, err := baseRendering(secondFactorSuite, module, auth)
		if err != nil {
			return nil, err
		}
		stands[secondFactorSuite] = secondFactor

		return stands, nil
	}
	for _, rel := range []string{secondFactorSuite, simulatedSignIn, directorySignIn} {
		stands[rel] = files[rel]
	}

	return stands, nil
}

// proofStands reports, in words, the first test the proof stands on that is not in the
// application as the command writes it for the auth. It answers nothing where each is the
// command's, byte for byte, so the proof compiles beside them and signs people in as they
// do. A test that is there and cannot be read stops the command with the error.
func (tn Tenancy) proofStands(a *app.App, auth string, password bool) (why string, err error) {
	set, why := tn.proofSuites(auth, password)
	if why != "" {
		return why, nil
	}
	stands, err := tn.standing(a, set, auth, false)
	if err != nil {
		return "", err
	}
	tests := make([]string, 0, len(stands))
	for rel := range stands {
		tests = append(tests, rel)
	}
	sort.Strings(tests)
	for _, rel := range tests {
		var held []byte
		if exists(a, rel) {
			if held, _, err = readFile(a, rel); err != nil {
				return "", err
			}
		}
		if !bytes.Equal(held, stands[rel]) {
			return fmt.Sprintf("%s is not as the command writes it for the %s auth, and the test that proves the conditions serves the application, waits for the engine and signs people in through it", rel, auth), nil
		}
	}

	return "", nil
}

// readProof reads the test the command renders: the grants its provesGrant calls name,
// as impulse check reads them, and the names it declares at the top level.
func readProof(src []byte) (proven []provenGrant, declared []string, err error) {
	file, err := parser.ParseFile(token.NewFileSet(), tenantPeopleTest, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "parser.ParseFile(): %s", tenantPeopleTest)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || err != nil {
			return err == nil
		}
		if fn, ok := call.Fun.(*ast.Ident); !ok || fn.Name != "provesGrant" || len(call.Args) != 6 {
			return true
		}
		var coordinates [4]string
		for i, arg := range call.Args[2:] {
			lit, ok := arg.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				err = errors.Newf("%s: a provesGrant call names a grant by something other than four literals", tenantPeopleTest)

				return false
			}
			text, unquoteErr := strconv.Unquote(lit.Value)
			if unquoteErr != nil {
				err = errors.Wrapf(unquoteErr, "strconv.Unquote(): %s", lit.Value)

				return false
			}
			coordinates[i] = text
		}
		proven = append(proven, provenGrant{role: coordinates[0], permission: coordinates[1], resource: coordinates[2], condition: coordinates[3]})

		return true
	})
	if err != nil {
		return nil, nil, err
	}

	return proven, topLevelNames(file), nil
}

// topLevelNames lists what a Go file declares in its package's scope: its functions, its
// constants and variables, and its types. A method is in its type's scope, not the
// package's, and is not listed.
func topLevelNames(file *ast.File) []string {
	var declared []string
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Recv == nil {
				declared = append(declared, decl.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						declared = append(declared, name.Name)
					}
				case *ast.TypeSpec:
					declared = append(declared, spec.Name.Name)
				}
			}
		}
	}

	return declared
}

// unprovenWords says, where the conditional grants the role would carry are not the ones
// the test proves, which differ: a grant the test does not prove is one impulse check
// refuses, and a proof of a grant the role does not carry fails as the test runs. An
// application whose auth sends no mail, or whose management somebody cut down, serves
// less than the test proves. It answers nothing where the two are the same.
func (tn Tenancy) unprovenWords(conditional []conditionalGrant, proven []provenGrant) string {
	role := tn.AdministratorRole()
	proofs := map[provenGrant]bool{}
	for _, p := range proven {
		proofs[p] = true
	}
	var unproven, uncarried []string
	for _, c := range conditional {
		p := provenGrant{role: role, permission: c.permission, resource: string(c.grant.Resource), condition: c.grant.Condition}
		if !proofs[p] {
			unproven = append(unproven, c.String())
		}
		delete(proofs, p)
	}
	for _, p := range proven {
		if proofs[p] {
			uncarried = append(uncarried, p.String())
		}
	}
	var differ []string
	if len(unproven) > 0 {
		differ = append(differ, "the role would carry "+strings.Join(unproven, ", ")+", which the test does not prove")
	}
	if len(uncarried) > 0 {
		differ = append(differ, "the test proves "+strings.Join(uncarried, ", ")+", which the role would not carry")
	}
	if len(differ) == 0 {
		return ""
	}

	return "the test the command writes proves the conditions of the tenanted skeleton's role, and they are not this application's: " + strings.Join(differ, "; ")
}

// declaredAlready says which name of the proof another test of the integration suite
// declares already, and where: written beside it, the test would not compile, and
// neither would any other test of the package. It answers nothing where no name is.
func declaredAlready(a *app.App, declared []string) string {
	entries, err := os.ReadDir(a.Abs(integrationSuite))
	if err != nil {
		return ""
	}
	ours := map[string]bool{}
	for _, name := range declared {
		ours[name] = true
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		rel := path.Join(integrationSuite, entry.Name())
		src, err := os.ReadFile(a.Abs(rel))
		if err != nil {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, name := range topLevelNames(file) {
			if ours[name] {
				return fmt.Sprintf("%s declares %s, a name the test that proves the conditions declares too, so the two would not compile together", rel, name)
			}
		}
	}

	return ""
}
