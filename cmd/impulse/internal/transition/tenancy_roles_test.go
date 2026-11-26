package transition

import (
	"bytes"
	"context"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

// collectionFile is where the base's generator writes its permission collection.
const collectionFile = "pkg/router/" + app.CollectionFileName

// tenantedRegeneration is a runner whose go generate ./... does what the generator of an
// application with tenants does: it writes the permission collection that names the
// tenant record and each auth's management of one tenant, and leaves the auth's
// management without the handlers named. Each run records the role file roleFile names
// as the generator reads it (read), where one is named.
type tenantedRegeneration struct {
	regeneration
	collection []byte
	roleFile   string
	read       []string
}

func (r *tenantedRegeneration) Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	if name == "go" && len(args) > 0 && args[0] == "generate" {
		if err := os.WriteFile(r.a.Abs(collectionFile), r.collection, 0o600); err != nil {
			return nil, errors.Wrap(err, "os.WriteFile()")
		}
		if r.roleFile != "" {
			data, err := os.ReadFile(r.a.Abs(r.roleFile))
			if err != nil {
				return nil, errors.Wrap(err, "os.ReadFile()")
			}
			r.read = append(r.read, string(data))
		}
	}

	return r.regeneration.Run(ctx, dir, env, name, args...)
}

// newTenantedRegeneration is the regeneration of an application with tenants made from
// a base with the auth: it writes the collection, and removes the generated list of the
// roles a person who signs up is given where the base has one.
func newTenantedRegeneration(a *app.App, auth string, collection []byte) *tenantedRegeneration {
	run := &tenantedRegeneration{collection: collection}
	run.a = a
	if signUpRoles := "pkg/identity/" + auth + "handlers/zz_gen_" + auth + "_sign_up_roles.go"; exists(a, signUpRoles) {
		run.removes = []string{signUpRoles}
	}

	return run
}

