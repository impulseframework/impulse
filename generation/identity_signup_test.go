package generation

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// signUpFiles are the files of the sign-up part of the crew's management, by path from
// the identity tree's root.
func signUpFiles(t *testing.T) []string {
	t.Helper()

	var got []string
	for _, file := range identityTreeFiles(t, "pkg/identity") {
		for _, part := range []string{"sign_in_methods", "sign_up", "invite_only"} {
			if strings.Contains(file, part) {
				got = append(got, file)

				break
			}
		}
	}

	return got
}

// holdsRoleGrants reports whether the crew's package was written the role file's answers
// on giving a role and taking one away (grants.go).
func holdsRoleGrants(t *testing.T) bool {
	t.Helper()

	return slices.Contains(identityTreeFiles(t, "pkg/identity"), "crewidentity/grants.go")
}

// assertFileContains fails for every line the file under the identity tree lacks, and
// for every line it holds that it must not.
func assertFileContains(t *testing.T, file string, want, wantNot []string) {
	t.Helper()

	content, err := os.ReadFile(filepath.Join("pkg", "identity", file))
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", file, err)
	}
	for _, line := range want {
		if !strings.Contains(string(content), line) {
			t.Errorf("%s lacks %q:\n%s", file, line, content)
		}
	}
	for _, line := range wantNot {
		if strings.Contains(string(content), line) {
			t.Errorf("%s holds %q:\n%s", file, line, content)
		}
	}
}

