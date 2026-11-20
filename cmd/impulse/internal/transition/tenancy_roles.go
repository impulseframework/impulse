package transition

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

// The roles half of add tenancy: the role a tenant's administrator holds, written into
// the role file of the auth whose people make tenants and named where the application
// reads it, and the grants the application's own administrator makes a tenant with. An
// application with tenants builds no router until a tenant's administrator role is
// named, since the generated create-your-tenant route gives its creator that role, so
// the command names one: the application then starts, and its served tests run, as it
// was made.
//
// The role is the tenanted reference application's (TenancyReferenceCandidate), in the
// application's own names and cut to what the application serves: the reference's role
// is the one the framework's own suites prove, each method that changes a person held to
// the condition that keeps a tenant's administrator from changing a person another
// tenant also holds, and a grant written from the collection alone would carry none of
// them.

// footholdFile is the file of an auth's package that names its administrators' roles.
const footholdFile = "foothold.go"

// The two functions of an auth's foothold that name roles by where they are held: the
// roles that count as an administrator's, and the ones the first administrator is given.
const (
	administratorRolesFunc      = "AdministratorRoles"
	firstAdministratorRolesFunc = "FirstAdministratorRoles"
)

// The reference's two administrator roles, as its role file names them: the
// application's own and a tenant's.
const (
	referenceAdministrator       = "Administrator"
	referenceTenantAdministrator = "TenantAdministrator"
)

// referenceRecord is the tenant record of the candidate the roles are read from, the
// singular of referenceTable.
const referenceRecord = "Tenant"

// AdministratorRole is the role tenancy writes for a tenant's administrator: the
// record's name with Administrator after it, as the generated contract names the
// accessor that answers it (<Record>AdministratorRoles) and as the reference names its
// own (TenantAdministrator).
func (tn Tenancy) AdministratorRole() string {
	return tn.Record() + "Administrator"
}

// roleHandStep is what is left to the author where the command could not write the
// role: the step, and what waits for it.
func (tn Tenancy) roleHandStep(auth string) string {
	return fmt.Sprintf("give the %[1]s auth's role file a domain role for a %[2]s's administrator (the tenanted skeleton's %[3]s is the pattern: its grants on the auth's people in one %[2]s, their groups and their roles) and return it, as held in every %[2]s, from %[4]s() and %[5]s() in the auth's %[6]s; until then %[7]s() names no role, the generated create-your-%[2]s route refuses to be mounted, and neither the application nor a test that builds its router runs", auth, tn.oneLower(), referenceTenantAdministrator, administratorRolesFunc, firstAdministratorRolesFunc, footholdFile, tn.rolesAccessor())
}