// referenceCollection reads the permission collection of the tenanted reference
// application, which says what its generators serve.
func referenceCollection(t *testing.T) []byte {
	t.Helper()

	reference, err := skeleton.FS(TenancyReferenceCandidate)
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(reference, collectionFile)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// tenanted applies tenancy to a new base whose regeneration writes the collection, and
// returns the application with the change.
func tenanted(t *testing.T, tn Tenancy, auth string, collection []byte) (*app.App, *Change) {
	t.Helper()

	a := newBase(t, auth)

	return a, tenantedOn(t, a, tn, auth, collection)
}

// tenantedOn applies tenancy to an application as it stands, with a regeneration that
// writes the collection, and returns the change.
func tenantedOn(t *testing.T, a *app.App, tn Tenancy, auth string, collection []byte) *Change {
	t.Helper()

	ch, err := tn.Apply(t.Context(), a, newTenantedRegeneration(a, auth, collection))
	if err != nil {
		t.Fatalf("Tenancy.Apply() error = %v", err)
	}

	return ch
}

// roleFileOf reads an application's role file.
func roleFileOf(t *testing.T, a *app.App, rel string) *roleFile {
	t.Helper()

	file, err := parseRoleFile([]byte(read(t, a, rel)))
	if err != nil {
		t.Fatalf("%s does not read as a role file: %v", rel, err)
	}

	return file
}

// unserved lists the grants of a role file the application does not serve as they are
// written: on a resource the collection does not name, at the other scope, for a
// permission the resource does not take or on a field it does not have.
func unserved(t *testing.T, a *app.App, file *roleFile) []string {
	t.Helper()

	served, err := a.Served()
	if err != nil || served == nil {
		t.Fatalf("App.Served() = %v, %v, want the application's collection", served, err)
	}
	var found []string
	for scope, roles := range map[string][]*roleEntry{app.ScopeGlobal: file.Roles.Global, app.ScopeDomain: file.Roles.Domain} {
		for _, role := range roles {
			for _, permission := range role.Permissions {
				for _, grant := range permission.Grants {
					cut, ok := servedGrant(served, scope, permission.Permission, grant)
					if !ok || len(cut.Fields) != len(grant.Fields) {
						found = append(found, role.Name+": "+permission.Permission+" on "+string(grant.Resource))
					}
				}
			}
		}
	}
	slices.Sort(found)

	return found
}

// recordGrants are a role's grants on what is named for the reference's tenant record,
// by permission, in the file's order.
func recordGrants(role *roleEntry) rolePermissions {
	kept := rolePermissions{}
	for _, permission := range role.Permissions {
		for _, grant := range permission.Grants {
			if ofTheRecord(string(grant.Resource)) {
				kept.add(permission.Permission, grant)
			}
		}
	}

	return kept
}

// unconditional are the grants among them that hold under no condition, and held the
// ones that hold under one, each as the change names it.
func unconditional(permissions rolePermissions) (free rolePermissions, held []string) {
	free = rolePermissions{}
	for _, permission := range permissions {
		for _, grant := range permission.Grants {
			if grant.Condition != "" {
				held = append(held, permission.Permission+" on "+string(grant.Resource)+" under "+strconv.Quote(grant.Condition))

				continue
			}
			free.add(permission.Permission, grant)
		}
	}

	return free, held
}

// oneRoleDirective is the line a harness carries above provesGrant where every grant the
// application's tests prove is one role's, as the command writes it.
const oneRoleDirective = "//\n//nolint:unparam // The role file's conditional grants are all one role's today; the next role's case names its own.\n"

// provenByRole lists the grants a test proves, by the role each provesGrant call names,
// each as the change names it and sorted, read as impulse check reads them.
func provenByRole(t *testing.T, proof string) map[string][]string {
	t.Helper()

	proven, _, err := readProof([]byte(proof))
	if err != nil {
		t.Fatalf("readProof() error = %v", err)
	}
	found := map[string][]string{}
	for _, p := range proven {
		found[p.role] = append(found[p.role], p.String())
	}
	for role := range found {
		slices.Sort(found[role])
	}

	return found
}

// accountConditions lists the grants the reference's own administrator role holds on the
// whole application's methods that act on an account, under managedByTenant = false,
// each as the change names it and sorted: the ones the command holds the application's
// administrator to where a person may be in several tenants.
func accountConditions(t *testing.T) []string {
	t.Helper()

	reference, err := referenceRoles()
	if err != nil {
		t.Fatal(err)
	}
	held := slices.DeleteFunc(conditionsOf(roleNamed(reference.Roles.Global, referenceAdministrator)), func(grant string) bool {
		return !strings.HasPrefix(grant, "Execute on ") || !strings.HasSuffix(grant, ` under "managedByTenant = false"`)
	})
	if len(held) == 0 {
		t.Fatal("the reference's Administrator holds no account method under managedByTenant = false, so this proves nothing of them")
	}

	return held
}

// withoutPassword is the grants among them that ask nothing of a password, which an
// auth with no password method does not serve.
func withoutPassword(grants []string) []string {
	return slices.DeleteFunc(slices.Clone(grants), func(grant string) bool {
		return strings.Contains(grant, "Password") || strings.Contains(grant, "SignInWays")
	})
}

// sorted is a copy of the lines in order.
func sorted(lines []string) []string {
	ordered := slices.Clone(lines)
	slices.Sort(ordered)

	return ordered
}

// grantSet is a role's grants as a set, one line a grant: its permission, its resource,
// its condition and its fields in order of name, the lines sorted. Two roles that hold
// the same grants in another order have the same set.
func grantSet(role *roleEntry) []string {
	var set []string
	for _, permission := range role.Permissions {
		for _, grant := range permission.Grants {
			fields := make([]string, len(grant.Fields))
			for i, field := range grant.Fields {
				fields[i] = string(field)
			}
			slices.Sort(fields)
			set = append(set, permission.Permission+" on "+string(grant.Resource)+" under "+strconv.Quote(grant.Condition)+" ("+strings.Join(fields, ", ")+")")
		}
	}
	slices.Sort(set)

	return set
}

// TestTenancy_Apply_writesTheAdministratorRole pins that an application is given the
// role a tenant's administrator holds as it gains tenants: an application with tenants
// builds no router until one is named, so one left to the author is an application that
// does not start, and whose served tests do not run, as it was made.
func TestTenancy_Apply_writesTheAdministratorRole(t *testing.T) {
	t.Parallel()

	t.Run("the reference's role, on what the application serves", func(t *testing.T) {
		t.Parallel()

		const (
			roles    = "pkg/auth/staff/roles.json"
			foothold = "pkg/auth/staff/foothold.go"
		)
		a, ch := tenanted(t, Tenancy{Table: "Tenants"}, "staff", referenceCollection(t))
		file := roleFileOf(t, a, roles)
		reference, err := referenceRoles()
		if err != nil {
			t.Fatal(err)
		}

		role := roleNamed(file.Roles.Domain, "TenantAdministrator")
		if role == nil {
			t.Fatalf("%s has no domain role TenantAdministrator; the change skipped %q", roles, ch.Skipped)
		}
		whole := recordGrants(roleNamed(reference.Roles.Domain, "TenantAdministrator"))
		if diff := cmp.Diff(whole, role.Permissions); diff != "" {
			t.Errorf("the role's grants are not the reference's on its tenants, each under the reference's condition (-reference +written):\n%s", diff)
		}
		_, held := unconditional(whole)
		if len(held) == 0 {
			t.Fatal("the reference's role holds no grant under a condition, so this proves nothing of them")
		}
		// A grant under a condition is one impulse check holds to a test that proves it, so
		// the role is written with the test: one written without it is an application that
		// fails its own check as it was made. A person may be in several tenants here, so the
		// same test proves the application's own administrator's conditions too.
		proof := read(t, a, tenantPeopleTest)
		accounts := accountConditions(t)
		want := map[string][]string{"TenantAdministrator": sorted(held), "Administrator": accounts}
		if got := provenByRole(t, proof); !cmp.Equal(got, want) {
			t.Errorf("%s proves, by role, other than each grant the two roles hold under a condition (-want +proven):\n%s", tenantPeopleTest, cmp.Diff(want, got))
		}
		for _, want := range []string{
			"func TestTenantPeople_aTenantAdministratorActsOnTheTenantsOwnPeopleAlone(t *testing.T) {",
			`provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "DisableStaffTenantUser", "managedHere = true")`,
			`provesGrant(t, staff.Roles(), "Administrator", "Execute", "DisableStaffUser", "managedByTenant = false")`,
			`"example.com/acme/depot/pkg/auth/staff"`, `"/api/tenants/"+tenant+"/"+method`, "staffAccount(ctx, t, s, name,", `password := "first-day-pass"`, `"/api/assign-staff-role"`,
			`const south = "` + devTenantSouth + `"`, "admin.signIn(ctx, adminUser)",
		} {
			if !strings.Contains(proof, want) {
				t.Errorf("%s does not contain %q", tenantPeopleTest, want)
			}
		}
		if formatted, err := format.Source([]byte(proof)); err != nil || string(formatted) != proof {
			t.Errorf("%s is not gofmt-clean as written (format error = %v)", tenantPeopleTest, err)
		}
		// The test's provesGrant calls name two roles, so the application's lint has nothing
		// to say of the parameter, and a mark would silence nothing, which the lint refuses:
		// the harness stays as the command wrote it for the standing tests.
		if harness := read(t, a, integrationHarness); strings.Contains(harness, "nolint:unparam") {
			t.Errorf("%s marks provesGrant for the lint, and the test beside it names two roles", integrationHarness)
		}
		if got := linesWith(ch.Did, integrationHarness+": written again", nil); len(got) != 0 {
			t.Errorf("the change says it wrote the harness again: %q", got)
		}
		if want := "Runs one tenant: manages its people, their groups and the roles they hold there, and declares the directory they sign in through."; role.Description != want {
			t.Errorf("the role's description = %q, want %q", role.Description, want)
		}
		administrator := roleNamed(file.Roles.Global, "Administrator")
		if administrator == nil {
			t.Fatalf("%s has no global role Administrator", roles)
		}
		if diff := cmp.Diff(recordGrants(roleNamed(reference.Roles.Global, "Administrator")), recordGrants(administrator)); diff != "" {
			t.Errorf("the application's administrator does not hold the reference's grants on tenants (-reference +written):\n%s", diff)
		}
		// Each global role is the reference's whole, and not only on tenants: what the
		// command leaves a role without is a dialog of the whole application that offers its
		// holder nothing.
		if got, want := len(file.Roles.Global), len(reference.Roles.Global); got != want {
			t.Errorf("%s has %d global role(s), want the reference's %d", roles, got, want)
		}
		for _, want := range reference.Roles.Global {
			written := roleNamed(file.Roles.Global, want.Name)
			if written == nil {
				t.Errorf("%s has no global role %s, which the reference's role file holds", roles, want.Name)

				continue
			}
			if diff := cmp.Diff(grantSet(want), grantSet(written)); diff != "" {
				t.Errorf("the global role %s does not hold the reference's grants (-reference +written):\n%s", want.Name, diff)
			}
		}
		// The application's own administrator deletes a tenant, so it is the one who
		// disables it first: its Update on the record names the field, as the reference's
		// does, and its List and Read read it.
		for _, permission := range []string{"List", "Read", "Update"} {
			if got := grantOn(administrator, permission, "Tenants").Fields; !slices.Contains(got, "disabled") {
				t.Errorf("%s on Tenants for the application's administrator names the fields %q, want disabled among them, as the reference's", permission, got)
			}
		}
		// The application's own administrator changes an account from the whole
		// application's side only where no tenant manages it, as the reference's does: each
		// of those grants took the condition, and no other grant of the role holds under one.
		if got := conditionsOf(administrator); !slices.Equal(got, accounts) {
			t.Errorf("the application's administrator holds under a condition (-the reference's account methods +written):\n%s", cmp.Diff(accounts, got))
		}
		if !strings.HasPrefix(administrator.Description, "Manages the application's tenants and people: brings a tenant in by inviting the person who will administer it, ") {
			t.Errorf("the administrator's description does not say the role brings tenants in: %q", administrator.Description)
		}
		if got := unserved(t, a, file); len(got) != 0 {
			t.Errorf("the role file grants what the application does not serve, which the permission engine refuses at start: %q", got)
		}

		src := []byte(read(t, a, foothold))
		for _, name := range []string{administratorRolesFunc, firstAdministratorRolesFunc} {
			named, ok := readFootholdRoles(src, name)
			if !ok || !slices.Equal(named.everyDomain, []string{"TenantAdministrator"}) {
				t.Errorf("%s: %s() names %q as held in every tenant (read = %v), want TenantAdministrator", foothold, name, named.everyDomain, ok)
			}
		}
		if formatted, err := format.Source(src); err != nil || !bytes.Equal(formatted, src) {
			t.Errorf("%s is not gofmt-clean after the change (format error = %v)", foothold, err)
		}
		deploy := read(t, a, deployTest)
		for _, want := range []string{"staffGlobal, staffEveryDomain := staff.FirstAdministratorRoles()", "global: staffGlobal, domain: staffEveryDomain}"} {
			if !strings.Contains(deploy, want) {
				t.Errorf("%s does not hold the role given in every tenant to the role file: no %q", deployTest, want)
			}
		}

		for prefix, texts := range map[string][]string{
			roles + ": the role TenantAdministrator, a domain role":                                              {"a tenant's administrator holds", "the tenanted skeleton's TenantAdministrator", strconv.Itoa(len(held)) + " of them under the condition the skeleton holds each to"},
			tenantPeopleTest + ": the test that proves each of the " + strconv.Itoa(len(held)) + " condition(s)": {"conditions-proven", "a person whose account the tenant manages"},
			roles + ": the role Administrator gained the 10 grant(s)":                                            {"Execute on InviteStaffTenant", "List on StaffInvitedTenants", "List on StaffListedTenants", "List on StaffInvitedTenantRoles"},
			foothold + ": AdministratorRoles() and FirstAdministratorRoles() name TenantAdministrator":           {"TenantAdministratorRoles()", "the application's router is built"},
			deployTest + ": the test of the first administrator's roles":                                         {"every tenant"},
		} {
			if got := linesWith(ch.Did, prefix, texts); len(got) != 1 {
				t.Errorf("the change does not say once %q with %q: %q", prefix, texts, ch.Did)
			}
		}
		heldLine := slices.IndexFunc(ch.Did, func(line string) bool {
			return strings.HasPrefix(line, roles+": the role Administrator changes an account from the whole application's side only where no tenant manages it: "+strconv.Itoa(len(accounts))+" grant(s) took the condition managedByTenant = false, as the tenanted skeleton's Administrator holds them (Execute on DisableStaffUser, Execute on EnableStaffUser, ") &&
				strings.HasSuffix(line, "), each proven by "+tenantPeopleTest)
		})
		facts := slices.IndexFunc(ch.Did, func(line string) bool {
			return strings.HasPrefix(line, roles+": each global role that lists or reads the application's accounts")
		})
		if got := linesWith(ch.Did, roles+": the role Administrator changes an account", nil); len(got) != 1 || heldLine < 0 || heldLine < facts {
			t.Errorf("the change does not say once, after who manages each account is read, that the administrator took the condition on its %d grant(s): %q", len(accounts), ch.Did)
		}
		// Nothing of the role is left to the author: the change names no grant left out.
		if got := skippedLine(ch, roles); len(got) != 0 {
			t.Errorf("the change leaves a step of the role file to the author, and the role is written whole: %q", got)
		}
		meaning := Tenancy{Table: "Tenants"}.MeaningIn(a)
		if !strings.Contains(meaning, "The roles are written.") || strings.Contains(meaning, "is left to write") {
			t.Error("the brief does not say the roles are written, or still says the served tests wait for one")
		}
		if !strings.Contains(meaning, "each under the condition the skeleton holds it to") || !strings.Contains(meaning, "`"+tenantPeopleTest+"`") || strings.Contains(meaning, "holds under a condition are yours to add") {
			t.Error("the brief does not say the role carries each grant under its condition with the test that proves it, or still leaves the conditional grants to the author")
		}
	})

	t.Run("another auth and another record, in the names the generator gives them", func(t *testing.T) {
		t.Parallel()

		const roles = "pkg/auth/crew/roles.json"
		// Lodestar is the crew auth over Sectors: its collection is the generator's own
		// naming of every resource the reference's role grants on.
		collection, err := os.ReadFile("../../../../lodestar/" + collectionFile)
		if err != nil {
			t.Fatal(err)
		}
		a, ch := tenanted(t, Tenancy{Table: "Sectors"}, "crew", collection)
		file := roleFileOf(t, a, roles)

		role := roleNamed(file.Roles.Domain, "SectorAdministrator")
		if role == nil {
			t.Fatalf("%s has no domain role SectorAdministrator; the change skipped %q", roles, ch.Skipped)
		}
		reference, err := referenceRoles()
		if err != nil {
			t.Fatal(err)
		}
		whole := recordGrants(roleNamed(reference.Roles.Domain, "TenantAdministrator"))
		_, held := unconditional(whole)
		if got, want := grantCount(role), grantCount(&roleEntry{Permissions: whole}); got != want {
			t.Errorf("the role carries %d grant(s), want each of the reference's %d in this application's names: %q", got, want, ch.Did)
		}
		if got := unserved(t, a, file); len(got) != 0 {
			t.Errorf("the role file grants what the application does not serve: %q", got)
		}
		fields := func(permission, resource string) []string {
			var found []string
			for _, grant := range role.Permissions.grants(permission) {
				if string(grant.Resource) != resource {
					continue
				}
				for _, field := range grant.Fields {
					found = append(found, string(field))
				}
			}

			return found
		}
		// The record's id is the application's record's, and the tenant of a Microsoft
		// Entra identity stays the directory's word.
		if got := fields("List", "SectorDirectories"); !slices.Contains(got, "sectorId") || slices.Contains(got, "tenantId") {
			t.Errorf("List on SectorDirectories names the fields %q, want sectorId and no tenantId", got)
		}
		if got := fields("List", "CrewSectorIdentities"); !slices.Contains(got, "tenant") {
			t.Errorf("List on CrewSectorIdentities names the fields %q, want the identity's tenant among them", got)
		}
		// The directory is taken with the tenant's own method, which says what taking it
		// does first, under no condition; the record's own create is refused, so the role
		// holds no Create on it, and keeps Delete, List and Read.
		granted := func(permission, resource string) []access.Grant {
			var found []access.Grant
			for _, grant := range role.Permissions.grants(permission) {
				if string(grant.Resource) == resource {
					found = append(found, grant)
				}
			}

			return found
		}
		if got := granted("Execute", "TakeSectorDirectory"); len(got) != 1 || got[0].Condition != "" {
			t.Errorf("Execute on TakeSectorDirectory is granted as %+v, want once under no condition", got)
		}
		if got := granted("Create", "SectorDirectories"); len(got) != 0 {
			t.Errorf("the role is granted Create on SectorDirectories, whose create is refused: %+v", got)
		}
		for _, permission := range []string{"Delete", "List", "Read"} {
			if got := granted(permission, "SectorDirectories"); len(got) != 1 {
				t.Errorf("%s on SectorDirectories is granted %d time(s), want once", permission, len(got))
			}
		}
		// A grant the reference holds under a condition is written under it, in this
		// application's names, and the test proves each in the same names: its role, its
		// resources, its routes and its auth's package.
		carried := conditionalGrants(role)
		if len(carried) != len(held) || !slices.ContainsFunc(carried, func(c conditionalGrant) bool {
			return c.String() == `Execute on DisableCrewSectorUser under "managedHere = true"`
		}) {
			t.Errorf("the role carries %q under a condition, want the reference's %d in this application's names", carried, len(held))
		}
		proof := read(t, a, tenantPeopleTest)
		want := make([]string, 0, len(carried))
		for _, c := range carried {
			want = append(want, c.String())
		}
		proven := provenByRole(t, proof)
		if got := proven["SectorAdministrator"]; !slices.Equal(got, sorted(want)) {
			t.Errorf("%s proves %q, want each grant the role holds under a condition and no other: %q", tenantPeopleTest, got, sorted(want))
		}
		// And the application's own administrator's, on the crew auth's methods of the whole
		// application, as the role now holds them.
		administrator := roleNamed(file.Roles.Global, "Administrator")
		if administrator == nil {
			t.Fatalf("%s has no global role Administrator", roles)
		}
		if got, accounts := proven["Administrator"], conditionsOf(administrator); len(proven) != 2 || len(got) != len(accountConditions(t)) || !slices.Equal(got, accounts) || !slices.Contains(got, `Execute on DisableCrewUser under "managedByTenant = false"`) {
			t.Errorf("%s proves %q of the application's administrator, which holds %q under a condition, want each of the reference's account methods in the crew auth's names", tenantPeopleTest, got, accounts)
		}
		for _, want := range []string{
			`provesGrant(t, crew.Roles(), "SectorAdministrator", "List", "CrewSectorSignInWays", "managedHere = true")`,
			`"/api/sectors/"+tenant+"/"+method`, "crewAccount(ctx, t, s, name,", `"/api/assign-crew-role"`, `spanner.InsertMap("CrewSessionIdentities"`,
		} {
			if !strings.Contains(proof, want) {
				t.Errorf("%s does not contain %q", tenantPeopleTest, want)
			}
		}
		for _, stale := range []string{"staff", "Staff", `"TenantAdministrator"`, "TenantUser", "/tenants/"} {
			if strings.Contains(proof, stale) {
				t.Errorf("%s contains %q, a name of the reference's and not this application's", tenantPeopleTest, stale)
			}
		}
		if got := skippedLine(ch, roles); len(got) != 0 {
			t.Errorf("the change leaves a step of the role file to the author: %q", got)
		}
		for permission, resource := range map[string]string{"Create": "Sectors", "Execute": "InviteCrewSector", "List": "CrewInvitedSectors"} {
			if !slices.ContainsFunc(administrator.Permissions.grants(permission), func(g access.Grant) bool {
				return string(g.Resource) == resource
			}) {
				t.Errorf("the application's administrator does not hold %s on %s", permission, resource)
			}
		}
		if got := linesWith(ch.Did, "pkg/auth/crew/foothold.go: ", []string{"name SectorAdministrator as held in every sector", "SectorAdministratorRoles()"}); len(got) != 1 {
			t.Errorf("the change does not say once where the role is named: %q", ch.Did)
		}
		// The whole application's dialogs ask this auth's list of the roles to give by where
		// each is held, in its own names.
		if got := grantOn(administrator, "List", "CrewGivableRoles").Fields; !slices.Contains(got, "domain") || !slices.Contains(got, "everyDomain") {
			t.Errorf("List on CrewGivableRoles names the fields %q, want domain and everyDomain among them", got)
		}
		if got := linesWith(ch.Did, roles+": each global role that lists the roles to give", []string{"once the application has sectors (", "List on CrewGivableRoles (domain, everyDomain)"}); len(got) != 1 {
			t.Errorf("the change does not say once, in this application's names, where each role to give is held: %q", ch.Did)
		}
	})

	t.Run("where a person belongs to one tenant the application's administrator manages every account", func(t *testing.T) {
		t.Parallel()

		const roles = "pkg/auth/staff/roles.json"
		a, ch := tenanted(t, Tenancy{Table: "Tenants", OneTenantPerPerson: true}, "staff", referenceCollection(t))
		file := roleFileOf(t, a, roles)
		administrator := roleNamed(file.Roles.Global, "Administrator")
		if administrator == nil {
			t.Fatalf("%s has no global role Administrator; the change skipped %q", roles, ch.Skipped)
		}
		if got := conditionsOf(administrator); len(got) != 0 {
			t.Errorf("the application's administrator holds %q under a condition, and a person belongs to one tenant", got)
		}
		role := roleNamed(file.Roles.Domain, "TenantAdministrator")
		if role == nil {
			t.Fatalf("%s has no domain role TenantAdministrator; the change skipped %q", roles, ch.Skipped)
		}
		held := conditionsOf(role)
		if got := provenByRole(t, read(t, a, tenantPeopleTest)); !cmp.Equal(got, map[string][]string{"TenantAdministrator": held}) {
			t.Errorf("%s proves %q, want the tenant's role's %q alone", tenantPeopleTest, got, held)
		}
		// Every grant the test proves is one role's, and the application's lint refuses a
		// parameter that takes one value in every call, so the harness written beside the
		// test says so of provesGrant.
		harness := read(t, a, integrationHarness)
		if got := strings.Count(harness, oneRoleDirective+"func provesGrant("); got != 1 {
			t.Errorf("%s marks provesGrant for the lint %d time(s), want once: every grant the test proves is one role's", integrationHarness, got)
		}
		if formatted, err := format.Source([]byte(harness)); err != nil || string(formatted) != harness {
			t.Errorf("%s is not gofmt-clean as written (format error = %v)", integrationHarness, err)
		}
		lint := linesWith(ch.Did, integrationHarness+": written again with its provesGrant marked for the application's lint (//nolint:unparam): every grant the new test proves is the TenantAdministrator role's", []string{"the lint refuses a parameter that takes one value in every call"})
		if len(lint) != 1 || strings.Contains(lint[0], "as the tenanted skeleton's is") {
			t.Errorf("the change does not say once that it marked the harness, in its words: %q", ch.Did)
		}
		if got := linesWith(ch.Did, roles+": the role Administrator changes an account", nil); len(got) != 0 {
			t.Errorf("the change says the administrator took a condition: %q", got)
		}
		if got := skippedLine(ch, roles); len(got) != 0 {
			t.Errorf("the change leaves a step of the role file to the author: %q", got)
		}
	})

	t.Run("an application that names a role already keeps it", func(t *testing.T) {
		t.Parallel()

		const (
			roles    = "pkg/auth/staff/roles.json"
			foothold = "pkg/auth/staff/foothold.go"
		)
		a := newBase(t, "staff")
		named := strings.Replace(read(t, a, foothold), `return []accesstypes.Role{"Administrator"}, nil`, `return []accesstypes.Role{"Administrator"}, []accesstypes.Role{"Owner"}`, 1)
		if err := os.WriteFile(a.Abs(foothold), []byte(named), 0o600); err != nil {
			t.Fatal(err)
		}
		// The role file is as it was but for where each role to give is held, which the whole
		// application's dialogs ask whatever role a tenant's administrator holds.
		want := roleFileOf(t, a, roles)
		listed := roleNamed(want.Roles.Global, "Administrator")
		if listed == nil {
			t.Fatalf("%s has no global role Administrator", roles)
		}
		gained := 0
		for i := range listed.Permissions {
			for j := range listed.Permissions[i].Grants {
				if grant := &listed.Permissions[i].Grants[j]; listed.Permissions[i].Permission == "List" && grant.Resource == "StaffGivableRoles" {
					grant.Fields = append(grant.Fields, "domain", "everyDomain")
					gained++
				}
			}
		}
		if gained != 1 {
			t.Fatalf("the base's Administrator holds %d List grant(s) on StaffGivableRoles, want one, so this proves nothing", gained)
		}
		run := &tenantedRegeneration{collection: referenceCollection(t)}
		run.a = a
		ch, err := Tenancy{Table: "Tenants"}.Apply(t.Context(), a, run)
		if err != nil {
			t.Fatalf("Tenancy.Apply() error = %v", err)
		}
		if diff := cmp.Diff(want, roleFileOf(t, a, roles)); diff != "" {
			t.Errorf("the role file changed beyond where each role to give is held, and the application named its own role for a tenant's administrator (-want +got):\n%s", diff)
		}
		if read(t, a, foothold) != named {
			t.Error("the foothold changed, and it named a role held in every tenant already")
		}
		if got := linesWith(ch.Did, foothold+": left as it is", []string{"Owner", "TenantAdministratorRoles()"}); len(got) != 1 {
			t.Errorf("the change does not say once that the application's own role stands: %q", ch.Did)
		}
		places := roles + ": each global role that lists the roles to give also reads where each role is held there, which the whole application's dialogs ask the list by once the application has tenants (Administrator: List on StaffGivableRoles (domain, everyDomain))"
		if got := linesWith(ch.Did, roles+": each global role that lists the roles to give", nil); !slices.Equal(got, []string{places}) {
			t.Errorf("the change says %q of where each role to give is held, want once %q", got, places)
		}
	})

	t.Run("a collection that names no tenant leaves the step, and says so", func(t *testing.T) {
		t.Parallel()

		const (
			roles    = "pkg/auth/staff/roles.json"
			foothold = "pkg/auth/staff/foothold.go"
		)
		a := newBase(t, "staff")
		before := read(t, a, foothold)
		ch, err := Tenancy{Table: "Tenants"}.Apply(t.Context(), a, &fakeExec{})
		if err != nil {
			t.Fatalf("Tenancy.Apply() error = %v", err)
		}
		if roleNamed(roleFileOf(t, a, roles).Roles.Domain, "TenantAdministrator") != nil {
			t.Error("a role with no grant the application serves was written")
		}
		if read(t, a, foothold) != before {
			t.Error("the foothold names a role the role file does not hold")
		}
		if got := skippedLine(ch, roles+": the role a tenant's administrator holds was not written", "names none of the staff auth's management of one tenant", "AdministratorRoles() and FirstAdministratorRoles()", "TenantAdministratorRoles() names no role"); len(got) != 1 {
			t.Errorf("the change does not say once why the role was not written and what to do: %q", ch.Skipped)
		}
		meaning := Tenancy{Table: "Tenants"}.MeaningIn(a)
		if strings.Contains(meaning, "The roles are written.") || !strings.Contains(meaning, "is left to write") {
			t.Error("the brief says the roles are written, or does not say what the served tests wait for")
		}
	})
}

// TestTenancy_Apply_regeneratesWithTheRole pins that the application is regenerated once
// the role a tenant's administrator holds is in the role file: the generated metadata
// lists the roles a field may hold from that file, and the role is written after the
// regeneration that serves the tenant record, so an application made without one more
// run fails impulse check's regen as it was made.
func TestTenancy_Apply_regeneratesWithTheRole(t *testing.T) {
	t.Parallel()

	const roles = "pkg/auth/staff/roles.json"
	a := newBase(t, "staff")
	run := newTenantedRegeneration(a, "staff", referenceCollection(t))
	run.roleFile = roles
	ch, err := Tenancy{Table: "Tenants"}.Apply(t.Context(), a, run)
	if err != nil {
		t.Fatalf("Tenancy.Apply() error = %v", err)
	}

	if roleNamed(roleFileOf(t, a, roles).Roles.Domain, "TenantAdministrator") == nil {
		t.Fatalf("%s has no domain role TenantAdministrator; the change skipped %q", roles, ch.Skipped)
	}
	if len(run.read) == 0 || strings.Contains(run.read[0], `"TenantAdministrator"`) {
		t.Fatalf("the first go generate ./... read a role file that held TenantAdministrator already, or none ran (%d run(s)), so this proves nothing of the order", len(run.read))
	}
	if last := run.read[len(run.read)-1]; !strings.Contains(last, `"TenantAdministrator"`) {
		t.Errorf("go generate ./... ran %d time(s), and the last read a role file without TenantAdministrator, so the generated metadata does not list the role", len(run.read))
	}
	if got := linesWith(ch.Did, "ran go generate ./... again once the role file held TenantAdministrator", nil); len(got) != 1 {
		t.Errorf("the change does not say once that it regenerated with the role: %q", ch.Did)
	}
}

// TestTenancy_grantTenantMaking_namesWhatItCouldNotGive pins the hand step the tenancy
// step leaves where no role the foothold names is in the role file: it names the lists a
// tenant is made with (the invitations that stand and the list of tenants), the list of the roles a new tenant's people may start with only
// where a tenant can have a directory, since only there is that list served.
func TestTenancy_grantTenantMaking_namesWhatItCouldNotGive(t *testing.T) {
	t.Parallel()

	const roles = "pkg/auth/staff/roles.json"
	tests := []struct {
		name    string
		tenancy Tenancy
		want    string
	}{
		{
			name:    "a tenant can have a directory",
			tenancy: Tenancy{Table: "Tenants"},
			want:    "and List on StaffInvitedTenants and StaffListedTenants and StaffInvitedTenantRoles, as the tenanted skeleton's Administrator holds them",
		},
		{
			name:    "no tenant has a directory",
			tenancy: Tenancy{Table: "Tenants", NoDirectories: true},
			want:    "and List on StaffInvitedTenants and StaffListedTenants, as the tenanted skeleton's Administrator holds them",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ch := &Change{}
			tt.tenancy.grantTenantMaking(&roleFile{}, &roleFile{}, nil, "staff", roles, &footholdRoles{}, ch)
			if got := skippedLine(ch, roles+": no global role the staff auth's", tt.want); len(got) != 1 {
				t.Errorf("Tenancy.grantTenantMaking() skipped %q, want one line holding %q", ch.Skipped, tt.want)
			}
		})
	}
}