// Test_prepareIdentity_signUp pins the sign-up part of an auth's management in an
// application without tenants: the declaration over the sign-in methods table, which
// serves no route and keeps a change event for every write; the list of the auth's
// methods; the roles a default role is picked from; the method that opens one, which
// declares its warning and the word a person types, and the method that makes one
// invite only again; the store's two calls; and the hook that gives the default role.
// The default role is one the opener could give a person directly, so the package
// answers the role file's grants on giving a role though the auth keeps no groups, and
// the client hands them to the rules. An auth whose directory writes its roles has the
// list alone, whose rows say why nothing is chosen there: the Sign-in page is every
// auth's, for what every sign-in asks for (identity_signinsettings_test.go).
func Test_prepareIdentity_signUp(t *testing.T) {
	// The generator writes relative to where the program runs, and the working directory
	// belongs to the whole process: no t.Parallel here.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables, []string{"MembersSessionSignInMethods"})...)
	r.extraOutlets[1].auth = &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	want := []string{
		"crewidentity/crew_sign_in_methods.go",
		"crewidentity/crew_sign_up_roles.go",
		"crewidentity/make_crew_invite_only.go",
		"crewidentity/open_crew_sign_up.go",
		"internal/crewresources/crew_session_sign_in_methods.go",
		// The table is declared for the members too, since the schema holds it, and the
		// list is drawn; their directory writes the roles, so nothing chooses over it.
		"internal/membersresources/members_session_sign_in_methods.go",
		"membersidentity/members_sign_in_methods.go",
	}
	if diff := cmp.Diff(want, signUpFiles(t)); diff != "" {
		t.Errorf("the sources of the sign-up part (-want +got):\n%s", diff)
	}

	for file, lines := range map[string][]string{
		// The table's rows are written by the two methods alone: the declaration serves
		// no route, and every write leaves its change event.
		"internal/crewresources/crew_session_sign_in_methods.go": {
			generationHeader, "// @resource\n\t// @suppress(listHandler)\n\t// @suppress(readHandler)\n\t// @suppress(patchHandler)\n\t// @outlet(default)\n\t// @rowWord(way to sign in, plural: ways to sign in)\n\tCrewSessionSignInMethod struct {",
			"Name       string  `spanner:\"Name\"`", "OpenSignUp bool    `spanner:\"OpenSignUp\"`", "OpenedFor  *string `spanner:\"OpenedFor\"`",
			"DefaultRole      *string   `spanner:\"DefaultRole\"`", "SelfServiceReset bool      `spanner:\"SelfServiceReset\"`",
			"func (CrewSessionSignInMethod) Config() resource.Config {\n\treturn defaultConfig().SetTrackChanges(true)\n}",
		},
		"crewidentity/crew_sign_in_methods.go": {
			generationHeader, "// @computed\n// @suppress(readHandler)\n// @outlet(default)\n// @order(Label asc)\n// @rowWord(way to sign in, plural: ways to sign in)\ntype CrewSignInMethod struct {",
			"// @primarykey\n\tName ", "DirectoryGivesRoles bool", "OwnRule             bool", "OwnRule: methods[i].OwnRule", `return "CrewSignInMethods"`,
			"func ListCrewSignInMethod(ctx context.Context, _ *resource.QuerySet[CrewSignInMethod], reader resource.Client, client *Client) iter.Seq2[*CrewSignInMethod, error] {",
			"client.manager.SignInMethods(ctx, reader)",
		},
		"crewidentity/crew_sign_up_roles.go": {
			generationHeader, "// @computed\n// @suppress(readHandler)\n// @outlet(default)\n// @order(Name asc)\n// @rowWord(sign-up role)\ntype CrewSignUpRole struct {",
			"// @primarykey\n\t// @attribute(name)\n\t// @roleOf(Crew)\n\tName ", "Description string", `return "CrewSignUpRoles"`,
			// The list asks with the context its frame hands it, which carries the
			// request's caller (Test_computedResourceHandlerTemplate_caller): it lists
			// the roles that caller may give.
			"func ListCrewSignUpRole(ctx context.Context, _ *resource.QuerySet[CrewSignUpRole], _ resource.Client, client *Client) iter.Seq2[*CrewSignUpRole, error] {",
			"client.manager.SignUpRoles(ctx)",
		},
		// Opening a method declares the warning and the word; the default role is picked
		// from the roles a person who signs up may be given, and is not required of the
		// request that asks for the warning.
		"crewidentity/open_crew_sign_up.go": {
			generationHeader, "// @rpc\n// @outlet(default)\n// @answers(204)\n// @warning(typed: open)\n// @class(access)\ntype OpenCrewSignUp struct {",
			"Name string `validate:\"required\"`", "// @enumerate(CrewSignUpRoles)\n\tDefaultRole string\n",
			"func (m *OpenCrewSignUp) Warning(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) (*resource.Warning, error) {",
			"client.manager.OpenSignUpWarning(ctx, txn, m.signUp())", "client.manager.OpenSignUp(ctx, txn, m.signUp())",
			"return &identity.SignUp{Method: m.Name, DefaultRole: accesstypes.Role(m.DefaultRole)}",
		},
		"crewidentity/make_crew_invite_only.go": {
			generationHeader, "// @rpc\n// @outlet(default)\n// @answers(204)\n// @class(access)\ntype MakeCrewInviteOnly struct {", "Name string `validate:\"required\"`",
			"client.manager.MakeInviteOnly(ctx, txn, m.Name)",
		},
		"crewidentity/store.go": {
			"func (store) SetSelfServiceReset(ctx context.Context, txn resource.ReadWriteTransaction, name string, on bool) error {",
			"crewresources.NewCrewSessionSignInMethodUpdatePatch(name).SetSelfServiceReset(on).SetUpdatedAt(spanner.CommitTimestamp)",
			"crewresources.NewCrewSessionSignInMethodCreatePatch(name).SetOpenSignUp(false).SetSelfServiceReset(on).SetUpdatedAt(spanner.CommitTimestamp)",
			"SelfServiceReset: row.Data.SelfServiceReset,",
			"func (store) SignInMethodRows(ctx context.Context, reader resource.ReadOnlyTransaction) ([]identity.SignInMethodRow, error) {",
			"func (store) SetSignInMethod(ctx context.Context, txn resource.ReadWriteTransaction, row *identity.SignInMethodRow) error {",
			// Either write is the generated patch's, buffered under the caller, which is
			// what leaves the change event.
			"patch.Buffer(ctx, txn, resource.UserEvent(ctx))",
			// The row is read with the audience it was opened for, and each of the two
			// writes, of a row that exists and of one that does not, writes it.
			"Columns().Name().OpenSignUp().OpenedFor().DefaultRole().SelfServiceReset())", "OpenedFor: row.Data.OpenedFor",
			"crewresources.NewCrewSessionSignInMethodUpdatePatch(row.Name).\n\t\t\tSetOpenSignUp(row.OpenSignUp).SetOpenedFor(row.OpenedFor).SetDefaultRole(row.DefaultRole)",
			"crewresources.NewCrewSessionSignInMethodCreatePatch(row.Name).\n\t\tSetOpenSignUp(row.OpenSignUp).SetOpenedFor(row.OpenedFor).SetDefaultRole(row.DefaultRole)",
		},
		// The default role is judged by the grant of the method that gives a role: the
		// package answers it, and the client hands the answers to the rules.
		"crewidentity/grants.go": {
			generationHeader, "var _ identity.RoleGrants = grants{}",
			"caller.ExecuteGate(ctx, AssignCrewRole{}.Method(), &AssignCrewRole{UserID: userID, Role: string(role)})",
		},
		"crewidentity/client.go": {
			"func (c *Client) SignedUp() session.SignUpSeatedHook[*spanner.ReadWriteTransaction] {", `return c.manager.SignedUp("Crew")`,
			"func (c *Client) AnySignInMethodOpen(ctx context.Context, reader resource.ReadOnlyTransaction) (bool, error) {", "c.manager.AnyMethodOpen(ctx, reader)",
			"opts = append(opts, identity.WithRoleGrants(grants{}))",
		},
	} {
		assertFileContains(t, file, lines, nil)
	}
	// The method that makes a method invite only declares no warning: it admits nobody.
	assertFileContains(t, "crewidentity/make_crew_invite_only.go", nil, []string{"@warning", "Warning("})
	// The members' directory writes the roles: nobody signs up to a role of the
	// application's choosing, so the client answers no hook and asks nothing.
	assertFileContains(t, "membersidentity/client.go", nil, []string{"SignedUp", "AnySignInMethodOpen"})
}

