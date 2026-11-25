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
// is the one the framework's own suites prove. The reference holds each method that
// changes a person to a condition, which keeps a tenant's administrator from changing a
// person another tenant also holds, and impulse check holds every conditional grant to a
// test that proves it over seeded rows (conditions-proven). So the command writes that
// test with the role (tenancy_proof.go), and the role carries every grant of the
// reference's. Where it cannot write a test that holds, the role is written with the
// grants that hold under no condition, and the others are named as what is left: a grant
// is never written without its condition, and never under one no test proves.

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
// serves, which the regenerated collection says, and reports whether it added the role
// to the role file: the generated metadata lists the roles a field may hold from that
// file, so a role added after the regeneration is listed by the next one.
func (tn Tenancy) writeRoles(a *app.App, ch *Change) (added bool, err error) {
	seat := tn.seatAuth(a)
	if seat == nil {
		return false, nil
	}
	foothold := path.Join(seat.Dir, footholdFile)
	rel := path.Join(seat.Dir, app.RolesFileName)
	if !exists(a, foothold) {
		ch.skipf("%s: not there, so the role a %s's administrator holds was not written: %s", foothold, tn.oneLower(), tn.roleHandStep(seat.Name))

		return false, nil
	}
	src, footholdMode, err := readFile(a, foothold)
	if err != nil {
		return false, err
	}
	named, ok := readFootholdRoles(src, administratorRolesFunc)
	switch {
	case !ok:
		ch.skipf("%s: %s() is not as the skeleton writes it (one return of the two lists of roles), so the role a %s's administrator holds was not written: %s", foothold, administratorRolesFunc, tn.oneLower(), tn.roleHandStep(seat.Name))

		return false, nil
	case len(named.everyDomain) > 0:
		ch.didf("%s: left as it is, since %s() names a role held in every %s already (%s), which %s() answers", foothold, administratorRolesFunc, tn.oneLower(), strings.Join(named.everyDomain, ", "), tn.rolesAccessor())

		return false, nil
	}
	served, err := a.Served()
	if err != nil {
		return false, err
	}
	data, mode, err := readFile(a, rel)
	var file *roleFile
	if err == nil {
		file, err = parseRoleFile(data)
	}
	if served == nil || err != nil {
		ch.skipf("%s: the role a %s's administrator holds was not written, since %s: %s", rel, tn.oneLower(), unreadWords(served, err), tn.roleHandStep(seat.Name))

		return false, nil
	}
	reference, err := referenceRoles()
	if err != nil {
		return false, err
	}

	role := tn.AdministratorRole()
	if roleNamed(file.Roles.Global, role) != nil {
		ch.skipf("%s: %s is a global role of the file, and a %s's administrator holds a domain role, so none was written: %s", rel, role, tn.oneLower(), tn.roleHandStep(seat.Name))

		return false, nil
	}
	roles := len(file.Roles.Domain)
	hold := tn.accountsToHold(file, reference, served, seat.Name, &named)
	held, proof, err := tn.holdAdministratorRole(a, file, reference, served, seat, rel, hold, ch)
	if err != nil || !held {
		return false, err
	}
	tn.grantTenantMaking(file, reference, served, seat.Name, rel, &named, ch)
	grantManagingFacts(file, served, seat.Name, rel, ch)
	tn.holdAccounts(hold, rel, ch)

	if err := writeRoleFile(a, rel, file, mode, proof); err != nil {
		return false, err
	}
	if err := tn.nameAdministrator(a, foothold, src, footholdMode, ch); err != nil {
		return false, err
	}

	return len(file.Roles.Domain) > roles, nil
}

// writeRoleFile writes a role file as it now stands and, beside it, the proof of its
// conditional grants: each file of the proof by its path, in the order of their names.
func writeRoleFile(a *app.App, rel string, file *roleFile, mode os.FileMode, proof map[string][]byte) error {
	encoded, err := file.encode()
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.Abs(rel), encoded, mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	written := make([]string, 0, len(proof))
	for name := range proof {
		written = append(written, name)
	}
	slices.Sort(written)
	for _, name := range written {
		if err := os.WriteFile(a.Abs(name), proof[name], 0o644); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
	}

	return nil
}

