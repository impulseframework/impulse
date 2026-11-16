package generation

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// identityFixtureGenerator is the two-auth fixture (the crew auth on the default
// outlet, the members auth on the portal outlet, the default outlet managing members)
// with the generated management turned on, over a schema holding the named tables.
func identityFixtureGenerator(t *testing.T, tables ...string) *resourceGenerator {
	t.Helper()

	r := userFixtureGenerator(t, false)
	r.genHandlers = true
	r.identityTree = "pkg/identity"
	r.modulePathRoot = "example.com/acme/beacon"
	r.router = "pkg/router"
	r.tableMap = make(map[string]*tableMetadata, len(tables))
	for _, table := range tables {
		r.tableMap[table] = &tableMetadata{}
	}

	return r
}

// The tables of the fixture's two auths, as the two libraries name them under each
// prefix.
var (
	crewIdentityTables    = []string{"CrewSessionUsers", "CrewSessions", "CrewUserRoles", "CrewRoles"}
	membersIdentityTables = []string{"MembersSessionUsers", "MembersSessions", "MembersUserRoles", "MembersRoles"}
	// crewEveryTable is every table the generated management declares, under the crew's
	// prefix.
	crewEveryTable = slices.Concat(crewIdentityTables, []string{
		"CrewSessionIdentities", "CrewSessionTokens", "CrewSessionMailLinks", "CrewSessionSignInAttempts",
		"CrewSessionAuthEvents", "CrewSessionImpersonations", "CrewSessionTrustedDevices", "CrewRoleGrants",
	})
)

