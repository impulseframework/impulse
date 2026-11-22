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
			unserved: `Execute on DisableStaffTenantUser under "heldElsewhere = false AND heldGlobally = false"`,
			wantWhy:  `the test proves Execute on DisableStaffTenantUser under "heldElsewhere = false AND heldGlobally = false", which the role would not carry`,
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
		})
	}
}

// TestTenancy_Apply_provesTheConditionsWithNoPassword holds the role and its proof to the
// application that has no password sign-in, the start of Tenant-owned users, by
// directory: the role carries the grants the application serves, which are not the ones
// that set, require or remove a password, and the test proves each of those and no other,
// signing people in through the simulated directory as the start's other tests do. The
// password version of the test in such an application posts a password to a method it
// does not serve and proves grants its role does not carry.
func TestTenancy_Apply_provesTheConditionsWithNoPassword(t *testing.T) {
	t.Parallel()

	const roles = "pkg/auth/members/roles.json"
	tn := Tenancy{Table: "Tenants", OneTenantPerPerson: true}
	start := byDirectoryStart("members")
	start.Tenants = &tn
	a, _ := started(t, &start, "send_members_user_password_reset_link")
	// The generator serves no management of a password for an auth with no password method.
	collection := withoutResources(t, []byte(names.Rename(string(referenceCollection(t)), "staff", "members")),
		"RequireMembersTenantPasswordChange", "RemoveMembersTenantPassword", "MembersTenantSignInWays", "SendMembersTenantUserPasswordResetLink",
		"RequireMembersPasswordChange", "RemoveMembersPassword", "MembersSignInWays", "SendMembersUserPasswordResetLink", "SetMembersSelfServiceReset",
	)
	ch := tenantedOn(t, a, tn, "members", collection)

	role := roleNamed(roleFileOf(t, a, roles).Roles.Domain, "TenantAdministrator")
	if role == nil {
		t.Fatalf("%s has no domain role TenantAdministrator; the change skipped %q", roles, ch.Skipped)
	}
	held := conditionsOf(role)
	if len(held) != 15 || slices.ContainsFunc(held, func(grant string) bool {
		return strings.Contains(grant, "Password") || strings.Contains(grant, "SignInWays")
	}) {
		t.Errorf("the role holds %d grant(s) under a condition, want the reference's 15 that ask nothing of a password: %q", len(held), held)
	}
	if !exists(a, tenantPeopleTest) {
		t.Fatalf("%s was not written; the change skipped %q", tenantPeopleTest, ch.Skipped)
	}
	proof := read(t, a, tenantPeopleTest)
	if got := provenBy(t, proof, "TenantAdministrator"); !slices.Equal(got, held) {
		t.Errorf("%s proves %q, want each grant the role holds under a condition and no other: %q", tenantPeopleTest, got, held)
	}
	for _, want := range []string{
		`provesGrant(t, members.Roles(), "TenantAdministrator", "Execute", "DisableMembersTenantUser", "heldElsewhere = false AND heldGlobally = false")`,
		"s.auth.SeedCastFile(ctx, io.Discard,", "signIn(ctx, members.PersonaRegistration,", "membersAccount(ctx, t, s, name,", `"/api/assign-members-role"`, `"unlink-members-tenant-session-identity"`,
	} {
		if !strings.Contains(proof, want) {
			t.Errorf("%s does not contain %q", tenantPeopleTest, want)
		}
	}
	if got := strings.Count(read(t, a, integrationHarness), oneRoleDirective+"func provesGrant("); got != 1 {
		t.Errorf("%s marks provesGrant for the lint %d time(s), want once: every grant the test proves is one role's", integrationHarness, got)
	}
	for _, never := range []string{`"password"`, "adminPassword", "passwordRemoval", "tenantWays", "first-day-pass", "second-subject-of-nora", "staff", "Staff"} {
		if strings.Contains(proof, never) {
			t.Errorf("%s contains %q, and the members auth has no password sign-in", tenantPeopleTest, never)
		}
	}
	if got := linesWith(ch.Did, tenantPeopleTest+": the test that proves each of the 15 condition(s)", []string{"the members auth has no password sign-in, so the test signs people in through the simulated directory (the session library's skipAuth build tag)", "under the default build it is skipped, and says so"}); len(got) != 1 {
		t.Errorf("the change does not say once that it wrote the test, and how it signs people in: %q", ch.Did)
	}
	if got := skippedLine(ch, roles); len(got) != 0 {
		t.Errorf("the change leaves a step of the role file to the author: %q", got)
	}
}

// TestTenantPeopleProof_provesTheReferencesConditions holds the test the command writes
// to the role it is written with: rendered in the reference's own names, it proves each
// grant the reference's role holds under a condition and no other, the ones that ask for
// a password left out of the version for an auth that has none. A grant the reference
// gains under a condition is one the command would write no proof of, so every new
// application would fall back to the role without its conditional grants; this fails
// first, and says which proof the template lacks.
func TestTenantPeopleProof_provesTheReferencesConditions(t *testing.T) {
	t.Parallel()

	reference, err := referenceRoles()
	if err != nil {
		t.Fatal(err)
	}
	whole := conditionsOf(roleNamed(reference.Roles.Domain, referenceTenantAdministrator))
	tests := []struct {
		name     string
		password bool
		want     []string
	}{
		{name: "an auth with the password method", password: true, want: whole},
		{name: "an auth with no password method", want: slices.DeleteFunc(slices.Clone(whole), func(grant string) bool {
			return strings.Contains(grant, "Password") || strings.Contains(grant, "SignInWays")
		})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data := &suiteData{Record: "Tenant", Kebab: "tenant", Segment: "tenants", South: devTenantSouth, Password: tt.password}
			files, err := renderSuites(tenantPeopleSuites, data, "example.com/acme/depot", "staff")
			if err != nil {
				t.Fatalf("renderSuites() error = %v", err)
			}
			if got := provenBy(t, string(files[tenantPeopleTest]), referenceTenantAdministrator); !slices.Equal(got, tt.want) {
				t.Errorf("the test proves the conditions (-the reference's role +the test):\n%s", cmp.Diff(tt.want, got))
			}
		})
	}
}

// TestTenancy_unprovenWords holds what the command says where the conditions the role
// would carry are not the ones its test proves: each way round is named, since a grant
// with no proof fails the application's check and a proof with no grant fails its tests.
func TestTenancy_unprovenWords(t *testing.T) {
	t.Parallel()

	tn := Tenancy{Table: "Sectors"}
	carried := func(resource, condition string) conditionalGrant {
		return conditionalGrant{permission: "Execute", grant: access.Grant{Resource: accesstypes.Resource(resource), Condition: condition}}
	}
	proven := func(role, resource, condition string) provenGrant {
		return provenGrant{role: role, permission: "Execute", resource: resource, condition: condition}
	}
	tests := []struct {
		name        string
		conditional []conditionalGrant
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tn.unprovenWords(tt.conditional, tt.proven); got != tt.want {
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
