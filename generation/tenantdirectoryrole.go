package generation

import (
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/resource"
)

// How a tenant admits a stranger its directory vouches for is the directories record's
// default seat column (@directoryDefaultSeat): with a role named there the tenant is
// open to the directory's people, each given the role at their first sign-in through
// it, and with none the tenant is invite only. The choice is the tenant's own, made by
// the tenant's administrator, so the generator writes it as two methods in the tenant's
// scope for a directories record that carries the column, on every outlet that serves
// sessions and the record:
//
//   - Open<Record>ToDirectory, POST <prefix>/<tenants>/{tenant}/open-<record>-to-directory,
//     names the role. It lets in people nobody named, so it declares a warning, which
//     the dialog a page runs it through shows before anything is saved
//     (resource.TenantDirectoryRules.OpenToDirectoryWarning), holding its button until
//     the person has typed the warning's word; and the role is held to what giving it
//     by hand needs: it exists in the tenant, and the caller could give it there
//     directly (the generated identity client's DirectoryRole, which asks the grant of
//     the method that gives a role in a tenant);
//   - Make<Record>InviteOnly, POST <prefix>/<tenants>/{tenant}/make-<record>-invite-only,
//     clears the role, which is plain.
//
// Each is served to an Execute grant on it in the tenant and writes the column through
// the directories record's update patch, so its change event is written with it. The
// record's own update never writes the column: it decodes output-only
// (resourceField.IsDirectoryDefaultSeat).
//
// The role is judged through the generated management of the auth whose sessions the
// outlets serve, so the methods are written where the session outlets that serve the
// directories record are all of one Auth, the program generates that auth's management
// (GenerateIdentity), and the auth's roles are given by hand. Where they are not, the
// program writes no method, and the application sets the column in its own code. The
// judgment is the one a method opened to sign-up is held to: the role file's grant of
// the method that gives a role (identity.RoleGrants.MayGive), which the generated
// management answers for an auth whose directories record carries the default seat
// (identityInstance.seatsDirectory, grants.go).

// directoryRoleBodyField is the one body field of the method that opens a tenant to its
// directory, as the wire names it.
const directoryRoleBodyField = "role"

// openToDirectoryMethodName is the method that opens the record to its directory's
// people, as grants and the client name it: OpenSectorToDirectory.
func openToDirectoryMethodName(record string) string {
	return "Open" + record + "ToDirectory"
}

// makeInviteOnlyMethodName is the method that makes the record invite only for its
// directory's people: MakeSectorInviteOnly.
func makeInviteOnlyMethodName(record string) string {
	return "Make" + record + "InviteOnly"
}

// directoryRoleMethodsOf are the two methods that say how the record admits its
// directory's people: the one that opens it, which warns, and the one that makes it
// invite only.
func directoryRoleMethodsOf(record string) []tenantOwnMethod {
	return []tenantOwnMethod{
		{Name: openToDirectoryMethodName(record), Typed: resource.OpenToDirectoryWord},
		{Name: makeInviteOnlyMethodName(record), Typed: ""},
	}
}

// directoryRoleServing is where the application serves the two methods that say how a
// tenant admits its directory's people: the tenant record, its directories record, the
// session outlets that serve the directories record, and the auth of those outlets,
// whose generated identity client judges the role.
type directoryRoleServing struct {
	record      *resourceInfo
	directories *resourceInfo
	outlets     []*routerOutlet
	identity    *identityInstance
}

// servedOn reports whether the outlet serves the methods. A nil serving serves none.
func (s *directoryRoleServing) servedOn(outlet string) bool {
	return s != nil && slices.ContainsFunc(s.outlets, func(on *routerOutlet) bool { return on.name == outlet })
}

// methods are the two methods, none for a nil serving.
func (s *directoryRoleServing) methods() []tenantOwnMethod {
	if s == nil {
		return nil
	}

	return directoryRoleMethodsOf(s.record.Name())
}