// Test_identityInstances pins the derivation: one instance per declared auth, named for
// the auth package, served on the auth's own outlet and on every outlet that manages it,
// with three packages under the tree; the tables the schema holds; a directory that
// writes the roles; and the refusals.
func Test_identityInstances(t *testing.T) {
	t.Parallel()

	type want struct {
		auth, accessor              string
		outlets                     []string
		resources, methods, handler string
		tables                      []string
		rolesSynced                 bool
	}
	both := slices.Concat(crewIdentityTables, membersIdentityTables)
	tests := []struct {
		name      string
		generator func(t *testing.T) *resourceGenerator
		want      []want
		wantErr   string
	}{
		{
			name: "no tree: no instances",
			generator: func(t *testing.T) *resourceGenerator {
				t.Helper()
				r := identityFixtureGenerator(t, both...)
				r.identityTree = ""

				return r
			},
		},
		{
			name:      "two auths, the default outlet managing members",
			generator: func(t *testing.T) *resourceGenerator { t.Helper(); return identityFixtureGenerator(t, both...) },
			want: []want{
				{
					auth: "Crew", accessor: "CrewIdentity", outlets: []string{"default"},
					resources: "pkg/identity/internal/crewresources", methods: "pkg/identity/crewidentity", handler: "pkg/identity/crewhandlers",
					tables: []string{"memberships", "roles", "sessions", "users"},
				},
				{
					auth: "Members", accessor: "MembersIdentity", outlets: []string{"portal", "default"},
					resources: "pkg/identity/internal/membersresources", methods: "pkg/identity/membersidentity", handler: "pkg/identity/membershandlers",
					tables: []string{"memberships", "roles", "sessions", "users"},
				},
			},
		},
		{
			name: "an auth with no permission store in the schema manages its users and sessions alone",
			generator: func(t *testing.T) *resourceGenerator {
				t.Helper()

				return identityFixtureGenerator(t, slices.Concat(crewIdentityTables, []string{"MembersSessionUsers", "MembersSessions"})...)
			},
			want: []want{
				{
					auth: "Crew", accessor: "CrewIdentity", outlets: []string{"default"},
					resources: "pkg/identity/internal/crewresources", methods: "pkg/identity/crewidentity", handler: "pkg/identity/crewhandlers",
					tables: []string{"memberships", "roles", "sessions", "users"},
				},
				{
					auth: "Members", accessor: "MembersIdentity", outlets: []string{"portal", "default"},
					resources: "pkg/identity/internal/membersresources", methods: "pkg/identity/membersidentity", handler: "pkg/identity/membershandlers",
					tables: []string{"sessions", "users"},
				},
			},
		},
		{
			name: "every table the two libraries keep is declared where the schema holds it",
			generator: func(t *testing.T) *resourceGenerator {
				t.Helper()

				return identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
			},
			want: []want{
				{
					auth: "Crew", accessor: "CrewIdentity", outlets: []string{"default"},
					resources: "pkg/identity/internal/crewresources", methods: "pkg/identity/crewidentity", handler: "pkg/identity/crewhandlers",
					tables: []string{"authEvents", "identities", "impersonations", "mailLinks", "memberships", "roleGrants", "roles", "sessions", "signInAttempts", "tokens", "trustedDevices", "users"},
				},
				{
					auth: "Members", accessor: "MembersIdentity", outlets: []string{"portal", "default"},
					resources: "pkg/identity/internal/membersresources", methods: "pkg/identity/membersidentity", handler: "pkg/identity/membershandlers",
					tables: []string{"memberships", "roles", "sessions", "users"},
				},
			},
		},
		{
			name: "a directory that writes the roles marks the auth",
			generator: func(t *testing.T) *resourceGenerator {
				t.Helper()
				r := identityFixtureGenerator(t, both...)
				r.extraOutlets[1].auth = &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}

				return r
			},
			want: []want{
				{
					auth: "Crew", accessor: "CrewIdentity", outlets: []string{"default"},
					resources: "pkg/identity/internal/crewresources", methods: "pkg/identity/crewidentity", handler: "pkg/identity/crewhandlers",
					tables: []string{"memberships", "roles", "sessions", "users"},
				},
				{
					auth: "Members", accessor: "MembersIdentity", outlets: []string{"portal", "default"},
					resources: "pkg/identity/internal/membersresources", methods: "pkg/identity/membersidentity", handler: "pkg/identity/membershandlers",
					tables: []string{"memberships", "roles", "sessions", "users"}, rolesSynced: true,
				},
			},
		},
		{
			name: "without the generated routes and handlers the tree is refused",
			generator: func(t *testing.T) *resourceGenerator {
				t.Helper()
				r := identityFixtureGenerator(t, both...)
				r.genHandlers = false

				return r
			},
			wantErr: "GenerateIdentity requires GenerateRoutes and GenerateHandlers",
		},
		{
			name: "a program that declares no auth is refused",
			generator: func(t *testing.T) *resourceGenerator {
				t.Helper()
				r := identityFixtureGenerator(t, both...)
				r.defaultOutlet = routerOutlet{}
				r.extraOutlets = nil

				return r
			},
			wantErr: "the program declares no Auth",
		},
		{
			name: "an auth whose users table the schema does not hold is refused",
			generator: func(t *testing.T) *resourceGenerator {
				t.Helper()
				return identityFixtureGenerator(t, crewIdentityTables...)
			},
			wantErr: `Auth("example.com/acme/beacon/pkg/auth/members"): the schema holds no table MembersSessionUsers`,
		},
		{
			name: "an auth whose sessions table the schema does not hold is refused",
			generator: func(t *testing.T) *resourceGenerator {
				t.Helper()

				return identityFixtureGenerator(t, slices.Concat(crewIdentityTables, []string{"MembersSessionUsers"})...)
			},
			wantErr: `Auth("example.com/acme/beacon/pkg/auth/members"): the schema holds no table MembersSessions`,
		},
		{
			name: "the memberships table without the roles table is refused",
			generator: func(t *testing.T) *resourceGenerator {
				t.Helper()

				return identityFixtureGenerator(t, slices.Concat(membersIdentityTables, []string{"CrewSessionUsers", "CrewSessions", "CrewUserRoles"})...)
			},
			wantErr: "the schema holds one of CrewUserRoles and CrewRoles and not the other",
		},
		{
			name: "Manages naming no declared auth is refused",
			generator: func(t *testing.T) *resourceGenerator {
				t.Helper()
				r := identityFixtureGenerator(t, both...)
				r.extraOutlets[1].auth = nil

				return r
			},
			wantErr: `outlet "default": Manages("example.com/acme/beacon/pkg/auth/members") names no declared auth`,
		},
		{
			name: "Manages naming the outlet's own auth is refused",
			generator: func(t *testing.T) *resourceGenerator {
				t.Helper()
				r := identityFixtureGenerator(t, both...)
				r.defaultOutlet.manages = []string{crewAuthPath}

				return r
			},
			wantErr: `outlet "default": Manages("example.com/acme/beacon/pkg/auth/crew") names the outlet's own auth`,
		},
		{
			name: "two auth packages sharing a name are refused",
			generator: func(t *testing.T) *resourceGenerator {
				t.Helper()
				r := identityFixtureGenerator(t, both...)
				r.defaultOutlet.manages = nil
				r.extraOutlets[1].auth = &outletAuth{importPath: "example.com/acme/other/crew", methods: []AuthMethod{PasswordMethod()}}

				return r
			},
			wantErr: "both name their tables Crew",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			instances, err := tt.generator(t).identityInstances()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("identityInstances() error = %v, want one containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("identityInstances() error = %v", err)
			}
			var got []want
			for _, inst := range instances {
				var tables []string
				for kind, held := range inst.tables {
					if held {
						tables = append(tables, string(kind))
					}
				}
				slices.Sort(tables)
				got = append(got, want{
					auth: inst.auth, accessor: inst.accessor(), outlets: inst.outlets,
					resources: inst.resources.Dir(), methods: inst.methods.Dir(), handler: inst.handlers.Dir(),
					tables: tables, rolesSynced: inst.rolesSynced,
				})
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(want{})); diff != "" {
				t.Errorf("identityInstances() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// identityTreeFiles lists the Go files under the tree, by path from the tree's root.
func identityTreeFiles(t *testing.T, tree string) []string {
	t.Helper()

	var files []string
	if err := filepath.WalkDir(tree, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			// Every walked path is under the tree.
			files = append(files, filepath.ToSlash(strings.TrimPrefix(path, tree+string(filepath.Separator))))
		}

		return nil
	}); err != nil {
		t.Fatalf("filepath.WalkDir(%s) error = %v", tree, err)
	}
	slices.Sort(files)

	return files
}

// Test_prepareIdentity pins what is written before the packages are loaded: per auth the
// declarations over the tables the schema holds, the client, the store and the methods,
// and the handlers' base, every file under the generation header; an auth whose
// directory writes its roles carries no method that assigns or removes one and none
// that creates a person, who would be created holding a role; the two packages the run
// must load are added to the load, and the handlers' to the outputs.
func Test_prepareIdentity(t *testing.T) {
	// The generator writes relative to where the program runs, and the working directory
	// belongs to the whole process: no t.Parallel here.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
	r.extraOutlets[1].auth = &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	want := []string{
		"crewhandlers/handlers.go",
		"crewidentity/assign_crew_role.go",
		"crewidentity/client.go",
		"crewidentity/create_crew_user.go",
		"crewidentity/delete_crew_user.go",
		"crewidentity/disable_crew_user.go",
		"crewidentity/enable_crew_user.go",
		"crewidentity/end_crew_session.go",
		"crewidentity/end_crew_session_impersonation.go",
		"crewidentity/end_crew_user_sessions.go",
		"crewidentity/invite_crew_user.go",
		"crewidentity/remove_crew_role.go",
		"crewidentity/rename_crew_user.go",
		"crewidentity/require_crew_password_change.go",
		"crewidentity/revoke_crew_session_token.go",
		"crewidentity/revoke_crew_user_trusted_devices.go",
		"crewidentity/set_crew_temporary_password.go",
		"crewidentity/set_crew_user_email.go",
		"crewidentity/set_crew_user_name.go",
		"crewidentity/store.go",
		"crewidentity/unlink_crew_session_identity.go",
		"crewidentity/unlock_crew_sign_in.go",
		"crewidentity/withdraw_crew_invitation.go",
		"internal/crewresources/crew_role_grants.go",
		"internal/crewresources/crew_roles.go",
		"internal/crewresources/crew_session_auth_events.go",
		"internal/crewresources/crew_session_identities.go",
		"internal/crewresources/crew_session_impersonations.go",
		"internal/crewresources/crew_session_mail_links.go",
		"internal/crewresources/crew_session_sign_in_attempts.go",
		"internal/crewresources/crew_session_tokens.go",
		"internal/crewresources/crew_session_trusted_devices.go",
		"internal/crewresources/crew_session_users.go",
		"internal/crewresources/crew_sessions.go",
		"internal/crewresources/crew_user_roles.go",
		"internal/crewresources/resources.go",
		"internal/membersresources/members_roles.go",
		"internal/membersresources/members_session_users.go",
		"internal/membersresources/members_sessions.go",
		"internal/membersresources/members_user_roles.go",
		"internal/membersresources/resources.go",
		"membershandlers/handlers.go",
		"membersidentity/client.go",
		"membersidentity/delete_members_user.go",
		"membersidentity/disable_members_user.go",
		"membersidentity/enable_members_user.go",
		"membersidentity/end_members_session.go",
		"membersidentity/end_members_user_sessions.go",
		"membersidentity/rename_members_user.go",
		"membersidentity/set_members_user_email.go",
		"membersidentity/set_members_user_name.go",
		"membersidentity/store.go",
	}
	if diff := cmp.Diff(want, identityTreeFiles(t, "pkg/identity")); diff != "" {
		t.Errorf("the tree's files (-want +got):\n%s", diff)
	}
	for _, file := range want {
		content, err := os.ReadFile(filepath.Join("pkg", "identity", file))
		if err != nil {
			t.Fatalf("os.ReadFile(%s) error = %v", file, err)
		}
		if !strings.HasPrefix(string(content), generationHeader) {
			t.Errorf("%s does not open with the generation header", file)
		}
	}

	wantLoad := []string{"pkg/identity/internal/crewresources", "pkg/identity/crewidentity", "pkg/identity/internal/membersresources", "pkg/identity/membersidentity"}
	if diff := cmp.Diff(wantLoad, r.loadPackages); diff != "" {
		t.Errorf("the packages added to the load (-want +got):\n%s", diff)
	}
	wantOutputs := []packageDir{"pkg/identity/crewhandlers", "pkg/identity/membershandlers"}
	if diff := cmp.Diff(wantOutputs, r.outputs); diff != "" {
		t.Errorf("the packages added to the outputs (-want +got):\n%s", diff)
	}

	// What the declarations say: the outlets that serve the auth, nothing written over
	// HTTP, and the roles table serving no route while the surface holds its name.
	for file, lines := range map[string][]string{
		"internal/membersresources/members_session_users.go": {"// @resource", "// @suppress(patchHandler)", "// @outlet(portal, default)", "MembersSessionUser struct {"},
		"internal/crewresources/crew_user_roles.go":          {"// @suppress(readHandler)", "// @suppress(patchHandler)", "// @outlet(default)", "CrewUserRole struct {", "UserID is the person: the id of their account"},
		"internal/crewresources/crew_roles.go":               {"// @suppress(readHandler)", "// @suppress(patchHandler)", "// @outlet(default)", "CrewRole struct {"},
		"crewidentity/client.go": {
			`const TablePrefix = "Crew"`, "func NewClient(controller access.Controller, sessions identity.SessionAPI, opts ...identity.Option) (*Client, error) {",
			// The manager is built for the auth by its name, the one its outlets bind to a request.
			`const AuthName = "crew"`, "identity.New(AuthName, store{}, controller, sessions, opts...)",
			// The roles a sign-in gives are the manager's, handed the session library's transaction.
			"func (c *Client) InvitationAccepted() session.InvitationAcceptedHook[*spanner.ReadWriteTransaction] {", `return c.manager.InvitationAccepted("Crew")`,
			"func (c *Client) DirectorySeated() session.DirectorySeatedHook[*spanner.ReadWriteTransaction] {", `return c.manager.DirectorySeated("Crew")`,
			// The methods a super administrator always executes are the two that give and
			// take the auth's roles, and the manager holds the engine to them.
			"return []accesstypes.Resource{AssignCrewRole{}.Method(), RemoveCrewRole{}.Method()}", "opts = append(opts, identity.WithSuperAdministratorMethods(SuperAdministratorMethods()...))",
		},
		// An auth whose directory writes its roles serves no method that gives one, so a
		// super administrator has none to be given.
		"membersidentity/client.go":                              {"func SuperAdministratorMethods() []accesstypes.Resource {\n\treturn nil\n}", "// The auth's roles are not given by hand here, so there are none."},
		"crewidentity/store.go":                                  {"AddColumns(crewresources.NewCrewSessionUserColumns().Disabled()).", "crewresources.NewCrewSessionImpersonationUpdatePatch(sessionID).SetEndedAt(&endedAt).SetEndReason(&reason)", `"SELECT PasswordHash IS NOT NULL FROM CrewSessionUsers WHERE Id = @id"`, `"SELECT Id FROM CrewSessionUsers WHERE NormalizedUsername = NORMALIZE_AND_CASEFOLD(@username)"`, "crewresources.NewCrewSessionUserDeletePatch(id).Buffer(ctx, txn)", `"SELECT Id FROM CrewSessionUsers WHERE NormalizedEmail = NORMALIZE_AND_CASEFOLD(@email) AND EmailVerifiedAt IS NOT NULL"`, "crewresources.NewCrewUserRoleQueryClause().UserID().Equal(userID.String())", `"Kind":      string(sessionstorage.MailLinkInvitation),`, "txn.BufferMap(invitationWrite{id: row.ID}, columns)", "crewresources.NewCrewSessionTokenUpdatePatch(id).SetRevokedAt(&revokedAt).SetRevokedReason(&reason)"},
		"internal/crewresources/crew_role_grants.go":             {`ResourceName string    ` + "`spanner:\"Resource\"`", "// @suppress(readHandler)"},
		"internal/crewresources/crew_session_tokens.go":          {"CrewSessionToken struct {", "RevokedAt     *time.Time"},
		"crewidentity/create_crew_user.go":                       {"Roles       []string", "Domain      *string", "EveryDomain bool", "client.manager.CreateUser(ctx, txn, account)"},
		"crewidentity/set_crew_temporary_password.go":            {"// @target(CrewSessionUser)", "client.manager.SetTemporaryPassword(ctx, txn, m.UserID, m.Password)"},
		"crewidentity/revoke_crew_user_trusted_devices.go":       {"// @target(CrewSessionUser)", "client.manager.RevokeTrustedDevices(ctx, txn, m.UserID)"},
		"internal/crewresources/crew_session_trusted_devices.go": {"CrewSessionTrustedDevice struct {", "RevokedAt  *time.Time", "// @suppress(patchHandler)"},
		"crewidentity/unlock_crew_sign_in.go":                    {"// @target(CrewSessionSignInAttempt)", "client.manager.UnlockSignIn(ctx, txn, m.Name)"},
		"crewidentity/invite_crew_user.go":                       {"Sent bool", "client.manager.Invite(ctx, txn, invitation, func(sent bool) { result.Sent = sent })"},
		"crewidentity/withdraw_crew_invitation.go":               {"// @target(CrewSessionMailLink)", "client.manager.WithdrawInvitation(ctx, txn, m.InvitationID)"},
		"crewidentity/disable_crew_user.go":                      {"// @rpc", "// @outlet(default)", "// @target(CrewSessionUser)", "func (m *DisableCrewUser) Execute(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) error {"},
		"crewidentity/set_crew_user_email.go":                    {"// @target(CrewSessionUser)", "Email  *string", "client.manager.SetEmail(ctx, txn, m.UserID, m.Email)"},
		"crewidentity/set_crew_user_name.go":                     {"type SetCrewUserName struct {", "// @target(CrewSessionUser)", "Name   *string", "// @answers(204)", "client.manager.SetName(ctx, txn, m.UserID, m.Name)"},
		"membersidentity/set_members_user_name.go":               {"type SetMembersUserName struct {", "// @target(MembersSessionUser)", "client.manager.SetName(ctx, txn, m.UserID, m.Name)"},
		"internal/crewresources/crew_session_users.go":           {"Name                   *string      `spanner:\"Name\"`"},
		"internal/crewresources/crew_session_mail_links.go":      {"Name            *string       `spanner:\"Name\"`"},
		// A role is given to and taken from a person by the id of their account, and given
		// to nobody an id names no user of.
		"crewidentity/assign_crew_role.go":                      {"UserID impulse.UUID", "client.manager.RequireUser(ctx, txn, m.UserID)", "client.manager.AssignRole(ctx, txn, scope, m.UserID, accesstypes.Role(m.Role))"},
		"crewidentity/remove_crew_role.go":                      {"UserID      impulse.UUID", "client.manager.RemoveRole(ctx, txn, scope, m.UserID, accesstypes.Role(m.Role))"},
		"internal/crewresources/crew_session_impersonations.go": {"ActorUserID     *impulse.UUID `spanner:\"ActorUserId\"`"},
		"membersidentity/store.go":                              {"var _ identity.Store = store{}", "func (store) UserDisabled(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) (disabled, found bool, err error) {", "membersresources.NewMembersRoleQuery()", `"example.com/acme/beacon/pkg/identity/internal/membersresources"`, "the schema holds no table MembersSessionImpersonations", "the schema holds no table MembersSessionTokens", "the schema holds no table MembersSessionIdentities"},
		// The handlers are constructed over the application with its live derivations,
		// which they keep to publish with.
		"crewhandlers/handlers.go": {"CrewIdentity() *crewidentity.Client", `"example.com/acme/beacon/pkg/identity/crewidentity"`, "liveDerivations *live.Derivations", "func New(deps Deps, derivations *live.Derivations) *Handlers {", "return &Handlers{Deps: deps, liveDerivations: derivations}"},
	} {
		content, err := os.ReadFile(filepath.Join("pkg", "identity", file))
		if err != nil {
			t.Fatalf("os.ReadFile(%s) error = %v", file, err)
		}
		for _, line := range lines {
			if !strings.Contains(string(content), line) {
				t.Errorf("%s lacks %q:\n%s", file, line, content)
			}
		}
	}

	// An auth whose roles a directory writes gives no role at a sign-in by the
	// application's hand: its client answers neither hook, and still carries the Go call
	// that buffers a membership, which the create of a tenant uses.
	members, err := os.ReadFile(filepath.Join("pkg", "identity", "membersidentity", "client.go"))
	if err != nil {
		t.Fatalf("os.ReadFile(membersidentity/client.go) error = %v", err)
	}
	for _, hook := range []string{"InvitationAccepted", "DirectorySeated"} {
		if strings.Contains(string(members), hook) {
			t.Errorf("membersidentity/client.go carries %s for an auth whose roles a directory writes:\n%s", hook, members)
		}
	}
	if !strings.Contains(string(members), "func (c *Client) AssignRole(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error {") {
		t.Errorf("membersidentity/client.go lacks the Go call that buffers a membership:\n%s", members)
	}
	// The Go call reads no account, so a user created earlier in the same transaction,
	// which the database cannot yet show, can be given a role.
	if strings.Contains(string(members), "c.manager.RequireUser(") {
		t.Errorf("membersidentity/client.go's AssignRole reads the account, which a user created in the same transaction is not yet:\n%s", members)
	}

	// An application with no tenant record has no tenant's methods, and the templates
	// the two kinds of method share write nothing of a tenant into the whole
	// application's.
	for _, file := range want {
		content, err := os.ReadFile(filepath.Join("pkg", "identity", file))
		if err != nil {
			t.Fatalf("os.ReadFile(%s) error = %v", file, err)
		}
		for _, tenantOnly := range []string{"@permissionScope(domain)", "manager.Tenant(ctx)", "a tenant's method"} {
			if strings.Contains(string(content), tenantOnly) {
				t.Errorf("%s holds %q with no tenant record:\n%s", file, tenantOnly, content)
			}
		}
	}
}

// Test_prepareIdentity_noMemberships pins the store of an auth whose schema holds no
// memberships table. The store still stands for identity.Store, so what would read the
// table is a stub with the interface's own signature: a person's memberships are asked
// for by the id of their account there too, as in the store that reads the table.
func Test_prepareIdentity_noMemberships(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	// The members auth keeps its users and sessions alone: the schema holds neither
	// MembersUserRoles nor MembersRoles.
	r := identityFixtureGenerator(t, slices.Concat(crewIdentityTables, []string{"MembersSessionUsers", "MembersSessions"})...)
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	for _, tt := range []struct {
		file          string
		want, wantNot []string
	}{
		{
			file: "membersidentity/store.go",
			want: []string{
				"var _ identity.Store = store{}",
				"the schema holds no table MembersUserRoles: the Members auth's memberships are not managed here",
				"func (store) Memberships(context.Context, resource.ReadWriteTransaction, impulse.UUID) ([]access.MembershipKey, error) {",
			},
			wantNot: []string{
				"func (store) Memberships(context.Context, resource.ReadWriteTransaction, string)",
				"membersresources.NewMembersUserRoleQuery",
			},
		},
		{
			file: "crewidentity/store.go",
			want: []string{
				"var _ identity.Store = store{}",
				"func (store) Memberships(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]access.MembershipKey, error) {",
			},
			wantNot: []string{"errNoMemberships"},
		},
	} {
		content, err := os.ReadFile(filepath.Join("pkg", "identity", tt.file))
		if err != nil {
			t.Fatalf("os.ReadFile(%s) error = %v", tt.file, err)
		}
		for _, line := range tt.want {
			if !strings.Contains(string(content), line) {
				t.Errorf("%s lacks %q:\n%s", tt.file, line, content)
			}
		}
		for _, line := range tt.wantNot {
			if strings.Contains(string(content), line) {
				t.Errorf("%s holds %q:\n%s", tt.file, line, content)
			}
		}
	}
}

// Test_prepareIdentity_staleAndForeign pins the tree's ownership across runs: a source an
// earlier run wrote and this one has no use for goes before the load, with the generated
// file beside it, while the file generated for the package as a whole stays for the
// sweep at the end of the run; and a file the generator did not write, in a package of
// its own or beside the generator's, refuses the run and is left where it is.
func Test_prepareIdentity_staleAndForeign(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	tables := slices.Concat(crewIdentityTables, membersIdentityTables)
	if err := identityFixtureGenerator(t, tables...).prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}
	header := generationHeader + "\n\npackage crewidentity\n"
	stale := []string{"pkg/identity/crewidentity/retire_crew_user.go", "pkg/identity/crewidentity/zz_gen_retire_crew_user.go", "pkg/identity/crewidentity/zz_gen_orphan.go"}
	kept := "pkg/identity/crewidentity/zz_gen_rpc_iface.go"
	for _, file := range append(slices.Clone(stale), kept) {
		if err := os.WriteFile(file, []byte(header), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%s) error = %v", file, err)
		}
	}

	if err := identityFixtureGenerator(t, tables...).prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() over the earlier run's tree error = %v", err)
	}
	for _, file := range stale {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Errorf("%s survived the run: os.Stat() error = %v, want it removed before the load", file, err)
		}
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("%s: os.Stat() error = %v, want the package's own generated file left for the sweep", kept, err)
	}
	if _, err := os.Stat("pkg/identity/crewidentity/assign_crew_role.go"); err != nil {
		t.Errorf("a source the run wrote again: os.Stat() error = %v", err)
	}

	for _, tt := range []struct {
		name, file, content string
	}{
		{name: "a file without the header in a package of the generator's", file: "pkg/identity/crewidentity/helpers.go", content: "package crewidentity\n"},
		{name: "a package of its own under the tree", file: "pkg/identity/extras/extras.go", content: header},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Dir(tt.file), 0o750); err != nil {
				t.Fatalf("os.MkdirAll() error = %v", err)
			}
			if err := os.WriteFile(tt.file, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("os.WriteFile(%s) error = %v", tt.file, err)
			}
			err := identityFixtureGenerator(t, tables...).prepareIdentity()
			if err == nil || !strings.Contains(err.Error(), "the generator did not write") || !strings.Contains(err.Error(), tt.file) {
				t.Errorf("prepareIdentity() error = %v, want a refusal naming %s", err, tt.file)
			}
			if _, statErr := os.Stat(tt.file); statErr != nil {
				t.Errorf("the refused file: os.Stat() error = %v, want it left where it is", statErr)
			}
			if err := os.Remove(tt.file); err != nil {
				t.Fatalf("os.Remove(%s) error = %v", tt.file, err)
			}
		})
	}
}