// holdAdministratorRole gives the role file the domain role a tenant's administrator
// holds, where the file has none of the name: the tenanted skeleton's grants on what the
// application serves, and, with the ones that hold under a condition, the test that
// proves each condition, which it returns for writeRoles to write beside the role file:
// the test, and the harness it stands on, each by its path.
// Where no such test can be written the role carries the grants that hold under no
// condition, and the change names the others and says why. With a hold the test is
// written with the part that proves the application's own administrator's conditions too
// where it can be (proveConditions). It reports false, having said why, where the
// application's permission collection names nothing the role could grant.
func (tn Tenancy) holdAdministratorRole(a *app.App, file, reference *roleFile, served *app.Served, seat *app.AuthPackage, rel string, hold *accountHold, ch *Change) (held bool, proof map[string][]byte, err error) {
	role, auth := tn.AdministratorRole(), seat.Name
	if roleNamed(file.Roles.Domain, role) != nil {
		ch.didf("%s: the domain role %s was there already and is left as it is", rel, role)
		if hold != nil {
			hold.why = fmt.Sprintf("the role a %s's administrator holds was there already, and the command writes the test only with that role", tn.oneLower())
		}

		return true, nil, nil
	}
	entry, left := tn.tenantAdministrator(reference, served, auth)
	conditional := conditionalGrants(entry)
	proof, unwritten, err := tn.proveConditions(a, seat, conditional, hold)
	if err != nil {
		return false, nil, err
	}
	if proof == nil {
		entry.Permissions = withoutConditions(entry.Permissions)
	}
	if len(entry.Permissions) == 0 {
		ch.skipf("%s: the role a %s's administrator holds was not written, since the application's permission collection (%s) names none of the %s auth's management of one %s: %s", rel, tn.oneLower(), strings.Join(served.Files, ", "), auth, tn.oneLower(), tn.roleHandStep(auth))

		return false, nil, nil
	}
	file.Roles.Domain = append(file.Roles.Domain, entry)
	grants := fmt.Sprintf("the grants of the tenanted skeleton's %s on what this application serves, %d in all, %d of them under the condition the skeleton holds each to", referenceTenantAdministrator, grantCount(entry), len(conditional))
	if proof == nil {
		grants = fmt.Sprintf("the grants of the tenanted skeleton's %s that hold under no condition, on what this application serves, %d in all", referenceTenantAdministrator, grantCount(entry))
	}
	ch.didf("%s: the role %s, a domain role, which a %s's administrator holds: %s, on the %s auth's people in one %s, their groups and the roles they hold there%s%s", rel, role, tn.oneLower(), grants, auth, tn.oneLower(), tn.directoryWords(), leftOutWords(left))
	switch {
	case proof != nil:
		ch.didf("%s: the test that proves each of the %d condition(s) on the served stack, written with the role since impulse check holds every conditional grant to a test that proves it (conditions-proven): a %[3]s's administrator changes the account of a person whose account the %[3]s manages, and is refused for a person whose account another %[3]s or the application's own administrators manage; reads the name, the address and whether the account is disabled of every person of the %[3]s, and the rest of an account the %[3]s manages%[5]s; takes a role held in the %[3]s, and not one held in every %[3]s; and withdraws an invitation while it stands%[4]s", tenantPeopleTest, len(conditional), tn.oneLower(), tn.proofSignInWords(a, seat), tn.proofResetWords(a, seat))
		if !hold.isProven() {
			ch.didf("%s: written again with its provesGrant marked for the application's lint (//nolint:unparam): every grant the new test proves is the %s role's, and the lint refuses a parameter that takes one value in every call", integrationHarness, role)
		}
	case len(conditional) > 0:
		unproven := make([]string, 0, len(conditional))
		for _, c := range conditional {
			unproven = append(unproven, c.String())
		}
		ch.skipf("%s: %d grant(s) the tenanted skeleton's %s holds under a condition were not written into %s: impulse check holds every conditional grant to a test that proves it over seeded rows (conditions-proven), and the command wrote no such test, since %s. They are %s. Add each with its condition and its proof, as the tenanted skeleton's test/integration/tenant_people_test.go proves them; until then a %s's administrator does none of them, and the application's own administrator does them through the %s auth's methods of the whole application", rel, len(conditional), referenceTenantAdministrator, role, unwritten, strings.Join(unproven, "; "), tn.oneLower(), auth)
	}

	return true, proof, nil
}

