package transition

import (
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
)

// withoutResources cuts the entries of the resources named from a permission collection
// the generator wrote, as the generator leaves them out for an application that does not
// serve them.
func withoutResources(t *testing.T, collection []byte, resources ...string) []byte {
	t.Helper()

	for _, name := range resources {
		entry := regexp.MustCompile(`(?m)^\t\t\t\{\n\t\t\t\tName: +"` + name + `",\n(?:\t\t\t\t.*\n)*?\t\t\t\},\n`)
		if len(entry.FindAll(collection, -1)) != 1 {
			t.Fatalf("the collection does not name the resource %s once", name)
		}
		collection = entry.ReplaceAll(collection, nil)
	}

	return collection
}

// conditionsOf lists a role's grants under a condition, each as the change names it and
// sorted.
func conditionsOf(role *roleEntry) []string {
	var held []string
	for _, c := range conditionalGrants(role) {
		held = append(held, c.String())
	}
	slices.Sort(held)

	return held
}

// TestTenancy_Apply_leavesTheConditionsWhereNoTestProvesThem holds the other half of the
// rule the role is written under: a grant under a condition is written only with the test
// that proves it. Where the command cannot write that test so that it holds as the
// application is, the role carries the grants under no condition, the change names each
// one left out with why the test was not written, and nothing of the author's is written
// over. A role written with a condition no test proves is an application that fails its
// own check as it was made, and a test written over the author's, or beside tests it does
// not compile with, is one that stops every test of the package.
func TestTenancy_Apply_leavesTheConditionsWhereNoTestProvesThem(t *testing.T) {
	t.Parallel()

	const (
		roles  = "pkg/auth/staff/roles.json"
		mine   = "package integration\n\n// The author's own test of a tenant's people.\n"
		helper = integrationSuite + "/mine_test.go"
	)
	tests := []struct {
		name string
		// prepare changes the base before tenancy is applied, and collection the
		// collection the regeneration writes.
		prepare    func(t *testing.T, a *app.App)
		collection func(t *testing.T, collection []byte) []byte
		// unserved is the conditional grant the application does not serve, which the
		// role does not carry and the change does not name among the ones to add.
		unserved string
		wantWhy  string
		// wantProof is what the test of a tenant's people holds afterward: nothing where
		// the command wrote none.
		wantProof string
	}{
		{
			name: "a test of that name is there already",
			prepare: func(t *testing.T, a *app.App) {
				t.Helper()
				if err := os.WriteFile(a.Abs(tenantPeopleTest), []byte(mine), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantWhy:   tenantPeopleTest + " is there already, and the command writes over nobody's test",
			wantProof: mine,
		},
		{
			name: "the test whose helper waits for the engine is not the command's",
			prepare: func(t *testing.T, a *app.App) {
				t.Helper()
				src := read(t, a, grantsSuite)
				if err := os.WriteFile(a.Abs(grantsSuite), []byte(src+"\n// The author's own.\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantWhy: grantsSuite + " is not as the command writes it for the staff auth",
		},
		{
			name: "another test of the package declares one of its names",
			prepare: func(t *testing.T, a *app.App) {
				t.Helper()
				if err := os.WriteFile(a.Abs(helper), []byte("package integration\n\nfunc tenantPost() {\n}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantWhy: helper + " declares tenantPost, a name the test that proves the conditions declares too",
		},
		{
			name: "the application serves less than the test proves",
			collection: func(t *testing.T, collection []byte) []byte {
				t.Helper()

				return withoutResources(t, collection, "DisableStaffTenantUser")
			},
			unserved: `Execute on DisableStaffTenantUser under "managedHere = true"`,
			wantWhy:  `the test proves Execute on DisableStaffTenantUser under "managedHere = true", which the role would not carry`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := newBase(t, "staff")
			if tt.prepare != nil {
				tt.prepare(t, a)
			}
			collection := referenceCollection(t)
			if tt.collection != nil {
				collection = tt.collection(t, collection)
			}
			tn := Tenancy{Table: "Tenants"}
			ch := tenantedOn(t, a, tn, "staff", collection)

			reference, err := referenceRoles()
			if err != nil {
				t.Fatal(err)
			}
			free, held := unconditional(recordGrants(roleNamed(reference.Roles.Domain, "TenantAdministrator")))
			held = slices.DeleteFunc(held, func(grant string) bool {
				return grant == tt.unserved
			})
			role := roleNamed(roleFileOf(t, a, roles).Roles.Domain, "TenantAdministrator")
			if role == nil {
				t.Fatalf("%s has no domain role TenantAdministrator; the change skipped %q", roles, ch.Skipped)
			}
			if diff := cmp.Diff(free, role.Permissions); diff != "" {
				t.Errorf("the role's grants are not the reference's that hold under no condition (-reference +written):\n%s", diff)
			}
			if tt.wantProof == "" && exists(a, tenantPeopleTest) {
				t.Errorf("%s was written, and it does not hold as the application is", tenantPeopleTest)
			}
			if tt.wantProof != "" && read(t, a, tenantPeopleTest) != tt.wantProof {
				t.Errorf("%s was written over; it is the author's", tenantPeopleTest)
			}
			left := skippedLine(ch, roles+": "+strconv.Itoa(len(held))+" grant(s) the tenanted skeleton's TenantAdministrator holds under a condition were not written into TenantAdministrator", append([]string{"conditions-proven", "the command wrote no such test, since ", tt.wantWhy, "a tenant's administrator does none of them"}, held...)...)
			if len(left) != 1 {
				t.Errorf("the change does not say once which %d grant(s) under a condition were left out, and why (%s): %q", len(held), tt.wantWhy, ch.Skipped)
			}
			if got := linesWith(ch.Did, tenantPeopleTest+": the test that proves", nil); len(got) != 0 {
				t.Errorf("the change says it wrote the test: %q", got)
			}
			// With no test written beside it the harness says nothing to the lint: a directive
			// that silences nothing is one the application's lint refuses.
			if exists(a, integrationHarness) && strings.Contains(read(t, a, integrationHarness), "nolint:unparam") {
				t.Errorf("%s marks provesGrant for the lint, and no test was written that calls it", integrationHarness)
			}
			if got := linesWith(ch.Did, integrationHarness+": written again with its provesGrant marked", nil); len(got) != 0 {
				t.Errorf("the change says it marked the harness: %q", got)
			}
			if got := linesWith(ch.Did, roles+": the role TenantAdministrator, a domain role", []string{"that hold under no condition"}); len(got) != 1 {
				t.Errorf("the change does not say once that the role carries the grants that hold under no condition: %q", ch.Did)
			}
			meaning := tn.MeaningIn(a)
			if !strings.Contains(meaning, "holds under a condition are yours to add, each with the test that proves it") || strings.Contains(meaning, "each under the condition the skeleton holds it to") {
				t.Error("the brief does not say the conditional grants are the author's to add with their proof, or says the role carries them")
			}
			// The application's own administrator takes its condition only with the test that
			// proves it: with no test written, its grants stay under none, and the change says
			// why, in the words it gives for the tenant's role.
			if got := conditionsOf(roleNamed(roleFileOf(t, a, roles).Roles.Global, referenceAdministrator)); len(got) != 0 {
				t.Errorf("the application's administrator holds %q under a condition, and no test proves it", got)
			}
			accounts := accountConditions(t)
			if got := skippedLine(ch, roles+": the role Administrator still changes every account from the whole application's side, also one a tenant manages: "+strconv.Itoa(len(accounts))+" grant(s) the tenanted skeleton's Administrator holds under managedByTenant = false were left under no condition, since impulse check holds every conditional grant to a test that proves it (conditions-proven) and the command wrote no such test: ", tt.wantWhy, ". They are Execute on DisableStaffUser, Execute on EnableStaffUser, ", ". Add the condition to each with its proof, as the tenanted skeleton's test/integration/grants_test.go proves them"); len(got) != 1 {
				t.Errorf("the change does not say once that the administrator's %d grant(s) stay under no condition, and why (%s): %q", len(accounts), tt.wantWhy, ch.Skipped)
			}
			if got := linesWith(ch.Did, roles+": the role Administrator changes an account", nil); len(got) != 0 {
				t.Errorf("the change says the administrator took a condition: %q", got)
			}
		})
	}
}

// TestTenancy_Apply_leavesTheAdministratorWhereTheTestDoesNotProveIt holds the
// application's own administrator to the rule the tenant's role is written under: its
// grants on the whole application's methods that act on an account take the condition
// the tenanted skeleton holds them to only with the test that proves it. Where the role
// does not hold them as the skeleton's does, the test with the administrator's part does
// not pair with the role file, so the command writes the tenant's role and its test as
// it would with no such part, leaves the administrator as it was and says why; where the
// tenant's role was there already the command writes no test, and says that.
func TestTenancy_Apply_leavesTheAdministratorWhereTheTestDoesNotProveIt(t *testing.T) {
	t.Parallel()

	const roles = "pkg/auth/staff/roles.json"
	// executeOf runs edit over the Execute grants of a role of the base's role file.
	executeOf := func(t *testing.T, a *app.App, role string, edit func(grants []access.Grant) []access.Grant) {
		t.Helper()

		file := roleFileOf(t, a, roles)
		entry := roleNamed(file.Roles.Global, role)
		for i := range entry.Permissions {
			if entry.Permissions[i].Permission == "Execute" {
				entry.Permissions[i].Grants = edit(entry.Permissions[i].Grants)
			}
		}
		encoded, err := file.encode()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(a.Abs(roles), encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name    string
		prepare func(t *testing.T, a *app.App)
		// wantHeld is what the administrator holds under a condition afterward, wantCount
		// the grants it was not held to, and wantWhy what the change says of why. wantProof
		// reports that the test of the tenant's role is written as it is with no such part.
		wantHeld  []string
		wantCount int
		wantWhy   []string
		wantProof bool
	}{
		{
			name: "the administrator lacks one of the grants",
			prepare: func(t *testing.T, a *app.App) {
				t.Helper()
				executeOf(t, a, referenceAdministrator, func(grants []access.Grant) []access.Grant {
					return slices.DeleteFunc(grants, func(g access.Grant) bool {
						return g.Resource == "DisableStaffUser"
					})
				})
			},
			wantCount: 11,
			wantWhy:   []string{`the test proves Execute on DisableStaffUser under "managedByTenant = false", which the role would not carry`, "They are Execute on EnableStaffUser, "},
			wantProof: true,
		},
		{
			name: "the administrator holds one of the grants under another condition",
			prepare: func(t *testing.T, a *app.App) {
				t.Helper()
				executeOf(t, a, referenceAdministrator, func(grants []access.Grant) []access.Grant {
					for i := range grants {
						if grants[i].Resource == "DisableStaffUser" {
							grants[i].Condition = "disabled = false"
						}
					}

					return grants
				})
			},
			wantHeld:  []string{`Execute on DisableStaffUser under "disabled = false"`},
			wantCount: 11,
			wantWhy:   []string{`the test proves Execute on DisableStaffUser under "managedByTenant = false", which the role would not carry`, "They are Execute on EnableStaffUser, "},
			wantProof: true,
		},
		{
			name: "the tenant's role was there already",
			prepare: func(t *testing.T, a *app.App) {
				t.Helper()
				file := roleFileOf(t, a, roles)
				file.Roles.Domain = append(file.Roles.Domain, &roleEntry{Name: "TenantAdministrator", Description: "The author's own.", Permissions: rolePermissions{
					{Permission: "Execute", Grants: []access.Grant{{Resource: "AssignStaffRole"}}},
				}})
				encoded, err := file.encode()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(a.Abs(roles), encoded, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantCount: 12,
			wantWhy:   []string{"the role a tenant's administrator holds was there already, and the command writes the test only with that role", "They are Execute on DisableStaffUser, "},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := newBase(t, "staff")
			tt.prepare(t, a)
			ch := tenantedOn(t, a, Tenancy{Table: "Tenants"}, "staff", referenceCollection(t))

			file := roleFileOf(t, a, roles)
			if got := conditionsOf(roleNamed(file.Roles.Global, referenceAdministrator)); !slices.Equal(got, tt.wantHeld) {
				t.Errorf("the application's administrator holds %q under a condition, want %q: nothing of it changed", got, tt.wantHeld)
			}
			texts := append([]string{"conditions-proven", "the command wrote no such test: "}, tt.wantWhy...)
			if got := skippedLine(ch, roles+": the role Administrator still changes every account from the whole application's side, also one a tenant manages: "+strconv.Itoa(tt.wantCount)+" grant(s) the tenanted skeleton's Administrator holds under managedByTenant = false were left under no condition", texts...); len(got) != 1 {
				t.Errorf("the change does not say once that %d grant(s) of the administrator stay under no condition, and why: %q", tt.wantCount, ch.Skipped)
			}
			if got := linesWith(ch.Did, roles+": the role Administrator changes an account", nil); len(got) != 0 {
				t.Errorf("the change says the administrator took a condition: %q", got)
			}
			if !tt.wantProof {
				if exists(a, tenantPeopleTest) {
					t.Errorf("%s was written, and the command writes it only with the tenant's role", tenantPeopleTest)
				}

				return
			}
			// The tenant's role and its test are as the command writes them with no part of
			// the administrator's: each condition, proven under the role's name alone, and the
			// harness marked for the lint.
			role := roleNamed(file.Roles.Domain, "TenantAdministrator")
			if role == nil {
				t.Fatalf("%s has no domain role TenantAdministrator; the change skipped %q", roles, ch.Skipped)
			}
			held := conditionsOf(role)
			if got := provenByRole(t, read(t, a, tenantPeopleTest)); len(held) == 0 || !cmp.Equal(got, map[string][]string{"TenantAdministrator": held}) {
				t.Errorf("%s proves %q, want the tenant's role's %q alone", tenantPeopleTest, got, held)
			}
			if got := strings.Count(read(t, a, integrationHarness), oneRoleDirective+"func provesGrant("); got != 1 {
				t.Errorf("%s marks provesGrant for the lint %d time(s), want once", integrationHarness, got)
			}
			if got := linesWith(ch.Did, integrationHarness+": written again with its provesGrant marked", nil); len(got) != 1 {
				t.Errorf("the change does not say once that it marked the harness: %q", ch.Did)
			}
		})
	}
}

// TestTenancy_Apply_provesTheConditionsWithNoPassword holds the role and its proof to the
// application that has no password sign-in, the start of Tenant-owned users, by
// directory: the role carries the grants the application serves, which are not the ones
// that set, require or remove a password, and the test proves each of those and no other,
// signing people in through the simulated directory as the start's other tests do. The
// password version of the test in such an application posts a password to a method it
// does not serve and proves grants its role does not carry. Where a person may be in
// several tenants the application's own administrator is held to the accounts no tenant
// manages on the same terms: each of its grants on the whole application's methods that
// act on an account takes the condition, less the ones that ask for a password.
func TestTenancy_Apply_provesTheConditionsWithNoPassword(t *testing.T) {
	t.Parallel()

	const roles = "pkg/auth/members/roles.json"
	accounts := accountConditions(t)
	tests := []struct {
		name      string
		oneTenant bool
		// wantAccounts is what the application's administrator holds under a condition
		// afterward, in the members auth's names.
		wantAccounts []string
	}{
		{name: "a person belongs to one tenant", oneTenant: true},
		{name: "a person may be in several tenants", wantAccounts: sorted(withoutPassword(strings.Split(names.Rename(strings.Join(accounts, "\n"), "staff", "members"), "\n")))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tn := Tenancy{Table: "Tenants", OneTenantPerPerson: tt.oneTenant}
			start := byDirectoryStart("members")
			start.Tenants = &tn
			a, _ := started(t, &start, "send_members_user_password_reset_link")
			// The generator serves no management of a password for an auth with no password method.
			collection := withoutResources(t, []byte(names.Rename(string(referenceCollection(t)), "staff", "members")),
				"RequireMembersTenantPasswordChange", "RemoveMembersTenantPassword", "MembersTenantSignInWays", "SendMembersTenantUserPasswordResetLink",
				"RequireMembersPasswordChange", "RemoveMembersPassword", "MembersSignInWays", "SendMembersUserPasswordResetLink", "SetMembersSelfServiceReset",
			)
			ch := tenantedOn(t, a, tn, "members", collection)

			file := roleFileOf(t, a, roles)
			role := roleNamed(file.Roles.Domain, "TenantAdministrator")
			if role == nil {
				t.Fatalf("%s has no domain role TenantAdministrator; the change skipped %q", roles, ch.Skipped)
			}
			held := conditionsOf(role)
			if len(held) != 15 || slices.ContainsFunc(held, func(grant string) bool {
				return strings.Contains(grant, "Password") || strings.Contains(grant, "SignInWays")
			}) {
				t.Errorf("the role holds %d grant(s) under a condition, want the reference's 15 that ask nothing of a password: %q", len(held), held)
			}
			administrator := roleNamed(file.Roles.Global, "Administrator")
			if administrator == nil {
				t.Fatalf("%s has no global role Administrator", roles)
			}
			if got := conditionsOf(administrator); !slices.Equal(got, tt.wantAccounts) {
				t.Errorf("the application's administrator holds under a condition (-want +written):\n%s", cmp.Diff(tt.wantAccounts, got))
			}
			if !exists(a, tenantPeopleTest) {
				t.Fatalf("%s was not written; the change skipped %q", tenantPeopleTest, ch.Skipped)
			}
			proof := read(t, a, tenantPeopleTest)
			want := map[string][]string{"TenantAdministrator": held}
			if len(tt.wantAccounts) > 0 {
				want["Administrator"] = tt.wantAccounts
			}
			if got := provenByRole(t, proof); !cmp.Equal(got, want) {
				t.Errorf("%s proves, by role, other than each grant the roles hold under a condition (-want +proven):\n%s", tenantPeopleTest, cmp.Diff(want, got))
			}
			for _, want := range []string{
				`provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "DisableMembersTenantUser", "managedHere = true")`,
				"s.auth.SeedCastFile(ctx, io.Discard,", "signIn(ctx, members.PersonaRegistration,", "membersAccount(ctx, t, s, name,", `"/api/assign-members-role"`, `"unlink-members-tenant-session-identity"`,
				`"/api/members-givable-roles?limit=all&filter=method:eq:AssignMembersRole,"`,
			} {
				if !strings.Contains(proof, want) {
					t.Errorf("%s does not contain %q", tenantPeopleTest, want)
				}
			}
			// The harness is marked for the lint where every grant the test proves is one
			// role's, and carries no mark where the test names two.
			wantMarks := 1
			if len(tt.wantAccounts) > 0 {
				wantMarks = 0
			}
			if got := strings.Count(read(t, a, integrationHarness), oneRoleDirective+"func provesGrant("); got != wantMarks {
				t.Errorf("%s marks provesGrant for the lint %d time(s), want %d", integrationHarness, got, wantMarks)
			}
			for _, never := range []string{`"password"`, "adminPassword", "passwordRemoval", "tenantWays", "first-day-pass", "second-subject-of-nora", "staff", "Staff"} {
				if strings.Contains(proof, never) {
					t.Errorf("%s contains %q, and the members auth has no password sign-in", tenantPeopleTest, never)
				}
			}
			if got := linesWith(ch.Did, tenantPeopleTest+": the test that proves each of the 15 condition(s)", []string{"the members auth has no password sign-in, so the test signs people in through the simulated directory (the session library's skipAuth build tag)", "under the default build it is skipped, and says so"}); len(got) != 1 {
				t.Errorf("the change does not say once that it wrote the test, and how it signs people in: %q", ch.Did)
			}
			if got := linesWith(ch.Did, roles+": the role Administrator changes an account from the whole application's side only where no tenant manages it: "+strconv.Itoa(len(tt.wantAccounts))+" grant(s)", nil); len(got) != min(len(tt.wantAccounts), 1) {
				t.Errorf("the change does not say once that the administrator took the condition on its %d grant(s), or says it where none did: %q", len(tt.wantAccounts), ch.Did)
			}
			if got := skippedLine(ch, roles); len(got) != 0 {
				t.Errorf("the change leaves a step of the role file to the author: %q", got)
			}
		})
	}
}

// TestTenantPeopleProof_provesTheReferencesConditions holds the test the command writes
// to the role it is written with: rendered in the reference's own names, it proves each
// grant the reference's role holds under a condition and no other, the ones that ask for
// a password left out of the version for an auth that has none. A grant the reference
// gains under a condition is one the command would write no proof of, so every new
// application would fall back to the role without its conditional grants; this fails
// first, and says which proof the template lacks. Rendered with the application's own
// administrator role, the test proves under that role's name each grant the reference's
// Administrator holds under managedByTenant = false, and rendered without it no line names
// that role.
func TestTenantPeopleProof_provesTheReferencesConditions(t *testing.T) {
	t.Parallel()

	reference, err := referenceRoles()
	if err != nil {
		t.Fatal(err)
	}
	whole := conditionsOf(roleNamed(reference.Roles.Domain, referenceTenantAdministrator))
	accounts := accountConditions(t)
	tests := []struct {
		name          string
		password      bool
		administrator string
		want          map[string][]string
	}{
		{name: "an auth with the password method", password: true, want: map[string][]string{referenceTenantAdministrator: whole}},
		{name: "an auth with no password method", want: map[string][]string{referenceTenantAdministrator: withoutPassword(whole)}},
		{
			name: "an auth with the password method, and the application's administrator", password: true, administrator: referenceAdministrator,
			want: map[string][]string{referenceTenantAdministrator: whole, referenceAdministrator: accounts},
		},
		{
			name: "an auth with no password method, and the application's administrator", administrator: referenceAdministrator,
			want: map[string][]string{referenceTenantAdministrator: withoutPassword(whole), referenceAdministrator: withoutPassword(accounts)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data := &suiteData{Record: "Tenant", Kebab: "tenant", Segment: "tenants", South: devTenantSouth, Password: tt.password, Administrator: tt.administrator}
			files, err := renderSuites(tenantPeopleSuites, data, "example.com/acme/depot", "staff")
			if err != nil {
				t.Fatalf("renderSuites() error = %v", err)
			}
			if got := provenByRole(t, string(files[tenantPeopleTest])); !cmp.Equal(got, tt.want) {
				t.Errorf("the test proves the conditions, by role (-the reference's roles +the test):\n%s", cmp.Diff(tt.want, got))
			}
		})
	}
}

// TestTenancy_unprovenWords holds what the command says where the conditions the role
// would carry are not the ones its test proves: each way round is named, since a grant
// with no proof fails the application's check and a proof with no grant fails its tests.
// Where the application's own administrator is held to the accounts no tenant manages
// too, a line under that role's name pairs with that role's grant and no other.
func TestTenancy_unprovenWords(t *testing.T) {
	t.Parallel()

	tn := Tenancy{Table: "Sectors"}
	carried := func(resource, condition string) conditionalGrant {
		return conditionalGrant{permission: "Execute", grant: access.Grant{Resource: accesstypes.Resource(resource), Condition: condition}}
	}
	proven := func(role, resource, condition string) provenGrant {
		return provenGrant{role: role, permission: "Execute", resource: resource, condition: condition}
	}
	administrator := func(grants ...conditionalGrant) *accountHold {
		return &accountHold{role: &roleEntry{Name: "Administrator"}, grants: grants}
	}
	tests := []struct {
		name        string
		conditional []conditionalGrant
		hold        *accountHold
		proven      []provenGrant
		want        string
	}{
		{
			name:        "each grant has its proof",
			conditional: []conditionalGrant{carried("DisableCrewSectorUser", "heldElsewhere = false")},
			proven:      []provenGrant{proven("SectorAdministrator", "DisableCrewSectorUser", "heldElsewhere = false")},
		},
		{
			name:        "a grant the test does not prove",
			conditional: []conditionalGrant{carried("DisableCrewSectorUser", "heldElsewhere = false"), carried("EnableCrewSectorUser", "heldElsewhere = false")},
			proven:      []provenGrant{proven("SectorAdministrator", "DisableCrewSectorUser", "heldElsewhere = false")},
			want:        `the test the command writes proves the conditions of the tenanted skeleton's role, and they are not this application's: the role would carry Execute on EnableCrewSectorUser under "heldElsewhere = false", which the test does not prove`,
		},
		{
			name:        "a proof of a grant under another condition, and of another role's",
			conditional: []conditionalGrant{carried("DisableCrewSectorUser", "heldElsewhere = false")},
			proven:      []provenGrant{proven("SectorAdministrator", "DisableCrewSectorUser", "heldGlobally = false"), proven("Administrator", "DisableCrewSectorUser", "heldElsewhere = false")},
			want:        `the test the command writes proves the conditions of the tenanted skeleton's role, and they are not this application's: the role would carry Execute on DisableCrewSectorUser under "heldElsewhere = false", which the test does not prove; the test proves Execute on DisableCrewSectorUser under "heldGlobally = false", Execute on DisableCrewSectorUser under "heldElsewhere = false", which the role would not carry`,
		},
		{
			name:        "each grant of both roles has its proof under its own role's name",
			conditional: []conditionalGrant{carried("DisableCrewSectorUser", "heldElsewhere = false")},
			hold:        administrator(carried("DisableCrewUser", "managedByTenant = false")),
			proven:      []provenGrant{proven("SectorAdministrator", "DisableCrewSectorUser", "heldElsewhere = false"), proven("Administrator", "DisableCrewUser", "managedByTenant = false")},
		},
		{
			name:        "a line under the tenant's role pairs with no grant of the administrator's",
			conditional: []conditionalGrant{carried("DisableCrewSectorUser", "heldElsewhere = false")},
			hold:        administrator(carried("DisableCrewUser", "managedByTenant = false")),
			proven:      []provenGrant{proven("SectorAdministrator", "DisableCrewSectorUser", "heldElsewhere = false"), proven("SectorAdministrator", "DisableCrewUser", "managedByTenant = false")},
			want:        `the test the command writes proves the conditions of the tenanted skeleton's role, and they are not this application's: the role would carry Execute on DisableCrewUser under "managedByTenant = false", which the test does not prove; the test proves Execute on DisableCrewUser under "managedByTenant = false", which the role would not carry`,
		},
		{
			name:        "a grant of the administrator's with no line, and a line under its name with no grant",
			conditional: []conditionalGrant{carried("DisableCrewSectorUser", "heldElsewhere = false")},
			hold:        administrator(carried("DisableCrewUser", "managedByTenant = false"), carried("EnableCrewUser", "managedByTenant = false")),
			proven:      []provenGrant{proven("SectorAdministrator", "DisableCrewSectorUser", "heldElsewhere = false"), proven("Administrator", "DisableCrewUser", "managedByTenant = false"), proven("Administrator", "DeleteCrewUser", "managedByTenant = false")},
			want:        `the test the command writes proves the conditions of the tenanted skeleton's role, and they are not this application's: the role would carry Execute on EnableCrewUser under "managedByTenant = false", which the test does not prove; the test proves Execute on DeleteCrewUser under "managedByTenant = false", which the role would not carry`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tn.unprovenWords(tt.conditional, tt.hold, tt.proven); got != tt.want {
				t.Errorf("unprovenWords() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Test_readProof holds the reading of the test the command renders to what impulse check
// reads of it: a provesGrant call names its grant by four literals, and one that does not
// is refused, since the check would count no proof from it.
func Test_readProof(t *testing.T) {
	t.Parallel()

	const src = `package integration

const south = "s"

var one, two int

type row struct{}

func (r row) name() string {
	return ""
}

func TestIt(t *testing.T) {
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "DisableStaffTenantUser", "heldElsewhere = false")
	provesGrant(t, staff.Roles(), ` + "`TenantAdministrator`" + `, "List", "StaffTenantUsers", "a = \"b\"")
}
`
	proven, declared, err := readProof([]byte(src))
	if err != nil {
		t.Fatalf("readProof() error = %v", err)
	}
	want := []provenGrant{
		{role: "TenantAdministrator", permission: "Execute", resource: "DisableStaffTenantUser", condition: "heldElsewhere = false"},
		{role: "TenantAdministrator", permission: "List", resource: "StaffTenantUsers", condition: `a = "b"`},
	}
	if !slices.Equal(proven, want) {
		t.Errorf("readProof() proves %q, want %q", proven, want)
	}
	if want := []string{"south", "one", "two", "row", "TestIt"}; !slices.Equal(declared, want) {
		t.Errorf("readProof() declares %q, want %q: a method is in its type's scope and not the package's", declared, want)
	}
	if _, _, err := readProof([]byte(strings.Replace(src, `"heldElsewhere = false")`, "condition)", 1))); err == nil || !strings.Contains(err.Error(), "names a grant by something other than four literals") {
		t.Errorf("readProof() error = %v, want a refusal of a grant named by a variable", err)
	}
	if _, _, err := readProof([]byte("package")); err == nil {
		t.Error("readProof() read a file that is not Go")
	}
}

// TestTenancy_proofStands holds the tests the proof stands on to the sign-in the auth has:
// an auth with no password signs people in through the simulated directory of the start
// of Tenant-owned users, by directory, and where no tenant has a directory there is no
// such start and no test of the command's to stand on.
func TestTenancy_proofStands(t *testing.T) {
	t.Parallel()

	a := newBase(t, "members")
	tn := Tenancy{Table: "Tenants", NoDirectories: true}
	why, err := tn.proofStands(a, "members", false)
	if err != nil {
		t.Fatalf("proofStands() error = %v", err)
	}
	if want := "the members auth has no password sign-in and no tenant has a directory, and the command writes no tests that sign a person in to such an application"; why != want {
		t.Errorf("proofStands() = %q, want %q", why, want)
	}
	// The base's own tests are not the ones the command writes for tenants, with or
	// without a password, until tenancy has written them.
	for _, password := range []bool{true, false} {
		why, err := Tenancy{Table: "Tenants"}.proofStands(a, "members", password)
		if err != nil {
			t.Fatalf("proofStands() error = %v", err)
		}
		if !strings.Contains(why, " is not as the command writes it for the members auth") {
			t.Errorf("proofStands(password = %v) = %q, want the first test that is not the command's", password, why)
		}
	}
}