// grantOn is the grant a role holds for the permission on the resource under no
// condition, the zero grant where it holds none.
func grantOn(role *roleEntry, permission, resource string) access.Grant {
	for _, grant := range role.Permissions.grants(permission) {
		if string(grant.Resource) == resource && grant.Condition == "" {
			return grant
		}
	}

	return access.Grant{}
}

// TestTenancy_Apply_readsWhoManagesEachAccount pins that an application given tenancy has
// its own administrators read who manages each account: each global role that lists or
// reads the accounts reads the two facts there, its other fields kept; each that lists or
// reads the identities table's list holds the same grant on the whole application's list
// of the sign-in identities, with the two facts; a role that holds neither gains nothing;
// and the change says so in one line.
func TestTenancy_Apply_readsWhoManagesEachAccount(t *testing.T) {
	t.Parallel()

	const roles = "pkg/auth/staff/roles.json"
	a := newBase(t, "staff")
	before := roleFileOf(t, a, roles)
	ch := tenantedOn(t, a, Tenancy{Table: "Tenants"}, "staff", referenceCollection(t))
	file := roleFileOf(t, a, roles)

	administrator, was := roleNamed(file.Roles.Global, "Administrator"), roleNamed(before.Roles.Global, "Administrator")
	if administrator == nil || was == nil {
		t.Fatalf("%s has no global role Administrator before and after tenancy", roles)
	}
	facts := []accesstypes.Tag{"managedByTenant", "managingTenant"}
	for _, permission := range []string{"List", "Read"} {
		listed := grantOn(was, permission, "StaffListedUsers")
		identities := grantOn(was, permission, "StaffSessionIdentities")
		if len(listed.Fields) == 0 || len(identities.Fields) == 0 {
			t.Fatalf("the base's Administrator holds no %s on StaffListedUsers and StaffSessionIdentities, so this proves nothing", permission)
		}
		if diff := cmp.Diff(slices.Concat(listed.Fields, facts), grantOn(administrator, permission, "StaffListedUsers").Fields); diff != "" {
			t.Errorf("%s on StaffListedUsers does not read who manages each account beside its own fields (-want +got):\n%s", permission, diff)
		}
		if diff := cmp.Diff(slices.Concat(identities.Fields, facts), grantOn(administrator, permission, "StaffListedIdentities").Fields); diff != "" {
			t.Errorf("%s on StaffListedIdentities is not the grant on StaffSessionIdentities with the two facts (-want +got):\n%s", permission, diff)
		}
		if diff := cmp.Diff(identities, grantOn(administrator, permission, "StaffSessionIdentities")); diff != "" {
			t.Errorf("%s on StaffSessionIdentities changed (-before +after):\n%s", permission, diff)
		}
	}
	if diff := cmp.Diff(roleNamed(before.Roles.Global, "FeatureAdministrator"), roleNamed(file.Roles.Global, "FeatureAdministrator")); diff != "" {
		t.Errorf("a role that lists neither the accounts nor the identities gained something (-before +after):\n%s", diff)
	}
	if got := unserved(t, a, file); len(got) != 0 {
		t.Errorf("the role file grants what the application does not serve: %q", got)
	}
	want := roles + ": each global role that lists or reads the application's accounts or their sign-in identities now reads who manages each account there (" +
		"Administrator: List on StaffListedUsers (managedByTenant, managingTenant); Administrator: List on StaffListedIdentities; " +
		"Administrator: Read on StaffListedUsers (managedByTenant, managingTenant); Administrator: Read on StaffListedIdentities)"
	if got := linesWith(ch.Did, roles+": each global role that lists or reads", nil); !slices.Equal(got, []string{want}) {
		t.Errorf("the change says %q of who manages each account, want once %q", got, want)
	}
}