// Test_prepareIdentity_retiredAuth pins what a run does with the packages an earlier run
// wrote for an auth the program no longer declares: they are removed, all three, before
// anything is loaded, and the auths still declared are written as before. A file a person
// put in one of them is not the generator's to remove: the run is refused and removes
// nothing.
func Test_prepareIdentity_retiredAuth(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	tables := slices.Concat(crewIdentityTables, membersIdentityTables)
	if err := identityFixtureGenerator(t, tables...).prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}
	retired := []string{"pkg/identity/membersidentity", "pkg/identity/membershandlers", "pkg/identity/internal/membersresources"}
	for _, dir := range retired {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("the first run: os.Stat(%s) error = %v, want the members' package written", dir, err)
		}
	}

	// The program now declares the crew alone: the portal outlet serves no auth, and the
	// default outlet manages none.
	crewOnly := func() *resourceGenerator {
		r := identityFixtureGenerator(t, tables...)
		r.defaultOutlet.manages = nil
		r.extraOutlets[1].auth = nil

		return r
	}

	note := "pkg/identity/membersidentity/notes.go"
	if err := os.WriteFile(note, []byte("package membersidentity\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", note, err)
	}
	err := crewOnly().prepareIdentity()
	if err == nil || !strings.Contains(err.Error(), "the generator did not write") || !strings.Contains(err.Error(), note) {
		t.Errorf("prepareIdentity() over a retired package a person wrote into: error = %v, want a refusal naming %s", err, note)
	}
	for _, dir := range retired {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("the refused run: os.Stat(%s) error = %v, want nothing removed", dir, err)
		}
	}
	if err := os.Remove(note); err != nil {
		t.Fatalf("os.Remove(%s) error = %v", note, err)
	}

	if err := crewOnly().prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() without the members auth error = %v", err)
	}
	for _, dir := range retired {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s survived the run: os.Stat() error = %v, want the retired auth's package removed", dir, err)
		}
	}
	for _, file := range []string{"pkg/identity/crewidentity/client.go", "pkg/identity/crewhandlers/handlers.go", "pkg/identity/internal/crewresources/resources.go"} {
		if _, err := os.Stat(file); err != nil {
			t.Errorf("the auth still declared: os.Stat(%s) error = %v", file, err)
		}
	}
}

