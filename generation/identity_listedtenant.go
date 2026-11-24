package generation

import (
	"fmt"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The whole application's list of tenants, in the generated management of the auth whose
// people make the tenants (identity_invitedtenant.go). Staff see every tenant with when it
// was made, where the tenant record marks that column (@tenantSince), and what no table
// holds of it: how many people it has, the kind of directory its people sign in through
// and who administers it. The list is a view over the tenant record's table that declares
// the table as where its rows are written (@rowsOf), so the facts sort, filter and page on
// the server like any column, and a row opens, is changed and is deleted as the record's
// own. It is declared in the instance's declarations package, beside the auth's other
// whole-application lists (identity_holdings.go), and served on the outlets that serve
// both the auth's management and the tenant record.
//
// The facts are what the tenant itself holds, never what reaches it from the whole
// application: a role held in every tenant and a group kept for the whole application add
// the same people to every tenant, the application's staff, and are counted for none. The
// record's fields are the ones the invitation carries, read off the resources package's
// syntax before the load (tenantInvitationIn), with the record's key.

// listedTenantSuffix is what the list's name says after the auth's: the auth's name, the
// suffix and the tenant record's (CrewListedSector).
const listedTenantSuffix = "Listed"

// listedTenantFacts are the columns the list adds to the record's fields, which no field
// of the record may be named as. Since is the list's own only where the record marks the
// column holding when a tenant was made (@tenantSince): a record that marks none may have
// a field named Since, which the list carries as any field.
func listedTenantFacts(invitation *identityTenantInvitation) []string {
	if invitation.since != nil {
		return []string{"People", "Since", "Directory", "Administrators"}
	}

	return []string{"People", "Directory", "Administrators"}
}

// listedTenantDirectoryKinds are the kinds of directory the list says a tenant's directory
// by, each by the sign-in method it is reached through, as resource.DirectoryName says the
// kind before a name. A method none of them names is said "Directory".
var listedTenantDirectoryKinds = []struct {
	method sessioninfo.AuthMethod
	words  string
}{
	{method: sessioninfo.MethodGoogle, words: "Google Workspace"},
	{method: sessioninfo.MethodAzure, words: "Microsoft Entra tenant"},
	{method: sessioninfo.MethodWorkOS, words: "WorkOS connection"},
	{method: sessioninfo.MethodOIDC, words: "OpenID Connect provider"},
}

// listedTenantName names the list of tenants: the auth's name, what the list is, and the
// tenant record's.
func (i *identityInstance) listedTenantName() string {
	return i.auth + listedTenantSuffix + i.tenant.name
}

// listsTenants reports whether the instance declares the whole application's list of
// tenants: it carries the invitation that makes a tenant, the schema holds the accounts
// the list names the administrators from, and at least one outlet serves both the auth's
// management and the tenant record, since a view over the record is served only where the
// record is.
func (i *identityInstance) listsTenants() bool {
	return i.invitesTenants() && i.has(identityUsers) && len(i.listedTenantOutlets()) > 0
}

// listedTenantOutlets are the outlets the list of tenants is served on: the ones serving
// the instance that serve the tenant record too, in the order the instance holds them.
func (i *identityInstance) listedTenantOutlets() []string {
	if i.tenant == nil {
		return nil
	}
	var outlets []string
	for _, outlet := range i.outlets {
		if slices.Contains(i.tenant.outlets, outlet) {
			outlets = append(outlets, outlet)
		}
	}

	return outlets
}

// listedTenantSources is the source file of the list of tenants, where the instance
// declares it.
func (r *resourceGenerator) listedTenantSources(inst *identityInstance) []identitySourceFile {
	if !inst.listsTenants() {
		return nil
	}

	return []identitySourceFile{{
		dir:      inst.resources,
		name:     fileStem(r.pluralize(inst.listedTenantName())),
		template: identityListedTenantsTemplate,
		needs:    []identityTable{identityUsers, identityMemberships},
	}}
}

// refuseListedTenantNamesTaken refuses a tenant record that would give the list of tenants
// a name the auth already gives another list (a record named Group names it as the groups
// with what each holds are named, <Auth>ListedGroup), and a record with a field named as
// one of the columns the list adds: either would declare one name twice. The field the
// record marks as when a tenant was made is the list's Since, under no name of its own.
func (i *identityInstance) refuseListedTenantNamesTaken() error {
	if !i.listsTenants() {
		return nil
	}
	name := i.listedTenantName()
	if slices.Contains(i.holdingNames(), name) {
		return errors.Newf("Auth(%q): the tenant record %s gives the list of tenants the name %s, which another list of the auth has; the tenant record takes another name", i.importPath, i.tenant.name, name)
	}
	invitation := i.tenant.invitation
	fields := []string{invitation.key.Name}
	for _, field := range invitation.listedFields() {
		fields = append(fields, field.Name)
	}
	if invitation.directoryOnly != nil {
		fields = append(fields, invitation.directoryOnly.Name)
	}
	facts := listedTenantFacts(invitation)
	for _, field := range fields {
		if slices.Contains(facts, field) {
			return errors.Newf("Auth(%q): the tenant record %s has a field named %s, and the list of tenants generated for it (%s) adds a column of that name (%s and %s are its own); the field takes another name",
				i.importPath, i.tenant.name, field, name, strings.Join(facts[:len(facts)-1], ", "), facts[len(facts)-1])
		}
	}

	return nil
}

// listedFields are the fields the invitation carries that the list of tenants carries
// under their own names: every one but the field the record marks as when a tenant was
// made, which the list says as Since.
func (t *identityTenantInvitation) listedFields() []identityTenantField {
	if t.since == nil {
		return t.fields
	}
	fields := make([]identityTenantField, 0, len(t.fields))
	for _, field := range t.fields {
		if field.Name != t.since.Name {
			fields = append(fields, field)
		}
	}

	return fields
}

// ListsTenants is listsTenants for the templates.
func (d *identitySourceData) ListsTenants() bool {
	return d.listsTenants()
}

// ListedTenant names the list of tenants for the templates.
func (d *identitySourceData) ListedTenant() string {
	return d.listedTenantName()
}

// ListedTenantOutlets are the outlets the list of tenants is served on, as @outlet takes
// them.
func (d *identitySourceData) ListedTenantOutlets() string {
	return strings.Join(d.listedTenantOutlets(), ", ")
}

// ListedTenantOrder is the field the list of tenants is ordered by: the one people call a
// tenant by, and the key where the record marks none.
func (d *identitySourceData) ListedTenantOrder() string {
	if name := d.tenant.invitation.nameField; name != "" {
		return name
	}

	return d.tenant.invitation.key.Name
}

// TenantKeyField is the tenant record's key as the list of tenants carries it: its field's
// name, its type as another package writes it and its column.
func (d *identitySourceData) TenantKeyField() identityTenantField {
	return d.tenant.invitation.key
}

// ListedTenantFields are the tenant record's fields the list of tenants carries under
// their own names: the ones an invitation carries, but the field the record marks as when
// a tenant was made.
func (d *identitySourceData) ListedTenantFields() []identityTenantField {
	return d.tenant.invitation.listedFields()
}

// TenantSinceField is the field the tenant record marks as when a tenant was made
// (@tenantSince), which the list of tenants says as Since; nil where the record marks
// none, and the list has no Since.
func (d *identitySourceData) TenantSinceField() *identityTenantField {
	return d.tenant.invitation.since
}

// ListedTenantImports are the import lines of the list of tenants, once each and in
// order: the ones its own columns need, and the ones the key's and the fields' types need.
// The time package is one of its own only where it says Since.
func (d *identitySourceData) ListedTenantImports() string {
	lines := map[string]bool{`"sync/atomic"`: true, `"github.com/impulseframework/impulse/accesstypes"`: true}
	if d.tenant.invitation.since != nil {
		lines[`"time"`] = true
	}
	for _, imp := range slices.Concat(d.tenant.invitation.keyImports, d.tenant.invitation.imports) {
		lines[imp] = true
	}

	return strings.Join(sortedImports(lines), "\n\t")
}

// ListedTenantDirectory is the SQL expression that says a tenant's directory by its kind,
// alias the row of the directories table it reads: a Google Workspace with its domain,
// the connection the row keeps, and every other kind by its kind alone. No other kind's
// connection is said: a Microsoft Entra tenant's is an id nobody knows, and what a person
// calls a directory is said by the sign-in registrations, which a query does not read.
func (d *identitySourceData) ListedTenantDirectory(alias string) string {
	directory := d.TenantDirectory()
	method, connection := alias+"."+directory.MethodColumn, alias+"."+directory.ConnectionColumn
	var b strings.Builder
	b.WriteString("CASE " + method)
	for _, kind := range listedTenantDirectoryKinds {
		words := sqlStringLiteral(kind.words)
		said := words
		if kind.method == sessioninfo.MethodGoogle {
			said = fmt.Sprintf("IF(IFNULL(%s, '') = '', %s, CONCAT(%s, %s))", connection, words, sqlStringLiteral(kind.words+" "), connection)
		}
		fmt.Fprintf(&b, "\n      WHEN %s THEN %s", sqlStringLiteral(string(kind.method)), said)
	}
	b.WriteString("\n      ELSE 'Directory'\n    END")

	return b.String()
}