// TestTenancy_Apply_readsWhereEachRoleToGiveIsHeld pins that an application given tenancy
// has its own administrators list the roles to give by where each is held: the whole
// application's dialogs that give a role ask the list by the two fields once the
// application has tenants, and the library refuses the list to a person whose grant does
// not name them, so the dialog offers no role. The grant keeps its own fields, gains the
// two after them, and the change says so in one line.
func TestTenancy_Apply_readsWhereEachRoleToGiveIsHeld(t *testing.T) {
	t.Parallel()

	const roles = "pkg/auth/staff/roles.json"
	a := newBase(t, "staff")
	before := roleFileOf(t, a, roles)
	ch := tenantedOn(t, a, Tenancy{Table: "Tenants"}, "staff", referenceCollection(t))
	file := roleFileOf(t, a, roles)

	administrator, was := roleNamed(file.Roles.Global, "Administrator"), roleNamed(before.Roles.Global, "Administrator")
	if administrator == nil || was == nil {
		t.Fatalf("%s has no global role Administrator before and after tenancy", roles)
	}
	places := []accesstypes.Tag{"domain", "everyDomain"}
	listed := grantOn(was, "List", "StaffGivableRoles")
	if len(listed.Fields) == 0 || slices.ContainsFunc(listed.Fields, func(field accesstypes.Tag) bool {
		return slices.Contains(places, field)
	}) {
		t.Fatalf("the base's Administrator holds List on StaffGivableRoles with the fields %q, want some and neither of %q, so this proves nothing", listed.Fields, places)
	}
	if diff := cmp.Diff(slices.Concat(listed.Fields, places), grantOn(administrator, "List", "StaffGivableRoles").Fields); diff != "" {
		t.Errorf("List on StaffGivableRoles does not read where each role is held beside its own fields (-want +got):\n%s", diff)
	}
	if got := unserved(t, a, file); len(got) != 0 {
		t.Errorf("the role file grants what the application does not serve: %q", got)
	}
	want := roles + ": each global role that lists the roles to give also reads where each role is held there, which the whole application's dialogs ask the list by once the application has tenants (Administrator: List on StaffGivableRoles (domain, everyDomain))"
	if got := linesWith(ch.Did, roles+": each global role that lists the roles to give", nil); !slices.Equal(got, []string{want}) {
		t.Errorf("the change says %q of where each role to give is held, want once %q", got, want)
	}
}