// Test_prepareIdentity_tenantLists pins the lists' sources: in an application with a
// tenant record every auth gets the lists whose tables the schema holds, declared beside
// the auth's tables as views over them and the tenant record's table; a later run of an
// application that no longer declares the record removes them before the load.
func Test_prepareIdentity_tenantLists(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	generator := func() *resourceGenerator {
		r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
		r.resource = "pkg/resources"
		r.tableMap["Ships"] = shipsTable()

		return r
	}
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": shipTenantSource})
	if err := generator().prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	lists := []string{
		"internal/crewresources/crew_ship_identities.go",
		"internal/crewresources/crew_ship_invitations.go",
		"internal/crewresources/crew_ship_user_roles.go",
		"internal/crewresources/crew_ship_users.go",
		// The members' schema holds neither a mail links table nor an identities table.
		"internal/membersresources/members_ship_user_roles.go",
		"internal/membersresources/members_ship_users.go",
	}
	var got []string
	for _, file := range identityTreeFiles(t, "pkg/identity") {
		if strings.HasPrefix(file, "internal/") && strings.Contains(file, "_ship_") {
			got = append(got, file)
		}
	}
	if diff := cmp.Diff(lists, got); diff != "" {
		t.Errorf("the lists' sources (-want +got):\n%s", diff)
	}

	for file, lines := range map[string][]string{
		"internal/crewresources/crew_ship_users.go": {
			generationHeader, "package crewresources", "// @virtual", "// @permissionScope(domain)", "// @outlet(default)", "// @order(Username asc)",
			"CrewShipUser struct {", "// @primarykey", "// @domain", "// @attribute(heldElsewhere)", "// @attribute(heldGlobally)",
			"const crewShipUserQuery = `",
			"WHERE o.UserId = u.Id AND o.Kind = 'every' AND o.Axis = ''\n  ) OR EXISTS (",
			"WHERE o.UserId = u.Id AND o.Kind = 'domain' AND o.Axis = '' AND o.DomainId != t.DomainId\n  )) AS HeldElsewhere,",
			"WHERE g.UserId = u.Id AND g.Kind = 'global'",
			"SELECT s.Id AS DomainId, m.UserId AS UserId\n  FROM CrewUserRoles@{FORCE_INDEX=_BASE_TABLE} m\n  CROSS JOIN Ships s",
			"JOIN CrewSessionUsers u ON u.Id = t.UserId\n",
			"func (CrewShipUser) Subquery() (query string, params map[string]any) {",
		},
		"internal/crewresources/crew_ship_user_roles.go": {
			"CrewShipUserRole struct {", "// @order(Username asc, Role asc)", "// @attribute(everyDomain)",
			// The row is keyed by the account's id and the role, two columns the database looks up.
			"// @primarykey\n\t\tUserID   impulse.UUID", "// @primarykey\n\t\t// @attribute(role)\n\t\tRole string",
			"SELECT\n  t.DomainId AS DomainId,\n  u.Id AS UserId,",
			"FROM CrewUserRoles m\n  WHERE m.Kind = 'domain' AND m.Axis = ''\n  UNION ALL",
			"FROM CrewUserRoles@{FORCE_INDEX=_BASE_TABLE} m\n  CROSS JOIN Ships s\n  WHERE m.Kind = 'every' AND m.Axis = ''",
			"WHERE d.Kind = 'domain' AND d.Axis = m.Axis AND d.DomainId = s.Id AND d.Role = m.Role AND d.UserId = m.UserId",
			"JOIN CrewSessionUsers u ON u.Id = t.UserId\n",
		},
		"internal/crewresources/crew_ship_invitations.go": {
			"CrewShipInvitation struct {", "// @order(CreatedAt desc)", "// @attribute(pending)",
			"l.TenantId AS DomainId",
			"FROM CrewSessionMailLinks l",
			"WHERE l.Kind = 'invitation' AND l.TenantId IS NOT NULL",
		},
		"internal/crewresources/crew_ship_identities.go": {
			"CrewShipIdentity struct {", "// @order(CreatedAt desc)", "// @primarykey", "// @domain", "// @attribute(heldElsewhere)", "// @attribute(heldGlobally)",
			"const crewShipIdentityQuery = `",
			"i.Id AS Id", "u.Id AS UserId", "i.Tenant AS Tenant",
			"WHERE o.UserId = u.Id AND o.Kind = 'domain' AND o.Axis = '' AND o.DomainId != t.DomainId\n  )) AS HeldElsewhere,",
			"JOIN CrewSessionUsers u ON u.Id = t.UserId\nJOIN CrewSessionIdentities i ON i.UserId = u.Id",
		},
		"internal/membersresources/members_ship_users.go": {"// @outlet(portal, default)", "MembersShipUser struct {", "FROM MembersUserRoles m", "JOIN MembersSessionUsers u ON"},
	} {
		content, err := os.ReadFile(filepath.Join("pkg", "identity", file))
		if err != nil {
			t.Fatalf("os.ReadFile(%s) error = %v", file, err)
		}
		for _, line := range lines {
			if !strings.Contains(string(content), line) {
				t.Errorf("%s lacks %q:\n%s", file, line, content)
			}
		}
		// No hash is a column of a list.
		for _, secret := range []string{"PasswordHash", "TokenHash"} {
			if strings.Contains(string(content), secret) {
				t.Errorf("%s names %s:\n%s", file, secret, content)
			}
		}
		// The forms Cloud Spanner planned badly over a million users (2026-11-13) are in
		// no list. A join that also compares the normalized name read the users table
		// whole for every tenant, and a fact that puts the two kinds of membership under
		// one OR read the memberships table by its key for every listed person. A key
		// joined from two values is one the database cannot look a row up by: a read of
		// one row of the roles held joined the tenant's memberships whole.
		for _, form := range []string{"NORMALIZE_AND_CASEFOLD", "o.Kind = 'every' OR", "CONCAT("} {
			if strings.Contains(string(content), form) {
				t.Errorf("%s holds %q, a form the database plans as a whole-table read:\n%s", file, form, content)
			}
		}
	}

	// The generated file that stands beside a list's source goes with it.
	beside := "pkg/identity/internal/crewresources/zz_gen_crew_ship_users.go"
	if err := os.WriteFile(beside, []byte(generationHeader+"\n\npackage crewresources\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", beside, err)
	}
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": "package resources\n\n// @resource\ntype Ship struct {\n\tID string\n}\n"})
	if err := generator().prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() without the tenant record error = %v", err)
	}
	for _, file := range append(slices.Clone(lists), strings.TrimPrefix(beside, "pkg/identity/")) {
		if _, err := os.Stat(filepath.Join("pkg", "identity", file)); !os.IsNotExist(err) {
			t.Errorf("%s survived the run: os.Stat() error = %v, want it removed with the tenant record", file, err)
		}
	}
	if _, err := os.Stat("pkg/identity/internal/crewresources/crew_session_users.go"); err != nil {
		t.Errorf("the declaration over the users table: os.Stat() error = %v", err)
	}
}