// proveConditions renders the proof of the conditional grants the role a tenant's
// administrator would carry: with a hold, first with the part that proves the grants it
// holds the application's own administrator to, and where that test cannot be written or
// does not pair, without it, as for the tenant's role alone. It returns the proof, and why
// no test was written at all, for the tenant's role's line. On the hold it records whether
// the test proves its grants and, where it does not, why: the words of the test with the
// part where the test without it was written, and the tenant's role's own where none was.
// With no conditional grant and no hold no test is rendered.
func (tn Tenancy) proveConditions(a *app.App, seat *app.AuthPackage, conditional []conditionalGrant, hold *accountHold) (proof map[string][]byte, unwritten string, err error) {
	if hold != nil {
		if proof, hold.why, err = tn.peopleProof(a, seat, conditional, hold); err != nil || proof != nil {
			hold.proven = proof != nil

			return proof, "", err
		}
	}
	if len(conditional) == 0 {
		return nil, "", nil
	}
	if proof, unwritten, err = tn.peopleProof(a, seat, conditional, nil); err != nil {
		return nil, "", err
	}
	if hold != nil && proof == nil {
		hold.why = unwritten
	}

	return proof, unwritten, nil
}

// proofSignInWords says, of an application whose auth has no password sign-in, how the
// test that proves the conditions signs people in, and what that makes of it under the
// default build.
func (tn Tenancy) proofSignInWords(a *app.App, seat *app.AuthPackage) string {
	if authHasPassword(a, seat) {
		return ""
	}

	return fmt.Sprintf("; the %s auth has no password sign-in, so the test signs people in through the simulated directory (the session library's skipAuth build tag), and under the default build it is skipped, and says so", seat.Name)
}