// TestTenancy_Apply_namesWhereEachRoleToGiveIsHeldInARoleFileThatDoesNotParse pins that
// a role file the permission engine would refuse is left as it is, and the change names
// the hand step: the fields to add, on which list, and what waits for them.
func TestTenancy_Apply_namesWhereEachRoleToGiveIsHeldInARoleFileThatDoesNotParse(t *testing.T) {
	t.Parallel()

	const roles = "pkg/auth/staff/roles.json"
	a := newBase(t, "staff")
	broken := `{"auth": "staff", "roles": "none"}` + "\n"
	if err := os.WriteFile(a.Abs(roles), []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	run := &tenantedRegeneration{collection: referenceCollection(t)}
	run.a = a
	ch, err := Tenancy{Table: "Tenants"}.Apply(t.Context(), a, run)
	if err != nil {
		t.Fatalf("Tenancy.Apply() error = %v", err)
	}
	start := roles + ": the role file does not read as the permission engine reads it ("
	if got := skippedLine(ch, start, "), so no role in it was given where each role to give is held: add domain and everyDomain to the fields of each global role's List grant on StaffGivableRoles; until then the whole application's dialogs that give a role offer its holder no role"); len(got) != 1 {
		t.Errorf("the change does not say once that no role was given where each role to give is held: %q", ch.Skipped)
	}
	if got := read(t, a, roles); got != broken {
		t.Errorf("the role file changed, and it does not parse: %q", got)
	}
}

// TestGrantManagingFacts_givesNothingTwice pins that a role file which reads who manages
// each account already gains nothing, and the change says nothing: a grant of the
// accounts that names the two facts keeps its fields, a grant of the identities' list
// the role holds under the twin's condition already is not given again, a role that
// lists neither keeps its grants, and a role held in a tenant is not the step's.
func TestGrantManagingFacts_givesNothingTwice(t *testing.T) {
	t.Parallel()

	const roles = "pkg/auth/staff/roles.json"
	a, _ := tenanted(t, Tenancy{Table: "Tenants"}, "staff", referenceCollection(t))
	served, err := a.Served()
	if err != nil || served == nil {
		t.Fatalf("App.Served() = %v, %v, want the application's collection", served, err)
	}
	grant := func(resource string, fields ...accesstypes.Tag) access.Grant {
		return access.Grant{Resource: accesstypes.Resource(resource), Fields: fields}
	}
	file := &roleFile{Auth: "staff", Roles: roleScopes{
		Global: []*roleEntry{
			{Name: "Auditor", Permissions: rolePermissions{{Permission: "List", Grants: []access.Grant{
				grant("StaffListedUsers", "username", "managedByTenant", "managingTenant"),
				grant("StaffSessionIdentities", "method", "userId"),
				grant("StaffListedIdentities", "method", "userId", "managedByTenant", "managingTenant"),
			}}}},
			{Name: "Viewer", Permissions: rolePermissions{{Permission: "List", Grants: []access.Grant{grant("StaffSessionUsers", "username")}}}},
		},
		Domain: []*roleEntry{
			{Name: "TenantAdministrator", Permissions: rolePermissions{{Permission: "List", Grants: []access.Grant{grant("StaffSessionIdentities", "method")}}}},
		},
	}}
	was, err := file.encode()
	if err != nil {
		t.Fatal(err)
	}

	ch := &Change{}
	grantManagingFacts(file, served, "staff", roles, ch)
	got, err := file.encode()
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(was), string(got)); diff != "" {
		t.Errorf("grantManagingFacts() changed a role file that reads who manages each account already (-before +after):\n%s", diff)
	}
	if len(ch.Did) != 0 {
		t.Errorf("grantManagingFacts() said %q, and nothing was gained", ch.Did)
	}
}