// Test_prepareIdentity_tenantMethods pins the methods one tenant runs: written beside the
// whole application's where the application has a tenant record, each named with the
// tenant record's name after the auth's, a method of the tenant's scope whose target is
// a row of one of the tenant's lists, and gone with the tenant record. Beside them is
// the computed resource that lists the roles of a tenant, which the field of the method
// that gives a role picks from, written where the roles are given by hand.
func Test_prepareIdentity_tenantMethods(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	generator := func() *resourceGenerator {
		r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
		r.resource = "pkg/resources"
		r.tableMap["Ships"] = shipsTable()
		r.extraOutlets[1].auth = &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}

		return r
	}
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": shipTenantSource})
	if err := generator().prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	methods := []string{
		"crewidentity/assign_crew_ship_role.go",
		"crewidentity/create_crew_ship_user.go",
		"crewidentity/crew_ship_roles.go",
		"crewidentity/delete_crew_ship_user.go",
		"crewidentity/disable_crew_ship_user.go",
		"crewidentity/enable_crew_ship_user.go",
		"crewidentity/end_crew_ship_user_sessions.go",
		"crewidentity/invite_crew_ship_user.go",
		"crewidentity/remove_crew_ship_role.go",
		"crewidentity/rename_crew_ship_user.go",
		"crewidentity/require_crew_ship_password_change.go",
		"crewidentity/revoke_crew_ship_user_trusted_devices.go",
		"crewidentity/set_crew_ship_temporary_password.go",
		"crewidentity/set_crew_ship_user_email.go",
		"crewidentity/set_crew_ship_user_name.go",
		"crewidentity/unlink_crew_ship_session_identity.go",
		"crewidentity/withdraw_crew_ship_invitation.go",
		// The members' directory writes the roles, and their schema holds neither mail
		// links nor remembered browsers: a tenant gives, takes and invites nothing there,
		// so it picks no role and lists none.
		"membersidentity/delete_members_ship_user.go",
		"membersidentity/disable_members_ship_user.go",
		"membersidentity/enable_members_ship_user.go",
		"membersidentity/end_members_ship_user_sessions.go",
		"membersidentity/rename_members_ship_user.go",
		"membersidentity/set_members_ship_user_email.go",
		"membersidentity/set_members_ship_user_name.go",
	}
	var got []string
	for _, file := range identityTreeFiles(t, "pkg/identity") {
		if !strings.HasPrefix(file, "internal/") && strings.Contains(file, "_ship_") {
			got = append(got, file)
		}
	}
	if diff := cmp.Diff(methods, got); diff != "" {
		t.Errorf("the sources of the tenant's methods and computed resources (-want +got):\n%s", diff)
	}

	guard := "if _, err := client.manager.Tenant(ctx); err != nil {"
	for file, lines := range map[string][]string{
		"crewidentity/disable_crew_ship_user.go": {
			generationHeader, "// @rpc\n// @permissionScope(domain)\n// @outlet(default)\n// @answers(204)", "type DisableCrewShipUser struct {", "// @target(CrewShipUser)",
			"// It is a tenant's method: served under the address of one Ship, it names a", guard, "client.manager.DisableUser(ctx, txn, m.UserID)",
		},
		"crewidentity/set_crew_ship_temporary_password.go": {"type SetCrewShipTemporaryPassword struct {", "// @target(CrewShipUser)", guard},
		"crewidentity/set_crew_ship_user_email.go":         {"type SetCrewShipUserEmail struct {", "// @target(CrewShipUser)", guard, "client.manager.SetEmail(ctx, txn, m.UserID, m.Email)"},
		"crewidentity/set_crew_ship_user_name.go": {
			"type SetCrewShipUserName struct {", "// @rpc\n// @permissionScope(domain)\n// @outlet(default)\n// @answers(204)", "// @target(CrewShipUser)",
			"// It is a tenant's method: served under the address of one Ship, it names a", guard, "client.manager.SetName(ctx, txn, m.UserID, m.Name)",
		},
		"crewidentity/withdraw_crew_ship_invitation.go": {
			"type WithdrawCrewShipInvitation struct {", "// @target(CrewShipInvitation)", "// @permissionScope(domain)", guard,
			"row of CrewShipInvitation, the tenant's own list of invitations", "client.manager.WithdrawInvitation(ctx, txn, m.InvitationID)",
		},
		"crewidentity/assign_crew_ship_role.go": {
			"type AssignCrewShipRole struct {", "// @permissionScope(domain)", "// @target(CrewShipUser)", "// @attribute(role)",
			"tenant, err := client.manager.Tenant(ctx)", "tenant.AssignRole(ctx, txn, m.UserID, accesstypes.Role(m.Role))",
			"// @attribute(role)\n\t// @enumerate(CrewShipRoles)\n\tRole string",
		},
		"crewidentity/crew_ship_roles.go": {
			generationHeader, "// @computed\n// @permissionScope(domain)\n// @suppress(readHandler)\n// @outlet(default)\n// @order(Name asc)", "type CrewShipRole struct {",
			"// @primarykey\n\t// @attribute(name)\n\tName ", "Description string", "return \"CrewShipRoles\"",
			"func ListCrewShipRole(ctx context.Context, qSet *resource.QuerySet[CrewShipRole], _ resource.Client, client *Client) iter.Seq2[*CrewShipRole, error] {",
			"domain, ok := qSet.Scope().Domain()", "client.manager.TenantRoles(ctx, domain)",
		},
		"crewhandlers/handlers.go":    {"func (h *Handlers) RPCClient() *crewidentity.Client {", "func (h *Handlers) ComputedClient() *crewidentity.Client {\n\treturn h.CrewIdentity()\n}"},
		"membershandlers/handlers.go": {"func (h *Handlers) ComputedClient() *membersidentity.Client {"},
		"crewidentity/unlink_crew_ship_session_identity.go": {
			"type UnlinkCrewShipSessionIdentity struct {", "// @permissionScope(domain)", "// @target(CrewShipIdentity)", guard,
			"row of CrewShipIdentity, the tenant's own list of its people's identities", "client.manager.UnlinkIdentity(ctx, txn, m.IdentityID)",
		},
		"crewidentity/remove_crew_ship_role.go": {
			"type RemoveCrewShipRole struct {", "// @target(CrewShipUserRole, key: UserID)\n\tUserID impulse.UUID", "// @target(CrewShipUserRole, key: Role)\n\tRole string",
			"tenant.RemoveRole(ctx, txn, m.UserID, accesstypes.Role(m.Role))",
		},
		"crewidentity/create_crew_ship_user.go": {
			"CreateCrewShipUser struct {", "// @permissionScope(domain)", "// @attribute(role)\n\t\t// @enumerate(CrewShipRoles)\n\t\tRoles []string", "tenant.CreateUser(ctx, txn, account)",
		},
		"crewidentity/invite_crew_ship_user.go": {
			"InviteCrewShipUser struct {", "// @attribute(role)\n\t\t// @enumerate(CrewShipRoles)\n\t\tRoles []string", "tenant.Invite(ctx, txn, invitation, func(sent bool) { result.Sent = sent })",
		},
		"membersidentity/disable_members_ship_user.go": {"// @outlet(portal, default)", "type DisableMembersShipUser struct {", "// @target(MembersShipUser)", guard},
	} {
		content, err := os.ReadFile(filepath.Join("pkg", "identity", file))
		if err != nil {
			t.Fatalf("os.ReadFile(%s) error = %v", file, err)
		}
		for _, line := range lines {
			if !strings.Contains(string(content), line) {
				t.Errorf("%s lacks %q:\n%s", file, line, content)
			}
		}
	}
	// The whole application's methods name the tenant a role is given in, and the field
	// lists the tenant record where the record is served on every outlet the methods are
	// on: the crew's are on the default outlet, the record's own. (Where a method is also
	// on an outlet the record is not on, the field stays typed:
	// Test_identitySourceData_TenantPicker.)
	for file, picks := range map[string]bool{
		"crewidentity/assign_crew_role.go": true,
		"crewidentity/create_crew_user.go": true,
		"crewidentity/invite_crew_user.go": true,
	} {
		content, err := os.ReadFile(filepath.Join("pkg", "identity", file))
		if err != nil {
			t.Fatalf("os.ReadFile(%s) error = %v", file, err)
		}
		if got := strings.Contains(string(content), "// @enumerate(Ships)"); got != picks {
			t.Errorf("%s declares a picker of the tenant record = %v, want %v:\n%s", file, got, picks, content)
		}
		if !strings.Contains(string(content), "Domain ") {
			t.Errorf("%s holds no Domain field: the case no longer tests what it names", file)
		}
	}

	// A tenant's request names no tenant: the methods that create and invite carry no
	// field for one, and no method of a tenant's reads where a role is held from its
	// request.
	for _, file := range methods {
		content, err := os.ReadFile(filepath.Join("pkg", "identity", file))
		if err != nil {
			t.Fatalf("os.ReadFile(%s) error = %v", file, err)
		}
		for _, field := range []string{"Domain ", "EveryDomain", "identity.Scope("} {
			if strings.Contains(string(content), field) {
				t.Errorf("%s holds %q: a tenant's method takes its tenant from where the request was made:\n%s", file, field, content)
			}
		}
	}

	// The methods go with the tenant record.
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": "package resources\n\n// @resource\ntype Ship struct {\n\tID string\n}\n"})
	if err := generator().prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() without the tenant record error = %v", err)
	}
	for _, file := range methods {
		if _, err := os.Stat(filepath.Join("pkg", "identity", file)); !os.IsNotExist(err) {
			t.Errorf("%s survived the run: os.Stat() error = %v, want it removed with the tenant record", file, err)
		}
	}
	if _, err := os.Stat("pkg/identity/crewidentity/disable_crew_user.go"); err != nil {
		t.Errorf("the whole application's method: os.Stat() error = %v", err)
	}
}