// writeRoles writes the role a tenant's administrator holds into the role file of the
// auth whose people make tenants, names it in the auth's foothold as held in every
// tenant, and gives the application's own administrator role the grants a tenant is
// made with. An application whose foothold names such a role already is left as it is.
// It runs after the regeneration, since the role grants only what the application
// serves, which the regenerated collection says.
func (tn Tenancy) writeRoles(a *app.App, ch *Change) error {
	seat := tn.seatAuth(a)
	if seat == nil {
		return nil
	}
	foothold := path.Join(seat.Dir, footholdFile)
	rel := path.Join(seat.Dir, app.RolesFileName)
	src, footholdMode, err := readFile(a, foothold)
	if err != nil {
		ch.skipf("%s: not there, so the role a %s's administrator holds was not written: %s", foothold, tn.oneLower(), tn.roleHandStep(seat.Name))

		return nil
	}
	named, ok := readFootholdRoles(src, administratorRolesFunc)
	switch {
	case !ok:
		ch.skipf("%s: %s() is not as the skeleton writes it (one return of the two lists of roles), so the role a %s's administrator holds was not written: %s", foothold, administratorRolesFunc, tn.oneLower(), tn.roleHandStep(seat.Name))

		return nil
	case len(named.everyDomain) > 0:
		ch.didf("%s: left as it is, since %s() names a role held in every %s already (%s), which %s() answers", foothold, administratorRolesFunc, tn.oneLower(), strings.Join(named.everyDomain, ", "), tn.rolesAccessor())

		return nil
	}
	served, err := a.Served()
	if err != nil {
		return err
	}
	data, mode, err := readFile(a, rel)
	var file *roleFile
	if err == nil {
		file, err = parseRoleFile(data)
	}
	if served == nil || err != nil {
		ch.skipf("%s: the role a %s's administrator holds was not written, since %s: %s", rel, tn.oneLower(), unreadWords(served, err), tn.roleHandStep(seat.Name))

		return nil
	}
	reference, err := referenceRoles()
	if err != nil {
		return err
	}

	role := tn.AdministratorRole()
	if roleNamed(file.Roles.Global, role) != nil {
		ch.skipf("%s: %s is a global role of the file, and a %s's administrator holds a domain role, so none was written: %s", rel, role, tn.oneLower(), tn.roleHandStep(seat.Name))

		return nil
	}
	if roleNamed(file.Roles.Domain, role) == nil {
		entry, left := tn.tenantAdministrator(reference, served, seat.Name)
		if len(entry.Permissions) == 0 {
			ch.skipf("%s: the role a %s's administrator holds was not written, since the application's permission collection (%s) names none of the %s auth's management of one %s: %s", rel, tn.oneLower(), strings.Join(served.Files, ", "), seat.Name, tn.oneLower(), tn.roleHandStep(seat.Name))

			return nil
		}
		file.Roles.Domain = append(file.Roles.Domain, entry)
		ch.didf("%s: the role %s, a domain role, which a %s's administrator holds: the grants of the tenanted skeleton's %s on what this application serves, %d in all, on the %s auth's people in one %s, their groups and the roles they hold there%s%s", rel, role, tn.oneLower(), referenceTenantAdministrator, grantCount(entry), seat.Name, tn.oneLower(), tn.directoryWords(), leftOutWords(left))
	} else {
		ch.didf("%s: the domain role %s was there already and is left as it is", rel, role)
	}
	tn.grantTenantMaking(file, reference, served, seat.Name, rel, named, ch)

	encoded, err := file.encode()
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.Abs(rel), encoded, mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}

	return tn.nameAdministrator(a, foothold, src, footholdMode, ch)
}

// unreadWords says why the role could not be cut to what the application serves: no
// collection was read, or the role file does not stand.
func unreadWords(served *app.Served, err error) string {
	if served == nil {
		return "no generator of the application has written a permission collection, which says what the role may grant"
	}

	return "the role file does not read as the permission engine reads it (" + refusalText(err) + ")"
}

// refusalText is an error's cause in words, without the chain's source locations.
func refusalText(err error) string {
	return errors.Cause(err).Error()
}

// directoryWords names the tenant's directory among what the role covers, where a
// tenant can have one.
func (tn Tenancy) directoryWords() string {
	if tn.NoDirectories {
		return ""
	}

	return fmt.Sprintf(", and the %s's directory and how it admits that directory's people", tn.oneLower())
}

// leftOutWords names the reference's grants the role does not carry, on what the
// application does not serve.
func leftOutWords(left []string) string {
	if len(left) == 0 {
		return ""
	}

	return fmt.Sprintf("; %d of the skeleton's grants are on what this application does not serve and were left out (%s)", len(left), strings.Join(left, ", "))
}

// grantCount counts a role's grants.
func grantCount(role *roleEntry) int {
	n := 0
	for i := range role.Permissions {
		n += len(role.Permissions[i].Grants)
	}

	return n
}

// referenceRoles reads the tenanted reference application's role file.
func referenceRoles() (*roleFile, error) {
	reference, err := skeleton.FS(TenancyReferenceCandidate)
	if err != nil {
		return nil, err
	}
	data, err := fs.ReadFile(reference, path.Join("pkg/auth", skeleton.PlaceholderAuth, app.RolesFileName))
	if err != nil {
		return nil, errors.Wrap(err, "fs.ReadFile()")
	}

	return parseRoleFile(data)
}

