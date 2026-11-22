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
// application's tests prove is one role's: the reference's own, word for word.
const oneRoleDirective = "//\n//nolint:unparam // The role file's conditional grants are all one role's today; the next role's case names its own.\n"

// provenBy lists the grants a test proves of one role, each as the change names it and
// sorted, read as impulse check reads them: from the literal coordinates of its
// provesGrant calls. A call that names another role fails the test.
func provenBy(t *testing.T, proof, role string) []string {
	t.Helper()

	proven, _, err := readProof([]byte(proof))
	if err != nil {
		t.Fatalf("readProof() error = %v", err)
	}
	found := make([]string, 0, len(proven))
	for _, p := range proven {
		if p.role != role {
			t.Errorf("the test proves a grant of the role %q, want only the role %q", p.role, role)
		}
		found = append(found, p.String())
	}
	slices.Sort(found)

	return found
}

// sorted is a copy of the lines in order.
func sorted(lines []string) []string {
	ordered := slices.Clone(lines)
	slices.Sort(ordered)

	return ordered
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
		// fails its own check as it was made.
		proof := read(t, a, tenantPeopleTest)
		if got := provenBy(t, proof, "TenantAdministrator"); !slices.Equal(got, sorted(held)) {
			t.Errorf("%s proves %q, want each grant the role holds under a condition and no other: %q", tenantPeopleTest, got, sorted(held))
		}
		for _, want := range []string{
			"func TestTenantPeople_aTenantAdministratorActsOnTheTenantsOwnPeopleAlone(t *testing.T) {",
			`provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "DisableStaffTenantUser", "heldElsewhere = false AND heldGlobally = false")`,
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
		// Every grant the test proves is one role's, and the application's lint refuses a
		// parameter that takes one value in every call, so the harness written beside the
		// test says so of provesGrant, as the reference's harness does.
		harness := read(t, a, integrationHarness)
		if got := strings.Count(harness, oneRoleDirective+"func provesGrant("); got != 1 {
			t.Errorf("%s marks provesGrant for the lint %d time(s), want once, as the tenanted skeleton's harness does", integrationHarness, got)
		}
		if formatted, err := format.Source([]byte(harness)); err != nil || string(formatted) != harness {
			t.Errorf("%s is not gofmt-clean as written (format error = %v)", integrationHarness, err)
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
			tenantPeopleTest + ": the test that proves each of the " + strconv.Itoa(len(held)) + " condition(s)": {"conditions-proven", "a person the tenant holds alone"},
			integrationHarness + ": written again with its provesGrant marked for the application's lint":        {"//nolint:unparam", "the TenantAdministrator role's"},
			roles + ": the role Administrator gained the 8 grant(s)":                                             {"Execute on InviteStaffTenant", "List on StaffInvitedTenants"},
			foothold + ": AdministratorRoles() and FirstAdministratorRoles() name TenantAdministrator":           {"TenantAdministratorRoles()", "the application's router is built"},
			deployTest + ": the test of the first administrator's roles":                                         {"every tenant"},
		} {
			if got := linesWith(ch.Did, prefix, texts); len(got) != 1 {
				t.Errorf("the change does not say once %q with %q: %q", prefix, texts, ch.Did)
			}
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
		// A grant the reference holds under a condition is written under it, in this
		// application's names, and the test proves each in the same names: its role, its
		// resources, its routes and its auth's package.
		carried := conditionalGrants(role)
		if len(carried) != len(held) || !slices.ContainsFunc(carried, func(c conditionalGrant) bool {
			return c.String() == `Execute on DisableCrewSectorUser under "heldElsewhere = false AND heldGlobally = false"`
		}) {
			t.Errorf("the role carries %q under a condition, want the reference's %d in this application's names", carried, len(held))
		}
		proof := read(t, a, tenantPeopleTest)
		want := make([]string, 0, len(carried))
		for _, c := range carried {
			want = append(want, c.String())
		}
		if got := provenBy(t, proof, "SectorAdministrator"); !slices.Equal(got, sorted(want)) {
			t.Errorf("%s proves %q, want each grant the role holds under a condition and no other: %q", tenantPeopleTest, got, sorted(want))
		}
		for _, want := range []string{
			`provesGrant(t, crew.Roles(), "SectorAdministrator", "List", "CrewSectorSignInWays", "password = false OR heldElsewhere = false")`,
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
		administrator := roleNamed(file.Roles.Global, "Administrator")
		for permission, resource := range map[string]string{"Create": "Sectors", "Execute": "InviteCrewSector", "List": "CrewInvitedSectors"} {
			if administrator == nil || !slices.ContainsFunc(administrator.Permissions.grants(permission), func(g access.Grant) bool {
				return string(g.Resource) == resource
			}) {
				t.Errorf("the application's administrator does not hold %s on %s", permission, resource)
			}
		}
		if got := linesWith(ch.Did, "pkg/auth/crew/foothold.go: ", []string{"name SectorAdministrator as held in every sector", "SectorAdministratorRoles()"}); len(got) != 1 {
			t.Errorf("the change does not say once where the role is named: %q", ch.Did)
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
		before := read(t, a, roles)
		run := &tenantedRegeneration{collection: referenceCollection(t)}
		run.a = a
		ch, err := Tenancy{Table: "Tenants"}.Apply(t.Context(), a, run)
		if err != nil {
			t.Fatalf("Tenancy.Apply() error = %v", err)
		}
		if read(t, a, roles) != before {
			t.Error("the role file changed, and the application named its own role for a tenant's administrator")
		}
		if read(t, a, foothold) != named {
			t.Error("the foothold changed, and it named a role held in every tenant already")
		}
		if got := linesWith(ch.Did, foothold+": left as it is", []string{"Owner", "TenantAdministratorRoles()"}); len(got) != 1 {
			t.Errorf("the change does not say once that the application's own role stands: %q", ch.Did)
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