// directoryRole resolves where the methods that say how a tenant admits its
// directory's people are served, nil where the program writes none: the tenant record
// declares no directories record or the record carries no default seat column; no
// session outlet serves the directories record; the outlets that do are not all of one
// Auth; the program does not generate that auth's management; a directory writes the
// auth's roles, so nobody gives one by hand; or the auth's client is handed no answers
// of the role file's grants (judgesRoleChanges), so the role could not be judged.
func (r *resourceGenerator) directoryRole() *directoryRoleServing {
	record := r.tenantRecord()
	if record == nil || !record.HasDeclaration() || record.declaration.Directories.DirectoryDefaultSeatField == nil {
		return nil
	}
	directories := record.declaration.Directories
	var outlets []*routerOutlet
	var auth *outletAuth
	for _, outlet := range r.allOutlets() {
		if !outlet.servesSessions || !directories.OnOutlet(outlet.name) {
			continue
		}
		if outlet.auth == nil || (auth != nil && auth.importPath != outlet.auth.importPath) {
			return nil
		}
		auth = outlet.auth
		outlets = append(outlets, outlet)
	}
	if auth == nil {
		return nil
	}
	for _, inst := range r.identity {
		if inst.importPath != auth.importPath {
			continue
		}
		if !inst.picksRoles() || !inst.hasList(listPeopleSuffix) || !inst.judgesRoleChanges() {
			return nil
		}

		return &directoryRoleServing{record: record, directories: directories, outlets: outlets, identity: inst}
	}

	return nil
}

// directoryRolePicker is the resource whose rows the role's picker lists, as a generated
// client names it: the roles that exist in one tenant, of the auth whose identity client
// judges the role (CrewSectorRoles). Empty where the program writes no method.
func (r *resourceGenerator) directoryRolePicker() string {
	role := r.directoryRole()
	if role == nil {
		return ""
	}

	return r.pluralize(role.identity.auth + role.identity.tenant.name + tenantRoleChoiceSuffix)
}

// directoryRoleData feeds the handler package's file of the two methods: the methods,
// the directories record's key and default seat as the methods write them, and the
// application's accessor of the generated identity client that judges the role.
type directoryRoleData struct {
	Source  string
	Package string
	// Name is the tenant record's struct name and Word what a sentence calls one
	// tenant; Directories is the directories record's struct name and Field its
	// default seat field.
	Name        string
	Word        string
	Directories string
	Field       string
	// Open is the method that opens the tenant to its directory and InviteOnly the one
	// that makes it invite only; DomainRoute is the tenant's own route as a doc names
	// it.
	Open        tenantOwnMethod
	InviteOnly  tenantOwnMethod
	DomainRoute string
	// SeatIsPointer says the default seat is a nullable string, which holds no role as
	// NULL; a plain string holds none as the empty string.
	SeatIsPointer bool
	// Auth is the Pascal name of the auth whose generated identity client judges the
	// role, IdentityAccessor the application's accessor of that client, and Picker the
	// list of the roles that exist in one tenant.
	Auth             string
	IdentityAccessor string
	Picker           string

	LocalPackageImports string
	ResourcePackage     string
	RouterPackage       string
	ApplicationName     string
	ReceiverName        string
}

// generateDirectoryRole emits the handler package's file of the two methods that say
// how a tenant admits its directory's people, where the program serves them.
func (r *resourceGenerator) generateDirectoryRole() error {
	role := r.directoryRole()
	if role == nil {
		return nil
	}
	methods := role.methods()

	begin := time.Now()
	destinationFilePath := filepath.Join(r.handler.Dir(), generatedGoFileName(tenantDirectoryRoleOutputName))
	data := &directoryRoleData{
		Source:              r.resource.Dir(),
		Package:             r.handler.Package(),
		Name:                role.record.Name(),
		Word:                r.directoryTenantWord(role.record),
		Directories:         role.directories.Name(),
		Field:               role.directories.DirectoryDefaultSeatField.Name(),
		Open:                methods[0],
		InviteOnly:          methods[1],
		DomainRoute:         fmt.Sprintf("%s/{%s}", r.domainRouteSegment, r.domainRouteParam),
		SeatIsPointer:       role.directories.DirectoryDefaultSeatField.Type() != "string",
		Auth:                role.identity.auth,
		IdentityAccessor:    role.identity.accessor(),
		Picker:              r.directoryRolePicker(),
		LocalPackageImports: r.localPackageImports(),
		ResourcePackage:     r.resource.Package(),
		RouterPackage:       r.router.Package(),
		ApplicationName:     r.applicationName,
		ReceiverName:        r.receiverName,
	}
	if err := r.writeFormattedGoFile(destinationFilePath, "tenantDirectoryRoleTemplate", tenantDirectoryRoleTemplate, data); err != nil {
		return errors.Wrap(err, "writeFormattedGoFile()")
	}
	log.Printf("Generated the directory's default role file in %s: %s", time.Since(begin), destinationFilePath)

	return nil
}