// tenantAdministrator is the role a tenant's administrator holds in this application:
// the reference's, in the application's names, with the grants on what the application
// serves in a tenant. left names the reference's grants that were left out.
func (tn Tenancy) tenantAdministrator(reference *roleFile, served *app.Served, auth string) (entry *roleEntry, left []string) {
	entry = &roleEntry{Name: tn.AdministratorRole(), Description: tn.administratorDescription(), Permissions: rolePermissions{}}
	source := roleNamed(reference.Roles.Domain, referenceTenantAdministrator)
	if source == nil {
		return entry, nil
	}
	for i := range source.Permissions {
		permission := source.Permissions[i].Permission
		for _, grant := range source.Permissions[i].Grants {
			if !ofTheRecord(string(grant.Resource)) {
				continue
			}
			cut, ok := servedGrant(served, app.ScopeDomain, permission, tn.inApplicationNames(grant, served, auth))
			if !ok {
				left = append(left, permission+" on "+tn.roleResource(string(grant.Resource), auth))

				continue
			}
			entry.Permissions.add(permission, cut)
		}
	}

	return entry, left
}

// administratorDescription says what the role a tenant's administrator holds is for.
func (tn Tenancy) administratorDescription() string {
	if tn.NoDirectories {
		return fmt.Sprintf("Runs one %s: manages its people, their groups and the roles they hold there.", tn.oneLower())
	}

	return fmt.Sprintf("Runs one %s: manages its people, their groups and the roles they hold there, and declares the directory they sign in through.", tn.oneLower())
}

// ofTheRecord reports whether a resource of the reference's role file is named for its
// tenant record: the record, its directories, an invitation to make one, and the auth's
// management of one tenant's people. The reference's own sample resource is not, and
// neither is the management of the whole application's people.
func ofTheRecord(resource string) bool {
	return strings.Contains(resource, referenceRecord)
}

// roleResource is a resource of the reference's role file as this application names it:
// the reference's auth the application's, and the reference's record the application's,
// as the generator derives each name (StaffTenantUsers is CrewSectorUsers for the crew
// auth over Sectors, StaffInvitedTenants is CrewInvitedSectors).
func (tn Tenancy) roleResource(resource, auth string) string {
	const hold = "\x00"
	resource = strings.Replace(resource, names.Pascal(skeleton.PlaceholderAuth), hold, 1)
	resource = strings.ReplaceAll(resource, referenceTable, tn.Table)
	resource = strings.ReplaceAll(resource, referenceRecord, tn.Record())

	return strings.Replace(resource, hold, names.Pascal(auth), 1)
}

// inApplicationNames is a grant of the reference's role file in this application's
// names: its resource (roleResource), and each field the generator names for the record
// (tenantId, tenantName), which is the application's record's where the resource has no
// field of the reference's name. A field that is not the record's keeps its name: the
// tenant of a Microsoft Entra identity is the directory's word.
func (tn Tenancy) inApplicationNames(grant access.Grant, served *app.Served, auth string) access.Grant {
	grant.Resource = accesstypes.Resource(tn.roleResource(string(grant.Resource), auth))
	r, ok := served.Resource(string(grant.Resource))
	if !ok || len(grant.Fields) == 0 {
		return grant
	}
	prefix := lowerFirst(referenceRecord)
	fields := make([]accesstypes.Tag, len(grant.Fields))
	for i, field := range grant.Fields {
		fields[i] = field
		rest, named := strings.CutPrefix(string(field), prefix)
		if _, has := r.Fields[string(field)]; has || !named {
			continue
		}
		if _, has := r.Fields[lowerFirst(tn.Record())+rest]; has {
			fields[i] = accesstypes.Tag(lowerFirst(tn.Record()) + rest)
		}
	}
	grant.Fields = fields

	return grant
}

// The opening of the description the base gives its administrator role, and what
// tenancy retells it as: the role now brings tenants in too.
const (
	administratorManages        = "Manages the application's people: creates accounts, invites people,"
	administratorManagesTenants = "Manages the application's %s and people: brings a %s in by inviting the person who will administer it, creates accounts and invites people,"
)