// Test_identityPackagePlace pins where the generator puts an instance's package, which is
// where a run looks for what an earlier one left: nowhere else in the tree is anything
// removed.
func Test_identityPackagePlace(t *testing.T) {
	t.Parallel()

	for dir, want := range map[string]bool{
		"crewidentity":           true,
		"crewhandlers":           true,
		"internal/crewresources": true,
		".":                      false,
		"identity":               false,
		"handlers":               false,
		"extras":                 false,
		"crewresources":          false,
		"internal":               false,
		"internal/resources":     false,
		"internal/crewidentity":  false,
		"crewidentity/sub":       false,
	} {
		if got := identityPackagePlace(path.Clean(dir)); got != want {
			t.Errorf("identityPackagePlace(%q) = %v, want %v", dir, got, want)
		}
	}
}

// Test_refuseIdentityNamesTaken pins the refusal of an application declaration under a
// name the generated management holds.
func Test_refuseIdentityNamesTaken(t *testing.T) {
	t.Parallel()

	inst := &identityInstance{importPath: crewAuthPath}
	if err := refuseIdentityNamesTaken(nil, func(m *rpcMethodInfo) *identityInstance { return m.identity }, map[string]string{"CreateCrewUser": "pkg/rpc"}); err != nil {
		t.Errorf("refuseIdentityNamesTaken() with nothing generated error = %v", err)
	}

	_, structs := featureFixtureClient(t)
	ship := fixtureResource(t, structs, "Ship", func(res *resourceInfo) { res.identity = inst })
	instanceOf := func(res *resourceInfo) *identityInstance { return res.identity }
	if err := refuseIdentityNamesTaken([]*resourceInfo{ship}, instanceOf, map[string]string{"Pilot": "pkg/resources"}); err != nil {
		t.Errorf("refuseIdentityNamesTaken() with no name shared error = %v", err)
	}
	err := refuseIdentityNamesTaken([]*resourceInfo{ship}, instanceOf, map[string]string{"Ship": "pkg/resources"})
	want := `Ship in pkg/resources takes a name of the generated management of Auth("example.com/acme/beacon/pkg/auth/crew")`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("refuseIdentityNamesTaken() error = %v, want one containing %q", err, want)
	}
}