// Test_prepareIdentity_signUpWithTenants pins the part in an application with a tenant
// record: a person who signs up creates a tenant of their own and holds no role of the
// whole application, so opening a method names no default role and there is no list to
// pick one from; and the one auth whose people create a tenant carries the choice.
func Test_prepareIdentity_signUpWithTenants(t *testing.T) {
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables, []string{"MembersSessionSignInMethods"})...)
	r.resource = "pkg/resources"
	r.tableMap["Ships"] = shipsTable()
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": shipTenantSource})
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	// The tenant record is served on the default outlet, the crew's: a person who signs
	// up to the crew creates a ship of their own, so the crew carries the choice. The
	// members sign in on the portal, where no ship is created, and the application
	// assigns their roles in a ship: a person who signed up there would get nothing, so
	// the members carry no choice, though their schema holds the table: they have the
	// list alone.
	want := []string{
		"crewidentity/crew_sign_in_methods.go",
		"crewidentity/make_crew_invite_only.go",
		"crewidentity/open_crew_sign_up.go",
		"internal/crewresources/crew_session_sign_in_methods.go",
		"internal/membersresources/members_session_sign_in_methods.go",
		"membersidentity/members_sign_in_methods.go",
	}
	if diff := cmp.Diff(want, signUpFiles(t)); diff != "" {
		t.Errorf("the sources of the sign-up part (-want +got):\n%s", diff)
	}
	assertFileContains(t, "crewidentity/open_crew_sign_up.go",
		[]string{
			"// @warning(typed: open)\n// @class(access)\ntype OpenCrewSignUp struct {", "Name string `validate:\"required\"`\n}",
			"return &identity.SignUp{Method: m.Name, CreatesTenant: true}", "they create a ship of their own",
		},
		[]string{"DefaultRole", "accesstypes"},
	)
	// No default role is picked and the auth keeps no groups, and the crew's roles in a
	// ship are given by hand: the list a role is picked from asks the role file which
	// roles the caller could give, so the package answers and the client hands it on.
	if !holdsRoleGrants(t) {
		t.Error("the crew's package was written no grants.go, want one where a tenant's role is picked from a list: the list asks which roles the caller could give")
	}
	assertFileContains(t, "crewidentity/client.go", []string{"identity.WithRoleGrants(grants{})"}, nil)
	assertFileContains(t, "crewidentity/grants.go", []string{"case AssignCrewShipRole{}.Method():\n\t\t\treturn couldGiveBy(ctx, caller, method, &AssignCrewShipRole{Role: string(role)})"}, []string{"GroupRole"})
}