// TestGrantRolePlaces_givesEachListGrantThePlacesItLacks pins which grants gain where
// each role to give is held: a global role's List grant on the list that names fields
// gains each of the two it does not name, after its own fields and under its own
// condition; a grant of every field, a bare grant, a grant of another permission or on
// another resource, and a role held in a tenant are left as they are. A second pass
// gains nothing and says nothing, and an application whose list has neither field gains
// nothing either.
func TestGrantRolePlaces_givesEachListGrantThePlacesItLacks(t *testing.T) {
	t.Parallel()

	const (
		roles = "pkg/auth/staff/roles.json"
		list  = "StaffGivableRoles"
		held  = "method = InviteStaffUser"
	)
	tn := Tenancy{Table: "Tenants"}
	withTenants, _ := tenanted(t, tn, "staff", referenceCollection(t))
	served, err := withTenants.Served()
	if err != nil || served == nil {
		t.Fatalf("App.Served() = %v, %v, want the application's collection", served, err)
	}
	unplaced, err := newBase(t, "staff").Served()
	if err != nil || unplaced == nil {
		t.Fatalf("App.Served() = %v, %v, want the base's collection", unplaced, err)
	}
	grant := func(resource, condition string, fields ...accesstypes.Tag) access.Grant {
		return access.Grant{Resource: accesstypes.Resource(resource), Condition: condition, Fields: fields}
	}
	role := func(name, permission string, g access.Grant) *roleEntry {
		return &roleEntry{Name: name, Permissions: rolePermissions{{Permission: permission, Grants: []access.Grant{g}}}}
	}
	file := func() *roleFile {
		return &roleFile{Auth: "staff", Roles: roleScopes{
			Global: []*roleEntry{
				role("Neither", "List", grant(list, "", "description", "method")),
				role("One", "List", grant(list, "", "description", "everyDomain")),
				role("Both", "List", grant(list, "", "description", "domain", "everyDomain")),
				role("Every", "List", grant(list, "", "*")),
				role("Bare", "List", grant(list, "")),
				role("Held", "List", grant(list, held, "description")),
				role("Elsewhere", "List", grant("StaffListedUsers", "", "username")),
				role("Reader", "Read", grant(list, "", "description")),
			},
			Domain: []*roleEntry{
				role("TenantAdministrator", "List", grant(list, "", "description")),
			},
		}}
	}

	placed := file()
	ch := &Change{}
	if !tn.grantRolePlaces(placed, served, "staff", roles, ch) {
		t.Error("grantRolePlaces() = false, want true: grants that name fields lack where each role is held")
	}
	for _, tt := range []struct {
		role       string
		domain     bool
		permission string
		want       access.Grant
	}{
		{role: "Neither", permission: "List", want: grant(list, "", "description", "method", "domain", "everyDomain")},
		{role: "One", permission: "List", want: grant(list, "", "description", "everyDomain", "domain")},
		{role: "Both", permission: "List", want: grant(list, "", "description", "domain", "everyDomain")},
		{role: "Every", permission: "List", want: grant(list, "", "*")},
		{role: "Bare", permission: "List", want: grant(list, "")},
		{role: "Held", permission: "List", want: grant(list, held, "description", "domain", "everyDomain")},
		{role: "Elsewhere", permission: "List", want: grant("StaffListedUsers", "", "username")},
		{role: "Reader", permission: "Read", want: grant(list, "", "description")},
		{role: "TenantAdministrator", domain: true, permission: "List", want: grant(list, "", "description")},
	} {
		scope := placed.Roles.Global
		if tt.domain {
			scope = placed.Roles.Domain
		}
		r := roleNamed(scope, tt.role)
		if r == nil {
			t.Fatalf("the role file has no role %s", tt.role)
		}
		if diff := cmp.Diff([]access.Grant{tt.want}, r.Permissions.grants(tt.permission)); diff != "" {
			t.Errorf("%s: %s grants after grantRolePlaces() (-want +got):\n%s", tt.role, tt.permission, diff)
		}
	}
	want := roles + ": each global role that lists the roles to give also reads where each role is held there, which the whole application's dialogs ask the list by once the application has tenants (" +
		"Neither: List on StaffGivableRoles (domain, everyDomain); One: List on StaffGivableRoles (domain); Held: List on StaffGivableRoles (domain, everyDomain))"
	if !slices.Equal(ch.Did, []string{want}) {
		t.Errorf("grantRolePlaces() said %q, want once %q", ch.Did, want)
	}

	was, err := placed.encode()
	if err != nil {
		t.Fatal(err)
	}
	again := &Change{}
	if tn.grantRolePlaces(placed, served, "staff", roles, again) {
		t.Error("grantRolePlaces() = true a second time, and every grant reads where each role is held already")
	}
	if got, err := placed.encode(); err != nil || string(got) != string(was) {
		t.Errorf("grantRolePlaces() changed the role file a second time (-first +second):\n%s", cmp.Diff(string(was), string(got)))
	}
	if len(again.Did)+len(again.Skipped) != 0 {
		t.Errorf("grantRolePlaces() said %q and skipped %q a second time, and nothing was gained", again.Did, again.Skipped)
	}

	// An application without tenants lists the roles to give with neither field, so none
	// is given.
	bare := file()
	was, err = bare.encode()
	if err != nil {
		t.Fatal(err)
	}
	none := &Change{}
	if tn.grantRolePlaces(bare, unplaced, "staff", roles, none) {
		t.Error("grantRolePlaces() = true over a list with neither field")
	}
	if got, err := bare.encode(); err != nil || string(got) != string(was) {
		t.Errorf("grantRolePlaces() changed the role file over a list with neither field (-before +after):\n%s", cmp.Diff(string(was), string(got)))
	}
	if len(none.Did)+len(none.Skipped) != 0 {
		t.Errorf("grantRolePlaces() said %q and skipped %q over a list with neither field", none.Did, none.Skipped)
	}
}

