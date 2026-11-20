package transition

import (
	"bytes"
	"context"
	"go/format"
	"io/fs"
	"os"
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
// management without the handlers named.
type tenantedRegeneration struct {
	regeneration
	collection []byte
}

func (r *tenantedRegeneration) Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	if name == "go" && len(args) > 0 && args[0] == "generate" {
		if err := os.WriteFile(r.a.Abs(collectionFile), r.collection, 0o600); err != nil {
			return nil, errors.Wrap(err, "os.WriteFile()")
		}
	}

	return r.regeneration.Run(ctx, dir, env, name, args...)
}

// referenceFile reads a file of the tenanted reference application.
func referenceFile(t *testing.T, rel string) []byte {
	t.Helper()

	reference, err := skeleton.FS(TenancyReferenceCandidate)
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(reference, rel)
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
	run := &tenantedRegeneration{collection: collection}
	run.a = a
	run.removes = []string{"pkg/identity/" + auth + "handlers/zz_gen_" + auth + "_sign_up_roles.go"}
	ch, err := tn.Apply(t.Context(), a, run)
	if err != nil {
		t.Fatalf("Tenancy.Apply() error = %v", err)
	}

	return a, ch
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
		a, ch := tenanted(t, Tenancy{Table: "Tenants"}, "staff", referenceFile(t, collectionFile))
		file := roleFileOf(t, a, roles)
		reference, err := referenceRoles()
		if err != nil {
			t.Fatal(err)
		}

		role := roleNamed(file.Roles.Domain, "TenantAdministrator")
		if role == nil {
			t.Fatalf("%s has no domain role TenantAdministrator; the change skipped %q", roles, ch.Skipped)
		}
		free, held := unconditional(recordGrants(roleNamed(reference.Roles.Domain, "TenantAdministrator")))
		if diff := cmp.Diff(free, role.Permissions); diff != "" {
			t.Errorf("the role's grants are not the reference's on its tenants that hold under no condition (-reference +written):\n%s", diff)
		}
		if len(held) == 0 {
			t.Fatal("the reference's role holds no grant under a condition, so this proves nothing of them")
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
			roles + ": the role TenantAdministrator, a domain role":                                    {"a tenant's administrator holds", "the tenanted skeleton's TenantAdministrator"},
			roles + ": the role Administrator gained the 8 grant(s)":                                   {"Execute on InviteStaffTenant", "List on StaffInvitedTenants"},
			foothold + ": AdministratorRoles() and FirstAdministratorRoles() name TenantAdministrator": {"TenantAdministratorRoles()", "the application's router is built"},
			deployTest + ": the test of the first administrator's roles":                               {"every tenant"},
		} {
			if got := linesWith(ch.Did, prefix, texts); len(got) != 1 {
				t.Errorf("the change does not say once %q with %q: %q", prefix, texts, ch.Did)
			}
		}
		// A grant under a condition is the author's, with the test that proves it: impulse
		// check refuses a conditional grant no test proves, so one written here is an
		// application that fails its own check as it was made.
		left := skippedLine(ch, roles+": "+strconv.Itoa(len(held))+" grant(s) the tenanted skeleton's TenantAdministrator holds under a condition were not written into TenantAdministrator", append([]string{"conditions-proven", "test/integration/tenant_people_test.go"}, held...)...)
		if len(left) != 1 || len(skippedLine(ch, roles)) != 1 {
			t.Errorf("the change does not say once, and as the one step of the role file left, which %d grant(s) under a condition are the author's to add: %q", len(held), ch.Skipped)
		}
		meaning := Tenancy{Table: "Tenants"}.MeaningIn(a)
		if !strings.Contains(meaning, "The roles are written.") || strings.Contains(meaning, "is left to write") {
			t.Error("the brief does not say the roles are written, or still says the served tests wait for one")
		}
		if !strings.Contains(meaning, "holds under no condition") || !strings.Contains(meaning, "each with the test that proves it") {
			t.Error("the brief does not say the role carries no grant under a condition, and that those are the author's to add with their proof")
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
		free, held := unconditional(recordGrants(roleNamed(reference.Roles.Domain, "TenantAdministrator")))
		if got, want := grantCount(role), grantCount(&roleEntry{Permissions: free}); got != want {
			t.Errorf("the role carries %d grant(s), want each of the reference's %d under no condition in this application's names: %q", got, want, ch.Did)
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
		// A grant the reference holds under a condition is never written without it: it is
		// left out, and named in this application's names with the condition to hold it to.
		for _, permission := range role.Permissions {
			for _, grant := range permission.Grants {
				if grant.Condition != "" || string(grant.Resource) == "DisableCrewSectorUser" {
					t.Errorf("the role carries %s on %s (condition %q), and no test of the application proves a condition", permission.Permission, grant.Resource, grant.Condition)
				}
			}
		}
		if got := skippedLine(ch, roles+": "+strconv.Itoa(len(held))+" grant(s) ", "were not written into SectorAdministrator", `Execute on DisableCrewSectorUser under "heldElsewhere = false AND heldGlobally = false"`, `List on CrewSectorSignInWays under "password = false OR heldElsewhere = false"`, "a sector's administrator does none of them"); len(got) != 1 {
			t.Errorf("the change does not say once which grants under a condition are left, in this application's names: %q", ch.Skipped)
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
		run := &tenantedRegeneration{collection: referenceFile(t, collectionFile)}
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

// TestRoleFile_keepsTheFile pins that a role file read to be changed is written back as
// it was: every role file the command renders reads and writes to the same bytes, so a
// change to one is the change alone, and the comparison signs of a condition stay as a
// person typed them.
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
