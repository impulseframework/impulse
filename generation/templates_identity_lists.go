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
	// group is kept (identityGroupPersonElsewhere, identityGroupPersonGlobally). Where it
	// keeps the roles held on a record, a role on a record of another tenant is held
	// elsewhere (identityRecordPersonElsewhere).
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
  )` + identityRecordPersonElsewhere + identityGroupPersonElsewhere + `) AS HeldElsewhere,
  {{ if .KeepsGroups }}({{ end }}EXISTS (
    SELECT 1 FROM {{ .Policy.UserRoles }} g
    WHERE g.UserId = u.Id AND g.Kind = {{ .KindGlobal }}
      AND {{ .Standing "g" }}
  )` + identityGroupPersonGlobally + ` AS HeldGlobally`

	// identityTenantPersonHeldHere and identityTenantPersonInEveryTenant are the two more
	// facts about a person that a row of a tenant's list of people carries, after
	// identityTenantPersonFacts, as identityTenantPersonFacts reads them: HeldHere, that
	// the person holds something in the row's tenant itself, and HeldInEveryTenant, that
	// the person holds a role in every tenant at once. Each check is an equality on the
	// leading columns of the store's index by user. Where the auth keeps groups, a place
	// in one of the tenant's own groups is something held in it, and a place in a group
	// kept for the whole application that holds a role in every tenant is a role held in
	// every tenant (identityGroupPersonHere, identityGroupPersonInEveryTenant). Where the
	// auth keeps the roles held on a record, a role on one of the tenant's records is
	// something held in it (identityRecordPersonHere). The list of
	// a tenant's identities carries neither as a column of its own: it reads HeldHere for
	// who manages the account (identityTenantManagedFacts).
	identityTenantPersonHeldHere = `,
  (EXISTS (
    SELECT 1 FROM {{ .Policy.UserRoles }} o
    WHERE o.UserId = u.Id AND o.Kind = {{ .KindDomain }} AND o.Axis = {{ .ScopeAxis }} AND o.DomainId = t.DomainId
      AND {{ .Standing "o" }}
  )` + identityRecordPersonHere + identityGroupPersonHere + `) AS HeldHere`

	// identityRecordPersonElsewhere and identityRecordPersonHere are what a role of the
	// person's own on a record adds to HeldElsewhere and HeldHere, where the auth keeps the
	// roles held on a record: one on a record of another tenant is held elsewhere, and one
	// on a record of the row's tenant is held there. Each is an equality on the leading
	// columns of the people's table's index by user, as the checks beside it are on theirs.
	// A role held through a group is counted by the person's place in the group.
	identityRecordPersonElsewhere = `{{- if .HoldsRecordRoles }} OR EXISTS (
    SELECT 1 FROM {{ .Policy.RecordUserRoles }} o
    WHERE o.UserId = u.Id AND o.Axis = {{ .ScopeAxis }} AND o.DomainId != t.DomainId
      AND {{ .Standing "o" }}
  ){{- end }}`

	identityRecordPersonHere = `{{- if .HoldsRecordRoles }} OR EXISTS (
    SELECT 1 FROM {{ .Policy.RecordUserRoles }} o
    WHERE o.UserId = u.Id AND o.Axis = {{ .ScopeAxis }} AND o.DomainId = t.DomainId
      AND {{ .Standing "o" }}
  ){{- end }}`

	identityTenantPersonInEveryTenant = `,
  (EXISTS (
    SELECT 1 FROM {{ .Policy.UserRoles }} o
    WHERE o.UserId = u.Id AND o.Kind = {{ .KindEvery }} AND o.Axis = {{ .ScopeAxis }}
      AND {{ .Standing "o" }}
  )` + identityGroupPersonInEveryTenant + `) AS HeldInEveryTenant`

	// identityTenantPersonRoles is what a person of a tenant's list of people holds in the
	// row's tenant itself, as a column of its select after the account's: the name of each
	// role once, in alphabetical order. They are the rows the tenant's one list of the
	// roles held (identityTenantHeldRolesTemplate) answers for the person there with
	// EveryDomain false: a role of the person's own held in the tenant that has not ended,
	// one that has not begun among them, and, where the auth keeps groups, the roles that
	// come through a group (identityGroupPersonRoles). The column is written over the
	// tables and does not take that list's query in: the query joins tables, six where the
	// auth keeps groups, and the statement a page reads the people by already holds the
	// twenty Spanner allows (identityTenantThroughDirectory). The person's memberships are
	// read by the store's index by user, as the facts read them (identityTenantPersonFacts),
	// and each period by the test the lists of roles apply (identitySourceData.Standing). A
	// served test holds the column to the list.
	identityTenantPersonRoles = `  ARRAY(
    SELECT DISTINCT hr.Role
    FROM (
      SELECT o.Role AS Role
      FROM {{ .Policy.UserRoles }} o
      WHERE o.UserId = u.Id AND o.Kind = {{ .KindDomain }} AND o.Axis = {{ .ScopeAxis }} AND o.DomainId = t.DomainId
        AND {{ .Standing "o" }}` + identityGroupPersonRoles + `
    ) hr
    ORDER BY hr.Role
  ) AS Roles,`

	// identityTenantThroughDirectory is the start of the fact that a person of a tenant's
	// list signs in through a tenant's directory, where the tenant record declares a
	// directory and the auth keeps identities: an identity linked to the account under
	// the method and the connection of a row of the directories table, whose tenant the
	// fact goes on to compare with the row's (identityTenantThroughFacts). The person's
	// identities are read by the identities table's index on the user, which stores the
	// method and the connection, and a directory by its table's index on the two. It is
	// written with EXISTS alone and joins no table: Spanner refuses a statement of more
	// than twenty joins of tables, and the list of a tenant's people holds eighteen where
	// the record declares the directory-only switch and the auth has the password method
	// and keeps groups (identityGroupPersonHere). A page reads the list with the methods
	// each row offers, and for a session that does not enter some tenant the statement
	// adds the query of the tenants a person administers (TenantsToEnter), two joins
	// more where the auth keeps groups: twenty.
	identityTenantThroughDirectory = `EXISTS (
    SELECT 1 FROM {{ .Tables.Identities }} li
    WHERE li.UserId = u.Id AND EXISTS (
      SELECT 1 FROM {{ .TenantDirectory.Table }} ld
      WHERE ld.{{ .TenantDirectory.MethodColumn }} = li.Method AND ld.{{ .TenantDirectory.ConnectionColumn }} = li.Connection AND ld.{{ .TenantDirectory.TenantColumn }} `

	// identityTenantThroughFacts are the two facts of whose directory a person of a
	// tenant's list signs in through, as the last columns of the select the list's facts
	// of who manages the account are read over (identityTenantManagedFacts), where the
	// tenant record declares a directory and the auth keeps identities: ThroughHere, the
	// person signs in through the row's tenant's directory, and ThroughAnother, through
	// another tenant's. They are no columns of the list itself.
	identityTenantThroughFacts = `{{ if and .TenantDirectory .HasIdentities }},
  ` + identityTenantThroughDirectory + `= t.DomainId
    )
  ) AS ThroughHere,
  ` + identityTenantThroughDirectory + `!= t.DomainId
    )
  ) AS ThroughAnother{{ end }}`

	// identityTenantManagedFacts are the two facts of who manages the account of a person
	// of a tenant's list, ManagedHere and ManagedByAnotherDirectory, as the first columns
	// after the others of the select they are read over: one party manages each account,
	// the first that fits of the tenant whose directory the person signs in through, the
	// one tenant the person holds something in and nothing anywhere else, and the
	// application's own administrators. They read the columns of where the person holds
	// roles (HeldHere, HeldElsewhere, HeldGlobally) and of whose directory they sign in
	// through (identityTenantThroughFacts), so each of those checks is written once and
	// read once for a row. Where the tenant record declares no directory, or the auth
	// keeps no identities, nobody signs in through a tenant's directory: the tenant
	// manages the account of the person it holds alone, and no other tenant's directory
	// manages anyone's.
	identityTenantManagedFacts = `{{ if and .TenantDirectory .HasIdentities }}(ThroughHere OR (NOT ThroughAnother AND ` + identityTenantHeldAlone + `)) AS ManagedHere,
  (ThroughAnother AND NOT ThroughHere) AS ManagedByAnotherDirectory{{ else }}(` + identityTenantHeldAlone + `) AS ManagedHere,
  FALSE AS ManagedByAnotherDirectory{{ end }}`

	// identityTenantHeldAlone is the fact that a person of a tenant's list holds
	// something in the tenant and nothing anywhere else, over the columns of where the
	// person holds roles.
	identityTenantHeldAlone = `HeldHere AND NOT HeldElsewhere AND NOT HeldGlobally`

	// identityTenantDisplayName is the name a person of a tenant's list is shown by, over
	// the account u: their name, or their username where they have none. The list's query
	// and its name query both write it, so a person's name on the list and beside an id
	// that points to them cannot differ.
	identityTenantDisplayName = `COALESCE(NULLIF(u.Name, ''), u.Username)`

	// identityManagingTenant is who manages an account as the whole application's lists
	// say it, as one column of a select over the account read under u: ManagingTenant, the
	// key of the tenant whose list of its people says ManagedHere on the person's row, the
	// lower key where two lists say it, and NULL where none does. ManagedHere is read as
	// the tenant's list reads it, by the same constants over the same columns, so the two
	// always agree; each list that carries the column says ManagedByTenant beside it, as
	// ManagingTenant IS NOT NULL.
	//
	// The tenants asked about are the ones in whose list the person is a row by something
	// they hold there themselves now (a membership in the tenant, a place in one of its
	// own groups, a role of their own on one of its records, each inside its period:
	// identitySourceData.Counting, as the list reads who its people are) and the ones whose
	// directory the person signs in through. A row a tenant's list holds only through a
	// role held in every tenant, or through a group kept for the whole application, says
	// ManagedHere through the tenant's directory alone, so those tenants are found by the
	// directory. Each is found through an index by user (the memberships', the group
	// members', the roles on records', the identities'): no row reads the tenant record's
	// table or the memberships table as a whole. A group's tenant and a
	// directory's are read as an array unnested beside the place's or the identity's row,
	// which Spanner counts as no join (identityTenantDirectoryPeople): the column adds to
	// the joins of the statement only the two of identityGroupPersonElsewhere and
	// identityGroupPersonGlobally, where the auth keeps groups.
	identityManagingTenant = `  (SELECT MIN(mt.DomainId) FROM (
    SELECT DomainId, ` + identityTenantManagedFacts + `
    FROM (
      SELECT
        t.DomainId AS DomainId,
` + identityTenantPersonFacts + identityTenantPersonHeldHere + identityTenantThroughFacts + `
      FROM (
        SELECT mm.DomainId AS DomainId
        FROM {{ .Policy.UserRoles }} mm
        WHERE mm.UserId = u.Id AND mm.Kind = {{ .KindDomain }} AND mm.Axis = {{ .ScopeAxis }}
          AND {{ .Counting "mm" }}
{{- if .KeepsGroups }}
        UNION DISTINCT
        SELECT mg AS DomainId
        FROM {{ .Policy.GroupMembers }} mp, UNNEST(ARRAY(
          SELECT g.DomainId FROM {{ .Policy.Groups }} g
          WHERE g.Id = mp.GroupId AND g.Kind = {{ .KindDomain }} AND g.Axis = {{ .ScopeAxis }}
        )) AS mg
        WHERE mp.UserId = u.Id AND {{ .Counting "mp" }}
{{- end }}
{{- if .HoldsRecordRoles }}
        UNION DISTINCT
        SELECT mr.DomainId AS DomainId
        FROM {{ .Policy.RecordUserRoles }} mr
        WHERE mr.UserId = u.Id AND mr.Axis = {{ .ScopeAxis }}
          AND {{ .Counting "mr" }}
{{- end }}
{{- if and .TenantDirectory .HasIdentities }}
        UNION DISTINCT
        SELECT md AS DomainId
        FROM {{ .Tables.Identities }} mi, UNNEST(ARRAY(
          SELECT d.{{ .TenantDirectory.TenantColumn }} FROM {{ .TenantDirectory.Table }} d
          WHERE d.{{ .TenantDirectory.MethodColumn }} = mi.Method AND d.{{ .TenantDirectory.ConnectionColumn }} = mi.Connection
        )) AS md
        WHERE mi.UserId = u.Id
{{- end }}
      ) t
    )
  ) mt
  WHERE mt.ManagedHere) AS ManagingTenant`

	// identityTenantPeopleFrom selects the people of every tenant, t the tenant and the
	// name and u the account: who holds a role in a tenant (one tenant's memberships, and
	// the memberships held in every tenant once per row of the tenant record's table),
	// joined to the account. A membership names its person by the id of their account,
	// and the join is on the users table's key: a page reads one row of the table for
	// each person. The memberships held in every tenant are read by the
	// table's key (the hint), where they are one short range; left to choose, the database
	// ranged over the index by user when a page continued from a cursor. Where the auth
	// keeps groups, the people a tenant holds through one join them
	// (identityTenantGroupPeople), and where it keeps the roles held on a record, the people
	// who hold one on a record of the tenant (identityTenantRecordPeople).
	//
	// A person is one of a tenant's people by what they hold at the moment: a membership
	// outside its period, ended or not yet begun, puts nobody in the list, whether or not
	// its row still stands in the table (identitySourceData.Counting). Where the tenant
	// record declares a directory and the auth keeps identities, a person who signs in
	// through a tenant's directory is one of its people whatever they hold
	// (identityTenantDirectoryPeople).
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
    AND {{ .Counting "m" }}` + identityTenantGroupPeople + identityTenantRecordPeople + identityTenantDirectoryPeople + `
) t
JOIN {{ .Tables.Users }} u ON u.Id = t.UserId`

	// identityTenantRecordPeople are the people a tenant's list holds because they hold a
	// role on one of the tenant's records, as a branch of the union the people are selected
	// from (identityTenantPeopleFrom), where the auth keeps the roles held on a record: the
	// rows of the people's table, each under its record's tenant. A role on a record counts
	// inside its period alone, as a membership does (identitySourceData.Counting). A person
	// who holds one through a group of the tenant is a member of the group, whom the group's
	// branch lists already. The branch reads one table and joins none.
	identityTenantRecordPeople = `{{- if .HoldsRecordRoles }}
  UNION DISTINCT
  SELECT rr.DomainId AS DomainId, rr.UserId AS UserId
  FROM {{ .Policy.RecordUserRoles }} rr
  WHERE rr.Axis = {{ .ScopeAxis }}
    AND {{ .Counting "rr" }}
{{- end }}`

	// identityTenantRecordsHeldOn is the records of the tenant a person of its list of
	// people holds a role on, as a column of the list's select after the roles, where the
	// auth keeps the roles held on a record: RecordsHeldOn, each record once by its
	// resource and its key, in that order. A role that has not ended counts, one that has
	// not begun among them, as the roles column reads a role (identityTenantPersonRoles):
	// the person's own, read by the people's table's key, and, where the auth keeps groups,
	// the ones a group the person has a place in holds there, read as an array unnested
	// beside the place's row, which Spanner counts as no join. The list's own facts are
	// read over the column (identityTenantRecordFacts), which is no column of the list.
	identityTenantRecordsHeldOn = `{{ if .HoldsRecordRoles }}
  ARRAY(
    SELECT AS STRUCT hr.Resource AS Resource, hr.RecordId AS RecordId
    FROM (
      SELECT o.Resource AS Resource, o.RecordId AS RecordId
      FROM {{ .Policy.RecordUserRoles }} o
      WHERE o.UserId = u.Id AND o.Axis = {{ .ScopeAxis }} AND o.DomainId = t.DomainId
        AND {{ .Standing "o" }}
{{- if .KeepsRecordGroupRoles }}
      UNION ALL
      SELECT gr.Resource AS Resource, gr.RecordId AS RecordId
      FROM {{ .Policy.GroupMembers }} gm, UNNEST(ARRAY(
        SELECT AS STRUCT r.Resource AS Resource, r.RecordId AS RecordId
        FROM {{ .Policy.RecordGroupRoles }} r
        WHERE r.GroupId = gm.GroupId AND r.Axis = {{ .ScopeAxis }} AND r.DomainId = t.DomainId
          AND {{ .Standing "r" }}
      )) AS gr
      WHERE gm.UserId = u.Id AND {{ .Standing "gm" }}
{{- end }}
    ) hr
    GROUP BY hr.Resource, hr.RecordId
    ORDER BY hr.Resource, hr.RecordId
  ) AS RecordsHeldOn,{{ end }}`

	// identityTenantRecordFacts are the three facts of the records a person of a tenant's
	// list holds a role on, as the last columns of the list, read over RecordsHeldOn
	// (identityTenantRecordsHeldOn): RecordsHeld, how many; RecordsHeldKind, their resource
	// where they are all of one kind, and empty otherwise; RecordHeldName, the record's name
	// where there is one record and its kind declares a name (@name), read by the record's
	// table's key, and empty otherwise.
	identityTenantRecordFacts = `{{ if .HoldsRecordRoles }},
  ARRAY_LENGTH(RecordsHeldOn) AS RecordsHeld,
  IF((SELECT COUNT(DISTINCT h.Resource) FROM UNNEST(RecordsHeldOn) h) = 1, RecordsHeldOn[SAFE_OFFSET(0)].Resource, '') AS RecordsHeldKind,
  {{ with .RecordNames }}IF(ARRAY_LENGTH(RecordsHeldOn) = 1, CASE RecordsHeldOn[SAFE_OFFSET(0)].Resource
{{- range . }}
    WHEN {{ .Resource }} THEN COALESCE((SELECT n.{{ .Name }} FROM {{ .Table }} n WHERE n.{{ .Key }} = RecordsHeldOn[SAFE_OFFSET(0)].RecordId), '')
{{- end }}
    ELSE '' END, ''){{ else }}''{{ end }} AS RecordHeldName{{ end }}`

	// identityTenantDirectoryPeople are the people a tenant's list holds because they
	// sign in through the tenant's directory, as a branch of the union the people are
	// selected from (identityTenantPeopleFrom), after the group branches, where the
	// tenant record declares a directory and the auth keeps identities: every account
	// with an identity under the method and the connection of a row of the directories
	// table, the directory's tenant as the row's tenant. The tenant that manages the
	// account so finds it, whether or not the person holds anything there. The
	// identities are read by the table's index on the method, the connection and the
	// subject, as the array of a directory's accounts unnested beside the directory's
	// row: Spanner counts that as no join, and a join here would bring the statement a
	// page reads the list by past the twenty it allows (identityTenantThroughDirectory).
	identityTenantDirectoryPeople = `{{- if and .TenantDirectory .HasIdentities }}
  UNION DISTINCT
  SELECT dr.{{ .TenantDirectory.TenantColumn }} AS DomainId, du AS UserId
  FROM {{ .TenantDirectory.Table }} dr, UNNEST(ARRAY(
    SELECT dl.UserId FROM {{ .Tables.Identities }} dl
    WHERE dl.Method = dr.{{ .TenantDirectory.MethodColumn }} AND dl.Connection = dr.{{ .TenantDirectory.ConnectionColumn }}
  )) AS du
{{- end }}`

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
	// person's, so the list reads the password alone (identityTenantAdministratorWithPassword).
	identityTenantOutsideDirectory = `td.{{ .TenantDirectory.TenantColumn }} IS NOT NULL{{ if .HasIdentities }} AND NOT EXISTS (
    SELECT 1 FROM {{ .Tables.Identities }} di
    WHERE di.UserId = u.Id AND di.Method = td.{{ .TenantDirectory.MethodColumn }} AND di.Connection = td.{{ .TenantDirectory.ConnectionColumn }}
  ){{ end }} AND NOT EXISTS (
    SELECT 1 FROM {{ .Policy.UserRoles }} sa
    WHERE sa.UserId = u.Id AND sa.Kind = {{ .KindGlobal }} AND sa.Role = {{ .SuperAdministratorRole }}
      AND {{ .Counting "sa" }}
  ){{ if .AdministratorsEnter }} AND NOT (` + identityTenantAdministratorWithPassword + `){{ end }}`

	// identityTenantAdministratorWithPassword is the fact that a person of a tenant's list
	// is one of the tenant's own administrators and has a password, as a condition of the
	// list's select, where the auth has a password method (AdministratorsEnter): t is the
	// person's membership row and u their account. The person holds, inside its period, one
	// of the roles the auth's permission engine names as a tenant's administrator's
	// (@{{ .TenantAdministratorsParam }}) in the tenant itself, by a membership of their own
	// or through a group kept in that tenant, and the account holds a password. It is the
	// one way into a directory-only tenant beside its directory: the people the switch keeps
	// out leave such a person out (identityTenantOutsideDirectory), and the list says who
	// they are (AdministratorWithPassword), both from this one condition.
	identityTenantAdministratorWithPassword = `u.PasswordHash IS NOT NULL AND (EXISTS (
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
  ){{ end }})`

	// identityTenantDirectoryFacts are the columns a tenant's list of its people carries
	// where the tenant record declares the directory-only switch, after the person's
	// facts: whether the person has no sign-in through the tenant's directory, and, while
	// the tenant is directory only, the sentence that says so; and, where the auth has a
	// password method, whether the person is one of the tenant's own administrators who
	// has a password, and so keeps a way in past the directory. The sentence
	// speaks of the tenant's directory and does not name it: a directory's connection
	// is a key, for a Microsoft Entra tenant an id nobody knows, and what a person
	// calls the directory is said by the sign-in registrations, which a query does not
	// read. The list is one tenant's, and a tenant has one directory, the one its
	// Sign-in page shows.
	identityTenantDirectoryFacts = `,
  (` + identityTenantOutsideDirectory + `) AS OutsideDirectory,
  IF(ts.{{ .TenantDirectory.OnlyColumn }} AND ` + identityTenantOutsideDirectory + `,
    'Has not signed in through this {{ .TenantWordLiteral }}\'s directory, so cannot enter while this {{ .TenantWordLiteral }} is directory only',
    NULL) AS CannotEnter{{ if .AdministratorsEnter }},
  (` + identityTenantAdministratorWithPassword + `) AS AdministratorWithPassword{{ end }}`

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
	"github.com/impulseframework/impulse/resource"
)