// TestRoleFile_keepsTheFile pins that a role file read to be changed is written back as
// it was: every role file the command renders reads and writes to the same bytes, so a
// change to one is the change alone, the file's auth stays its first key, and the
// comparison signs of a condition stay as a person typed them.
func TestRoleFile_keepsTheFile(t *testing.T) {
	t.Parallel()

	for _, candidate := range []string{skeleton.Base, TenancyReferenceCandidate, "outlets", "sites"} {
		sub, err := skeleton.FS(candidate)
		if err != nil {
			t.Fatal(err)
		}
		files, err := fs.Glob(sub, "pkg/auth/*/"+app.RolesFileName)
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: role files = %q, %v", candidate, files, err)
		}
		for _, rel := range files {
			data, err := fs.ReadFile(sub, rel)
			if err != nil {
				t.Fatal(err)
			}
			file, err := parseRoleFile(data)
			if err != nil {
				t.Fatalf("%s/%s: parseRoleFile() error = %v", candidate, rel, err)
			}
			written, err := file.encode()
			if err != nil {
				t.Fatalf("%s/%s: encode() error = %v", candidate, rel, err)
			}
			if diff := cmp.Diff(string(data), string(written)); diff != "" {
				t.Errorf("%s/%s is not written back as it was read (-file +written):\n%s", candidate, rel, diff)
			}
		}
	}
}