// Test_generatedSetMethods holds the refused field names to the methods the generated set
// declares on a resource's struct: a method added to the template without its name here
// would let a field of that name through to code that does not compile.
func Test_generatedSetMethods(t *testing.T) {
	t.Parallel()

	var declared []string
	for _, m := range regexp.MustCompile(`(?m)^func \(\{\{ \.Resource\.Name \}\}\) (\w+)\(`).FindAllStringSubmatch(resourceFileTemplate, -1) {
		declared = append(declared, m[1])
	}
	if len(declared) == 0 {
		t.Fatal("no method found on the resource's struct in resourceFileTemplate: the pattern no longer reads the template")
	}
	if diff := cmp.Diff(declared, generatedSetMethods()); diff != "" {
		t.Errorf("the methods the template declares against generatedSetMethods() (-template +refused):\n%s", diff)
	}
}

// Test_identityInstance_createsUsers pins which auths create their people by hand: a
// password method or a registration that admits invited users alone, and never one
// whose directory writes the roles, since a person is created holding a role.
func Test_identityInstance_createsUsers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		inst identityInstance
		want bool
	}{
		{name: "a password method", inst: identityInstance{password: true}, want: true},
		{name: "a registration for invited users alone", inst: identityInstance{invited: true}, want: true},
		{name: "a directory that admits its own members", inst: identityInstance{}, want: false},
		{name: "a directory that writes the roles", inst: identityInstance{rolesSynced: true}, want: false},
		{name: "invited users whose roles a directory writes", inst: identityInstance{invited: true, rolesSynced: true}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.inst.createsUsers(); got != tt.want {
				t.Errorf("createsUsers() = %t, want %t", got, tt.want)
			}
		})
	}
}