// Test_prepareIdentity_ownResetWithoutTheSignUpChoice pins an auth that declares the
// password method and carries no choice of who may sign up: its people join a tenant
// somebody else made, and they have passwords to reset like anybody's. The management
// carries the list of the auth's sign-in methods and the switch for a person's own
// reset, and neither method of the other choice.
func Test_prepareIdentity_ownResetWithoutTheSignUpChoice(t *testing.T) {
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables, []string{"MembersSessionSignInMethods"})...)
	r.resource = "pkg/resources"
	r.tableMap["Ships"] = shipsTable()
	r.extraOutlets[1].auth = &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{PasswordMethod(), AzureMethod("directory").Labeled("Company directory")}}
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": shipTenantSource})
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	var got []string
	for _, file := range identityTreeFiles(t, "pkg/identity") {
		if !strings.HasPrefix(file, "membersidentity/") {
			continue
		}
		for _, part := range []string{"sign_in_methods", "sign_up", "invite_only", "self_service_reset"} {
			if strings.Contains(file, part) {
				got = append(got, file)

				break
			}
		}
	}
	want := []string{"membersidentity/members_sign_in_methods.go", "membersidentity/set_members_self_service_reset.go"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the members' sources of the Sign-in page (-want +got):\n%s", diff)
	}
	assertFileContains(t, "membersidentity/set_members_self_service_reset.go",
		[]string{"type SetMembersSelfServiceReset struct {", "client.manager.SetSelfServiceReset(ctx, txn, m.On)"}, nil)
	assertFileContains(t, "membersidentity/members_sign_in_methods.go",
		[]string{"type MembersSignInMethod struct {", "client.manager.SignInMethods(ctx, reader)", "SelfServiceReset: methods[i].SelfServiceReset,"}, nil)
	assertFileContains(t, "membersidentity/store.go",
		[]string{"membersresources.NewMembersSessionSignInMethodUpdatePatch(name).SetSelfServiceReset(on).SetUpdatedAt(spanner.CommitTimestamp)"}, nil)
	// The choice of who may sign up is still the crew's alone.
	for _, file := range []string{"membersidentity/open_members_sign_up.go", "membersidentity/make_members_invite_only.go"} {
		if slices.Contains(identityTreeFiles(t, "pkg/identity"), file) {
			t.Errorf("the members' management holds %s, want none: the members carry no choice of who may sign up", file)
		}
	}
}

// Test_prepareIdentity_signUpWithoutTheTable pins an auth whose schema holds no sign-in
// methods table: every method is invite only and nothing chooses otherwise, so the
// management carries no list and no method, and the store, which still stands for
// identity.Store, answers no rows and refuses a write saying which table is missing. The
// role file's grants are still answered, for the list the roles given by hand are picked
// from.
func Test_prepareIdentity_signUpWithoutTheTable(t *testing.T) {
	t.Chdir(t.TempDir())

	tables := slices.DeleteFunc(slices.Concat(crewEveryTable, membersIdentityTables), func(table string) bool {
		return table == "CrewSessionSignInMethods"
	})
	if err := identityFixtureGenerator(t, tables...).prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	if got := signUpFiles(t); len(got) != 0 {
		t.Errorf("the sources of the sign-up part = %v, want none without the table", got)
	}
	assertFileContains(t, "crewidentity/store.go",
		[]string{
			"func (store) SignInMethodRows(context.Context, resource.ReadOnlyTransaction) ([]identity.SignInMethodRow, error) {\n\treturn nil, nil\n}",
			"the schema holds no table CrewSessionSignInMethods: there is no row to write for the sign-in method %s",
		},
		[]string{"NewCrewSessionSignInMethod"},
	)
	// No default role is picked, and the roles a person is given by hand are still
	// picked from the roles the caller could give: the package answers the role file's
	// grants for that list, and the client hands them on.
	if !holdsRoleGrants(t) {
		t.Error("the crew's package was written no grants.go, want one: the list the whole application's role fields pick from asks which roles the caller could give")
	}
	assertFileContains(t, "crewidentity/client.go", []string{"identity.WithRoleGrants(grants{})"}, nil)
}