func TestRolePermissions_add(t *testing.T) {
	t.Parallel()

	grant := func(resource, condition string) access.Grant {
		return access.Grant{Resource: accesstypes.Resource(resource), Condition: condition}
	}
	names := func(p rolePermissions) []string {
		var found []string
		for i := range p {
			found = append(found, p[i].Permission)
		}

		return found
	}

	sorted := rolePermissions{{Permission: "Create"}, {Permission: "List", Grants: []access.Grant{grant("Orders", "")}}}
	if sorted.add("List", grant("Orders", "")) {
		t.Error("add() gave a grant the role carries already")
	}
	if !sorted.add("List", grant("Orders", "open = true")) || len(sorted.grants("List")) != 2 {
		t.Error("add() did not give the grant held to another condition")
	}
	if !sorted.add("Execute", grant("Orders", "")) || !slices.Equal(names(sorted), []string{"Create", "Execute", "List"}) {
		t.Errorf("add() placed a new permission at %q, want it among the others by name", names(sorted))
	}
	unsorted := rolePermissions{{Permission: "List"}, {Permission: "Create"}}
	if !unsorted.add("Execute", grant("Orders", "")) || !slices.Equal(names(unsorted), []string{"List", "Create", "Execute"}) {
		t.Errorf("add() placed a new permission at %q in a file that orders them its own way, want it last", names(unsorted))
	}
}

func TestReadFootholdRoles(t *testing.T) {
	t.Parallel()

	const header = "package staff\n\nimport \"github.com/impulseframework/impulse/accesstypes\"\n\n"
	tests := []struct {
		name            string
		body            string
		wantOK          bool
		wantGlobal      []string
		wantEveryDomain []string
	}{
		{
			name:       "the base's form",
			body:       "func AdministratorRoles() (global, everyDomain []accesstypes.Role) {\n\treturn []accesstypes.Role{\"Administrator\"}, nil\n}\n",
			wantOK:     true,
			wantGlobal: []string{"Administrator"},
		},
		{
			name:            "a role held in every tenant",
			body:            "func AdministratorRoles() (global, everyDomain []accesstypes.Role) {\n\treturn []accesstypes.Role{\"Administrator\"}, []accesstypes.Role{\"TenantAdministrator\"}\n}\n",
			wantOK:          true,
			wantGlobal:      []string{"Administrator"},
			wantEveryDomain: []string{"TenantAdministrator"},
		},
		{
			name: "roles computed are not read",
			body: "func AdministratorRoles() (global, everyDomain []accesstypes.Role) {\n\treturn roles(), nil\n}\n",
		},
		{
			name: "more than a return is not read",
			body: "func AdministratorRoles() (global, everyDomain []accesstypes.Role) {\n\tglobal = nil\n\n\treturn global, nil\n}\n",
		},
		{
			name: "no such function",
			body: "func Roles() []accesstypes.Role {\n\treturn nil\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := readFootholdRoles([]byte(header+tt.body), administratorRolesFunc)
			if ok != tt.wantOK {
				t.Fatalf("readFootholdRoles() ok = %v, want %v", ok, tt.wantOK)
			}
			if !slices.Equal(got.global, tt.wantGlobal) || !slices.Equal(got.everyDomain, tt.wantEveryDomain) {
				t.Errorf("readFootholdRoles() = %q, %q, want %q, %q", got.global, got.everyDomain, tt.wantGlobal, tt.wantEveryDomain)
			}
			if ok && got.resultType != "[]accesstypes.Role" {
				t.Errorf("readFootholdRoles() result type = %q, want []accesstypes.Role", got.resultType)
			}
		})
	}
}

// lockedRegeneration is a tenanted regeneration that takes every permission off one file
// of the application as go generate ./... runs. The regeneration is the last step before
// the roles are written, so every step before it reads the file, and the step that writes
// the roles finds it there and cannot read it.
type lockedRegeneration struct {
	tenantedRegeneration
	locks string
}

func (r *lockedRegeneration) Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	if name == "go" && len(args) > 0 && args[0] == "generate" {
		if err := os.Chmod(r.a.Abs(r.locks), 0); err != nil {
			return nil, errors.Wrap(err, "os.Chmod()")
		}
	}

	return r.tenantedRegeneration.Run(ctx, dir, env, name, args...)
}

// skipWhereNoFileIsUnreadable skips a test that needs a file it cannot read where the
// user it runs as reads a file whatever its mode says, as root does.
func skipWhereNoFileIsUnreadable(t *testing.T) {
	t.Helper()

	if os.Geteuid() == 0 {
		t.Skip("the test runs as root, which reads a file whatever its mode, so it can make no file unreadable")
	}
	probe := filepath.Join(t.TempDir(), "probe")
	if err := os.WriteFile(probe, nil, 0); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	if _, err := os.ReadFile(probe); err == nil {
		t.Skip("the test's user reads a file with every permission taken off it, so it can make no file unreadable")
	}
}

// A file the role step reads that is there and cannot be read stops the command with the
// error: read as a file that is not there, the foothold would have the command say the
// application has none, a test the proof of the role's conditions stands on would have it
// say the test is not the command's and write the role without its conditional grants, and
// the deployment test would be passed over with nothing said.
func TestTenancy_Apply_stopsOnAFileItCannotRead(t *testing.T) {
	t.Parallel()

	const (
		roles    = "pkg/auth/staff/roles.json"
		foothold = "pkg/auth/staff/foothold.go"
	)
	for _, tt := range []struct {
		name string
		file string
		// wantWritten is whether the role is in the role file by the time the file is read.
		wantWritten bool
	}{
		{name: "the foothold", file: foothold},
		{name: "a test the proof of the role's conditions stands on", file: grantsSuite},
		{name: "the deployment test", file: deployTest, wantWritten: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			skipWhereNoFileIsUnreadable(t)

			a := newBase(t, "staff")
			run := &lockedRegeneration{locks: tt.file}
			run.collection = referenceCollection(t)
			run.a = a
			run.removes = []string{"pkg/identity/staffhandlers/zz_gen_staff_sign_up_roles.go"}

			ch, err := Tenancy{Table: "Tenants"}.Apply(t.Context(), a, run)
			if err == nil {
				t.Fatalf("Tenancy.Apply() error = nil with %s there and unreadable; the change skipped %q", tt.file, ch.Skipped)
			}
			if !errors.Is(err, fs.ErrPermission) {
				t.Errorf("Tenancy.Apply() error = %v, want the refusal to read %s", err, tt.file)
			}
			if got := roleNamed(roleFileOf(t, a, roles).Roles.Domain, "TenantAdministrator") != nil; got != tt.wantWritten {
				t.Errorf("%s has the role TenantAdministrator = %t, want %t", roles, got, tt.wantWritten)
			}
		})
	}
}

// TestWriteRolePlaces_stopsOnARoleFileItCannotRead pins that a role file that is there
// and cannot be read stops the command with the error: read as a file that is not there,
// the change would say nothing, and the whole application's dialogs would offer its
// roles' holders no role.
func TestWriteRolePlaces_stopsOnARoleFileItCannotRead(t *testing.T) {
	t.Parallel()
	skipWhereNoFileIsUnreadable(t)

	const roles = "pkg/auth/staff/roles.json"
	tn := Tenancy{Table: "Tenants"}
	a, _ := tenanted(t, tn, "staff", referenceCollection(t))
	if err := os.Chmod(a.Abs(roles), 0); err != nil {
		t.Fatal(err)
	}
	ch := &Change{}
	if err := tn.writeRolePlaces(a, ch); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("writeRolePlaces() error = %v, want the refusal to read %s", err, roles)
	}
	if len(ch.Did)+len(ch.Skipped) != 0 {
		t.Errorf("writeRolePlaces() said %q and skipped %q, and it read no role file", ch.Did, ch.Skipped)
	}
}