type (
	// {{ .TenantUser }} is one person of the {{ .Auth }} auth as one {{ .TenantName }} sees
	// them: a row for each person who holds a role in the tenant or in every tenant{{ if and .TenantDirectory .HasIdentities }},
	// and for each person who signs in through the tenant's directory{{ end }}, with
	// the columns of {{ .User }}. A person is one account for the whole application, so
	// the same person is a row in each tenant they hold a role in: the account's id is the
	// key of a row within its tenant, and DomainId, the tenant, holds the row to it.
	//
	// A role counts inside its period alone: a person whose only role in the tenant has
	// ended, or has not begun, is not one of its people, whether or not the role's row
	// still stands. {{ .TenantRole }} lists a role that has not begun, with its period.
	//
	// Six columns are facts about the person that no table holds, each under the name a
	// grant's condition reads. HeldElsewhere (heldElsewhere) says the person also holds a
	// role outside this tenant's own: in another tenant,{{ if .HoldsRecordRoles }} on a record of another tenant,{{ end }} or in every tenant.
	// HeldGlobally (heldGlobally) says the person holds a role for the whole application.
	// HeldHere (heldHere) says the person holds something in this tenant itself, a role of
	// their own{{ if .HoldsRecordRoles }}, a place in one of its groups or a role on one of its records{{ else }} or a place in one of its groups{{ end }}: a person listed only through a role held in every
	// tenant, or through a group kept for the whole application, holds nothing here, and
	// the removal from the tenant has nothing of theirs to take. HeldInEveryTenant
	// (heldInEveryTenant) says the person holds a role in every tenant at once, which is
	// how the application's own staff come to be among each tenant's people.
	// ManagedHere (managedHere) says this tenant manages the person's account: the
	// person signs in through the tenant's directory, or signs in through no tenant's
	// directory and holds something in this tenant and nothing anywhere else.
	// ManagedByAnotherDirectory (managedByAnotherDirectory) says the person signs in
	// through another tenant's directory, and that tenant manages the account. An account
	// no tenant manages is the application's own administrators' to manage. A role file
	// uses the facts to say what a tenant may see and do of a person whose account it
	// does not manage. The list filters and sorts by the username and by the six facts;
	// the account's other columns are no filter keys, so a role file may grant any of
	// them under a condition at no cost to a page. DisplayName is what a list that points
	// to a person shows them by: their name, or their username when they have none. It is
	// the view's name (@name), so a field of another resource that holds a person's id
	// gets a name field read from it.
	//
	// Roles (roles) is what the person holds in this tenant itself: the name of each role
	// once, in alphabetical order, of the roles {{ .TenantHeldRole }} lists for the person
	// here, their own and the ones that come through a group. A role held in every tenant at
	// once is not among them: HeldInEveryTenant says it, and a role file grants or keeps back
	// each by itself. It is no filter key.
	{{- if .HoldsRecordRoles }}
	//
	// A person who holds a role on one of the tenant's records is one of its people, whether
	// or not they hold anything else there. RecordsHeld (recordsHeld) is how many of the
	// tenant's records the person holds a role on that has not ended, their own or through a
	// group; RecordsHeldKind (recordsHeldKind) is the records' resource where they are all of
	// one kind, and empty otherwise; RecordHeldName (recordHeldName) is the record's name
	// where the person holds a role on one record and its kind declares a name (@name), and
	// empty otherwise. None of the three is a filter key. A role on one of the tenant's
	// records is something held in the tenant: HeldHere says it, the removal from the tenant
	// takes it, and the tenant manages the account of a person who holds nothing anywhere
	// else (ManagedHere). A role on a record of another tenant is held elsewhere
	// (HeldElsewhere).
	{{- end }}
	{{- if .CountsFailedSignIns }}
	//
	// LockedUntil (lockedUntil) is until when the person's sign-in is locked by failed
	// tries, and nothing for a person who is not locked: the latest lock still ahead among
	// the counts the session library keeps for the account under its username and under its
	// id (session.AccountAttemptKeys with no address). It is no filter key either.
	{{- end }}
	{{- if .TenantDirectory }}
	//
	// Two more columns say what the tenant's directory-only switch does to the person.
	// OutsideDirectory says the person has no sign-in through the tenant's directory, so
	// the switch keeps them out while it is on: it is who turning the switch on would
	// shut out, asked before it is on. CannotEnter is the sentence the list shows while
	// the tenant is directory only, on the row of a person it keeps out ("Has not signed
	// in through this {{ .TenantWord }}'s directory, so cannot enter while this {{ .TenantWord }} is
	// directory only"), and nothing on every other row: the sentence never says the
	// directory's connection, which is a key and no name. A person the switch keeps out
	// stays in the list with their roles, and a person who signs in through the tenant's
	// directory is a row whether or not they hold anything in the tenant (HeldHere says
	// which), so the tenant that manages the account finds it. A super administrator is
	// outside no tenant's directory.
	{{- if .AdministratorsEnter }} Neither is the
	// {{ .TenantWord }}'s own administrator who has a password: a person who holds one of the roles
	// the application names as a {{ .TenantWord }}'s administrator's in the {{ .TenantWord }} itself enters it
	// with the password and a code mailed to them, so the switch keeps them out of nothing
	// (Set{{ .TenantUser }}AdministratorRoles names the roles).
	//
	// AdministratorWithPassword says the person is one of the {{ .TenantWord }}'s own administrators
	// and has a password, so they enter the {{ .TenantWord }} with it and a code mailed to them while
	// the {{ .TenantWord }} is directory only: the warning of the method that turns the switch on
	// counts and names them.
	{{- end }}
	{{- end }}
	//
	// @virtual
	// @permissionScope(domain)
	// @outlet({{ .Outlets }})
	// @order(Username asc)
	// @page(default: 25, max: 200)
	// @rowWord(person, plural: people)
	{{ .TenantUser }} struct {
		// @primarykey
		ID impulse.UUID ` + "`spanner:\"Id\" index:\"true\"`" + `
		// @domain
		DomainID string  ` + "`spanner:\"DomainId\" index:\"true\"`" + `
		Username string  ` + "`spanner:\"Username\" index:\"true\"`" + `
		Name     *string ` + "`spanner:\"Name\"`" + `
		// @name
		DisplayName            string     ` + "`spanner:\"DisplayName\" allow_filter:\"true\"`" + `
		Email                  *string    ` + "`spanner:\"Email\"`" + `
		EmailVerifiedAt        *time.Time ` + "`spanner:\"EmailVerifiedAt\"`" + `
		Disabled               bool       ` + "`spanner:\"Disabled\"`" + `
		PasswordChangeRequired bool       ` + "`spanner:\"PasswordChangeRequired\"`" + `
		CreatedAt              time.Time  ` + "`spanner:\"CreatedAt\"`" + `
		LastSignInAt           *time.Time ` + "`spanner:\"LastSignInAt\"`" + `
		// @roleOf({{ .Auth }})
		Roles []string ` + "`spanner:\"Roles\"`" + `
		{{- if .HoldsRecordRoles }}
		RecordsHeld     int64  ` + "`spanner:\"RecordsHeld\"`" + `
		RecordsHeldKind string ` + "`spanner:\"RecordsHeldKind\"`" + `
		RecordHeldName  string ` + "`spanner:\"RecordHeldName\"`" + `
		{{- end }}
		{{- if .CountsFailedSignIns }}
		LockedUntil *time.Time ` + "`spanner:\"LockedUntil\"`" + `
		{{- end }}
		// @attribute(heldElsewhere)
		HeldElsewhere bool ` + "`spanner:\"HeldElsewhere\" index:\"true\"`" + `
		// @attribute(heldGlobally)
		HeldGlobally bool ` + "`spanner:\"HeldGlobally\" index:\"true\"`" + `
		// @attribute(heldHere)
		HeldHere bool ` + "`spanner:\"HeldHere\" index:\"true\"`" + `
		// @attribute(heldInEveryTenant)
		HeldInEveryTenant bool ` + "`spanner:\"HeldInEveryTenant\" index:\"true\"`" + `
		// @attribute(managedHere)
		ManagedHere bool ` + "`spanner:\"ManagedHere\" index:\"true\"`" + `
		// @attribute(managedByAnotherDirectory)
		ManagedByAnotherDirectory bool ` + "`spanner:\"ManagedByAnotherDirectory\" index:\"true\"`" + `
		{{- if .TenantDirectory }}
		OutsideDirectory bool    ` + "`spanner:\"OutsideDirectory\" index:\"true\"`" + `
		CannotEnter      *string ` + "`spanner:\"CannotEnter\"`" + `
		{{- if .AdministratorsEnter }}
		AdministratorWithPassword bool ` + "`spanner:\"AdministratorWithPassword\" index:\"true\"`" + `
		{{- end }}
		{{- end }}
	}
)

// {{ GoCamel .TenantUser }}Query selects the people of every tenant: who holds a role in a
// tenant ({{ .Policy.UserRoles }}, one tenant's memberships, and the memberships held in
// every tenant once per row of {{ .TenantTable }}){{ if .HoldsRecordRoles }}, who holds a role on one of its records
// ({{ .Policy.RecordUserRoles }}){{ end }}{{ if and .TenantDirectory .HasIdentities }}, and who signs in through a tenant's
// directory ({{ .TenantDirectory.Table }} and {{ .Tables.Identities }}){{ end }}, joined to the account by the id a
// membership names its person by. Who manages each account is read over the columns of
// that select, so where the person holds roles is read once for a row.
const {{ GoCamel .TenantUser }}Query = ` + "`" + `
SELECT
  *{{ .PeopleLeftOut }},
  ` + identityTenantManagedFacts + identityTenantRecordFacts + `
FROM (
SELECT
  u.Id AS Id,
  t.DomainId AS DomainId,
  u.Username AS Username,
  u.Name AS Name,
  ` + identityTenantDisplayName + ` AS DisplayName,
  u.Email AS Email,
  u.EmailVerifiedAt AS EmailVerifiedAt,
  u.Disabled AS Disabled,
  u.PasswordChangeRequired AS PasswordChangeRequired,
  u.CreatedAt AS CreatedAt,
  u.LastSignInAt AS LastSignInAt,
` + identityTenantPersonRoles + identityTenantRecordsHeldOn + `{{ if .CountsFailedSignIns }}
{{ .LockedUntil "u" }},{{ end }}
` + identityTenantPersonFacts + identityTenantPersonHeldHere + identityTenantPersonInEveryTenant + identityTenantThroughFacts + `{{ if .TenantDirectory }}` + identityTenantDirectoryFacts + `{{ end }}
` + identityTenantPeopleFrom + `{{ if .TenantDirectory }}` + identityTenantDirectoryJoins + `{{ end }}
) p
` + "`" + `

// {{ GoCamel .TenantUser }}NameQuery selects, for every {{ .TenantWord }}, its people's ids and the name each is
// shown by: the rows of {{ GoCamel .TenantUser }}Query, one for one, from the same people and their
// accounts, without the facts of where a person holds roles and who manages the account. A list
// that shows a person by name reads the name from it (NameSubquery): a name's query is written
// into the statement of the list that shows it, the facts' joins are the larger part of the
// list's own query, and Spanner refuses a statement of more than twenty joins.
const {{ GoCamel .TenantUser }}NameQuery = ` + "`" + `
SELECT
  u.Id AS Id,
  t.DomainId AS DomainId,
  ` + identityTenantDisplayName + ` AS DisplayName
` + identityTenantPeopleFrom + `
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

// NameSubquery is the query a list that points to a person reads the person's name from
// (resource.NameSubqueryer): the person's id, the {{ .TenantWord }} and DisplayName.
func ({{ .TenantUser }}) NameSubquery() (query string, params map[string]any) {
	return {{ GoCamel .TenantUser }}NameQuery, nil
}

var _ resource.NameSubqueryer = {{ .TenantUser }}{}
{{- if .OffersToEntering }}

` + identityTenantsToEnterDoc + `
func ({{ .TenantUser }}) TenantsToEnter() resource.TenantsToEnter {
	return {{ GoCamel .User }}TenantsToEnter("Id")
}
{{- end }}
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
	// @rowWord(held role)
	{{ .TenantRole }} struct {
		// @domain
		DomainID string ` + "`spanner:\"DomainId\" index:\"true\"`" + `
		// @primarykey
		UserID   impulse.UUID ` + "`spanner:\"UserId\" index:\"true\"`" + `
		Username string       ` + "`spanner:\"Username\" index:\"true\"`" + `
		// @primarykey
		// @attribute(role)
		// @roleOf({{ .Auth }})
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
	// there, and Name the name its sender gave the person, where they gave one. State
	// says what became of the invitation (open, accepted, withdrawn, expired,
	// notMailed), and SentBy who sent it. An invitation that makes a tenant names no
	// tenant, so it is never a row of this list. The link itself is in the message alone
	// and its hash is not a column.
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
	// @rowWord(invitation)
	{{ .TenantMailLink }} struct {
		// @primarykey
		ID impulse.UUID ` + "`spanner:\"Id\" index:\"true\"`" + `
		// @domain
		DomainID   string        ` + "`spanner:\"DomainId\" index:\"true\"`" + `
		Email      string        ` + "`spanner:\"Email\" index:\"true\"`" + `
		Name       *string       ` + "`spanner:\"Name\"`" + `
		// @attribute(role)
		// @roleOf({{ .Auth }})
		RoleNames  []string      ` + "`spanner:\"RoleNames\"`" + `
		InvitedByUserID  *impulse.UUID ` + "`spanner:\"InvitedByUserId\"`" + `
		SentBy     *string       ` + "`spanner:\"SentBy\"`" + `
		CreatedAt  time.Time     ` + "`spanner:\"CreatedAt\" index:\"true\"`" + `
		ExpiresAt  time.Time     ` + "`spanner:\"ExpiresAt\"`" + `
		ConsumedAt *time.Time    ` + "`spanner:\"ConsumedAt\"`" + `
		WithdrawnAt *time.Time   ` + "`spanner:\"WithdrawnAt\"`" + `
		State      string        ` + "`spanner:\"State\" index:\"true\"`" + `
		// @attribute(pending)
		Pending bool ` + "`spanner:\"Pending\" index:\"true\"`" + `
	}
)

// {{ GoCamel .TenantMailLink }}Query selects the invitations of every tenant: the mail
// links of the invitation kind that name one. The table's index by tenant holds them
// newest first, with every column of the table the list reads, so a page reads its own
// rows from the index and no row of the table; the sender's name is read from the users
// table, by the sender's id.
const {{ GoCamel .TenantMailLink }}Query = ` + "`" + `
SELECT
  l.Id AS Id,
  l.TenantId AS DomainId,
  l.Email AS Email,
  l.Name AS Name,
  l.RoleNames AS RoleNames,
  l.InvitedByUserId AS InvitedByUserId,
{{- if .Has "users" }}
  COALESCE(NULLIF(iu.Name, ''), iu.Username) AS SentBy,
{{- else }}
  CAST(NULL AS STRING) AS SentBy,
{{- end }}
  l.CreatedAt AS CreatedAt,
  l.ExpiresAt AS ExpiresAt,
  l.ConsumedAt AS ConsumedAt,
  l.WithdrawnAt AS WithdrawnAt,
{{ .MailLinkState "l" }},
  (l.ConsumedAt IS NULL AND l.ExpiresAt > CURRENT_TIMESTAMP()) AS Pending
FROM {{ .Tables.MailLinks }} l
{{- if .Has "users" }}
LEFT JOIN {{ .Tables.Users }} iu ON iu.Id = l.InvitedByUserId
{{- end }}
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
	{{- if .OffersToEntering }}
	"github.com/impulseframework/impulse/resource"
	{{- end }}
)

type (
	// {{ .TenantIdentity }} is one way a person of one {{ .TenantName }} signs in to the
	// {{ .Auth }} auth through a directory: a row for each identity of a person of
	// {{ .TenantUser }}, with the columns of {{ .Identity }} and the
	// person's username. An account is one row for the whole application, so the identity
	// of a person who holds a role in two tenants is a row in each: the identity's id is
	// the key of a row within its tenant, and DomainId, the tenant, holds the row to it.
	// Tenant is the link's own column, the organization its directory vouched for, and is
	// not the row's tenant.
	//
	// HeldElsewhere, HeldGlobally, ManagedHere and ManagedByAnotherDirectory are four
	// facts of the person's row in {{ .TenantUser }}, under the same names (heldElsewhere,
	// heldGlobally, managedHere, managedByAnotherDirectory): an identity is the account's,
	// so what a tenant may see and do of one is what it may of the account.
	//
	// @virtual
	// @permissionScope(domain)
	// @outlet({{ .Outlets }})
	// @order(CreatedAt desc)
	// @page(default: 25, max: 200)
	// @rowWord(sign-in identity)
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
		// @attribute(managedHere)
		ManagedHere bool ` + "`spanner:\"ManagedHere\" index:\"true\"`" + `
		// @attribute(managedByAnotherDirectory)
		ManagedByAnotherDirectory bool ` + "`spanner:\"ManagedByAnotherDirectory\" index:\"true\"`" + `
	}
)

// {{ GoCamel .TenantIdentity }}Query selects the identities of the people of every tenant:
// the people as {{ .TenantUser }} selects them, each joined to the rows of
// {{ .Tables.Identities }} that link to the account, through the table's index on the
// user, which carries the columns the list shows. Who manages the account is read as
// {{ .TenantUser }} reads it, over the columns of that select.
const {{ GoCamel .TenantIdentity }}Query = ` + "`" + `
SELECT
  * EXCEPT (HeldHere{{ if and .TenantDirectory .HasIdentities }}, ThroughHere, ThroughAnother{{ end }}),
  ` + identityTenantManagedFacts + `
FROM (
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
` + identityTenantPersonFacts + identityTenantPersonHeldHere + identityTenantThroughFacts + `
` + identityTenantPeopleFrom + `
JOIN {{ .Tables.Identities }} i ON i.UserId = u.Id
) p
` + "`" + `

// Subquery is the query the rows are read from.
func ({{ .TenantIdentity }}) Subquery() (query string, params map[string]any) {
	return {{ GoCamel .TenantIdentity }}Query, nil
}
{{- if .OffersToEntering }}

` + identityTenantsToEnterDoc + `
func ({{ .TenantIdentity }}) TenantsToEnter() resource.TenantsToEnter {
	return {{ GoCamel .User }}TenantsToEnter("UserId")
}
{{- end }}
`
)
