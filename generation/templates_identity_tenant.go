package generation

// The sources of a tenant's methods (identity_tenant.go). A method that does to one
// person what the whole application's does is written from the whole application's
// template, which renders both: the pieces below are what the tenant's rendering adds
// and the whole application's leaves out. The methods that differ in what a request
// carries (a role given and taken, an address invited) have a template of their own
// here.

const (
	// identityTenantUserNote is the paragraph a tenant's method that names a person
	// adds to its comment.
	identityTenantUserNote = `
{{- if .ForTenant }}
//
// It is a tenant's method: served under the address of one {{ .TenantName }}, it names a
// row of {{ .TenantUser }}, the tenant's own list of the auth's people. A person who holds
// no role in the tenant is not found there, and what the tenant may do to a person who
// also holds a role outside it is the role file's to say, in a condition on the
// method's grant that reads the facts of the row (heldElsewhere, heldGlobally).
{{- end }}`

	// identityTenantResetNote is the paragraph the tenant's method that mails a reset
	// link adds to its comment. It leaves nothing to a condition of the role file: a
	// tenant's administrator sends the link to any person of the tenant.
	identityTenantResetNote = `
{{- if .ForTenant }}
//
// It is a tenant's method: served under the address of one {{ .TenantName }}, it names a
// row of {{ .TenantUser }}, the tenant's own list of the auth's people. A person who holds
// no role in the tenant is not found there. Every person of the tenant may be sent the
// link, whoever else they hold a role with: it goes to the person's own mailbox and
// changes nothing until they use it, so its grant needs no condition on the row.
{{- end }}`

	// identityTenantInvitationNote is the paragraph a tenant's method that names an
	// invitation adds to its comment.
	identityTenantInvitationNote = `
{{- if .ForTenant }}
//
// It is a tenant's method: served under the address of one {{ .TenantName }}, it names a
// row of {{ .TenantMailLink }}, the tenant's own list of invitations, so an invitation
// into another tenant or into the application is not found there.
{{- end }}`

	// identityTenantIdentityNote is the paragraph a tenant's method that names an
	// identity adds to its comment.
	identityTenantIdentityNote = `
{{- if .ForTenant }}
//
// It is a tenant's method: served under the address of one {{ .TenantName }}, it names a
// row of {{ .TenantIdentity }}, the tenant's own list of its people's identities. An
// identity of a person who holds no role in the tenant is not found there, and what the
// tenant may do to a person who also holds a role outside it is the role file's to say,
// in a condition on the method's grant that reads the facts of the row (heldElsewhere,
// heldGlobally).
{{- end }}`

	// identityTenantScope is the annotation that makes a method a tenant's.
	identityTenantScope = `
{{- if .ForTenant }}
// @permissionScope(domain)
{{- end }}`

	// identityCallerEntersNote is the paragraph a method that changes how a person signs
	// in, or whether their account stands, adds to its comment, on both sides, where its
	// row offers it only to a caller who enters the tenants the person administers
	// (OffersToEntering): the methods the identity package's requireEnteringFor refuses.
	identityCallerEntersNote = `
{{- if .OffersToEntering }}
//
// A row offers it only to a caller whose session enters every {{ .TenantWord }} the person
// administers (@callerEnters); the method refuses anyone else.
{{- end }}`

	// identityCallerEnters is the annotation of such a method, after the scope's line.
	identityCallerEnters = `
{{- if .OffersToEntering }}
// @callerEnters
{{- end }}`

	// identityNotOnOwnAccountNote is the paragraph the methods that disable and delete a
	// person add to their comment, on both sides and in every tree: the person's own row
	// does not offer them.
	identityNotOnOwnAccountNote = `
//
// The person's own row does not offer it (@notOnOwnAccount): nobody disables or deletes
// themselves, and the method refuses the call.`

	// identityNotOnOwnAccount is the annotation of such a method, after the scope's line
	// and any @callerEnters: the row keeps it off the caller's own account by its ID.
	identityNotOnOwnAccount = `
// @notOnOwnAccount(ID)`

	// identityTenantsToEnterDoc is the comment of TenantsToEnter on each struct whose row is
	// a person or one of their identities (OffersToEntering).
	identityTenantsToEnterDoc = `// TenantsToEnter names the {{ .TenantWord }}s the person administers: a caller whose session
// does not enter one of them is not offered the methods that change how the person signs
// in or whether their account stands.`

	// identityGroupCallerEntersNote is the paragraph the whole application's methods that
	// put a person in a group and take one out add to their comment, after
	// identityJudgedNote, where their row offers them only to a caller who enters the
	// tenant the group administers (OffersToEntering): the methods the identity package's
	// requireEnteringToChange refuses on a group's row. A tenant's own group is changed
	// under the tenant's address, which a caller has entered, so its methods say nothing.
	identityGroupCallerEntersNote = `
{{- if and .OffersToEntering (not .ForTenant) }}
//
// Where the group is kept in a {{ .TenantWord }} and holds one of that {{ .TenantWord }}'s
// administrator roles there, a row offers it only to a caller whose session enters the
// {{ .TenantWord }} (@callerEnters); the method refuses anyone else.
{{- end }}`

	// identityGroupCallerEnters is the annotation of such a method, after the scope's line.
	identityGroupCallerEnters = `
{{- if and .OffersToEntering (not .ForTenant) }}
// @callerEnters
{{- end }}`

	// identityGroupTenantsToEnterDoc is the comment of TenantsToEnter on each struct of the
	// whole application whose row is a group or one of its people (OffersToEntering).
	identityGroupTenantsToEnterDoc = `// TenantsToEnter names the {{ .TenantWord }} the group is kept in, where the group holds one
// of that {{ .TenantWord }}'s administrator roles there: a caller whose session does not
// enter it is not offered the methods that put a person in the group or take one out.`

	// identityRoleTenantsToEnterDoc is the comment of TenantsToEnter on the whole
	// application's lists of the roles people hold and of the roles held for a set time
	// (OffersToEntering).
	identityRoleTenantsToEnterDoc = `// TenantsToEnter names the {{ .TenantWord }} the row's role makes its holder an administrator of,
// where a person holds the role of their own in that {{ .TenantWord }} itself: a caller whose
// session does not enter it is not offered the method that takes the role away.`

	// identityGroupRoleTenantsToEnterDoc is the comment of TenantsToEnter on the whole
	// application's list of the roles the groups hold (OffersToEntering).
	identityGroupRoleTenantsToEnterDoc = `// TenantsToEnter names the {{ .TenantWord }} the group is kept in, where the row's role is one
// of that {{ .TenantWord }}'s administrator roles held there: a caller whose session does not
// enter it is not offered the method that takes the role away from the group.`

	// identityRoleRemovalEntersNote is the paragraph the whole application's method that
	// takes a role from a person adds to its comment where the rows a page runs it from
	// offer it only to a caller who enters the tenant the role administers
	// (RoleRemovalRows): the method the identity package's requireEnteringToChange
	// refuses for such a role.
	identityRoleRemovalEntersNote = `
{{- if .RoleRemovalRows }}
//
// It names no row: a page runs it from a row of the roles people hold, filling the
// request from the row. Where the role is one of a {{ .TenantWord }}'s administrator roles held
// in that {{ .TenantWord }} itself, such a row offers it only to a caller whose session enters
// the {{ .TenantWord }} (@callerEnters); the method refuses anyone else.
{{- end }}`

	// identityGroupRoleRemovalEntersNote is the paragraph the whole application's method
	// that takes a role away from a group adds to its comment, after identityJudgedNote,
	// where a row of the roles the groups hold offers it only to a caller who enters the
	// tenant the group is kept in (OffersToEntering). A tenant's own group is changed under
	// the tenant's address, which a caller has entered, so its method says nothing.
	identityGroupRoleRemovalEntersNote = `
{{- if and .OffersToEntering (not .ForTenant) }}
//
// Where the group is kept in a {{ .TenantWord }} and the role is one of that {{ .TenantWord }}'s
// administrator roles held there, a row of the roles the groups hold offers it only to a
// caller whose session enters the {{ .TenantWord }} (@callerEnters); the method refuses anyone
// else.
{{- end }}`

	// identityTenantGuard opens the body of a tenant's method that hands the identity
	// package nothing of the tenant: the frame has located the row within the tenant,
	// and the method still runs for a request under the tenant's address alone.
	identityTenantGuard = `
{{- if .ForTenant }}
	// A tenant's method acts for a request made under the tenant's address.
	if _, err := client.manager.Tenant(ctx); err != nil {
		return errors.Wrap(err, "identity.Manager.Tenant()")
	}
{{- end }}`

	// identityTenantGuardAnswering is identityTenantGuard for a method that answers a
	// result.
	identityTenantGuardAnswering = `
{{- if .ForTenant }}
	// A tenant's method acts for a request made under the tenant's address.
	if _, err := client.manager.Tenant(ctx); err != nil {
		return nil, errors.Wrap(err, "identity.Manager.Tenant()")
	}
{{- end }}`

	// identitySetUserEmailTemplate is the method that asks for a change of a person's
	// address.
	identitySetUserEmailTemplate = `// Code generated by resourcegeneration. DO NOT EDIT.
// Source: {{ .Source }}

package {{ .Package }}

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

type (
	// Set{{ .Subject }}UserEmail asks for a change of the email address of a person of the
	// {{ .Auth }} auth. Nothing about the account changes: a link is mailed to the new
	// address once the transaction has committed, and the account keeps the address it
	// has until the person follows that link from the new mailbox. Nothing is mailed
	// to the old address to confirm the change; it is told once the change is made. An
	// account always keeps an address, so one is replaced and never taken off, and the
	// address of a person who signs in through an organization's directory is the
	// directory's to change. The answer says whether the link went (Sent) and, where
	// it did not, why (Reason).` + identityTenantUserNote + identityCallerEntersNote + `
	//
	// @rpc` + identityTenantScope + identityCallerEnters + `
	// @outlet({{ .Outlets }})` + identityCredentialClass + `
	Set{{ .Subject }}UserEmail struct {
		// @target({{ .SubjectUser }})
		UserID impulse.UUID
		Email  string ` + "`validate:\"required\"`" + `
	}
	// Set{{ .Subject }}UserEmailResult answers whether the link that confirms the new
	// address was mailed and, where it was not, why, in a sentence for the
	// administrator who asked. Both are set once the transaction has committed: a
	// caller that runs the method in a transaction of its own reads them after its
	// ExecuteFunc returns.
	Set{{ .Subject }}UserEmailResult struct {
		Sent   bool
		Reason string
	}
)

// Execute checks the request in the caller's transaction and registers the mailing for
// after its commit.
func (m *Set{{ .Subject }}UserEmail) Execute(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) (*Set{{ .Subject }}UserEmailResult, error) {` + identityTenantGuardAnswering + `
	result := &Set{{ .Subject }}UserEmailResult{}
	if err := client.manager.SetEmail(ctx, txn, m.UserID, m.Email, func(sent bool, reason string) {
		result.Sent, result.Reason = sent, reason
	}); err != nil {
		return nil, errors.Wrap(err, "identity.Manager.SetEmail()")
	}

	return result, nil
}
`

	// identitySendPasswordResetLinkTemplate is the method that mails a person the link
	// that resets their password.
	identitySendPasswordResetLinkTemplate = `// Code generated by resourcegeneration. DO NOT EDIT.
// Source: {{ .Source }}

package {{ .Package }}

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

type (
	// Send{{ .Subject }}UserPasswordResetLink mails a person of the {{ .Auth }} auth a link
	// that resets their password, to the email address they confirmed, once the
	// transaction has committed. Nothing about the account changes when it is sent:
	// the password it has signs the person in, and their sessions stand, until the
	// link is used. The link works once and the message says who asked. The answer
	// says whether the link went (Sent) and, where it did not, why (Reason): the
	// account has no password to reset or no confirmed address, or the address was
	// mailed too often.` + identityTenantResetNote + `
	//
	// @rpc` + identityTenantScope + `
	// @outlet({{ .Outlets }})` + identityCredentialClass + `
	Send{{ .Subject }}UserPasswordResetLink struct {
		// @target({{ .SubjectUser }})
		UserID impulse.UUID
	}
	// Send{{ .Subject }}UserPasswordResetLinkResult answers whether the link was mailed
	// and, where it was not, why, in a sentence for the administrator who asked. Both
	// are set once the transaction has committed: a caller that runs the method in a
	// transaction of its own reads them after its ExecuteFunc returns.
	Send{{ .Subject }}UserPasswordResetLinkResult struct {
		Sent   bool
		Reason string
	}
)

// Execute checks the request in the caller's transaction and registers the mailing for
// after its commit.
func (m *Send{{ .Subject }}UserPasswordResetLink) Execute(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) (*Send{{ .Subject }}UserPasswordResetLinkResult, error) {` + identityTenantGuardAnswering + `
	result := &Send{{ .Subject }}UserPasswordResetLinkResult{}
	if err := client.manager.SendPasswordResetLink(ctx, txn, m.UserID, func(sent bool, reason string) {
		result.Sent, result.Reason = sent, reason
	}); err != nil {
		return nil, errors.Wrap(err, "identity.Manager.SendPasswordResetLink()")
	}

	return result, nil
}
`

	// identitySetUserNameTemplate is the method that writes a person's name.
	identitySetUserNameTemplate = `// Code generated by resourcegeneration. DO NOT EDIT.
// Source: {{ .Source }}

package {{ .Package }}

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

// Set{{ .Subject }}UserName writes the name of a person of the {{ .Auth }} auth, or
// takes it off (no Name, or nothing but space): the name as people read it, which a
// page shows in place of the username. It identifies nobody, so nothing follows it: no
// session ends and no role moves. The person may change it themselves, and a name
// written here stands until it is written again: a later sign-in through a directory
// fills a name only where the person holds none. A name is kept without the space
// around it and is at most 200 characters.` + identityTenantUserNote + `
//
// @rpc` + identityTenantScope + `
// @outlet({{ .Outlets }})
// @answers(204)
type Set{{ .Subject }}UserName struct {
	// @target({{ .SubjectUser }})
	UserID impulse.UUID
	Name   *string
}

// Execute buffers the name into the caller's transaction.
func (m *Set{{ .Subject }}UserName) Execute(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) error {` + identityTenantGuard + `
	if err := client.manager.SetName(ctx, txn, m.UserID, m.Name); err != nil {
		return errors.Wrap(err, "identity.Manager.SetName()")
	}

	return nil
}
`

	// identityDirectoryRoleChoicesTemplate declares the roles a tenant may be opened to
	// its directory's people with, a computed resource: the roles that exist in the
	// tenant that the caller could give there directly. The role of the method that
	// opens a tenant is picked from it, so the dialog offers what the method takes.
	identityDirectoryRoleChoicesTemplate = `// Code generated by resourcegeneration. DO NOT EDIT.
// Source: {{ .Source }}

package {{ .Package }}

import (
	"context"
	"iter"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// {{ .DirectoryRoleChoice }} is one role a {{ .TenantWord }} may be opened to its directory's people
// with: a role that exists in the {{ .TenantWord }} ({{ Pluralize .TenantRoleChoice }}) that the
// caller could give there directly, with its description. The role of the method that
// opens a {{ .TenantWord }} to its directory is picked from the list, which holds the roles the
// caller may give (the grant of Assign{{ .Auth }}{{ .TenantName }}Role, asked with each role, for
// no person in particular) and no other: opening a {{ .TenantWord }} gives the role to everyone
// its directory vouches for, so the dialog offers what the caller could give by hand,
// and a role it leaves out is one the method refuses.
//
// It is a computed resource: the role file's roles are held by the permission engine,
// so the engine is asked and no query selects the list. The role file grants it like
// any resource, to the role that holds the method.
//
// @computed
// @permissionScope(domain)
// @suppress(readHandler)
// @outlet({{ .Outlets }})
// @order(Name asc)
// @rowWord(role)
type {{ .DirectoryRoleChoice }} struct {
	// @primarykey
	// @attribute(name)
	// @roleOf({{ .Auth }})
	Name        string ` + "`spanner:\"Name\"`" + `
	Description string ` + "`spanner:\"Description\"`" + `
}

// Resource implements resource.Resourcer.
func ({{ .DirectoryRoleChoice }}) Resource() accesstypes.Resource {
	return "{{ Pluralize .DirectoryRoleChoice }}"
}

// List{{ .DirectoryRoleChoice }} yields the roles the {{ .TenantWord }} the request was checked in
// may be opened to its directory's people with; the generated handler applies the
// request's filter, sort and page over them. The roles are the ones the request's
// caller may give: the list's frame stamps the caller the request was checked as on the
// context, as a method's does, and the rule reads it there. The rule is handed the
// database and not a transaction: it reads the {{ .TenantWord }}'s roles from the permission
// engine outside any, and asks the grant of the method that gives a role in a
// {{ .TenantWord }}, which may read a row, in one of its own.
func List{{ .DirectoryRoleChoice }}(ctx context.Context, qSet *resource.QuerySet[{{ .DirectoryRoleChoice }}], db resource.Client, client *Client) iter.Seq2[*{{ .DirectoryRoleChoice }}, error] {
	return func(yield func(*{{ .DirectoryRoleChoice }}, error) bool) {
		domain, ok := qSet.Scope().Domain()
		if !ok {
			yield(nil, errors.Newf("%s lists the roles of one tenant and was checked in a scope that names none", qSet.Resource()))

			return
		}
		roles, err := client.manager.DirectoryRoles(ctx, db, domain)
		if err != nil {
			yield(nil, errors.Wrap(err, "identity.Manager.DirectoryRoles()"))

			return
		}
		for _, role := range roles {
			if !yield(&{{ .DirectoryRoleChoice }}{Name: string(role.Name), Description: role.Description}, nil) {
				return
			}
		}
	}
}
`

	// identityTenantRoleChoicesTemplate declares the roles that exist in one tenant, a
	// computed resource: the role file's roles are in the engine's memory and only a
	// custom role is a row, so the list is read from the engine and no query selects it.
	identityTenantRoleChoicesTemplate = `// Code generated by resourcegeneration. DO NOT EDIT.
// Source: {{ .Source }}

package {{ .Package }}

import (
	"context"
	"iter"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	{{- if .OffersRolesToGive }}
	"github.com/impulseframework/impulse/identity"
	{{- end }}
	"github.com/impulseframework/impulse/resource"
)

// {{ .TenantRoleChoice }} is one role that exists in one {{ .TenantName }}: a tenant role
// of the release's role file, or a custom role held in the tenant or in every tenant.
// They are the roles a person is given there, so the list is what a role is picked
// from: the field of a method that names a role lists it.
//
// It is a computed resource. The role file's roles are held by the permission engine
// and only a custom role is a row of a table, so the engine is asked and no query
// selects the list.
{{- if .OffersRolesToGive }} A person is listed the roles they could give with one
// method. A role file says which roles a person hands out on the grant of each method
// that gives one, whose role field picks from this list, each for itself:
//
//	{{ .GivingMethods }}
//
// A condition there reads the role (new.role != 'TenantAdmin'). The list is asked for
// one method at a time, by a filter that names it
// (method:eq:{{ (index .TenantGivings 0).Method }}), and leaves out a role that method
// refuses the caller (identity.Manager.RolesToGive): the limit is said once, on the
// method's grant, and a dialog offers what its own method takes. A role one method
// takes and another refuses is in the first one's list alone, and a request that names
// no method is listed no role. The role file grants the list like any resource, and a
// condition on the role's name there (name != 'TenantAdmin') narrows it further: the
// library judges each role the list answers. A caller filters by the fields they may
// read, and a computed list filters only by a field granted with no condition: the role
// is granted method among the fields of a grant with no condition, or, beside a grant
// that carries one, as a grant of the field alone ({{ Pluralize .TenantRoleChoice }}.method).
{{- else }} The role file grants it like any resource, and a grant may carry
// a condition on the role's name (name != 'TenantAdmin'): the library judges each role
// the engine answers, so a person is listed the roles their grant admits and picks
// among roles they may give. The method that gives a role carries the same limit on
// its own grant (new.role != 'TenantAdmin'), which is what refuses a request that
// names a role the list left out.
{{- end }}
//
// @computed
// @permissionScope(domain)
// @suppress(readHandler)
// @outlet({{ .Outlets }})
// @order(Name asc)
// @rowWord(role)
type {{ .TenantRoleChoice }} struct {
	// @primarykey
	// @attribute(name)
	// @roleOf({{ .Auth }})
	Name        string ` + "`spanner:\"Name\"`" + `
	Description string ` + "`spanner:\"Description\"`" + `
	{{- if .OffersRolesToGive }}
	// Method is the method the role is listed for, the one the request's filter
	// names: the caller could give the role with it.
	Method string ` + "`spanner:\"Method\" allow_filter:\"true\"`" + `
	{{- end }}
}

// Resource implements resource.Resourcer.
func ({{ .TenantRoleChoice }}) Resource() accesstypes.Resource {
	return "{{ Pluralize .TenantRoleChoice }}"
}

// List{{ .TenantRoleChoice }} yields the roles that exist in the tenant the request was
// checked in{{ if .OffersRolesToGive }} that the caller could give there with the method the request's filter
// names, and none for a request that names no method; the generated handler applies
// the rest of the filter, the sort and the page over them.{{ else }}; the generated handler applies the request's filter, sort and page over
// them.{{ end }}
func List{{ .TenantRoleChoice }}(ctx context.Context, qSet *resource.QuerySet[{{ .TenantRoleChoice }}], _ resource.Client, client *Client) iter.Seq2[*{{ .TenantRoleChoice }}, error] {
	return func(yield func(*{{ .TenantRoleChoice }}, error) bool) {
		domain, ok := qSet.Scope().Domain()
		if !ok {
			yield(nil, errors.Newf("%s lists the roles of one tenant and was checked in a scope that names none", qSet.Resource()))

			return
		}
		{{- if .OffersRolesToGive }}
		method, named, err := identity.GivingMethod(qSet.Resource(), []accesstypes.Resource{
			{{- range .TenantGivings }}
			{{ .Method }}{}.Method(),
			{{- end }}
		}, qSet.Filter().Take("Method"))
		if err != nil {
			yield(nil, errors.Wrap(err, "identity.GivingMethod()"))

			return
		}
		if !named {
			return
		}
		roles, err := client.manager.RolesToGive(ctx, method, domain)
		if err != nil {
			yield(nil, errors.Wrap(err, "identity.Manager.RolesToGive()"))

			return
		}
		{{- else }}
		roles, err := client.manager.TenantRoles(ctx, domain)
		if err != nil {
			yield(nil, errors.Wrap(err, "identity.Manager.TenantRoles()"))

			return
		}
		{{- end }}
		for _, role := range roles {
			if !yield(&{{ .TenantRoleChoice }}{Name: string(role.Name), Description: role.Description{{ if .OffersRolesToGive }}, Method: string(method){{ end }}}, nil) {
				return
			}
		}
	}
}
`

	// identityTenantAssignRoleTemplate is the method that gives a person a role in a
	// tenant.
	identityTenantAssignRoleTemplate = `// Code generated by resourcegeneration. DO NOT EDIT.
// Source: {{ .Source }}

package {{ .Package }}

import (
	"context"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/identity"
	"github.com/impulseframework/impulse/resource"
)

// Assign{{ .Subject }}Role gives a person of the {{ .Auth }} auth a role in one
// {{ .TenantName }}: the tenant the request is made under, and no other. The role must
// exist there. The person's next request is served with the role.
//
// The role is given for a period where the request names one (StartsAt, EndsAt, each
// optional): outside it the role grants nothing, and nothing is written when it begins
// or ends. Giving a role the person holds there already gives it the period of this
// request in place of the one it had, so a request that names neither makes the role
// stand until it is taken away; ending a role now is taking it away.
//
// The role carries the reason the request gives (Reason), which is shown wherever the
// person's roles are listed. It is replaced with the period: a role given again carries
// the reason of the new request, and none where the request gives none.
//
// It is a tenant's method: it names a row of {{ .TenantUser }}, the tenant's own list of
// the auth's people, so the role goes to a person who holds one in the tenant already.
// A person new to the tenant is invited to it, holding a role from the start.
//
// @rpc
// @permissionScope(domain)
// @outlet({{ .Outlets }})
// @answers(204)` + identityAccessClass + `
type Assign{{ .Subject }}Role struct {
	// @target({{ .TenantUser }})
	UserID impulse.UUID
	// Role is the role the person is given, picked from the roles that exist in the
	// tenant ({{ Pluralize .TenantRoleChoice }}). A grant's condition reads it as new.role,
	// so a role file can limit which roles the grant's holder hands out.
	//
	// @attribute(role)
	// @enumerate({{ Pluralize .TenantRoleChoice }})
	Role string ` + "`validate:\"required\"`" + identityPeriodFields + identityReasonField + `
}

// Execute buffers the membership into the caller's transaction.
func (m *Assign{{ .Subject }}Role) Execute(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) error {
	tenant, err := client.manager.Tenant(ctx)
	if err != nil {
		return errors.Wrap(err, "identity.Manager.Tenant()")
	}
	period, err := identity.RequestPeriod(m.StartsAt, m.EndsAt)
	if err != nil {
		return errors.Wrap(err, "identity.RequestPeriod()")
	}
	if err := tenant.AssignRoleFor(ctx, txn, m.UserID, accesstypes.Role(m.Role), period, identity.RequestReason(m.Reason)); err != nil {
		return errors.Wrap(err, "identity.Tenant.AssignRoleFor()")
	}

	return nil
}
`

	// identityTenantRemoveRoleTemplate is the method that takes away a role a person
	// holds in a tenant.
	identityTenantRemoveRoleTemplate = `// Code generated by resourcegeneration. DO NOT EDIT.
// Source: {{ .Source }}

package {{ .Package }}

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// Remove{{ .Subject }}Role takes away a role a person of the {{ .Auth }} auth holds in
// one {{ .TenantName }}: the tenant the request is made under, and no other. The person's
// next request is served without it.
//
// It is a tenant's method: it names a row of {{ .TenantHeldRole }}, the tenant's one
// list of the roles held there, by the row's key columns (the person's id, the role, and
// the group the role comes through, which is none for a role of the person's own), so a
// condition on its grant reads the row (role, everyDomain). A role the person holds in
// every tenant alone is a row of the list too and is not one tenant's to take away: it
// is refused, whatever the role file says. So is a role that comes to the person
// through a group, whose row the list holds too: it is taken away from the group, or the
// person is taken out of the group, on the group's page.
//
// A person who removes a role from themselves keeps an administrator role nobody else
// would hold in the tenant (identity.WithAdministratorRoles names them). A person left
// with no role anywhere is deleted with their identity links and sessions, in the same
// transaction: an account nothing admits is not kept.
//
// @rpc
// @permissionScope(domain)
// @outlet({{ .Outlets }})
// @answers(204)` + identityAccessClass + `
type Remove{{ .Subject }}Role struct {
	// @target({{ .TenantHeldRole }}, key: UserID)
	UserID impulse.UUID
	// @target({{ .TenantHeldRole }}, key: Role)
	Role string ` + "`validate:\"required\"`" + `
	// GroupID is the group the role comes through, as the row of the list names it:
	// empty for a role of the person's own, the one kind this method takes away, and so
	// left out of a request that takes one away.
	//
	// @target({{ .TenantHeldRole }}, key: GroupID)
	GroupID string
}

// Execute buffers the removal into the caller's transaction.
func (m *Remove{{ .Subject }}Role) Execute(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) error {
	if m.GroupID != "" {
		return httpio.NewBadRequestMessage("that role comes to the person through a group, so it is not taken away from the person: take the role away from the group, or take the person out of the group, on the group's page")
	}
	tenant, err := client.manager.Tenant(ctx)
	if err != nil {
		return errors.Wrap(err, "identity.Manager.Tenant()")
	}
	if err := tenant.RemoveRole(ctx, txn, m.UserID, accesstypes.Role(m.Role)); err != nil {
		return errors.Wrap(err, "identity.Tenant.RemoveRole()")
	}

	return nil
}
`

	// identityTenantInviteUserTemplate is the method that invites an address into a
	// tenant.
	identityTenantInviteUserTemplate = `// Code generated by resourcegeneration. DO NOT EDIT.
// Source: {{ .Source }}

package {{ .Package }}

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/identity"
	"github.com/impulseframework/impulse/resource"
)

type (
	// Invite{{ .Subject }}User invites an address into one {{ .TenantName }} of the
	// {{ .Auth }} auth: the tenant the request is made under, and no other. A link is
	// mailed to the address, and whoever follows it and signs in, or signs up, is given
	// the roles in the tenant. An invitation gives at least one role, and each must
	// exist in the tenant. Name is the name the sender gives the person: whoever
	// accepts the invitation takes it where their account holds none.
	//
	// The invitation is written in the transaction, beside whatever else it writes,
	// and its message is sent once the transaction has committed, since a message
	// cannot be taken back. The answer says whether the message went (Sent). When it
	// did not, the invitation is ended, not mailed, so no link stands that nobody
	// holds: invite again.
	//
	// @rpc
	// @permissionScope(domain)
	// @outlet({{ .Outlets }})` + identityAccessClass + `
	Invite{{ .Subject }}User struct {
		Email string ` + "`validate:\"required\"`" + `
		Name  *string
		// Roles are the roles the person is given, picked from the roles that exist in the
		// tenant ({{ Pluralize .TenantRoleChoice }}). A grant's condition reads each of them as
		// new.role, so a role file can limit which roles the grant's holder hands out.
		//
		// @attribute(role)
		// @enumerate({{ Pluralize .TenantRoleChoice }})
		Roles []string
	}
	// Invite{{ .Subject }}UserResult answers the invitation's id and whether its
	// message went. Sent is set once the transaction has committed.
	Invite{{ .Subject }}UserResult struct {
		ID   impulse.UUID
		Sent bool
	}
)

// Execute buffers the invitation into the caller's transaction and registers the send
// for after its commit.
func (m *Invite{{ .Subject }}User) Execute(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) (*Invite{{ .Subject }}UserResult, error) {
	tenant, err := client.manager.Tenant(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Manager.Tenant()")
	}
	invitation := &identity.Invitation{Email: m.Email, Name: m.Name}
	for _, role := range m.Roles {
		invitation.Roles = append(invitation.Roles, accesstypes.Role(role))
	}
	result := &Invite{{ .Subject }}UserResult{}
	id, err := tenant.Invite(ctx, txn, invitation, func(sent bool) { result.Sent = sent })
	if err != nil {
		return nil, errors.Wrap(err, "identity.Tenant.Invite()")
	}
	result.ID = id

	return result, nil
}
`

	// identityTenantRemoveUserTemplate is the method that removes a person from a tenant:
	// everything of their own the tenant holds, in one call, after a warning that says
	// whether the account goes with it.
	identityTenantRemoveUserTemplate = `// Code generated by resourcegeneration. DO NOT EDIT.
// Source: {{ .Source }}

package {{ .Package }}

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

// Remove{{ .Subject }}User removes a person of the {{ .Auth }} auth from one {{ .TenantName }},
// the tenant the request is made under, in one transaction: every role of their own held in
// the tenant goes, and their place in each group kept in it. What reaches the tenant another
// way stays: a role held in every tenant, a role for the whole application and a place in a
// group kept for the whole application. A person left holding nothing that has not ended is
// deleted with their sign-in identities and sessions, and a person who reaches the tenant no
// more is told by mail.
//
// It is a tenant's method: it names a row of {{ .TenantUser }}, the tenant's own list of the
// auth's people. It takes only what is the tenant's own, so its grant needs no condition on
// the row. Each role it takes is one the caller could take away with the method that takes
// one role: a role the caller may not take away refuses the removal. Nobody removes
// themselves with it: the person's own row does not offer it (@notOnOwnAccount), and a
// person leaves a tenant from their own account page.
//
// The method declares a warning, which a person types "remove" under before the call is
// confirmed: the roles that go, and whether the account goes with them.
//
// @rpc
// @permissionScope(domain)
// @notOnOwnAccount(ID)
// @outlet({{ .Outlets }})
// @answers(204)
// @warning(typed: remove)` + identityAccessClass + `
type Remove{{ .Subject }}User struct {
	// @target({{ .TenantUser }})
	UserID impulse.UUID
}

// Warning says what the removal would take from the person, the roles by name, and whether
// the account goes with it. It writes nothing.
func (m *Remove{{ .Subject }}User) Warning(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) (*resource.Warning, error) {
	tenant, err := client.manager.Tenant(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Manager.Tenant()")
	}
	warning, err := tenant.RemovalWarning(ctx, txn, m.UserID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Tenant.RemovalWarning()")
	}

	return warning, nil
}

// Execute buffers the removal into the caller's transaction.
func (m *Remove{{ .Subject }}User) Execute(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) error {
	tenant, err := client.manager.Tenant(ctx)
	if err != nil {
		return errors.Wrap(err, "identity.Manager.Tenant()")
	}
	if err := tenant.RemovePerson(ctx, txn, m.UserID); err != nil {
		return errors.Wrap(err, "identity.Tenant.RemovePerson()")
	}

	return nil
}
`
)
