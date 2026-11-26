package generation

// The store's read of the people a tenant's delete considers (identity.Store.TenantHoldings).

const (
	// identityTenantHoldingsStoreTemplate is the store's TenantHoldings: one statement from
	// the tenant's key, whatever the tenant holds (TenantHoldingsSQL), and a second over the
	// people it found (TenantHoldingsSessionsSQL), read in the caller's transaction, and a
	// stub that answers nobody where the store keeps no table a person holds something in
	// a tenant by.
	identityTenantHoldingsStoreTemplate = `{{ if .TenantHoldingsSQL }}
{{- if .Has "memberships" }}
// tenantHoldingsMembership is one membership of a person as TenantHoldings reads it.
type tenantHoldingsMembership struct {
	Kind     string ` + "`spanner:\"Kind\"`" + `
	Axis     string ` + "`spanner:\"Axis\"`" + `
	DomainID string ` + "`spanner:\"DomainId\"`" + `
	Role     string ` + "`spanner:\"Role\"`" + `
}
{{ end }}
{{- if .KeepsGroups }}
// tenantHoldingsPlace is one place of a person in a group, with where the group is kept,
// as TenantHoldings reads it.
type tenantHoldingsPlace struct {
	ID       string ` + "`spanner:\"Id\"`" + `
	Kind     string ` + "`spanner:\"Kind\"`" + `
	Axis     string ` + "`spanner:\"Axis\"`" + `
	DomainID string ` + "`spanner:\"DomainId\"`" + `
}
{{ end }}
{{- if .HoldsRecordRoles }}
// tenantHoldingsRecordRole is one role of a person on a record, with its end, as
// TenantHoldings reads it.
type tenantHoldingsRecordRole struct {
	Axis     string           ` + "`spanner:\"Axis\"`" + `
	DomainID string           ` + "`spanner:\"DomainId\"`" + `
	Resource string           ` + "`spanner:\"Resource\"`" + `
	RecordID string           ` + "`spanner:\"RecordId\"`" + `
	Role     string           ` + "`spanner:\"Role\"`" + `
	EndsAt   spanner.NullTime ` + "`spanner:\"EndsAt\"`" + `
}
{{ end }}
{{- if .HasIdentities }}
// tenantHoldingsLink is one identity link of a person as TenantHoldings reads it{{ if .TenantDirectory }}, with
// the tenants whose directory it came through{{ end }}.
type tenantHoldingsLink struct {
	ID         string   ` + "`spanner:\"Id\"`" + `
	Method     string   ` + "`spanner:\"Method\"`" + `
	Connection string   ` + "`spanner:\"Connection\"`" + `
	{{- if .TenantDirectory }}
	Tenants    []string ` + "`spanner:\"Tenants\"`" + `
	{{- end }}
}
{{ end }}
// tenantHoldingsSession is one session of a person that has not ended, as TenantHoldings
// reads it{{ if .Has "impersonations" }}, with whether its impersonation record stands{{ end }}.
type tenantHoldingsSession struct {
	ID     string ` + "`spanner:\"Id\"`" + `
	{{- if .Has "impersonations" }}
	Record bool   ` + "`spanner:\"Record\"`" + `
	{{- end }}
}
{{ if .Has "impersonations" }}
// tenantHoldingsHeld is one impersonation record a person holds as actor that has not
// ended, with whether its session stands, as TenantHoldings reads it.
type tenantHoldingsHeld struct {
	SessionID string ` + "`spanner:\"SessionId\"`" + `
	Session   bool   ` + "`spanner:\"Session\"`" + `
}
{{ end }}
// TenantHoldings reads the people a tenant's delete considers and what each holds, in
// the caller's transaction, by two statements: the first starts from the tenant's key and
// reaches each person's rows by their tables' indexes on the user, and the second reads,
// for the people the first found, their sessions, the impersonation records they hold and
// what the database removes with their rows. A row whose user is no account's id names
// nobody, and a role on a record whose period has ended is left out, as RecordMemberships
// leaves it (identity.Store.TenantHoldings).
func (store) TenantHoldings(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain) ([]identity.PersonHoldings, error) {
	stmt := spanner.Statement{SQL: {{ .TenantHoldingsSQL }}, Params: map[string]any{}}
	{{- if .Has "memberships" }}
	held := access.NewMembershipKey(accesstypes.DomainPolicyScope(domain), "", "")
	stmt.Params["kind"], stmt.Params["axis"], stmt.Params["domain"] = held.Kind, held.Axis, held.Domain
	{{- end }}
	{{- if .KeepsGroups }}
	kept := access.NewGroupRoleKey("", accesstypes.DomainPolicyScope(domain), "")
	stmt.Params["groupKind"], stmt.Params["groupAxis"], stmt.Params["groupDomain"] = kept.Kind, kept.Axis, kept.Domain
	{{- end }}
	{{- if .HoldsRecordRoles }}
	onRecords := access.NewRecordMembershipKey(accesstypes.NewRecordScope(domain, "", ""), "", "")
	stmt.Params["recordAxis"], stmt.Params["recordDomain"] = onRecords.Axis, onRecords.Domain
	now := time.Now()
	{{- end }}
	{{- if and .TenantDirectory .HasIdentities }}
	stmt.Params["tenant"] = string(domain)
	{{- end }}
	var people []identity.PersonHoldings
	if err := txn.SpannerReadOnlyTransaction().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		var user string
		var username spanner.NullString
		if err := row.ColumnByName("UserId", &user); err != nil {
			return errors.Wrap(err, "spanner.Row.ColumnByName()")
		}
		if err := row.ColumnByName("Username", &username); err != nil {
			return errors.Wrap(err, "spanner.Row.ColumnByName()")
		}
		id, ok := identity.UserID(accesstypes.User(user))
		if !ok {
			return nil
		}
		person := identity.PersonHoldings{ID: id, Username: username.StringVal, Found: username.Valid}
		{{- if .Has "memberships" }}
		var memberships []*tenantHoldingsMembership
		if err := row.ColumnByName("Memberships", &memberships); err != nil {
			return errors.Wrap(err, "spanner.Row.ColumnByName()")
		}
		for _, m := range memberships {
			person.Memberships = append(person.Memberships, access.MembershipKey{Kind: m.Kind, Axis: m.Axis, Domain: m.DomainID, Role: accesstypes.Role(m.Role), User: accesstypes.User(user)})
		}
		{{- end }}
		{{- if .KeepsGroups }}
		var places []*tenantHoldingsPlace
		if err := row.ColumnByName("Places", &places); err != nil {
			return errors.Wrap(err, "spanner.Row.ColumnByName()")
		}
		for _, place := range places {
			groupID, err := impulse.UUIDFromString(place.ID)
			if err != nil {
				return errors.Wrap(err, "impulse.UUIDFromString()")
			}
			person.Places = append(person.Places, identity.GroupPlace{ID: groupID, Kind: place.Kind, Axis: place.Axis, Domain: place.DomainID})
		}
		{{- end }}
		{{- if .HoldsRecordRoles }}
		var records []*tenantHoldingsRecordRole
		if err := row.ColumnByName("RecordRoles", &records); err != nil {
			return errors.Wrap(err, "spanner.Row.ColumnByName()")
		}
		for _, r := range records {
			if r.EndsAt.Valid && !r.EndsAt.Time.After(now) {
				continue
			}
			person.RecordRoles = append(person.RecordRoles, access.RecordMembershipKey{
				Axis: r.Axis, Domain: r.DomainID, Resource: r.Resource, Record: r.RecordID, Role: accesstypes.Role(r.Role), User: accesstypes.User(user),
			})
		}
		{{- end }}
		{{- if .HasIdentities }}
		var links []*tenantHoldingsLink
		if err := row.ColumnByName("Links", &links); err != nil {
			return errors.Wrap(err, "spanner.Row.ColumnByName()")
		}
		for _, link := range links {
			linkID, err := impulse.UUIDFromString(link.ID)
			if err != nil {
				return errors.Wrap(err, "impulse.UUIDFromString()")
			}
			person.Links = append(person.Links, identity.Link{ID: linkID, Method: sessioninfo.AuthMethod(link.Method), Connection: link.Connection})
			{{- if .TenantDirectory }}
			for _, tenant := range link.Tenants {
				if !slices.Contains(person.DirectoryTenants, accesstypes.Domain(tenant)) {
					person.DirectoryTenants = append(person.DirectoryTenants, accesstypes.Domain(tenant))
				}
			}
			{{- end }}
		}
		{{- if .TenantDirectory }}
		slices.Sort(person.DirectoryTenants)
		{{- end }}
		{{- end }}
		people = append(people, person)

		return nil
	}); err != nil {
		return nil, errors.Wrap(err, "spanner.RowIterator.Do()")
	}
	if len(people) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(people))
	at := make(map[string]int, len(people))
	for i := range people {
		ids = append(ids, people[i].ID.String())
		at[people[i].ID.String()] = i
	}
	{{- if .UserCascades }}
	costs := []int{
		{{- range .UserCascades }}
		resource.DeleteCost{IndexEntries: {{ .IndexEntries }}}.Mutations(0),
		{{- end }}
	}
	{{- end }}
	stmt = spanner.Statement{SQL: {{ .TenantHoldingsSessionsSQL }}, Params: map[string]any{"people": ids}}
	if err := txn.SpannerReadOnlyTransaction().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		var user string
		if err := row.ColumnByName("UserId", &user); err != nil {
			return errors.Wrap(err, "spanner.Row.ColumnByName()")
		}
		person := &people[at[user]]
		var sessions []*tenantHoldingsSession
		if err := row.ColumnByName("Sessions", &sessions); err != nil {
			return errors.Wrap(err, "spanner.Row.ColumnByName()")
		}
		for _, session := range sessions {
			sessionID, err := impulse.UUIDFromString(session.ID)
			if err != nil {
				return errors.Wrap(err, "impulse.UUIDFromString()")
			}
			person.Sessions = append(person.Sessions, identity.LiveSession{ID: sessionID{{ if .Has "impersonations" }}, Record: session.Record{{ end }}})
		}
		{{- if .Has "impersonations" }}
		var held []*tenantHoldingsHeld
		if err := row.ColumnByName("Held", &held); err != nil {
			return errors.Wrap(err, "spanner.Row.ColumnByName()")
		}
		for _, record := range held {
			sessionID, err := impulse.UUIDFromString(record.SessionID)
			if err != nil {
				return errors.Wrap(err, "impulse.UUIDFromString()")
			}
			person.Held = append(person.Held, identity.HeldRecord{SessionID: sessionID, Session: record.Session})
		}
		{{- end }}
		{{- if .UserCascades }}
		for i, cost := range costs {
			var count int64
			if err := row.ColumnByName("WithUser"+strconv.Itoa(i), &count); err != nil {
				return errors.Wrap(err, "spanner.Row.ColumnByName()")
			}
			person.WithUser += int(count) * cost
		}
		{{- end }}

		return nil
	}); err != nil {
		return nil, errors.Wrap(err, "spanner.RowIterator.Do()")
	}

	return people, nil
}
{{ else }}
// TenantHoldings answers nobody: the store keeps no table a person holds something in a
// tenant by (identity.Store.TenantHoldings).
func (store) TenantHoldings(context.Context, resource.ReadWriteTransaction, accesstypes.Domain) ([]identity.PersonHoldings, error) {
	return nil, nil
}
{{ end }}`
)