// grantTenantMaking gives the application's own administrator role, the first global
// role the foothold names, the grants a tenant is made with: the record itself, the
// invitation that makes one, its withdrawal and the list of the invitations that stand,
// as the reference's administrator holds them, on what the application serves. A grant
// the role carries already is left as it is.
func (tn Tenancy) grantTenantMaking(file, reference *roleFile, served *app.Served, auth, rel string, named footholdRoles, ch *Change) {
	source := roleNamed(reference.Roles.Global, referenceAdministrator)
	var role *roleEntry
	if len(named.global) > 0 {
		role = roleNamed(file.Roles.Global, named.global[0])
	}
	if role == nil || source == nil {
		ch.skipf("%s: no global role the %s auth's %s() names is in the file, so none was given the grants a %s is made with: give the role that manages the application's people Create, Delete, List, Read and Update on %s, Execute on Invite%[6]s%[7]s and Withdraw%[6]sInvited%[7]s, and List on %[6]sInvited%[5]s, as the tenanted skeleton's %[8]s holds them", rel, auth, administratorRolesFunc, tn.oneLower(), tn.Table, names.Pascal(auth), tn.Record(), referenceAdministrator)

		return
	}
	var gained []string
	for i := range source.Permissions {
		permission := source.Permissions[i].Permission
		for _, grant := range source.Permissions[i].Grants {
			if !ofTheRecord(string(grant.Resource)) {
				continue
			}
			cut, ok := servedGrant(served, app.ScopeGlobal, permission, tn.inApplicationNames(grant, served, auth))
			if ok && role.Permissions.add(permission, cut) {
				gained = append(gained, permission+" on "+string(cut.Resource))
			}
		}
	}
	if len(gained) == 0 {
		return
	}
	if rest, ok := strings.CutPrefix(role.Description, administratorManages); ok {
		role.Description = fmt.Sprintf(administratorManagesTenants, tn.manyLower(), tn.oneLower()) + rest
	}
	ch.didf("%s: the role %s gained the %d grant(s) a %s is made with, as the tenanted skeleton's %s holds them on what this application serves (%s)", rel, role.Name, len(gained), tn.oneLower(), referenceAdministrator, strings.Join(gained, ", "))
}

// footholdRoles is what one of the foothold's two functions returns: the roles it names
// for the whole application and the ones it names as held in every tenant.
type footholdRoles struct {
	global, everyDomain []string
	// everyDomainAt and everyDomainEnd bound the second result in the source, and
	// resultType is the type the function declares its results as.
	everyDomainAt, everyDomainEnd int
	resultType                    string
}

// readFootholdRoles reads a function of the foothold in the form the skeleton writes it:
// one return of two results, each nil or a literal of role names. ok is false for any
// other form, and for a file without the function.
func readFootholdRoles(src []byte, name string) (roles footholdRoles, ok bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, footholdFile, src, parser.SkipObjectResolution)
	if err != nil {
		return roles, false
	}
	for _, decl := range f.Decls {
		fn, isFunc := decl.(*ast.FuncDecl)
		if !isFunc || fn.Recv != nil || fn.Name.Name != name || fn.Body == nil || len(fn.Body.List) != 1 {
			continue
		}
		ret, isReturn := fn.Body.List[0].(*ast.ReturnStmt)
		if !isReturn || len(ret.Results) != 2 || fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
			return roles, false
		}
		var read [2][]string
		for i, result := range ret.Results {
			if read[i], ok = roleNames(result); !ok {
				return roles, false
			}
		}
		offset := func(pos token.Pos) int {
			return fset.Position(pos).Offset
		}
		declared := fn.Type.Results.List[len(fn.Type.Results.List)-1].Type

		return footholdRoles{
			global: read[0], everyDomain: read[1],
			everyDomainAt: offset(ret.Results[1].Pos()), everyDomainEnd: offset(ret.Results[1].End()),
			resultType: string(src[offset(declared.Pos()):offset(declared.End())]),
		}, true
	}

	return roles, false
}