// proofResetWords says, of an application whose auth has a password sign-in, what the
// test that proves the conditions shows of the password reset link a tenant's
// administrator sends; an auth with no password has no such link, and nothing is said.
func (tn Tenancy) proofResetWords(a *app.App, seat *app.AuthPackage) string {
	if !authHasPassword(a, seat) {
		return ""
	}

	return fmt.Sprintf("; sends a password reset link to everyone but a person another %[1]s's directory manages", tn.oneLower())
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

// startingRolesWords names the list of the roles a new tenant's people may start with,
// beside the list of the invitations that stand, where a tenant can have a directory.
func (tn Tenancy) startingRolesWords(auth string) string {
	if tn.NoDirectories {
		return ""
	}

	return fmt.Sprintf(" and %sInvited%sRoles", names.Pascal(auth), tn.Record())
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
// the reference's, in the application's names, with its grants on what the application
// serves in a tenant, each under the condition the reference holds it to. left names the
// reference's grants on what the application does not serve, which are not in the role.
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
	administratorManages        = "Manages the application's people: invites people,"
	administratorManagesTenants = "Manages the application's %s and people: brings a %s in by inviting the person who will administer it, invites people,"
)

// grantTenantMaking gives the application's own administrator role, the first global
// role the foothold names, the grants a tenant is made with: the record itself, the
// invitation that makes one, its withdrawal, the list of the invitations that stand, the
// list of tenants and, where a tenant can have a directory, the list of the roles its
// people may start with,
// as the reference's administrator holds them, on what the application serves. A grant
// the role carries already is left as it is.
func (tn Tenancy) grantTenantMaking(file, reference *roleFile, served *app.Served, auth, rel string, named *footholdRoles, ch *Change) {
	source := roleNamed(reference.Roles.Global, referenceAdministrator)
	var role *roleEntry
	if len(named.global) > 0 {
		role = roleNamed(file.Roles.Global, named.global[0])
	}
	if role == nil || source == nil {
		ch.skipf("%s: no global role the %s auth's %s() names is in the file, so none was given the grants a %s is made with: give the role that manages the application's people Create, Delete, List, Read and Update on %s, Execute on Invite%[6]s%[7]s and Withdraw%[6]sInvited%[7]s, and List on %[6]sInvited%[5]s and %[6]sListed%[5]s%[9]s, as the tenanted skeleton's %[8]s holds them", rel, auth, administratorRolesFunc, tn.oneLower(), tn.Table, names.Pascal(auth), tn.Record(), referenceAdministrator, tn.startingRolesWords(auth))

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

// The whole application's lists of the accounts and of their sign-in identities that say
// who manages each account, and the identities table's own list, each after the auth's
// name, as the generated management names them.
const (
	listedUsersSuffix       = "ListedUsers"
	sessionIdentitiesSuffix = "SessionIdentities"
	listedIdentitiesSuffix  = "ListedIdentities"
)

// managingFacts are the fields of the whole application's two lists that say who manages
// each account: whether a tenant does, and which.
var managingFacts = []accesstypes.Tag{"managedByTenant", "managingTenant"}

// grantManagingFacts gives each global role of the auth's role file that lists or reads
// the application's accounts or their sign-in identities who manages each account there,
// on what the application serves (managingFactsOf), and says so in one line, none where
// no role gained anything. It runs where the tenant-making grants are given, so a role
// file the command leaves unwritten gains nothing here either.
func grantManagingFacts(file *roleFile, served *app.Served, auth, rel string, ch *Change) {
	gained := make([]string, 0, len(file.Roles.Global))
	for _, role := range file.Roles.Global {
		gained = append(gained, managingFactsOf(role, served, names.Pascal(auth))...)
	}
	if len(gained) == 0 {
		return
	}
	ch.didf("%s: each global role that lists or reads the application's accounts or their sign-in identities now reads who manages each account there (%s)", rel, strings.Join(gained, "; "))
}

// managingFactsOf gives one global role the facts of who manages each account, for the
// auth of that name, and answers what it gained in the file's order. A grant of List or
// Read on the accounts' list gains each of the two facts the list takes for the
// permission and the grant does not name already (factsToGain), its other fields and its
// condition kept. A grant of List or Read on the identities table's list is given again
// on the whole application's list of the identities (onListedIdentities), unless the role
// carries one there under that condition already.
func managingFactsOf(role *roleEntry, served *app.Served, auth string) []string {
	accounts := accesstypes.Resource(auth + listedUsersSuffix)
	identities := accesstypes.Resource(auth + sessionIdentitiesSuffix)
	listed := accesstypes.Resource(auth + listedIdentitiesSuffix)
	var gained []string
	for i := range role.Permissions {
		permission := role.Permissions[i].Permission
		if permission != string(accesstypes.List) && permission != string(accesstypes.Read) {
			continue
		}
		var twins []access.Grant
		for j := range role.Permissions[i].Grants {
			grant := &role.Permissions[i].Grants[j]
			switch grant.Resource {
			case accounts:
				if fields := factsToGain(served, permission, *grant); len(fields) > 0 {
					grant.Fields = append(grant.Fields, fields...)
					gained = append(gained, fmt.Sprintf("%s: %s on %s (%s)", role.Name, permission, accounts, tagsText(fields)))
				}
			case identities:
				twins = append(twins, *grant)
			}
		}
		for _, twin := range twins {
			if repeat, ok := onListedIdentities(served, permission, twin, listed); ok && role.Permissions.add(permission, repeat) {
				gained = append(gained, role.Name+": "+permission+" on "+string(listed))
			}
		}
	}

	return gained
}

// factsToGain are the facts of who manages an account that a grant on the accounts' list
// gains for the permission: each the list the application serves takes for it, that the
// grant does not name already. A grant that names every field (*) names the facts
// already, and a bare grant names no field to add them to: neither gains any.
func factsToGain(served *app.Served, permission string, grant access.Grant) []accesstypes.Tag {
	r, ok := served.Resource(string(grant.Resource))
	if !ok || r.Scope != app.ScopeGlobal || namesNoField(grant) {
		return nil
	}
	var fields []accesstypes.Tag
	for _, fact := range managingFacts {
		if r.FieldTakes(string(fact), permission) && !slices.Contains(grant.Fields, fact) {
			fields = append(fields, fact)
		}
	}

	return fields
}

// onListedIdentities is a grant on the identities table's list given again on the whole
// application's list of the identities: the same fields and condition and the two facts,
// cut to what the application serves (servedGrant). A grant that names every field (*),
// or a bare one, is given as it stands, where the application serves the list for the
// permission.
func onListedIdentities(served *app.Served, permission string, twin access.Grant, listed accesstypes.Resource) (access.Grant, bool) {
	repeat := access.Grant{Resource: listed, Condition: twin.Condition}
	if namesNoField(twin) {
		if _, ok := servedGrant(served, app.ScopeGlobal, permission, repeat); !ok {
			return access.Grant{}, false
		}
		repeat.Fields = twin.Fields

		return repeat, true
	}
	repeat.Fields = slices.Concat(twin.Fields, managingFacts)

	return servedGrant(served, app.ScopeGlobal, permission, repeat)
}

// namesNoField reports whether a grant names no field the facts could join: it names
// every field (*), or it is bare.
func namesNoField(grant access.Grant) bool {
	return len(grant.Fields) == 0 || slices.Contains(grant.Fields, "*")
}

// tagsText is the fields as a sentence lists them, ", " between them.
func tagsText(fields []accesstypes.Tag) string {
	words := make([]string, len(fields))
	for i, field := range fields {
		words[i] = string(field)
	}

	return strings.Join(words, ", ")
}

// applicationManages is the condition the tenanted skeleton holds its own administrator's
// grants on the whole application's methods that act on an account to: the account is
// one no tenant manages.
const applicationManages = "managedByTenant = false"

// accountHold is what the command holds the application's own administrator role to
// where a person may be in several tenants: the role, and the grants it holds under no
// condition on the whole application's methods that act on an account that the tenanted
// skeleton's Administrator holds under managedByTenant = false, each as the role holds it
// once held, in the role file's order. proven reports that they took the condition with
// the test that proves it, and why says why they did not.
type accountHold struct {
	role   *roleEntry
	grants []conditionalGrant
	proven bool
	why    string
}

// accountsToHold finds what the command holds the application's own administrator role to
// (accountHold): the role is the first global role the foothold's AdministratorRoles()
// names, as grantTenantMaking finds it, and the grants are the tenanted skeleton's
// Administrator's Execute grants under managedByTenant = false, each in the application's
// names and kept where the application serves it for the whole application, that the
// role holds under no condition. It answers nil, and nothing is held, where a person
// belongs to one tenant, where the file has no such role, or where the role holds none of
// them under no condition.
func (tn Tenancy) accountsToHold(file, reference *roleFile, served *app.Served, auth string, named *footholdRoles) *accountHold {
	source := roleNamed(reference.Roles.Global, referenceAdministrator)
	if tn.OneTenantPerPerson || len(named.global) == 0 || source == nil {
		return nil
	}
	role := roleNamed(file.Roles.Global, named.global[0])
	if role == nil {
		return nil
	}
	execute := string(accesstypes.Execute)
	skeletons := map[accesstypes.Resource]bool{}
	for _, grant := range source.Permissions.grants(execute) {
		if grant.Condition != applicationManages {
			continue
		}
		if cut, ok := servedGrant(served, app.ScopeGlobal, execute, tn.inApplicationNames(grant, served, auth)); ok {
			skeletons[cut.Resource] = true
		}
	}
	hold := &accountHold{role: role}
	for _, grant := range role.Permissions.grants(execute) {
		if grant.Condition == "" && skeletons[grant.Resource] {
			grant.Condition = applicationManages
			hold.grants = append(hold.grants, conditionalGrant{permission: execute, grant: grant})
		}
	}
	if len(hold.grants) == 0 {
		return nil
	}

	return hold
}

// isProven reports whether the test written with the role file proves the grants of the
// hold, which a nil hold has none of.
func (h *accountHold) isProven() bool {
	return h != nil && h.proven
}

// resources names the grants of the hold as a line of the change does, in the role
// file's order.
func (h *accountHold) resources() string {
	words := make([]string, len(h.grants))
	for i, c := range h.grants {
		words[i] = c.permission + " on " + string(c.grant.Resource)
	}

	return strings.Join(words, ", ")
}

// holdAccounts puts the condition on each grant of the hold where the test written with
// the role file proves it, and says in one line what was done: the grants that took it,
// or, where none did, that the role still changes every account and why. It says nothing
// where there was nothing to hold.
func (tn Tenancy) holdAccounts(hold *accountHold, rel string, ch *Change) {
	if hold == nil {
		return
	}
	if !hold.proven {
		ch.skipf("%s: the role %s still changes every account from the whole application's side, also one a %s manages: %d grant(s) the tenanted skeleton's %s holds under %s were left under no condition, since impulse check holds every conditional grant to a test that proves it (conditions-proven) and the command wrote no such test: %s. They are %s. Add the condition to each with its proof, as the tenanted skeleton's test/integration/grants_test.go proves them", rel, hold.role.Name, tn.oneLower(), len(hold.grants), referenceAdministrator, applicationManages, hold.why, hold.resources())

		return
	}
	execute := hold.role.Permissions.grants(string(accesstypes.Execute))
	for _, c := range hold.grants {
		for i := range execute {
			if execute[i].Resource == c.grant.Resource && execute[i].Condition == "" {
				execute[i].Condition = applicationManages
			}
		}
	}
	ch.didf("%s: the role %s changes an account from the whole application's side only where no %s manages it: %d grant(s) took the condition %s, as the tenanted skeleton's %s holds them (%s), each proven by %s", rel, hold.role.Name, tn.oneLower(), len(hold.grants), applicationManages, referenceAdministrator, hold.resources(), tenantPeopleTest)
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
	if seat == nil || !exists(a, deployTest) {
		return nil
	}
	data, mode, err := readFile(a, deployTest)
	if err != nil {
		return err
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

// grantsWords is what the brief says the role a tenant's administrator holds carries:
// every grant of the reference's, with the test that proves the conditional ones, where
// the command wrote that test, and the grants under no condition where it did not.
func (tn Tenancy) grantsWords(a *app.App) string {
	if tn.carriesConditions(a) {
		return fmt.Sprintf("the grants the tenanted skeleton's `%s` holds, on what this application serves, each under the condition the skeleton holds it to (the methods that change a person's account are a tenant's administrator's for a person whose account the tenant manages, and never for one whose account another tenant or the application's own administrators manage), and the command wrote the test that proves each condition on the served stack beside it, `%s`, since `impulse check` refuses a conditional grant no test proves", referenceTenantAdministrator, tenantPeopleTest)
	}

	return fmt.Sprintf("the grants the tenanted skeleton's `%s` holds under no condition, on what this application serves, and the grants the skeleton holds under a condition are yours to add, each with the test that proves it, since `impulse check` refuses a conditional grant no test proves (the change's lines name them and say why the command wrote no such test, and until then a tenant's administrator does none of them)", referenceTenantAdministrator)
}

// carriesConditions reports whether the role a tenant's administrator holds, in the role
// file of the application's seat auth, carries a grant under a condition.
func (tn Tenancy) carriesConditions(a *app.App) bool {
	seat := tn.seatAuth(a)
	if seat == nil {
		return false
	}
	data, err := os.ReadFile(a.Abs(path.Join(seat.Dir, app.RolesFileName)))
	if err != nil {
		return false
	}
	file, err := parseRoleFile(data)
	if err != nil {
		return false
	}
	role := roleNamed(file.Roles.Domain, tn.AdministratorRole())

	return role != nil && len(conditionalGrants(role)) > 0
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
