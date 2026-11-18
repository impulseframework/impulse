package generation

// The sources of a tenant's lists (identity_lists.go): views per auth, declared
// beside the tables they read. Each is a @virtual struct whose query is a constant of
// the file, so the generator reads which tables it reads as it does for any view, and
// the tree holds Go files alone.

const (
	// identityTenantPersonFacts are the two facts about a person that a row of a tenant's
	// list carries, as the last columns of its select: t.DomainId is the row's tenant and u
	// the person's account (identityTenantPeopleFrom). A membership names its person by the
	// id of their account. Each check is an equality on the
	// leading columns of the store's index by user (the user, then the kind and the axis),
	// so it reads that person's memberships and nothing else. HeldElsewhere is two checks
	// for that reason: written as one, with the two kinds under an OR, the database read
	// the memberships table by its key for every listed person (measured on Spanner).
	// Where the auth keeps groups, a place in a group counts as a role held where the
	// group is kept (identityGroupPersonElsewhere, identityGroupPersonGlobally).
	//
	// A membership that has ended is no role held anywhere. One that has not begun is one:
	// the person is known there and will hold it, as the rules on where a person belongs
	// read it (identitySourceData.Standing).
	identityTenantPersonFacts = `  (EXISTS (
    SELECT 1 FROM {{ .Policy.UserRoles }} o
    WHERE o.UserId = u.Id AND o.Kind = {{ .KindEvery }} AND o.Axis = {{ .ScopeAxis }}
      AND {{ .Standing "o" }}
  ) OR EXISTS (
    SELECT 1 FROM {{ .Policy.UserRoles }} o
    WHERE o.UserId = u.Id AND o.Kind = {{ .KindDomain }} AND o.Axis = {{ .ScopeAxis }} AND o.DomainId != t.DomainId
      AND {{ .Standing "o" }}
  )` + identityGroupPersonElsewhere + `) AS HeldElsewhere,
  {{ if .KeepsGroups }}({{ end }}EXISTS (
    SELECT 1 FROM {{ .Policy.UserRoles }} g
    WHERE g.UserId = u.Id AND g.Kind = {{ .KindGlobal }}
      AND {{ .Standing "g" }}
  )` + identityGroupPersonGlobally + ` AS HeldGlobally`

	// identityTenantPeopleFrom selects the people of every tenant, t the tenant and the
	// name and u the account: who holds a role in a tenant (one tenant's memberships, and
	// the memberships held in every tenant once per row of the tenant record's table),
	// joined to the account. A membership names its person by the id of their account,
	// and the join is on the users table's key: a page reads one row of the table for
	// each person. The memberships held in every tenant are read by the
	// table's key (the hint), where they are one short range; left to choose, the database
	// ranged over the index by user when a page continued from a cursor. Where the auth
	// keeps groups, the people a tenant holds through one join them
	// (identityTenantGroupPeople).
	//
	// A person is one of a tenant's people by what they hold at the moment: a membership
	// outside its period, ended or not yet begun, puts nobody in the list, whether or not
	// its row still stands in the table (identitySourceData.Counting).
	identityTenantPeopleFrom = `FROM (
  SELECT m.DomainId AS DomainId, m.UserId AS UserId
  FROM {{ .Policy.UserRoles }} m
  WHERE m.Kind = {{ .KindDomain }} AND m.Axis = {{ .ScopeAxis }}
    AND {{ .Counting "m" }}
  UNION DISTINCT
  SELECT s.{{ .TenantKey }} AS DomainId, m.UserId AS UserId
  FROM {{ .Policy.UserRoles }}@{FORCE_INDEX=_BASE_TABLE} m
  CROSS JOIN {{ .TenantTable }} s
  WHERE m.Kind = {{ .KindEvery }} AND m.Axis = {{ .ScopeAxis }}
    AND {{ .Counting "m" }}` + identityTenantGroupPeople + `
) t
JOIN {{ .Tables.Users }} u ON u.Id = t.UserId`

	// identityTenantOutsideDirectory is the fact that a person of a tenant's list has no
	// sign-in through the tenant's directory, as a condition of the list's select: ts is
	// the tenant's row and td its directory's (identityTenantDirectoryJoins), u the
	// person's account. A tenant with no directory has nobody outside it, and a super
	// administrator is outside no tenant's: no tenant's sign-in settings shut them out.
	// A sign-in through a directory is an identity linked to the account under the
	// directory's method and connection, read by the identities table's index on the
	// user; an auth that keeps no identities signs nobody in through a directory.
	//
	// The super administrator is the person who holds the role now: the membership
	// counts inside its period alone (identitySourceData.Counting), as the sign-in rule
	// and the session's reach read it, which ask the permission engine and its listing
	// of what a person holds at the present. The exemption says who enters now, so a
	// membership that has ended exempts nobody, and neither does one that has not begun
	// (Standing, which the facts of where a person belongs use, would exempt a person
	// the sign-in keeps out). The library's writes give the role no period
	// (access.ValidatePeriod); the test is here so that a row written past them is read
	// as every other reader reads it.
	//
	// The tenant's own administrator who has a password is outside no directory either,
	// where the auth has a password method: they enter a directory-only tenant with the
	// password and a second factor (signin.TenantDeclaration.AdmitsAdministrator). The
	// administrator is who the permission engine says it is (access.administersTenant):
	// a person holding, inside its period, one of the roles the application names as a
	// tenant's administrator's in the tenant itself, by a membership of their own or
	// through a group kept in that tenant, and never one held in every tenant or
	// through a group kept for the whole application. The names are the engine's own,
	// read as a parameter the generated client hands the list, so the list, the sign-in
	// and the session's reach answer over one set of names; with none named nobody is
	// exempt. Whether the person gave the second factor is a session's to say, not a
	// person's, so the list reads the password alone.
	identityTenantOutsideDirectory = `td.{{ .TenantDirectory.TenantColumn }} IS NOT NULL{{ if .HasIdentities }} AND NOT EXISTS (
    SELECT 1 FROM {{ .Tables.Identities }} di
    WHERE di.UserId = u.Id AND di.Method = td.{{ .TenantDirectory.MethodColumn }} AND di.Connection = td.{{ .TenantDirectory.ConnectionColumn }}
  ){{ end }} AND NOT EXISTS (
    SELECT 1 FROM {{ .Policy.UserRoles }} sa
    WHERE sa.UserId = u.Id AND sa.Kind = {{ .KindGlobal }} AND sa.Role = {{ .SuperAdministratorRole }}
      AND {{ .Counting "sa" }}
  ){{ if .AdministratorsEnter }} AND NOT (u.PasswordHash IS NOT NULL AND (EXISTS (
    SELECT 1 FROM {{ .Policy.UserRoles }} ta
    WHERE ta.UserId = u.Id AND ta.Kind = {{ .KindDomain }} AND ta.Axis = {{ .ScopeAxis }} AND ta.DomainId = t.DomainId
      AND ta.Role IN UNNEST(@{{ .TenantAdministratorsParam }})
      AND {{ .Counting "ta" }}
  ){{ if .KeepsGroups }} OR EXISTS (
    SELECT 1 FROM {{ .Policy.GroupRoles }} tr
    JOIN {{ .Policy.Groups }} tg ON tg.Id = tr.GroupId
    JOIN {{ .Policy.GroupMembers }} tm ON tm.GroupId = tr.GroupId
    WHERE tm.UserId = u.Id
      AND tg.Kind = {{ .KindDomain }} AND tg.Axis = {{ .ScopeAxis }} AND tg.DomainId = t.DomainId
      AND tr.Kind = {{ .KindDomain }} AND tr.Axis = {{ .ScopeAxis }} AND tr.DomainId = t.DomainId
      AND tr.Role IN UNNEST(@{{ .TenantAdministratorsParam }})
      AND {{ .Counting "tr" }}
      AND {{ .Counting "tm" }}
  ){{ end }})){{ end }}`

	// identityTenantDirectoryFacts are the two columns a tenant's list of its people
	// carries where the tenant record declares the directory-only switch, after the
	// person's facts: whether the person has no sign-in through the tenant's directory,
	// and, while the tenant is directory only, the sentence that says so. The sentence
	// speaks of the tenant's directory and does not name it: a directory's connection
	// is a key, for a Microsoft Entra tenant an id nobody knows, and what a person
	// calls the directory is said by the sign-in registrations, which a query does not
	// read. The list is one tenant's, and a tenant has one directory, the one its
	// Sign-in page shows.
	identityTenantDirectoryFacts = `,
  (` + identityTenantOutsideDirectory + `) AS OutsideDirectory,
  IF(ts.{{ .TenantDirectory.OnlyColumn }} AND ` + identityTenantOutsideDirectory + `,
    'Has not signed in through this {{ .TenantWordLiteral }}\'s directory, so cannot enter while this {{ .TenantWordLiteral }} is directory only',
    NULL) AS CannotEnter`

	// identityTenantDirectoryJoins join each row of a tenant's list to its tenant's row
	// and to the tenant's directory, where it has one: a tenant has one directory. Both
	// are outer joins, so the facts add columns and drop no row: a membership whose
	// domain the tenant record no longer holds lists as it did before the facts, with
	// nobody outside a directory there.
	identityTenantDirectoryJoins = `
LEFT JOIN {{ .TenantTable }} ts ON ts.{{ .TenantKey }} = t.DomainId
LEFT JOIN {{ .TenantDirectory.Table }} td ON td.{{ .TenantDirectory.TenantColumn }} = ts.{{ .TenantKey }}`

	// identityTenantUsersTemplate declares the tenant's people.
	identityTenantUsersTemplate = `// Code generated by resourcegeneration. DO NOT EDIT.
// Source: {{ .Source }}

package {{ .Package }}

import (
	{{- if .AdministratorsEnter }}
	"sync/atomic"
	{{- end }}
	"time"

	"github.com/impulseframework/impulse"
	{{- if .AdministratorsEnter }}
	"github.com/impulseframework/impulse/accesstypes"
	{{- end }}
)

type (
	// {{ .TenantUser }} is one person of the {{ .Auth }} auth as one {{ .TenantName }} sees
	// them: a row for each person who holds a role in the tenant or in every tenant, with
	// the columns of {{ .User }}. A person is one account for the whole application, so
	// the same person is a row in each tenant they hold a role in: the account's id is the
	// key of a row within its tenant, and DomainId, the tenant, holds the row to it.
	//
	// A role counts inside its period alone: a person whose only role in the tenant has
	// ended, or has not begun, is not one of its people, whether or not the role's row
	// still stands. {{ .TenantRole }} lists a role that has not begun, with its period.
	//
	// Two columns are facts about the person that no table holds, each under the name a
	// grant's condition reads. HeldElsewhere (heldElsewhere) says the person also holds a
	// role outside this tenant's own: in another tenant, or in every tenant. HeldGlobally
	// (heldGlobally) says the person holds a role for the whole application. A role file
	// uses them to say what a tenant may see and do of a person who is not the tenant's
	// alone. The list filters and sorts by the username and by the two facts; the
	// account's other columns are no filter keys, so a role file may grant any of them
	// under a condition at no cost to a page.
	{{- if .TenantDirectory }}
	//
	// Two more columns say what the tenant's directory-only switch does to the person.
	// OutsideDirectory says the person has no sign-in through the tenant's directory, so
	// the switch keeps them out while it is on: it is who turning the switch on would
	// shut out, asked before it is on. CannotEnter is the sentence the list shows while
	// the tenant is directory only, on the row of a person it keeps out ("Has not signed
	// in through this {{ .TenantWord }}'s directory, so cannot enter while this {{ .TenantWord }} is
	// directory only"), and nothing on every other row: the sentence never says the
	// directory's connection, which is a key and no name. The list is made from who holds a role in the tenant,
	// not from who can sign in, so a person the switch keeps out stays in it with their
	// roles. A super administrator is outside no tenant's directory.
	{{- if .AdministratorsEnter }} Neither is the
	// {{ .TenantWord }}'s own administrator who has a password: a person who holds one of the roles
	// the application names as a {{ .TenantWord }}'s administrator's in the {{ .TenantWord }} itself enters it
	// with the password and a code mailed to them, so the switch keeps them out of nothing
	// (Set{{ .TenantUser }}AdministratorRoles names the roles).
	{{- end }}
	{{- end }}
	//
	// @virtual
	// @permissionScope(domain)
	// @outlet({{ .Outlets }})
	// @order(Username asc)
	// @page(default: 25, max: 200)
	{{ .TenantUser }} struct {
		// @primarykey
		ID impulse.UUID ` + "`spanner:\"Id\" index:\"true\"`" + `
		// @domain
		DomainID               string     ` + "`spanner:\"DomainId\" index:\"true\"`" + `
		Username               string     ` + "`spanner:\"Username\" index:\"true\"`" + `
		Name                   *string    ` + "`spanner:\"Name\"`" + `
		Email                  *string    ` + "`spanner:\"Email\"`" + `
		EmailVerifiedAt        *time.Time ` + "`spanner:\"EmailVerifiedAt\"`" + `
		Disabled               bool       ` + "`spanner:\"Disabled\"`" + `
		PasswordChangeRequired bool       ` + "`spanner:\"PasswordChangeRequired\"`" + `
		CreatedAt              time.Time  ` + "`spanner:\"CreatedAt\"`" + `
		LastSignInAt           *time.Time ` + "`spanner:\"LastSignInAt\"`" + `
		// @attribute(heldElsewhere)
		HeldElsewhere bool ` + "`spanner:\"HeldElsewhere\" index:\"true\"`" + `
		// @attribute(heldGlobally)
		HeldGlobally bool ` + "`spanner:\"HeldGlobally\" index:\"true\"`" + `
		{{- if .TenantDirectory }}
		OutsideDirectory bool    ` + "`spanner:\"OutsideDirectory\" index:\"true\"`" + `
		CannotEnter      *string ` + "`spanner:\"CannotEnter\"`" + `
		{{- end }}
	}
)

// {{ GoCamel .TenantUser }}Query selects the people of every tenant: who holds a role in a
// tenant ({{ .Policy.UserRoles }}, one tenant's memberships, and the memberships held in
// every tenant once per row of {{ .TenantTable }}), joined to the account by the id a
// membership names its person by.
const {{ GoCamel .TenantUser }}Query = ` + "`" + `
SELECT
  u.Id AS Id,
  t.DomainId AS DomainId,
  u.Username AS Username,
  u.Name AS Name,
  u.Email AS Email,
  u.EmailVerifiedAt AS EmailVerifiedAt,
  u.Disabled AS Disabled,
  u.PasswordChangeRequired AS PasswordChangeRequired,
  u.CreatedAt AS CreatedAt,
  u.LastSignInAt AS LastSignInAt,
` + identityTenantPersonFacts + `{{ if .TenantDirectory }}` + identityTenantDirectoryFacts + `{{ end }}
` + identityTenantPeopleFrom + `{{ if .TenantDirectory }}` + identityTenantDirectoryJoins + `{{ end }}
` + "`" + `

{{- if .AdministratorsEnter }}

// {{ GoCamel .TenantUser }}Administrators are the roles whose holders administer a {{ .TenantWord }}, as the
// {{ .Auth }} auth's permission engine names them: what the list's query reads as
// @{{ .TenantAdministratorsParam }}. None until the auth's identity client is built, and then
// nobody is taken for an administrator.
var {{ GoCamel .TenantUser }}Administrators atomic.Pointer[[]string]

// Set{{ .TenantUser }}AdministratorRoles tells the list which roles administer a {{ .TenantWord }}: the
// ones the {{ .Auth }} auth's permission engine answers who administers a {{ .TenantWord }} by
// (access.WithTenantAdministratorRoles). The auth's generated identity client calls it
// where it is built, so the list says who the directory-only switch keeps out by the
// names the sign-in and the session's reach decide by. It is one value for the process,
// as the auth's engine is.
func Set{{ .TenantUser }}AdministratorRoles(roles []accesstypes.Role) {
	names := make([]string, 0, len(roles))
	for _, role := range roles {
		names = append(names, string(role))
	}
	{{ GoCamel .TenantUser }}Administrators.Store(&names)
}
{{- end }}

// Subquery is the query the rows are read from.
func ({{ .TenantUser }}) Subquery() (query string, params map[string]any) {
	{{- if .AdministratorsEnter }}
	administrators := []string{}
	if named := {{ GoCamel .TenantUser }}Administrators.Load(); named != nil {
		administrators = *named
	}

	return {{ GoCamel .TenantUser }}Query, map[string]any{ {{- printf "%q" .TenantAdministratorsParam }}: administrators}
	{{- else }}
	return {{ GoCamel .TenantUser }}Query, nil
	{{- end }}
}
`

	// identityTenantRolesTemplate declares the roles held in a tenant.
	identityTenantRolesTemplate = `// Code generated by resourcegeneration. DO NOT EDIT.
// Source: {{ .Source }}

package {{ .Package }}

import (
	"time"

	"github.com/impulseframework/impulse"
)

type (
	// {{ .TenantRole }} is one role a person of the {{ .Auth }} auth holds in one
	// {{ .TenantName }}: a row for each role held in the tenant, by a membership of the
	// tenant's own or by one held in every tenant. The key is the account's id and the
	// role, two columns that name the row within its tenant; DomainId is the tenant.
	//
	// EveryDomain is a fact about the row, under the name a grant's condition reads
	// (everyDomain): the role is held here through a membership held in every tenant
	// alone, so it is not this tenant's to take away. A role the person holds both ways is
	// the tenant's own row, with the other standing behind it. The role is read by a
	// condition too (role), so a role file can say which roles its holder takes away.
	//
	// StartsAt and EndsAt are the role's period, the first instant it counts and the first
	// it no longer does, each empty for none, and Reason is why the role was given, where
	// the request gave one: a tenant's administrator reads there why someone holds a role
	// with them for a set time. A role that has ended is not listed: it grants nothing and
	// goes at the person's next sign-in. One that has not begun is listed, where it is
	// given another period or taken away.
	//
	// The roles a person holds through a group are not rows here. The one list a page
	// shows a person's roles from, these rows beside the ones that come through a group,
	// is {{ .TenantHeldRole }}, whose rows Remove{{ .TenantSubject }}Role names.
	//
	// @virtual
	// @permissionScope(domain)
	// @outlet({{ .Outlets }})
	// @order(Username asc, Role asc)
	// @page(default: 25, max: 200)
	{{ .TenantRole }} struct {
		// @domain
		DomainID string ` + "`spanner:\"DomainId\" index:\"true\"`" + `
		// @primarykey
		UserID   impulse.UUID ` + "`spanner:\"UserId\" index:\"true\"`" + `
		Username string       ` + "`spanner:\"Username\" index:\"true\"`" + `
		// @primarykey
		// @attribute(role)
		Role string ` + "`spanner:\"Role\" index:\"true\"`" + `
		// @attribute(everyDomain)
		EveryDomain bool       ` + "`spanner:\"EveryDomain\" index:\"true\"`" + `
		CreatedAt   time.Time  ` + "`spanner:\"CreatedAt\"`" + `
		StartsAt    *time.Time ` + "`spanner:\"StartsAt\"`" + `
		EndsAt      *time.Time ` + "`spanner:\"EndsAt\"`" + `
		Reason      *string    ` + "`spanner:\"Reason\"`" + `
	}
)

// {{ GoCamel .TenantRole }}Query selects the roles held in every tenant: one tenant's
// memberships of {{ .Policy.UserRoles }}, and the memberships held in every tenant once
// per row of {{ .TenantTable }} where the tenant holds no membership of its own for the
// same person and role, each joined to the account as the people's list joins it: by the
// id a membership names its person by, the users table's key, so a page reads one row of
// the users table for each role held. A row
// is keyed by the account's id and the role, each a column the database looks up: a
// read of one row, and the check a method makes of the row it names, read the one
// person's memberships and no other row of the tenant.
const {{ GoCamel .TenantRole }}Query = ` + "`" + `
SELECT
  t.DomainId AS DomainId,
  u.Id AS UserId,
  u.Username AS Username,
  t.Role AS Role,
  t.EveryDomain AS EveryDomain,
  t.CreatedAt AS CreatedAt,
  t.StartsAt AS StartsAt,
  t.EndsAt AS EndsAt,
  t.Reason AS Reason
FROM (
  SELECT m.DomainId AS DomainId, m.UserId AS UserId, m.Role AS Role, FALSE AS EveryDomain, m.CreatedAt AS CreatedAt, m.StartsAt AS StartsAt, m.EndsAt AS EndsAt, m.Reason AS Reason
  FROM {{ .Policy.UserRoles }} m
  WHERE m.Kind = {{ .KindDomain }} AND m.Axis = {{ .ScopeAxis }}
    AND {{ .Standing "m" }}
  UNION ALL
  SELECT s.{{ .TenantKey }} AS DomainId, m.UserId AS UserId, m.Role AS Role, TRUE AS EveryDomain, m.CreatedAt AS CreatedAt, m.StartsAt AS StartsAt, m.EndsAt AS EndsAt, m.Reason AS Reason
  FROM {{ .Policy.UserRoles }}@{FORCE_INDEX=_BASE_TABLE} m
  CROSS JOIN {{ .TenantTable }} s
  WHERE m.Kind = {{ .KindEvery }} AND m.Axis = {{ .ScopeAxis }}
    AND {{ .Standing "m" }}
    AND NOT EXISTS (
      SELECT 1 FROM {{ .Policy.UserRoles }} d
      WHERE d.Kind = {{ .KindDomain }} AND d.Axis = m.Axis AND d.DomainId = s.{{ .TenantKey }} AND d.Role = m.Role AND d.UserId = m.UserId
        AND {{ .Standing "d" }}
    )
) t
JOIN {{ .Tables.Users }} u ON u.Id = t.UserId
` + "`" + `

// Subquery is the query the rows are read from.
func ({{ .TenantRole }}) Subquery() (query string, params map[string]any) {
	return {{ GoCamel .TenantRole }}Query, nil
}
`

	// identityTenantMailLinksTemplate declares a tenant's invitations.
	identityTenantMailLinksTemplate = `// Code generated by resourcegeneration. DO NOT EDIT.
// Source: {{ .Source }}

package {{ .Package }}

import (
	"time"

	"github.com/impulseframework/impulse"
)

type (
	// {{ .TenantMailLink }} is one invitation the {{ .Auth }} auth mailed into one
	// {{ .TenantName }}: a row of {{ .Tables.MailLinks }} of the invitation kind that names
	// the tenant, with the columns {{ .MailLink }} serves of it. DomainId is the tenant the
	// invitation seats a person in (the link's TenantId), and RoleNames the roles it gives
	// there, and Name the name its sender gave the person, where they gave one. The
	// link itself is in the message alone and its hash is not a column.
	//
	// Pending is a fact about the row, under the name a grant's condition reads
	// (pending): the invitation stands, neither accepted nor withdrawn (both consume it)
	// nor expired. The roles are read by a condition too, as role: a list, so a
	// condition that reads it is judged once for each role the invitation gives and
	// holds only when it holds for every one. A role file can so say which invitations
	// its holder withdraws by the roles they give.
	//
	// @virtual
	// @permissionScope(domain)
	// @outlet({{ .Outlets }})
	// @order(CreatedAt desc)
	// @page(default: 25, max: 200)
	{{ .TenantMailLink }} struct {
		// @primarykey
		ID impulse.UUID ` + "`spanner:\"Id\" index:\"true\"`" + `
		// @domain
		DomainID   string        ` + "`spanner:\"DomainId\" index:\"true\"`" + `
		Email      string        ` + "`spanner:\"Email\" index:\"true\"`" + `
		Name       *string       ` + "`spanner:\"Name\"`" + `
		// @attribute(role)
		RoleNames  []string      ` + "`spanner:\"RoleNames\"`" + `
		InvitedByUserID  *impulse.UUID ` + "`spanner:\"InvitedByUserId\"`" + `
		CreatedAt  time.Time     ` + "`spanner:\"CreatedAt\" index:\"true\"`" + `
		ExpiresAt  time.Time     ` + "`spanner:\"ExpiresAt\"`" + `
		ConsumedAt *time.Time    ` + "`spanner:\"ConsumedAt\"`" + `
		// @attribute(pending)
		Pending bool ` + "`spanner:\"Pending\" index:\"true\"`" + `
	}
)

// {{ GoCamel .TenantMailLink }}Query selects the invitations of every tenant: the mail
// links of the invitation kind that name one. The table's index by tenant holds them
// newest first, with the columns the list shows, so a page reads its own rows from the
// index and nothing else.
const {{ GoCamel .TenantMailLink }}Query = ` + "`" + `
SELECT
  l.Id AS Id,
  l.TenantId AS DomainId,
  l.Email AS Email,
  l.Name AS Name,
  l.RoleNames AS RoleNames,
  l.InvitedByUserId AS InvitedByUserId,
  l.CreatedAt AS CreatedAt,
  l.ExpiresAt AS ExpiresAt,
  l.ConsumedAt AS ConsumedAt,
  (l.ConsumedAt IS NULL AND l.ExpiresAt > CURRENT_TIMESTAMP()) AS Pending
FROM {{ .Tables.MailLinks }} l
WHERE l.Kind = {{ .InvitationKind }} AND l.TenantId IS NOT NULL
` + "`" + `

// Subquery is the query the rows are read from.
func ({{ .TenantMailLink }}) Subquery() (query string, params map[string]any) {
	return {{ GoCamel .TenantMailLink }}Query, nil
}
`

	// identityTenantIdentitiesTemplate declares the identities of a tenant's people.
	identityTenantIdentitiesTemplate = `// Code generated by resourcegeneration. DO NOT EDIT.
// Source: {{ .Source }}

package {{ .Package }}

import (
	"time"

	"github.com/impulseframework/impulse"
)

type (
	// {{ .TenantIdentity }} is one way a person of one {{ .TenantName }} signs in to the
	// {{ .Auth }} auth through a directory: a row for each identity of a person who holds a
	// role in the tenant or in every tenant, with the columns of {{ .Identity }} and the
	// person's username. An account is one row for the whole application, so the identity
	// of a person who holds a role in two tenants is a row in each: the identity's id is
	// the key of a row within its tenant, and DomainId, the tenant, holds the row to it.
	// Tenant is the link's own column, the organization its directory vouched for, and is
	// not the row's tenant.
	//
	// HeldElsewhere and HeldGlobally are the two facts of the person's row in
	// {{ .TenantUser }}, under the same names (heldElsewhere, heldGlobally): an identity is
	// the account's, so what a tenant may see and do of one is what it may of the account.
	//
	// @virtual
	// @permissionScope(domain)
	// @outlet({{ .Outlets }})
	// @order(CreatedAt desc)
	// @page(default: 25, max: 200)
	{{ .TenantIdentity }} struct {
		// @primarykey
		ID impulse.UUID ` + "`spanner:\"Id\" index:\"true\"`" + `
		// @domain
		DomainID    string       ` + "`spanner:\"DomainId\" index:\"true\"`" + `
		UserID      impulse.UUID ` + "`spanner:\"UserId\" index:\"true\"`" + `
		Username    string       ` + "`spanner:\"Username\" index:\"true\"`" + `
		Method      string       ` + "`spanner:\"Method\" index:\"true\"`" + `
		Connection  string       ` + "`spanner:\"Connection\"`" + `
		Subject     string       ` + "`spanner:\"Subject\"`" + `
		Tenant      *string      ` + "`spanner:\"Tenant\"`" + `
		EmailAtLink *string      ` + "`spanner:\"EmailAtLink\"`" + `
		CreatedAt   time.Time    ` + "`spanner:\"CreatedAt\" index:\"true\"`" + `
		LastUsedAt  time.Time    ` + "`spanner:\"LastUsedAt\"`" + `
		// @attribute(heldElsewhere)
		HeldElsewhere bool ` + "`spanner:\"HeldElsewhere\" index:\"true\"`" + `
		// @attribute(heldGlobally)
		HeldGlobally bool ` + "`spanner:\"HeldGlobally\" index:\"true\"`" + `
	}
)

// {{ GoCamel .TenantIdentity }}Query selects the identities of the people of every tenant:
// the people as {{ .TenantUser }} selects them, each joined to the rows of
// {{ .Tables.Identities }} that link to the account, through the table's index on the
// user, which carries the columns the list shows.
const {{ GoCamel .TenantIdentity }}Query = ` + "`" + `
SELECT
  i.Id AS Id,
  t.DomainId AS DomainId,
  u.Id AS UserId,
  u.Username AS Username,
  i.Method AS Method,
  i.Connection AS Connection,
  i.Subject AS Subject,
  i.Tenant AS Tenant,
  i.EmailAtLink AS EmailAtLink,
  i.CreatedAt AS CreatedAt,
  i.LastUsedAt AS LastUsedAt,
` + identityTenantPersonFacts + `
` + identityTenantPeopleFrom + `
JOIN {{ .Tables.Identities }} i ON i.UserId = u.Id
` + "`" + `

// Subquery is the query the rows are read from.
func ({{ .TenantIdentity }}) Subquery() (query string, params map[string]any) {
	return {{ GoCamel .TenantIdentity }}Query, nil
}
`
)