// roleNames reads one result of a foothold function: nil, or a literal of role names.
func roleNames(expr ast.Expr) ([]string, bool) {
	if id, ok := expr.(*ast.Ident); ok {
		return nil, id.Name == "nil"
	}
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	roles := make([]string, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		basic, ok := elt.(*ast.BasicLit)
		if !ok || basic.Kind != token.STRING {
			return nil, false
		}
		role, err := strconv.Unquote(basic.Value)
		if err != nil {
			return nil, false
		}
		roles = append(roles, role)
	}

	return roles, true
}

// nameAdministrator names the role in the foothold as held in every tenant: by
// AdministratorRoles, which the generated contract's accessor answers a tenant's
// creator's roles from, and by FirstAdministratorRoles where that function names no such
// role, so the first administrator of a deployment administers every tenant, as the
// reference's does.
func (tn Tenancy) nameAdministrator(a *app.App, foothold string, src []byte, mode os.FileMode, ch *Change) error {
	role := tn.AdministratorRole()
	namedIn := []string{}
	// The later function in the file is edited first, so the earlier one's offsets hold.
	type edit struct {
		name  string
		roles footholdRoles
	}
	var edits []edit
	for _, name := range []string{administratorRolesFunc, firstAdministratorRolesFunc} {
		roles, ok := readFootholdRoles(src, name)
		if !ok || len(roles.everyDomain) > 0 {
			continue
		}
		edits = append(edits, edit{name, roles})
		namedIn = append(namedIn, name+"()")
	}
	slices.SortFunc(edits, func(x, y edit) int {
		return y.roles.everyDomainAt - x.roles.everyDomainAt
	})
	edited := src
	for _, e := range edits {
		literal := fmt.Sprintf("%s{%q}", e.roles.resultType, role)
		edited = slices.Concat(edited[:e.roles.everyDomainAt], []byte(literal), edited[e.roles.everyDomainEnd:])
	}
	if err := os.WriteFile(a.Abs(foothold), edited, mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: %s name %s as held in every %s, so %s() answers it, the generated create-your-%[4]s route gives it to the person who creates a %[4]s, and the application's router is built", foothold, strings.Join(namedIn, " and "), role, tn.oneLower(), tn.rolesAccessor())

	return tn.holdDeployTest(a, ch)
}

// holdDeployTest brings the deployment package's test of the first administrator's roles
// to the role named: the base's test reads the roles given for the whole application
// alone, and the reference's holds the ones given in every tenant to the role file too,
// so an invitation never names a role its acceptance could not give. A test that is not
// as the skeleton writes it is left as it is, since it still holds what it held.
func (tn Tenancy) holdDeployTest(a *app.App, ch *Change) error {
	seat := tn.seatAuth(a)
	data, mode, err := readFile(a, deployTest)
	if err != nil || seat == nil {
		return nil
	}
	global := seat.Name + "Global"
	everyDomain := seat.Name + "EveryDomain"
	read := fmt.Sprintf("%s, _ := %s.%s()", global, seat.Name, firstAdministratorRolesFunc)
	row := fmt.Sprintf("global: %s}", global)
	text := string(data)
	if strings.Count(text, read) != 1 || strings.Count(text, row) != 1 {
		return nil
	}
	text = strings.Replace(text, read, fmt.Sprintf("%s, %s := %s.%s()", global, everyDomain, seat.Name, firstAdministratorRolesFunc), 1)
	text = strings.Replace(text, row, fmt.Sprintf("global: %s, domain: %s}", global, everyDomain), 1)
	if err := os.WriteFile(a.Abs(deployTest), []byte(text), mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: the test of the first administrator's roles holds the role given in every %s to the role file too", deployTest, tn.oneLower())

	return nil
}

// namesAdministrator reports whether the application's seat auth names a role held in
// every tenant, which is what the brief says of the role: written by the command, or
// left to the author.
func (tn Tenancy) namesAdministrator(a *app.App) bool {
	if a == nil {
		return false
	}
	seat := tn.seatAuth(a)
	if seat == nil {
		return false
	}
	src, err := os.ReadFile(a.Abs(path.Join(seat.Dir, footholdFile)))
	if err != nil {
		return false
	}
	named, ok := readFootholdRoles(src, administratorRolesFunc)

	return ok && len(named.everyDomain) > 0
}
